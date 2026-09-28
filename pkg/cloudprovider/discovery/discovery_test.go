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

package discovery

import (
	"bytes"
	"context"
	"flag"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/klog/v2"
	"sigs.k8s.io/dranet/pkg/cloudprovider/coreweave"
)

func TestCloudProviderProbeOrder(t *testing.T) {
	probes := cloudProviderProbes(context.Background(), "", Dependencies{})
	want := []CloudProviderHint{
		CloudProviderHintGCE,
		CloudProviderHintAWS,
		CloudProviderHintAzure,
		CloudProviderHintOKE,
		CloudProviderHintAlibaba,
		CloudProviderHintCKS,
		CloudProviderHintWebhook,
	}

	if len(probes) != len(want) {
		t.Fatalf("cloudProviderProbes() returned %d probes, want %d", len(probes), len(want))
	}
	for i := range want {
		if probes[i].hint != want[i] {
			t.Errorf("cloudProviderProbes()[%d].hint = %q, want %q", i, probes[i].hint, want[i])
		}
	}
}

func TestDetectCloudProviderReturnsFirstMatch(t *testing.T) {
	probes := []cloudProviderProbe{
		{hint: CloudProviderHintAzure, match: func() bool { return true }},
		{hint: CloudProviderHintCKS, match: func() bool { return true }},
	}

	if got := detectCloudProvider(probes); got != CloudProviderHintAzure {
		t.Fatalf("detectCloudProvider() = %q, want %q", got, CloudProviderHintAzure)
	}
}

func TestGetInstancePropertiesCKS(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "cks-node",
		Labels: map[string]string{
			coreweave.LabelCKSCluster:   "use15",
			coreweave.LabelFabricFlavor: "infiniband",
			coreweave.LabelFabric:       "US-EAST-15A-FAB66",
		},
	}}
	dependencies := Dependencies{
		NodeClient: fake.NewSimpleClientset(node).CoreV1().Nodes(),
		NodeName:   node.Name,
	}

	instance, err := GetInstanceProperties(context.Background(), CloudProviderHintCKS, "", dependencies)
	if err != nil {
		t.Fatalf("GetInstanceProperties() error = %v", err)
	}
	if _, ok := instance.(*coreweave.Instance); !ok {
		t.Fatalf("GetInstanceProperties() = %T, want *coreweave.Instance", instance)
	}
}

func TestGetInstancePropertiesCKSRequiresDependencies(t *testing.T) {
	if _, err := GetInstanceProperties(context.Background(), CloudProviderHintCKS, "", Dependencies{}); err == nil {
		t.Fatal("GetInstanceProperties() error = nil, want missing Kubernetes dependency error")
	}
}

// captureLogs routes klog output to a buffer for one test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	state := klog.CaptureState()
	t.Cleanup(state.Restore)
	var flags flag.FlagSet
	klog.InitFlags(&flags)
	if err := flags.Set("logtostderr", "false"); err != nil {
		t.Fatalf("could not set klog flag logtostderr: %v", err)
	}
	var buf bytes.Buffer
	klog.SetOutput(&buf)
	return &buf
}

// Without a hint, discovery can pick another provider or none. The options
// of the other providers then configure nothing, and DRANET keeps running.
func TestGetInstancePropertiesIgnoresOptionsOfAnotherProvider(t *testing.T) {
	logs := captureLogs(t)
	options := ProviderOptions{CloudProviderHintOKE: {"rdma-child-ipv4-cidr": "10.192.0.0/14"}}
	namespaces := []string{"oke"}
	for _, hint := range testHints() {
		options[hint] = map[string]string{"a": "1"}
		namespaces = append(namespaces, namespace(hint))
	}
	instance, err := GetInstanceProperties(context.Background(), CloudProviderHintNone, "", Dependencies{ProviderOptions: options})
	klog.Flush()
	if err != nil || instance != nil {
		t.Errorf("GetInstanceProperties() = %v, %v, want nil, nil", instance, err)
	}
	last := -1
	for _, ns := range namespaces {
		at := strings.Index(logs.String(), "Ignoring the "+ns+`.* cloud provider options, because the cloud provider is "NONE"`)
		if at <= last {
			t.Fatalf("GetInstanceProperties() logged %q, want one warning per provider in the order %v", logs.String(), namespaces)
		}
		last = at
	}
}

// The OKE case parses its own options, so a bad key fails before any IMDS read.
func TestGetInstancePropertiesParsesOKEOptions(t *testing.T) {
	options := ProviderOptions{CloudProviderHintOKE: {"bogus": "1"}}
	instance, err := GetInstanceProperties(context.Background(), CloudProviderHintOKE, "", Dependencies{ProviderOptions: options})
	if want := `oke options: unknown option "bogus"`; err == nil || err.Error() != want {
		t.Errorf("GetInstanceProperties() error = %v, want %q", err, want)
	}
	if instance != nil {
		t.Errorf("GetInstanceProperties() = %v, want nil", instance)
	}
}
