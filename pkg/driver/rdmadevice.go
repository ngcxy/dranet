/*
Copyright 2024 Google LLC

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

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

// Based on existing RDMA CNI plugin
// https://github.com/k8snetworkplumbingwg/rdma-cni

func MoveRDMALinkIn(hostIfName string, containerNsPAth string) error {
	containerNs, err := ns.GetNS(containerNsPAth)
	if err != nil {
		return err
	}
	hostDev, err := netlink.RdmaLinkByName(hostIfName)
	if err != nil {
		return err
	}

	if err = netlink.RdmaLinkSetNsFd(hostDev, uint32(containerNs.Fd())); err != nil {
		return fmt.Errorf("failed to move %q to container ns: %v", hostDev.Attrs.Name, err)
	}

	return nil
}

func MoveRDMALinkOut(containerNsPAth string, ifName string) error {
	containerNs, err := ns.GetNS(containerNsPAth)
	if err != nil {
		return err
	}
	defaultNs, err := ns.GetCurrentNS()
	if err != nil {
		return err
	}
	defer defaultNs.Close()

	err = containerNs.Do(func(_ ns.NetNS) error {
		dev, err := netlink.RdmaLinkByName(ifName)
		if err != nil {
			return fmt.Errorf("failed to find %q: %v", ifName, err)
		}

		if err = netlink.RdmaLinkSetNsFd(dev, uint32(defaultNs.Fd())); err != nil {
			return fmt.Errorf("failed to move %q to host netns: %v", dev.Attrs.Name, err)
		}
		return nil
	})

	if err != nil {
		return err
	}

	return nil
}
