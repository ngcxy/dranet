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

package oke

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	resourceapi "k8s.io/api/resource/v1"
	"sigs.k8s.io/dranet/pkg/apis"
	"sigs.k8s.io/dranet/pkg/cloudprovider"
)

const (
	OKEAttrPrefix = "oke.dra.net"

	// RDMA topology attributes (from /opc/v2/host/).
	AttrOKEHPCIslandId     = OKEAttrPrefix + "/" + "hpcIslandId"
	AttrOKENetworkBlockId  = OKEAttrPrefix + "/" + "networkBlockId"
	AttrOKELocalBlockId    = OKEAttrPrefix + "/" + "localBlockId"
	AttrOKERackId          = OKEAttrPrefix + "/" + "rackId"
	AttrOKEGpuMemoryFabric = OKEAttrPrefix + "/" + "gpuMemoryFabricId"

	// Instance shape (from /opc/v2/instance/).
	AttrOKEShape = OKEAttrPrefix + "/" + "shape"

	// RDMA fabric attributes (from the rdmaFabricData object in /opc/v2/host/).
	AttrOKERDMAFabricIPv6   = OKEAttrPrefix + "/" + "rdmaFabricIpv6"
	AttrOKERDMAFabricPlanes = OKEAttrPrefix + "/" + "rdmaFabricPlanes"

	// imdsEndpoint is the Oracle Cloud Instance Metadata Service endpoint.
	imdsEndpoint = "http://169.254.169.254/opc/v2"

	imdsInitialRetryInterval = 1 * time.Second
	imdsInitialWait          = 15 * time.Second
	imdsRefreshInterval      = 5 * time.Minute
	// imdsRequestTimeout limits each request, at startup and in the background.
	imdsRequestTimeout = 5 * time.Second
)

// imdsHostRDMATopologyData contains the RDMA topology fields from the
// /opc/v2/host/ response. IMDS populates them only for instances in a
// dedicated pool, with RDMA topology data enabled for the tenancy.
type imdsHostRDMATopologyData struct {
	CustomerGpuMemoryFabric string `json:"customerGpuMemoryFabric"`
	CustomerHPCIslandId     string `json:"customerHPCIslandId"`
	CustomerHostId          string `json:"customerHostId"`
	CustomerLocalBlock      string `json:"customerLocalBlock"`
	CustomerNetworkBlock    string `json:"customerNetworkBlock"`
}

// imdsRDMAFabricData contains the RDMA fabric fields embedded in the
// /opc/v2/host/ response.
type imdsRDMAFabricData struct {
	IPv6   *bool  `json:"ipv6"`
	Planes *int64 `json:"planes"`
}

// imdsHostMetadata contains the fields used from the /opc/v2/host/ response.
type imdsHostMetadata struct {
	NetworkBlockId   string                    `json:"networkBlockId"`
	RackId           string                    `json:"rackId"`
	RDMATopologyData *imdsHostRDMATopologyData `json:"rdmaTopologyData"`
	RDMAFabricData   *imdsRDMAFabricData       `json:"rdmaFabricData"`
}

// imdsInstanceMetadata contains the fields we care about from /opc/v2/instance/.
type imdsInstanceMetadata struct {
	Shape string `json:"shape"`
}

// rdmaFabric describes the RDMA fabric of the instance.
type rdmaFabric struct {
	IPv6   bool
	Planes int64
}

// okeMetadata is one immutable snapshot of the instance metadata.
type okeMetadata struct {
	HPCIslandId    string
	NetworkBlockId string
	LocalBlockId   string
	RackId         string
	// GpuMemoryFabric is set only on shapes that support it.
	GpuMemoryFabric string
	Shape           string
	RDMAFabric      *rdmaFabric
}

type metadataFetcher func(context.Context) (*okeMetadata, error)

var _ cloudprovider.CloudInstance = (*OKEInstance)(nil)

// OKEInstance holds the OKE instance metadata and refreshes it from IMDS in
// the background.
type OKEInstance struct {
	metadata             atomic.Pointer[okeMetadata]
	fetchMetadata        metadataFetcher
	initialRetryInterval time.Duration
	initialWait          time.Duration
	refreshInterval      time.Duration
}

func newOKEInstance(metadata *okeMetadata, fetch metadataFetcher) *OKEInstance {
	instance := &OKEInstance{
		fetchMetadata:        fetch,
		initialRetryInterval: imdsInitialRetryInterval,
		initialWait:          imdsInitialWait,
		refreshInterval:      imdsRefreshInterval,
	}
	if metadata != nil {
		instance.metadata.Store(metadata)
	}
	return instance
}

