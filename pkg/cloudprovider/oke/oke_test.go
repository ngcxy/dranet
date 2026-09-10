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
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/dranet/pkg/cloudprovider"
)

func TestGetDeviceAttributes(t *testing.T) {
	tests := []struct {
		name     string
		instance *OKEInstance
		id       cloudprovider.DeviceIdentifiers
		want     map[resourceapi.QualifiedName]resourceapi.DeviceAttribute
	}{
		{
			name: "full topology with gpu memory fabric and shape (GB200/GB300 shapes)",
			instance: newOKEInstance(&okeMetadata{
				HPCIslandId:     "fake-island-id",
				NetworkBlockId:  "fake-network-block-id",
				LocalBlockId:    "fake-local-block-id",
				RackId:          "fake-rack-id",
				GpuMemoryFabric: "fake-gpu-memory-fabric-id",
				Shape:           "BM.GPU.GB200.4",
			}, nil),
			id: cloudprovider.DeviceIdentifiers{Name: "dev1"},
			want: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				AttrOKEHPCIslandId:     {StringValue: ptr.To("fake-island-id")},
				AttrOKENetworkBlockId:  {StringValue: ptr.To("fake-network-block-id")},
				AttrOKELocalBlockId:    {StringValue: ptr.To("fake-local-block-id")},
				AttrOKERackId:          {StringValue: ptr.To("fake-rack-id")},
				AttrOKEGpuMemoryFabric: {StringValue: ptr.To("fake-gpu-memory-fabric-id")},
				AttrOKEShape:           {StringValue: ptr.To("BM.GPU.GB200.4")},
			},
		},
		{
			name: "H100 fallback: only networkBlockId and rackId (no rdmaTopologyData)",
			instance: newOKEInstance(&okeMetadata{
				NetworkBlockId: "fake-network-block-id",
				RackId:         "fake-rack-id",
			}, nil),
			id: cloudprovider.DeviceIdentifiers{Name: "dev1"},
			want: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				AttrOKENetworkBlockId: {StringValue: ptr.To("fake-network-block-id")},
				AttrOKERackId:         {StringValue: ptr.To("fake-rack-id")},
			},
		},
		{
			name: "partial topology (only hpcIslandId and networkBlockId)",
			instance: newOKEInstance(&okeMetadata{
				HPCIslandId:    "fake-island-id",
				NetworkBlockId: "fake-network-block-id",
			}, nil),
			id: cloudprovider.DeviceIdentifiers{Name: "dev1"},
			want: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				AttrOKEHPCIslandId:    {StringValue: ptr.To("fake-island-id")},
				AttrOKENetworkBlockId: {StringValue: ptr.To("fake-network-block-id")},
			},
		},
		{
			name:     "no metadata",
			instance: newOKEInstance(nil, nil),
			id:       cloudprovider.DeviceIdentifiers{Name: "dev1"},
			want:     map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{},
		},
		{
			name: "attributes are node-level, same for any device identifier",
			instance: newOKEInstance(&okeMetadata{
				HPCIslandId:    "fake-island-id",
				NetworkBlockId: "fake-network-block-id",
				RackId:         "fake-rack-id",
			}, nil),
			id: cloudprovider.DeviceIdentifiers{
				Name:       "pci-0000-0c-00-0",
				MAC:        "a0:88:c2:a7:c5:04",
				PCIAddress: "0000:0c:00.0",
			},
			want: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				AttrOKEHPCIslandId:    {StringValue: ptr.To("fake-island-id")},
				AttrOKENetworkBlockId: {StringValue: ptr.To("fake-network-block-id")},
				AttrOKERackId:         {StringValue: ptr.To("fake-rack-id")},
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

func TestGetDeviceAttributesRDMAFabric(t *testing.T) {
	tests := []struct {
		name     string
		instance *OKEInstance
		want     map[resourceapi.QualifiedName]resourceapi.DeviceAttribute
	}{
		{
			name:     "IPv4 fabric with zero planes",
			instance: newOKEInstance(&okeMetadata{RDMAFabric: &rdmaFabric{IPv6: false, Planes: 0}}, nil),
			want: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				AttrOKERDMAFabricIPv6:   {BoolValue: ptr.To(false)},
				AttrOKERDMAFabricPlanes: {IntValue: ptr.To[int64](0)},
			},
		},
		{
			name:     "IPv6 fabric with multiple planes",
			instance: newOKEInstance(&okeMetadata{RDMAFabric: &rdmaFabric{IPv6: true, Planes: 8}}, nil),
			want: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				AttrOKERDMAFabricIPv6:   {BoolValue: ptr.To(true)},
				AttrOKERDMAFabricPlanes: {IntValue: ptr.To[int64](8)},
			},
		},
		{
			name:     "fabric data is absent",
			instance: newOKEInstance(&okeMetadata{RackId: "fake-rack-id"}, nil),
			want: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				AttrOKERackId: {StringValue: ptr.To("fake-rack-id")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.instance.GetDeviceAttributes(cloudprovider.DeviceIdentifiers{Name: "dev1"})
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("GetDeviceAttributes() returned unexpected diff (-want, +got):\n%s", diff)
			}
		})
	}
}

