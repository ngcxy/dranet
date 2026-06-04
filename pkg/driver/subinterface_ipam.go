/*
Copyright The Kubernetes Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package driver

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

// Bucket layout:
//
//	ip_allocations (root bucket)
//	  └── subinterface_ips (nested bucket for subinterface IP allocations)
//	        └── <allocatedIP> = <POD_UID> (e.g. "10.244.0.8" = "pod-uid-123")
var (
	ipAllocationsBucket = []byte("ip_allocations")
	subinterfaceIPsKey  = []byte("subinterface_ips")
)

// ipamBoltCheckpointer manages IP address allocations from given CIDR ranges,
// persisting state in a long-lived bbolt database.
type ipamBoltCheckpointer struct {
	db *bolt.DB
}

// newIPAMBoltCheckpointer opens (or creates) a long-lived bbolt connection at the given path.
func newIPAMBoltCheckpointer(path string) (*ipamBoltCheckpointer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, fmt.Errorf("create IPAM db directory: %w", err)
	}

	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open IPAM db: %w", err)
	}

	// Ensure root bucket and nested subinterface bucket exist
	err = db.Update(func(tx *bolt.Tx) error {
		root, err := tx.CreateBucketIfNotExists(ipAllocationsBucket)
		if err != nil {
			return err
		}
		_, err = root.CreateBucketIfNotExists(subinterfaceIPsKey)
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize IPAM root and nested buckets: %w", err)
	}

	return &ipamBoltCheckpointer{db: db}, nil
}

func (c *ipamBoltCheckpointer) Close() error {
	return c.db.Close()
}

// Allocate allocates a free IP address from the given CIDR prefix.
func (c *ipamBoltCheckpointer) Allocate(podUID types.UID, cidr netip.Prefix) (netip.Addr, error) {
	bounds, err := calculateBounds(cidr)
	if err != nil {
		return netip.Addr{}, err
	}

	var allocatedIP netip.Addr
	err = c.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket(ipAllocationsBucket)
		if root == nil {
			return berrors.ErrBucketNotFound
		}
		subBucket := root.Bucket(subinterfaceIPsKey)
		if subBucket == nil {
			return berrors.ErrBucketNotFound
		}

		iterator := iPIterator(bounds.ipFirst, bounds.ipLast, bounds.offset)
		for {
			ip := iterator()
			if !ip.IsValid() {
				break
			}
			// Check if IP is already allocated in the subinterface bucket
			if subBucket.Get([]byte(ip.String())) != nil {
				continue
			}
			// Persist allocation under subinterface bucket
			if err := subBucket.Put([]byte(ip.String()), []byte(podUID)); err != nil {
				return err
			}
			allocatedIP = ip
			return nil
		}
		return fmt.Errorf("no more free IP addresses in range %s", cidr.String())
	})

	if err != nil {
		return netip.Addr{}, err
	}

	klog.V(2).Infof("Successfully allocated IP %s for pod %s from CIDR %s", allocatedIP.String(), podUID, cidr.String())
	return allocatedIP, nil
}

// Release releases an allocated IP for a given Pod UID.
func (c *ipamBoltCheckpointer) Release(podUID types.UID, cidr netip.Prefix) error {
	return c.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket(ipAllocationsBucket)
		if root == nil {
			return nil
		}
		subBucket := root.Bucket(subinterfaceIPsKey)
		if subBucket == nil {
			return nil
		}

		var targetKey []byte
		_ = subBucket.ForEach(func(k, v []byte) error {
			if string(v) == string(podUID) {
				targetKey = k
				return fmt.Errorf("found") // break early
			}
			return nil
		})

		if targetKey != nil {
			klog.V(2).Infof("Releasing IP %s for pod %s", string(targetKey), podUID)
			return subBucket.Delete(targetKey)
		}

		return nil
	})
}

// bounds defines computed static boundary bounds for IP allocation
type bounds struct {
	ipFirst netip.Addr
	ipLast  netip.Addr
	offset  uint64
}

func calculateBounds(cidr netip.Prefix) (bounds, error) {
	var size uint64
	hostsBits := cidr.Addr().BitLen() - cidr.Bits()
	if hostsBits >= 64 {
		size = math.MaxInt64
	} else {
		size = uint64(1) << uint(hostsBits)
	}
	size = size - 1 // skip network address

	reserved := 6
	if size <= 64 {
		reserved = 2
	} else if size <= 128 {
		reserved = 4
	}

	ipFirst := cidr.Masked().Addr().Next()
	ipFirst, err := addOffsetAddress(ipFirst, uint64(reserved))
	if err != nil {
		return bounds{}, err
	}

	ipLast, err := broadcastAddress(cidr)
	if err != nil {
		return bounds{}, err
	}

	rangeSize := size - uint64(reserved)
	var offset uint64
	switch {
	case rangeSize >= math.MaxInt64:
		offset = rand.Uint64()
	case rangeSize == 0:
		return bounds{}, fmt.Errorf("no available addresses")
	default:
		offset = uint64(rand.Int63n(int64(rangeSize)))
	}

	return bounds{
		ipFirst: ipFirst,
		ipLast:  ipLast,
		offset:  offset,
	}, nil
}

// iPIterator allows to iterate over all the IP addresses
// in a range defined by the start and last address.
func iPIterator(first netip.Addr, last netip.Addr, offset uint64) func() netip.Addr {
	modulo := func(addr netip.Addr) netip.Addr {
		if addr.Compare(last) == 1 {
			return first
		}
		return addr
	}
	next := func(addr netip.Addr) netip.Addr {
		return modulo(addr.Next())
	}
	start, err := addOffsetAddress(first, offset)
	if err != nil {
		return func() netip.Addr { return netip.Addr{} }
	}
	start = modulo(start)
	ip := start
	seen := false
	return func() netip.Addr {
		value := ip
		if value == start {
			if seen {
				return netip.Addr{}
			}
			seen = true
		}
		ip = next(ip)
		return value
	}
}

func broadcastAddress(subnet netip.Prefix) (netip.Addr, error) {
	base := subnet.Masked().Addr()
	bytes := base.AsSlice()
	n := 8*len(bytes) - subnet.Bits()
	for i := len(bytes) - 1; i >= 0 && n > 0; i-- {
		if n >= 8 {
			bytes[i] = 0xff
			n -= 8
		} else {
			mask := ^uint8(0) >> (8 - n)
			bytes[i] |= mask
			break
		}
	}
	addr, ok := netip.AddrFromSlice(bytes)
	if !ok {
		return netip.Addr{}, fmt.Errorf("invalid address %v", bytes)
	}
	return addr, nil
}

func addOffsetAddress(address netip.Addr, offset uint64) (netip.Addr, error) {
	addressBytes := address.AsSlice()
	addressBig := big.NewInt(0).SetBytes(addressBytes)
	r := big.NewInt(0).Add(addressBig, big.NewInt(int64(offset))).Bytes()
	lenDiff := len(addressBytes) - len(r)
	if lenDiff > 0 {
		r = append(make([]byte, lenDiff), r...)
	} else if lenDiff < 0 {
		return netip.Addr{}, fmt.Errorf("invalid address %v", r)
	}
	addr, ok := netip.AddrFromSlice(r)
	if !ok {
		return netip.Addr{}, fmt.Errorf("invalid address %v", r)
	}
	return addr, nil
}