// GetDeviceAttributes returns OKE-specific attributes for a device.
// These are node-level attributes applied to all devices since the OCI IMDS
// host endpoint exposes per-node metadata, not per-NIC metadata.
func (o *OKEInstance) GetDeviceAttributes(id cloudprovider.DeviceIdentifiers) map[resourceapi.QualifiedName]resourceapi.DeviceAttribute {
	attributes := make(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute)
	metadata := o.metadata.Load()
	if metadata == nil {
		return attributes
	}

	if metadata.HPCIslandId != "" {
		attributes[AttrOKEHPCIslandId] = resourceapi.DeviceAttribute{StringValue: &metadata.HPCIslandId}
	}
	if metadata.NetworkBlockId != "" {
		attributes[AttrOKENetworkBlockId] = resourceapi.DeviceAttribute{StringValue: &metadata.NetworkBlockId}
	}
	if metadata.LocalBlockId != "" {
		attributes[AttrOKELocalBlockId] = resourceapi.DeviceAttribute{StringValue: &metadata.LocalBlockId}
	}
	if metadata.RackId != "" {
		attributes[AttrOKERackId] = resourceapi.DeviceAttribute{StringValue: &metadata.RackId}
	}
	if metadata.GpuMemoryFabric != "" {
		attributes[AttrOKEGpuMemoryFabric] = resourceapi.DeviceAttribute{StringValue: &metadata.GpuMemoryFabric}
	}
	if metadata.Shape != "" {
		attributes[AttrOKEShape] = resourceapi.DeviceAttribute{StringValue: &metadata.Shape}
	}
	if metadata.RDMAFabric != nil {
		attributes[AttrOKERDMAFabricIPv6] = resourceapi.DeviceAttribute{BoolValue: &metadata.RDMAFabric.IPv6}
		attributes[AttrOKERDMAFabricPlanes] = resourceapi.DeviceAttribute{IntValue: &metadata.RDMAFabric.Planes}
	}

	return attributes
}

// ocidSuffix returns the unique identifier suffix of an OCI OCID, the segment
// after the last '.'. DRA string attributes are capped at 64 bytes, but full
// OCIDs are ~90+ characters; the suffix is always 60 characters and is unique
// per resource within a tenancy, making it safe to use as an attribute value.
func ocidSuffix(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if !strings.Contains(s, "ocid") {
		return "", fmt.Errorf("not a valid OCID (missing 'ocid' prefix): %q", s)
	}
	i := strings.LastIndex(s, ".")
	if i < 0 {
		return "", fmt.Errorf("not a valid OCID (missing '.' separator): %q", s)
	}
	suffix := s[i+1:]
	if len(suffix) > 60 {
		suffix = suffix[len(suffix)-60:]
	}
	return suffix, nil
}

// GetDeviceConfig returns nil as OCI does not provide device-specific
// network configuration through IMDS.
func (o *OKEInstance) GetDeviceConfig(id cloudprovider.DeviceIdentifiers) *apis.NetworkConfig {
	return nil
}

// OnOKE returns true if running on an Oracle Cloud Infrastructure instance.
// Detection is done by probing the OCI IMDS v2 endpoint.
func OnOKE(ctx context.Context) bool {
	pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return wait.PollUntilContextCancel(pollCtx, 1*time.Second, true, func(ctx context.Context) (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, imdsEndpoint+"/instance/", nil)
		if err != nil {
			return false, nil
		}
		req.Header.Set("Authorization", "Bearer Oracle")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false, nil
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK, nil
	}) == nil
}

// queryIMDS decodes the JSON response of one OCI IMDS v2 endpoint into out.
func queryIMDS(ctx context.Context, client *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("could not create OCI IMDS request for %s: %w", url, err)
	}
	req.Header.Set("Authorization", "Bearer Oracle")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach OCI IMDS endpoint %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OCI IMDS endpoint %s returned status %d", url, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("could not parse OCI IMDS response from %s: %w", url, err)
	}
	return nil
}

// fabric returns the value when both fields are present, otherwise nil.
// A partial object is treated like an absent one.
func (d *imdsRDMAFabricData) fabric() *rdmaFabric {
	if d == nil || d.IPv6 == nil || d.Planes == nil {
		return nil
	}
	return &rdmaFabric{IPv6: *d.IPv6, Planes: *d.Planes}
}

// metadataFromIMDS builds a snapshot from the endpoints that answered. Either
// response can be nil.
func metadataFromIMDS(host *imdsHostMetadata, instance *imdsInstanceMetadata) *okeMetadata {
	metadata := &okeMetadata{}
	if instance != nil {
		metadata.Shape = instance.Shape
	}
	if host == nil {
		return metadata
	}

	metadata.NetworkBlockId = host.NetworkBlockId
	metadata.RackId = host.RackId
	metadata.RDMAFabric = host.RDMAFabricData.fabric()
	if data := host.RDMAFabricData; data != nil && metadata.RDMAFabric == nil {
		klog.V(2).Infof("Ignoring incomplete rdmaFabricData from OCI IMDS: ipv6 present=%t, planes present=%t", data.IPv6 != nil, data.Planes != nil)
	}

	// rdmaTopologyData is absent without a dedicated pool. Fall back to the
	// top-level networkBlockId and rackId when they are present.
	topo := host.RDMATopologyData
	if topo == nil {
		return metadata
	}

	// A malformed OCID leaves its field empty, so the merge keeps the last
	// valid value and the rest of the snapshot stays usable.
	suffix := func(name, ocid string) string {
		s, err := ocidSuffix(ocid)
		if err != nil {
			klog.Warningf("Ignoring invalid %s from OCI IMDS: %v", name, err)
		}
		return s
	}
	metadata.HPCIslandId = suffix("HPCIslandId", topo.CustomerHPCIslandId)
	metadata.NetworkBlockId = suffix("NetworkBlockId", topo.CustomerNetworkBlock)
	metadata.LocalBlockId = suffix("LocalBlockId", topo.CustomerLocalBlock)
	metadata.GpuMemoryFabric = suffix("GpuMemoryFabric", topo.CustomerGpuMemoryFabric)
	return metadata
}