func TestOCIDSuffix(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "valid OCID extracts 60-char suffix",
			input: "ocid1.hpcisland.oc1.test-region-1.aaaaaaaa2mvjha24vj6evyafdqtis6nzqibhrnxxhzt65zkc3upy4xlrz5za",
			want:  "aaaaaaaa2mvjha24vj6evyafdqtis6nzqibhrnxxhzt65zkc3upy4xlrz5za",
		},
		{
			name:  "OCID with suffix longer than 60 chars is truncated to last 60",
			input: "ocid1.hpcisland.oc1.test-region-1.xaaaaaaaa2mvjha24vj6evyafdqtis6nzqibhrnxxhzt65zkc3upy4xlrz5za",
			want:  "aaaaaaaa2mvjha24vj6evyafdqtis6nzqibhrnxxhzt65zkc3upy4xlrz5za",
		},
		{
			name:  "empty string returns empty (field not present on shape)",
			input: "",
			want:  "",
		},
		{
			name:    "non-OCID string returns error",
			input:   "fakehexhash",
			wantErr: true,
		},
		{
			name:    "non-OCID dotted string returns error",
			input:   "some.dotted.value",
			wantErr: true,
		},
		{
			name:    "OCID without dot separator returns error",
			input:   "ocid1-hpcisland-no-dots",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ocidSuffix(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ocidSuffix(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ocidSuffix(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ocidSuffix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetDeviceConfig(t *testing.T) {
	instance := newOKEInstance(&okeMetadata{
		HPCIslandId:    "fake-island-id",
		NetworkBlockId: "fake-network-block-id",
		RackId:         "fake-rack-id",
	}, nil)
	got := instance.GetDeviceConfig(cloudprovider.DeviceIdentifiers{Name: "dev1"})
	if got != nil {
		t.Errorf("GetDeviceConfig() = %v, want nil", got)
	}
}

func TestQueryIMDS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/opc/v2/instance/" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer Oracle" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		if _, err := w.Write([]byte(`{"shape":"BM.GPU.B4.8"}`)); err != nil {
			t.Errorf("Write() returned error: %v", err)
		}
	}))
	defer server.Close()

	var got imdsInstanceMetadata
	if err := queryIMDS(context.Background(), server.Client(), server.URL+"/opc/v2/instance/", &got); err != nil {
		t.Fatalf("queryIMDS() returned error: %v", err)
	}
	if got.Shape != "BM.GPU.B4.8" {
		t.Errorf("queryIMDS() = %#v, want shape BM.GPU.B4.8", got)
	}
}

func TestQueryIMDSRejectsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	var got imdsInstanceMetadata
	if err := queryIMDS(context.Background(), server.Client(), server.URL, &got); err == nil {
		t.Fatal("queryIMDS() returned no error")
	}
}

