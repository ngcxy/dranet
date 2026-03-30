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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	resourceapi "k8s.io/api/resource/v1"
	"sigs.k8s.io/dranet/pkg/apis"
	"sigs.k8s.io/dranet/pkg/cloudprovider"
)

const (
	OKEAttrPrefix = "oke.dra.net"

	AttrOKEShape              = OKEAttrPrefix + "/" + "shape"
	AttrOKEFaultDomain        = OKEAttrPrefix + "/" + "faultDomain"
	AttrOKEAvailabilityDomain = OKEAttrPrefix + "/" + "availabilityDomain"

	// imdsEndpoint is the Oracle Cloud Instance Metadata Service endpoint.
	imdsEndpoint = "http://169.254.169.254/opc/v2"
)

// imdsInstanceMetadata contains the fields we care about from the OCI IMDS
// instance metadata response.
type imdsInstanceMetadata struct {
	Shape              string `json:"shape"`
	FaultDomain        string `json:"faultDomain"`
	AvailabilityDomain string `json:"availabilityDomain"`
}

var _ cloudprovider.CloudInstance = (*OKEInstance)(nil)

// OKEInstance holds OCI/OKE specific instance data.
type OKEInstance struct {
	Shape              string
	FaultDomain        string
	AvailabilityDomain string
}

// GetDeviceAttributes returns OKE-specific attributes for a device.
// These are node-level attributes applied to all devices since OCI IMDS
// does not expose per-RDMA-NIC metadata.
func (o *OKEInstance) GetDeviceAttributes(id cloudprovider.DeviceIdentifiers) map[resourceapi.QualifiedName]resourceapi.DeviceAttribute {
	attributes := make(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute)

	if o.Shape != "" {
		attributes[AttrOKEShape] = resourceapi.DeviceAttribute{StringValue: &o.Shape}
	}
	if o.FaultDomain != "" {
		attributes[AttrOKEFaultDomain] = resourceapi.DeviceAttribute{StringValue: &o.FaultDomain}
	}
	if o.AvailabilityDomain != "" {
		attributes[AttrOKEAvailabilityDomain] = resourceapi.DeviceAttribute{StringValue: &o.AvailabilityDomain}
	}

	return attributes
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

// GetInstance retrieves OCI instance properties by querying the IMDS.
func GetInstance(ctx context.Context) (cloudprovider.CloudInstance, error) {
	var instance *OKEInstance
	err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 15*time.Second, true, func(ctx context.Context) (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, imdsEndpoint+"/instance/", nil)
		if err != nil {
			klog.Infof("could not create OCI IMDS request ... retrying: %v", err)
			return false, nil
		}
		req.Header.Set("Authorization", "Bearer Oracle")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			klog.Infof("could not reach OCI IMDS ... retrying: %v", err)
			return false, nil
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			klog.Infof("OCI IMDS returned status %d ... retrying", resp.StatusCode)
			return false, nil
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			klog.Infof("could not read OCI IMDS response ... retrying: %v", err)
			return false, nil
		}

		var metadata imdsInstanceMetadata
		if err := json.Unmarshal(body, &metadata); err != nil {
			return false, fmt.Errorf("could not parse OCI IMDS response: %w", err)
		}

		instance = &OKEInstance{
			Shape:              metadata.Shape,
			FaultDomain:        metadata.FaultDomain,
			AvailabilityDomain: metadata.AvailabilityDomain,
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return instance, nil
}