// fetchOKEMetadata reads the host and instance endpoints independently and
// builds a snapshot from the ones that answered. A host failure returns the
// snapshot together with the error, so start keeps polling for the host data.
func fetchOKEMetadata(ctx context.Context, client *http.Client, endpoint string) (*okeMetadata, error) {
	host := &imdsHostMetadata{}
	hostErr := queryIMDS(ctx, client, endpoint+"/host/", host)
	if hostErr != nil {
		// A decode error can leave the struct half filled.
		host = nil
	}

	instance := &imdsInstanceMetadata{}
	if err := queryIMDS(ctx, client, endpoint+"/instance/", instance); err != nil {
		if hostErr != nil {
			return nil, fmt.Errorf("%w; %w", hostErr, err)
		}
		klog.Warningf("Could not query OCI IMDS instance metadata: %v", err)
		instance = nil
	}

	return metadataFromIMDS(host, instance), hostErr
}

// mergeMetadata keeps the last non-empty value for a field that next omits.
func mergeMetadata(current, next *okeMetadata) *okeMetadata {
	if current == nil {
		return next
	}

	merged := *next
	merged.HPCIslandId = cmp.Or(merged.HPCIslandId, current.HPCIslandId)
	merged.NetworkBlockId = cmp.Or(merged.NetworkBlockId, current.NetworkBlockId)
	merged.LocalBlockId = cmp.Or(merged.LocalBlockId, current.LocalBlockId)
	merged.RackId = cmp.Or(merged.RackId, current.RackId)
	merged.GpuMemoryFabric = cmp.Or(merged.GpuMemoryFabric, current.GpuMemoryFabric)
	merged.Shape = cmp.Or(merged.Shape, current.Shape)
	if merged.RDMAFabric == nil {
		merged.RDMAFabric = current.RDMAFabric
	}
	return &merged
}

// refreshMetadata stores the snapshot of a full or partial read, then returns
// the read error.
func (o *OKEInstance) refreshMetadata(ctx context.Context) error {
	if o.fetchMetadata == nil {
		return errors.New("OKE metadata fetcher is not configured")
	}

	next, err := o.fetchMetadata(ctx)
	if next != nil {
		// start calls this before it starts refreshLoop. After startup,
		// refreshLoop is the only writer, so the read and write stay serialized.
		o.metadata.Store(mergeMetadata(o.metadata.Load(), next))
	}
	return err
}

// refreshLoop refreshes the metadata every refresh interval until ctx ends.
// A failed read keeps the last known values of the fields it did not update.
func (o *OKEInstance) refreshLoop(ctx context.Context) {
	for {
		timer := time.NewTimer(wait.Jitter(o.refreshInterval, 0.1))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return
		}

		if err := o.refreshMetadata(ctx); err != nil && ctx.Err() == nil {
			klog.Warningf("Could not refresh OCI IMDS host metadata, keeping the last known values: %v", err)
		}
	}
}

// GetInstance reads the OKE instance topology, shape, and RDMA fabric metadata
// from IMDS. It returns after the first successful host read or after the
// startup window. It keeps refreshing the metadata in the background until ctx
// ends.
func GetInstance(ctx context.Context) (cloudprovider.CloudInstance, error) {
	// A dedicated client, so the per-request timeout does not affect other callers.
	client := &http.Client{Timeout: imdsRequestTimeout}
	instance, err := newOKEInstance(nil, nil).start(ctx, client, imdsEndpoint)
	if err != nil {
		return nil, err
	}
	return instance, nil
}

// start polls IMDS for the startup window and runs the refresh loop until
// ctx ends.
func (o *OKEInstance) start(ctx context.Context, client *http.Client, endpoint string) (*OKEInstance, error) {
	o.fetchMetadata = func(ctx context.Context) (*okeMetadata, error) {
		return fetchOKEMetadata(ctx, client, endpoint)
	}

	var lastErr error
	err := wait.PollUntilContextTimeout(ctx, o.initialRetryInterval, o.initialWait, true, func(ctx context.Context) (bool, error) {
		lastErr = o.refreshMetadata(ctx)
		if lastErr != nil {
			klog.Infof("could not get OCI IMDS host metadata, retrying: %v", lastErr)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		klog.Warningf("OCI IMDS host metadata is not available after %s, retrying in the background: %v", o.initialWait, lastErr)
	}
	go o.refreshLoop(ctx)
	return o, nil
}