func TestRDMAFabricDataFabric(t *testing.T) {
	tests := []struct {
		name string
		data *imdsRDMAFabricData
		want *rdmaFabric
	}{
		{
			name: "absent object",
			data: nil,
			want: nil,
		},
		{
			name: "IPv4 with zero planes",
			data: &imdsRDMAFabricData{IPv6: ptr.To(false), Planes: ptr.To[int64](0)},
			want: &rdmaFabric{IPv6: false, Planes: 0},
		},
		{
			name: "missing IPv6",
			data: &imdsRDMAFabricData{Planes: ptr.To[int64](8)},
			want: nil,
		},
		{
			name: "missing planes",
			data: &imdsRDMAFabricData{IPv6: ptr.To(true)},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, tt.data.fabric()); diff != "" {
				t.Errorf("fabric() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFetchOKEMetadata(t *testing.T) {
	const shape = `{"shape":"BM.GPU.B4.8"}`
	tests := []struct {
		name             string
		hostResponse     string
		hostStatus       int
		instanceResponse string
		instanceStatus   int
		want             *okeMetadata
		wantErr          bool
	}{
		{
			name:             "embedded fabric data is used",
			hostResponse:     `{"networkBlockId":"network-1","rackId":"rack-1","rdmaFabricData":{"ipv6":false,"planes":0}}`,
			instanceResponse: shape,
			want:             &okeMetadata{NetworkBlockId: "network-1", RackId: "rack-1", Shape: "BM.GPU.B4.8", RDMAFabric: &rdmaFabric{IPv6: false, Planes: 0}},
		},
		{
			name:             "partial fabric data is ignored",
			hostResponse:     `{"networkBlockId":"network-1","rackId":"rack-1","rdmaFabricData":{"ipv6":true}}`,
			instanceResponse: shape,
			want:             &okeMetadata{NetworkBlockId: "network-1", RackId: "rack-1", Shape: "BM.GPU.B4.8"},
		},
		{
			name:             "topology without fabric data",
			hostResponse:     `{"networkBlockId":"network-1","rackId":"rack-1"}`,
			instanceResponse: shape,
			want:             &okeMetadata{NetworkBlockId: "network-1", RackId: "rack-1", Shape: "BM.GPU.B4.8"},
		},
		{
			name:             "host down keeps the shape and returns the host error",
			hostStatus:       http.StatusServiceUnavailable,
			instanceResponse: shape,
			want:             &okeMetadata{Shape: "BM.GPU.B4.8"},
			wantErr:          true,
		},
		{
			// The type mismatch fills networkBlockId before the decoder reports
			// the error. None of the host data may be used.
			name:             "host decode error discards the host data",
			hostResponse:     `{"networkBlockId":"network-1","rackId":5}`,
			instanceResponse: shape,
			want:             &okeMetadata{Shape: "BM.GPU.B4.8"},
			wantErr:          true,
		},
		{
			name:           "instance down keeps the host data",
			hostResponse:   `{"networkBlockId":"network-1","rackId":"rack-1"}`,
			instanceStatus: http.StatusNotFound,
			want:           &okeMetadata{NetworkBlockId: "network-1", RackId: "rack-1"},
		},
		{
			name:           "both down returns an error",
			hostStatus:     http.StatusServiceUnavailable,
			instanceStatus: http.StatusServiceUnavailable,
			want:           nil,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			respond := func(w http.ResponseWriter, status int, body string) {
				if status != 0 {
					http.Error(w, "not ready", status)
					return
				}
				_, _ = w.Write([]byte(body))
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer Oracle" {
					http.Error(w, "missing authorization", http.StatusUnauthorized)
					return
				}
				switch r.URL.Path {
				case "/host/":
					respond(w, tt.hostStatus, tt.hostResponse)
				case "/instance/":
					respond(w, tt.instanceStatus, tt.instanceResponse)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			got, err := fetchOKEMetadata(context.Background(), server.Client(), server.URL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("fetchOKEMetadata() error = %v, want error %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("fetchOKEMetadata() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFetchOKEMetadataHostTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/host/":
			// Hold the request until the client gives up.
			<-r.Context().Done()
		case "/instance/":
			_, _ = w.Write([]byte(`{"shape":"BM.GPU.B4.8"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// The timeout applies to each request, so the instance read still happens.
	client := server.Client()
	client.Timeout = 50 * time.Millisecond
	got, err := fetchOKEMetadata(context.Background(), client, server.URL)
	if err == nil {
		t.Fatal("fetchOKEMetadata() returned no error for a hanging host endpoint")
	}
	want := &okeMetadata{Shape: "BM.GPU.B4.8"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("fetchOKEMetadata() mismatch (-want +got):\n%s", diff)
	}
}

func TestFetchOKEMetadataFullTopology(t *testing.T) {
	const (
		islandOCID  = "ocid1.hpcisland.oc1.test-region-1.aaaaaaaa2mvjha24vj6evyafdqtis6nzqibhrnxxhzt65zkc3upy4xlrz5za"
		networkOCID = "ocid1.networkblock.oc1.test-region-1.bbbbbbbb2mvjha24vj6evyafdqtis6nzqibhrnxxhzt65zkc3upy4xlrz5za"
		localOCID   = "ocid1.localblock.oc1.test-region-1.cccccccc2mvjha24vj6evyafdqtis6nzqibhrnxxhzt65zkc3upy4xlrz5za"
		fabricOCID  = "ocid1.computegpumemoryfabric.oc1.test-region-1.dddddddd2mvjha24vj6evyafdqtis6nzqibhrnxxhzt65zkc3upy4xlrz5za"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/host/":
			_, _ = fmt.Fprintf(w, `{"networkBlockId":"top-level-network","rackId":"rack-1","rdmaTopologyData":{"customerHPCIslandId":%q,"customerNetworkBlock":%q,"customerLocalBlock":%q,"customerGpuMemoryFabric":%q,"customerHostId":"ocid1.host.oc1..host"},"rdmaFabricData":{"ipv6":true,"planes":2}}`, islandOCID, networkOCID, localOCID, fabricOCID)
		case "/instance/":
			_, _ = w.Write([]byte(`{"shape":"BM.GPU.GB300.4"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	metadata, err := fetchOKEMetadata(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetchOKEMetadata() returned error: %v", err)
	}

	suffix := func(ocid string) *string {
		return ptr.To(ocid[strings.LastIndex(ocid, ".")+1:])
	}
	// The topology block overrides the top-level networkBlockId.
	want := map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
		AttrOKEHPCIslandId:      {StringValue: suffix(islandOCID)},
		AttrOKENetworkBlockId:   {StringValue: suffix(networkOCID)},
		AttrOKELocalBlockId:     {StringValue: suffix(localOCID)},
		AttrOKERackId:           {StringValue: ptr.To("rack-1")},
		AttrOKEGpuMemoryFabric:  {StringValue: suffix(fabricOCID)},
		AttrOKEShape:            {StringValue: ptr.To("BM.GPU.GB300.4")},
		AttrOKERDMAFabricIPv6:   {BoolValue: ptr.To(true)},
		AttrOKERDMAFabricPlanes: {IntValue: ptr.To[int64](2)},
	}
	got := newOKEInstance(metadata, nil).GetDeviceAttributes(cloudprovider.DeviceIdentifiers{Name: "dev1"})
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetDeviceAttributes() mismatch (-want +got):\n%s", diff)
	}
}

func TestMetadataFromIMDS(t *testing.T) {
	const (
		networkOCID = "ocid1.networkblock.oc1.test-region-1.bbbbbbbb2mvjha24vj6evyafdqtis6nzqibhrnxxhzt65zkc3upy4xlrz5za"
		localOCID   = "ocid1.localblock.oc1.test-region-1.cccccccc2mvjha24vj6evyafdqtis6nzqibhrnxxhzt65zkc3upy4xlrz5za"
	)
	tests := []struct {
		name     string
		host     *imdsHostMetadata
		instance *imdsInstanceMetadata
		want     *okeMetadata
	}{
		{
			name:     "shape only without host data",
			instance: &imdsInstanceMetadata{Shape: "BM.GPU.B4.8"},
			want:     &okeMetadata{Shape: "BM.GPU.B4.8"},
		},
		{
			name: "malformed OCID leaves only that field empty",
			host: &imdsHostMetadata{
				NetworkBlockId: "top-level-network",
				RackId:         "rack-1",
				RDMATopologyData: &imdsHostRDMATopologyData{
					CustomerHPCIslandId:  "not-an-ocid",
					CustomerNetworkBlock: networkOCID,
					CustomerLocalBlock:   localOCID,
				},
				RDMAFabricData: &imdsRDMAFabricData{IPv6: ptr.To(true), Planes: ptr.To[int64](2)},
			},
			instance: &imdsInstanceMetadata{Shape: "BM.GPU.GB300.4"},
			want: &okeMetadata{
				NetworkBlockId: "bbbbbbbb2mvjha24vj6evyafdqtis6nzqibhrnxxhzt65zkc3upy4xlrz5za",
				LocalBlockId:   "cccccccc2mvjha24vj6evyafdqtis6nzqibhrnxxhzt65zkc3upy4xlrz5za",
				RackId:         "rack-1",
				Shape:          "BM.GPU.GB300.4",
				RDMAFabric:     &rdmaFabric{IPv6: true, Planes: 2},
			},
		},
		{
			name: "malformed network block does not fall back to the top-level value",
			host: &imdsHostMetadata{
				NetworkBlockId:   "top-level-network",
				RackId:           "rack-1",
				RDMATopologyData: &imdsHostRDMATopologyData{CustomerNetworkBlock: "not-an-ocid"},
			},
			want: &okeMetadata{RackId: "rack-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, metadataFromIMDS(tt.host, tt.instance)); diff != "" {
				t.Errorf("metadataFromIMDS() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// captureLogs routes klog output at verbosity 2 to a buffer for one test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	state := klog.CaptureState()
	t.Cleanup(state.Restore)
	var flags flag.FlagSet
	klog.InitFlags(&flags)
	for name, value := range map[string]string{"v": "2", "logtostderr": "false"} {
		if err := flags.Set(name, value); err != nil {
			t.Fatalf("could not set klog flag %s: %v", name, err)
		}
	}
	var buf bytes.Buffer
	klog.SetOutput(&buf)
	return &buf
}

func TestMetadataFromIMDSLogsIncompleteFabricData(t *testing.T) {
	logs := captureLogs(t)
	got := metadataFromIMDS(&imdsHostMetadata{RackId: "rack-1", RDMAFabricData: &imdsRDMAFabricData{}}, nil)
	klog.Flush()
	if got.RDMAFabric != nil {
		t.Fatalf("metadataFromIMDS() stored fabric %+v from an empty object", *got.RDMAFabric)
	}
	if want := "ipv6 present=false, planes present=false"; !strings.Contains(logs.String(), want) {
		t.Errorf("metadataFromIMDS() logged %q, want it to contain %q", logs.String(), want)
	}
}

func TestMergeMetadata(t *testing.T) {
	// Fresh pointers for every row so a merge cannot mutate shared input.
	ipv4 := func() *rdmaFabric { return &rdmaFabric{IPv6: false, Planes: 0} }
	ipv6 := func() *rdmaFabric { return &rdmaFabric{IPv6: true, Planes: 8} }
	tests := []struct {
		name    string
		current *okeMetadata
		next    *okeMetadata
		want    *okeMetadata
	}{
		{
			name:    "first snapshot is stored as read",
			current: nil,
			next:    &okeMetadata{RackId: "rack-1"},
			want:    &okeMetadata{RackId: "rack-1"},
		},
		{
			name:    "omitted fields keep the last non-empty value",
			current: &okeMetadata{HPCIslandId: "island-1", NetworkBlockId: "network-1", LocalBlockId: "local-1", GpuMemoryFabric: "fabric-1", Shape: "BM.GPU.GB200.4"},
			next:    &okeMetadata{RackId: "rack-1"},
			want:    &okeMetadata{HPCIslandId: "island-1", NetworkBlockId: "network-1", LocalBlockId: "local-1", RackId: "rack-1", GpuMemoryFabric: "fabric-1", Shape: "BM.GPU.GB200.4"},
		},
		{
			name:    "omitted rack id keeps the last value",
			current: &okeMetadata{RackId: "rack-1"},
			next:    &okeMetadata{Shape: "BM.GPU.GB200.4"},
			want:    &okeMetadata{RackId: "rack-1", Shape: "BM.GPU.GB200.4"},
		},
		{
			name:    "changed values are accepted",
			current: &okeMetadata{HPCIslandId: "island-1", NetworkBlockId: "network-1", LocalBlockId: "local-1", RackId: "rack-1", GpuMemoryFabric: "fabric-1", Shape: "BM.GPU.GB200.4"},
			next:    &okeMetadata{HPCIslandId: "island-2", NetworkBlockId: "network-2", LocalBlockId: "local-2", RackId: "rack-2", GpuMemoryFabric: "fabric-2", Shape: "BM.GPU.GB300.4"},
			want:    &okeMetadata{HPCIslandId: "island-2", NetworkBlockId: "network-2", LocalBlockId: "local-2", RackId: "rack-2", GpuMemoryFabric: "fabric-2", Shape: "BM.GPU.GB300.4"},
		},
		{
			name:    "first fabric data is stored",
			current: &okeMetadata{RackId: "rack-1"},
			next:    &okeMetadata{RackId: "rack-1", RDMAFabric: ipv4()},
			want:    &okeMetadata{RackId: "rack-1", RDMAFabric: ipv4()},
		},
		{
			name:    "changed fabric data is accepted",
			current: &okeMetadata{RDMAFabric: ipv4()},
			next:    &okeMetadata{RDMAFabric: ipv6()},
			want:    &okeMetadata{RDMAFabric: ipv6()},
		},
		{
			name:    "fabric data with false and zero replaces the last value",
			current: &okeMetadata{RDMAFabric: ipv6()},
			next:    &okeMetadata{RDMAFabric: ipv4()},
			want:    &okeMetadata{RDMAFabric: ipv4()},
		},
		{
			name:    "identical fabric data is kept",
			current: &okeMetadata{RDMAFabric: ipv4()},
			next:    &okeMetadata{RDMAFabric: ipv4()},
			want:    &okeMetadata{RDMAFabric: ipv4()},
		},
		{
			name:    "omitted fabric data keeps the last value",
			current: &okeMetadata{RDMAFabric: ipv4()},
			next:    &okeMetadata{RackId: "rack-1"},
			want:    &okeMetadata{RackId: "rack-1", RDMAFabric: ipv4()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, mergeMetadata(tt.current, tt.next)); diff != "" {
				t.Errorf("mergeMetadata() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRefreshMetadataStoresPartialRead(t *testing.T) {
	instance := newOKEInstance(&okeMetadata{
		NetworkBlockId: "network-1",
		RackId:         "rack-1",
		RDMAFabric:     &rdmaFabric{IPv6: false, Planes: 0},
	}, func(context.Context) (*okeMetadata, error) {
		return &okeMetadata{Shape: "BM.GPU.B4.8"}, errors.New("host endpoint is down")
	})

	if err := instance.refreshMetadata(context.Background()); err == nil {
		t.Fatal("refreshMetadata() returned no error for a partial read")
	}
	// The shape is stored, and the host fields stay.
	want := &okeMetadata{
		NetworkBlockId: "network-1",
		RackId:         "rack-1",
		Shape:          "BM.GPU.B4.8",
		RDMAFabric:     &rdmaFabric{IPv6: false, Planes: 0},
	}
	if diff := cmp.Diff(want, instance.metadata.Load()); diff != "" {
		t.Errorf("refreshMetadata() mismatch (-want +got):\n%s", diff)
	}
}

func TestRefreshMetadataKeepsValidOCIDOnMalformedRefresh(t *testing.T) {
	instance := newOKEInstance(&okeMetadata{LocalBlockId: "local-1", RackId: "rack-1"}, func(context.Context) (*okeMetadata, error) {
		host := &imdsHostMetadata{
			RackId:           "rack-2",
			RDMATopologyData: &imdsHostRDMATopologyData{CustomerLocalBlock: "not-an-ocid"},
		}
		return metadataFromIMDS(host, nil), nil
	})

	if err := instance.refreshMetadata(context.Background()); err != nil {
		t.Fatalf("refreshMetadata() returned error: %v", err)
	}
	want := &okeMetadata{LocalBlockId: "local-1", RackId: "rack-2"}
	if diff := cmp.Diff(want, instance.metadata.Load()); diff != "" {
		t.Errorf("refreshMetadata() mismatch (-want +got):\n%s", diff)
	}
}

func TestRefreshMetadataKeepsLastKnownDataOnFailure(t *testing.T) {
	want := &okeMetadata{
		NetworkBlockId: "network-1",
		RDMAFabric:     &rdmaFabric{IPv6: false, Planes: 0},
	}
	instance := newOKEInstance(want, func(context.Context) (*okeMetadata, error) {
		return nil, errors.New("IMDS is not ready")
	})

	if err := instance.refreshMetadata(context.Background()); err == nil {
		t.Fatal("refreshMetadata() returned no error")
	}
	if instance.metadata.Load() != want {
		t.Fatal("refreshMetadata() replaced the last known metadata after a failure")
	}
}

func TestRefreshLoopPicksUpLateFabricData(t *testing.T) {
	attempts := make(chan int, 2)
	attempt := 0
	instance := newOKEInstance(nil, func(context.Context) (*okeMetadata, error) {
		attempt++
		select {
		case attempts <- attempt:
		default:
		}
		if attempt == 1 {
			return &okeMetadata{NetworkBlockId: "network-1"}, nil
		}
		return &okeMetadata{RDMAFabric: &rdmaFabric{IPv6: true, Planes: 8}}, nil
	})
	instance.refreshInterval = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go instance.refreshLoop(ctx)

	for wantAttempt := 1; wantAttempt <= 2; wantAttempt++ {
		select {
		case gotAttempt := <-attempts:
			if gotAttempt != wantAttempt {
				t.Fatalf("refreshLoop() attempt = %d, want %d", gotAttempt, wantAttempt)
			}
		case <-time.After(time.Second):
			t.Fatalf("refreshLoop() did not make attempt %d", wantAttempt)
		}
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := instance.metadata.Load(); got != nil && got.RDMAFabric != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	got := instance.metadata.Load()
	if got == nil || got.NetworkBlockId != "network-1" || got.RDMAFabric == nil || !got.RDMAFabric.IPv6 || got.RDMAFabric.Planes != 8 {
		t.Fatalf("refreshLoop() stored %#v, want network-1 with IPv6 and 8 planes", got)
	}
}

func TestRefreshLoopStopsOnContextCancel(t *testing.T) {
	instance := newOKEInstance(&okeMetadata{RackId: "rack-1"}, func(context.Context) (*okeMetadata, error) {
		return &okeMetadata{RackId: "rack-1"}, nil
	})
	instance.refreshInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		instance.refreshLoop(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refreshLoop() did not stop after the context was cancelled")
	}
}

func TestRefreshLoopRefreshesPeriodicallyAfterFailure(t *testing.T) {
	attempts := make(chan int, 3)
	attempt := 0
	instance := newOKEInstance(&okeMetadata{RackId: "rack-1"}, func(context.Context) (*okeMetadata, error) {
		attempt++
		select {
		case attempts <- attempt:
		default:
		}
		if attempt == 1 {
			return nil, errors.New("IMDS is not ready")
		}
		return &okeMetadata{RackId: "rack-1"}, nil
	})
	instance.refreshInterval = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go instance.refreshLoop(ctx)

	// A failed refresh keeps the last snapshot and does not stop the loop.
	for wantAttempt := 1; wantAttempt <= 3; wantAttempt++ {
		select {
		case gotAttempt := <-attempts:
			if gotAttempt != wantAttempt {
				t.Fatalf("refreshLoop() attempt = %d, want %d", gotAttempt, wantAttempt)
			}
		case <-time.After(time.Second):
			t.Fatalf("refreshLoop() did not make attempt %d", wantAttempt)
		}
	}
	if got := instance.metadata.Load(); got == nil || got.RackId != "rack-1" {
		t.Fatalf("refreshLoop() stored %#v, want rack-1", got)
	}
}

func TestGetDeviceAttributesDuringRefresh(t *testing.T) {
	var fetches atomic.Int64
	instance := newOKEInstance(nil, func(context.Context) (*okeMetadata, error) {
		n := fetches.Add(1)
		// The rack and shape change together on every fetch.
		return &okeMetadata{
			RackId:     fmt.Sprintf("rack-%d", n),
			Shape:      fmt.Sprintf("shape-%d", n),
			RDMAFabric: &rdmaFabric{IPv6: true, Planes: 8},
		}, nil
	})
	instance.refreshInterval = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go instance.refreshLoop(ctx)

	// waitForSnapshot returns once the stored snapshot differs from previous.
	waitForSnapshot := func(previous *okeMetadata) *okeMetadata {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if current := instance.metadata.Load(); current != previous {
				return current
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("refreshLoop() did not store a new snapshot")
		return nil
	}
	waitForSnapshot(nil)

	// Read while the loop keeps storing new snapshots. Every read must see a
	// snapshot, and every value in a read must come from the same snapshot.
	stop := make(chan struct{})
	observed := make([]atomic.Int64, 4)
	var wg sync.WaitGroup
	for i := range observed {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				attributes := instance.GetDeviceAttributes(cloudprovider.DeviceIdentifiers{Name: "dev1"})
				rack, ok := attributes[AttrOKERackId]
				if !ok {
					t.Errorf("GetDeviceAttributes() returned no rack after the first snapshot")
					return
				}
				shape := attributes[AttrOKEShape]
				if want := strings.Replace(*rack.StringValue, "rack-", "shape-", 1); *shape.StringValue != want {
					t.Errorf("GetDeviceAttributes() mixed snapshots: rack %q with shape %q", *rack.StringValue, *shape.StringValue)
					return
				}
				observed[i].Add(1)
			}
		}()
	}
	// Wait until every reader has observed a snapshot, then keep them active
	// across at least one more stored snapshot.
	allObserved := func() bool {
		for i := range observed {
			if observed[i].Load() == 0 {
				return false
			}
		}
		return true
	}
	deadline := time.Now().Add(time.Second)
	for !allObserved() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	waitForSnapshot(instance.metadata.Load())
	close(stop)
	wg.Wait()
	for i := range observed {
		if observed[i].Load() == 0 {
			t.Errorf("reader %d observed no snapshot", i)
		}
	}
}

func TestStartReturnsAfterFirstHostRead(t *testing.T) {
	tests := []struct {
		name         string
		hostResponse string
		want         *okeMetadata
	}{
		{
			name:         "with fabric data",
			hostResponse: `{"networkBlockId":"network-1","rackId":"rack-1","rdmaFabricData":{"ipv6":true,"planes":0}}`,
			want:         &okeMetadata{NetworkBlockId: "network-1", RackId: "rack-1", Shape: "BM.GPU.GB300.4", RDMAFabric: &rdmaFabric{IPv6: true, Planes: 0}},
		},
		{
			name:         "without fabric data",
			hostResponse: `{"networkBlockId":"network-1","rackId":"rack-1"}`,
			want:         &okeMetadata{NetworkBlockId: "network-1", RackId: "rack-1", Shape: "BM.GPU.GB300.4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hostRequests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/host/":
					hostRequests.Add(1)
					_, _ = w.Write([]byte(tt.hostResponse))
				case "/instance/":
					_, _ = w.Write([]byte(`{"shape":"BM.GPU.GB300.4"}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			instance := newOKEInstance(nil, nil)
			instance.initialRetryInterval = time.Millisecond
			instance.initialWait = 2 * time.Second
			instance.refreshInterval = time.Hour

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			startedAt := time.Now()
			got, err := instance.start(ctx, server.Client(), server.URL)
			if err != nil {
				t.Fatalf("start() returned error: %v", err)
			}
			if got != instance {
				t.Fatal("start() did not return the instance")
			}
			// The first successful host read ends the startup poll, with or
			// without fabric data.
			if elapsed := time.Since(startedAt); elapsed >= instance.initialWait {
				t.Fatalf("start() waited %s for metadata", elapsed)
			}
			if got := hostRequests.Load(); got != 1 {
				t.Fatalf("start() made %d host requests, want 1", got)
			}
			if diff := cmp.Diff(tt.want, instance.metadata.Load()); diff != "" {
				t.Errorf("start() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStartReturnsErrorWhenContextIsCancelled(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	instance := newOKEInstance(nil, nil)
	instance.initialRetryInterval = time.Millisecond
	instance.initialWait = time.Second

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := instance.start(ctx, server.Client(), server.URL)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("start() returned error %v, want context.Canceled", err)
	}
	if got != nil {
		t.Fatalf("start() returned %v with an error, want nil", got)
	}
}

func TestStartKeepsPollingWhileIMDSIsDown(t *testing.T) {
	var hostRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/host/" {
			hostRequests.Add(1)
		}
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	instance := newOKEInstance(nil, nil)
	instance.initialRetryInterval = time.Millisecond
	instance.initialWait = 200 * time.Millisecond
	// An hourly background interval keeps the request count to the startup polls.
	instance.refreshInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := instance.start(ctx, server.Client(), server.URL); err != nil {
		t.Fatalf("start() returned error: %v", err)
	}
	if instance.metadata.Load() != nil {
		t.Fatal("start() stored metadata while IMDS was unavailable")
	}
	if got := instance.GetDeviceAttributes(cloudprovider.DeviceIdentifiers{Name: "dev1"}); len(got) != 0 {
		t.Fatalf("GetDeviceAttributes() = %v without metadata, want none", got)
	}
	// A failing endpoint must keep start() polling for the whole window.
	if got := hostRequests.Load(); got < 2 {
		t.Fatalf("start() made %d host requests before giving up, want at least 2", got)
	}
}

func TestStartKeepsShapeWhileHostIsDown(t *testing.T) {
	var hostRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/host/":
			hostRequests.Add(1)
			http.Error(w, "not ready", http.StatusServiceUnavailable)
		case "/instance/":
			_, _ = w.Write([]byte(`{"shape":"BM.GPU.GB300.4"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	instance := newOKEInstance(nil, nil)
	instance.initialRetryInterval = time.Millisecond
	instance.initialWait = 200 * time.Millisecond
	instance.refreshInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startedAt := time.Now()
	if _, err := instance.start(ctx, server.Client(), server.URL); err != nil {
		t.Fatalf("start() returned error: %v", err)
	}
	// The shape alone does not end the startup poll.
	if elapsed := time.Since(startedAt); elapsed < instance.initialWait {
		t.Fatalf("start() returned after %s with the host endpoint down", elapsed)
	}
	if got := hostRequests.Load(); got < 2 {
		t.Fatalf("start() made %d host requests before giving up, want at least 2", got)
	}
	want := map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
		AttrOKEShape: {StringValue: ptr.To("BM.GPU.GB300.4")},
	}
	got := instance.GetDeviceAttributes(cloudprovider.DeviceIdentifiers{Name: "dev1"})
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetDeviceAttributes() mismatch (-want +got):\n%s", diff)
	}
}

func TestStartPicksUpHostAfterShape(t *testing.T) {
	var hostRequests atomic.Int64
	var shapeSeenBeforeHost atomic.Bool
	instance := newOKEInstance(nil, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/host/":
			if hostRequests.Add(1) < 3 {
				// The earlier attempts stored the shape without host data.
				if m := instance.metadata.Load(); m != nil && m.Shape != "" && m.RackId == "" {
					shapeSeenBeforeHost.Store(true)
				}
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"networkBlockId":"network-1","rackId":"rack-1","rdmaFabricData":{"ipv6":true,"planes":0}}`))
		case "/instance/":
			_, _ = w.Write([]byte(`{"shape":"BM.GPU.GB300.4"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	instance.initialRetryInterval = time.Millisecond
	instance.initialWait = 2 * time.Second
	instance.refreshInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startedAt := time.Now()
	if _, err := instance.start(ctx, server.Client(), server.URL); err != nil {
		t.Fatalf("start() returned error: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed >= instance.initialWait {
		t.Fatalf("start() waited %s for metadata", elapsed)
	}
	if !shapeSeenBeforeHost.Load() {
		t.Error("start() did not store the shape while the host endpoint was down")
	}
	want := &okeMetadata{NetworkBlockId: "network-1", RackId: "rack-1", Shape: "BM.GPU.GB300.4", RDMAFabric: &rdmaFabric{IPv6: true, Planes: 0}}
	if diff := cmp.Diff(want, instance.metadata.Load()); diff != "" {
		t.Errorf("start() mismatch (-want +got):\n%s", diff)
	}
}

func TestRefreshLoopRecoversFromOutage(t *testing.T) {
	var available atomic.Bool
	instance := newOKEInstance(nil, func(context.Context) (*okeMetadata, error) {
		if !available.Load() {
			return nil, errors.New("IMDS is not ready")
		}
		return &okeMetadata{NetworkBlockId: "network-1", RackId: "rack-1", Shape: "VM.Standard.E5.Flex"}, nil
	})
	instance.refreshInterval = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go instance.refreshLoop(ctx)

	// Failed refreshes leave the snapshot empty, and the loop keeps trying.
	time.Sleep(10 * time.Millisecond)
	if instance.metadata.Load() != nil {
		t.Fatal("refreshLoop() stored metadata from a failing fetch")
	}

	available.Store(true)
	deadline := time.Now().Add(time.Second)
	for instance.metadata.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	want := &okeMetadata{NetworkBlockId: "network-1", RackId: "rack-1", Shape: "VM.Standard.E5.Flex"}
	if diff := cmp.Diff(want, instance.metadata.Load()); diff != "" {
		t.Errorf("metadata after recovery mismatch (-want +got):\n%s", diff)
	}
}
