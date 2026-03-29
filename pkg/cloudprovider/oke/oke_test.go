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
	"testing"

	"sigs.k8s.io/dranet/pkg/cloudprovider"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/utils/ptr"
)

func TestGetDeviceAttributes(t *testing.T) {
	tests := []struct {
		name     string
		instance *OKEInstance
		id       cloudprovider.DeviceIdentifiers
		want     map[resourceapi.QualifiedName]resourceapi.DeviceAttribute
	}{
		{
			name: "instance with all metadata",
			instance: &OKEInstance{
				Shape:              "BM.GPU.H100.8",
				FaultDomain:        "FAULT-DOMAIN-1",
				AvailabilityDomain: "TrcQ:US-ASHBURN-AD-2",
			},
			id: cloudprovider.DeviceIdentifiers{Name: "dev1"},
			want: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				AttrOKEShape:              {StringValue: ptr.To("BM.GPU.H100.8")},
				AttrOKEFaultDomain:        {StringValue: ptr.To("FAULT-DOMAIN-1")},
				AttrOKEAvailabilityDomain: {StringValue: ptr.To("TrcQ:US-ASHBURN-AD-2")},
			},
		},
		{
			name: "instance with only shape",
			instance: &OKEInstance{
				Shape:              "VM.Standard.E3.Flex",
				FaultDomain:        "",
				AvailabilityDomain: "",
			},
			id: cloudprovider.DeviceIdentifiers{Name: "dev1"},
			want: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				AttrOKEShape: {StringValue: ptr.To("VM.Standard.E3.Flex")},
			},
		},
		{
			name: "instance with no metadata",
			instance: &OKEInstance{
				Shape:              "",
				FaultDomain:        "",
				AvailabilityDomain: "",
			},
			id:   cloudprovider.DeviceIdentifiers{Name: "dev1"},
			want: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{},
		},
		{
			name: "attributes are node-level, same for any device identifier",
			instance: &OKEInstance{
				Shape:              "BM.GPU.H100.8",
				FaultDomain:        "FAULT-DOMAIN-3",
				AvailabilityDomain: "TrcQ:US-ASHBURN-AD-2",
			},
			id: cloudprovider.DeviceIdentifiers{
				Name:       "pci-0000-0c-00-0",
				MAC:        "a0:88:c2:a7:c5:04",
				PCIAddress: "0000:0c:00.0",
			},
			want: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				AttrOKEShape:              {StringValue: ptr.To("BM.GPU.H100.8")},
				AttrOKEFaultDomain:        {StringValue: ptr.To("FAULT-DOMAIN-3")},
				AttrOKEAvailabilityDomain: {StringValue: ptr.To("TrcQ:US-ASHBURN-AD-2")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.instance.GetDeviceAttributes(tt.id)
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("GetDeviceAttributes() returned unexpected diff (-want, +got):\n%s", diff)
			}
		})
	}
}

func TestGetDeviceConfig(t *testing.T) {
	instance := &OKEInstance{
		Shape:              "BM.GPU.H100.8",
		FaultDomain:        "FAULT-DOMAIN-1",
		AvailabilityDomain: "TrcQ:US-ASHBURN-AD-2",
	}
	got := instance.GetDeviceConfig(cloudprovider.DeviceIdentifiers{Name: "dev1"})
	if got != nil {
		t.Errorf("GetDeviceConfig() = %v, want nil", got)
	}
}
