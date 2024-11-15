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
	"context"
	"fmt"
	"os/exec"

	"github.com/containerd/nri/pkg/api"
	resourceapi "k8s.io/api/resource/v1alpha3"
	"k8s.io/klog/v2"
)

func getNetworkNamespace(pod *api.PodSandbox) string {
	// get the pod network namespace
	for _, namespace := range pod.Linux.GetNamespaces() {
		if namespace.Type == "network" {
			return namespace.Path
		}
	}
	return "<host-network>"
}

// POD LIFECYCLE HOOKS

// Pod Start runs AFTER CNI ADD and BEFORE the containers are created
// It runs in the Container Runtime network namespace and receives as paremeters:
// - the Pod network namespace path
// - the ResourceClaim AllocationResult
func podStartHook(ctx context.Context, netns string, allocation resourceapi.AllocationResult) error {
	// Process the configurations of the ResourceClaim
	for _, config := range allocation.Devices.Config {
		if config.Opaque == nil {
			continue
		}
		klog.V(4).Infof("podStartHook Configuration %s", config.Opaque.Parameters.String())
		// TODO get config options here, it can add ips or commands
		// to add routes, run dhcp, rename the interface ... whatever
	}
	// Process the configurations of the ResourceClaim
	for _, result := range allocation.Devices.Results {
		if result.Driver != driverName {
			continue
		}
		klog.V(4).Infof("podStartHook Device %s", result.Device)
		// ------------------------------------------------------------------
		// EXAMPLE: move interface by name to the specified network namespace
		// ------------------------------------------------------------------
		// TODO see https://github.com/containernetworking/plugins/tree/main/plugins/main
		// for better examples of low level implementations using netlink for more complex
		// scenarios like host-device, ipvlan, macvlan, ...
		cmd := exec.Command("ip", "link", "set", result.Device, "netns", netns)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to move interface %s to namespace %s: %w", result.Device, netns, err)
		}
	}
	return nil
}

// Pod Stop runs on Pod deletion, Pod deletion shoud be best effort, is recommended
// to avoid returning an error in this hook.
// It runs in the Container Runtime network namespace and receives as paremeters:
// - the Pod network namespace path
// - the ResourceClaim allocation
func podStopHook(ctx context.Context, netns string, allocation resourceapi.AllocationResult) error {
	// Process the configurations of the ResourceClaim
	for _, config := range allocation.Devices.Config {
		if config.Opaque == nil {
			continue
		}
		klog.V(4).Infof("podStopHook Configuration %s", config.Opaque.Parameters.String())
		// TODO get config options here, it can add ips or commands
		// to add routes, run dhcp, rename the interface ... whatever
	}
	// Process the configurations of the ResourceClaim
	for _, result := range allocation.Devices.Results {
		if result.Driver != driverName {
			continue
		}
		klog.V(4).Infof("podStopHook Device %s", result.Device)
		// TODO get config options here, it can add ips or commands
		// to add routes, run dhcp, rename the interface ... whatever
	}
	return nil
}
