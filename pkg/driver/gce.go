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
	"encoding/json"

	"cloud.google.com/go/compute/metadata"

	"k8s.io/klog/v2"
)

type gceNetworkInterface struct {
	IPv4    string   `json:"ip,omitempty"`
	IPv6    []string `json:"ipv6,omitempty"`
	Mac     string   `json:"mac,omitempty"`
	MTU     int      `json:"mtu,omitempty"`
	Network string   `json:"network,omitempty"`
}

func getInstanceNetworkInterfaces(ctx context.Context) []gceNetworkInterface {
	// Get google compute instance metadata for network interfaces
	// https://cloud.google.com/compute/docs/metadata/predefined-metadata-keys
	var gceInterfaces []gceNetworkInterface
	if !metadata.OnGCE() {
		return gceInterfaces
	}

	instanceName, err := metadata.InstanceNameWithContext(ctx)
	if err != nil {
		klog.Infof("could not get instance name on GCE .... skipping GCE network interface attributes: %v", err)
	} else {
		klog.Infof("Getting GCE network interface attributes for instance %s", instanceName)
	}

	// TODO Check accelerator type machines
	instanceType, err := metadata.GetWithContext(ctx, "instance/machine-type")
	if err != nil {
		klog.Infof("could not get instance type on GCE .... skipping GCE network interface attributes: %v", err)
	} else {
		klog.Infof("Getting GCE accelerator attributes for instance type %s", instanceType)
	}

	//  curl "http://metadata.google.internal/computeMetadata/v1/instance/network-interfaces/?recursive=true" -H "Metadata-Flavor: Google"
	// [{"accessConfigs":[{"externalIp":"35.225.164.134","type":"ONE_TO_ONE_NAT"}],"dnsServers":["169.254.169.254"],"forwardedIps":[],"gateway":"10.128.0.1","ip":"10.128.0.70","ipAliases":["10.24.3.0/24"],"mac":"42:01:0a:80:00:46","mtu":1460,"network":"projects/628944397724/networks/default","subnetmask":"255.255.240.0","targetInstanceIps":[]},{"accessConfigs":[{"externalIp":"","type":"ONE_TO_ONE_NAT"}],"dnsServers":["169.254.169.254"],"forwardedIps":[],"gateway":"192.168.1.1","ip":"192.168.1.2","ipAliases":[],"mac":"42:01:c0:a8:01:02","mtu":8244,"network":"projects/628944397724/networks/aojea-dra-net-1","subnetmask":"255.255.255.0","targetInstanceIps":[]},{"accessConfigs":[{"externalIp":"","type":"ONE_TO_ONE_NAT"}],"dnsServers":["169.254.169.254"],"forwardedIps":[],"gateway":"192.168.2.1","ip":"192.168.2.2","ipAliases":[],"mac":"42:01:c0:a8:02:02","mtu":8244,"network":"projects/628944397724/networks/aojea-dra-net-2","subnetmask":"255.255.255.0","targetInstanceIps":[]},{"accessConfigs":[{"externalIp":"","type":"ONE_TO_ONE_NAT"}],"dnsServers":["169.254.169.254"],"forwardedIps":[],"gateway":"192.168.3.1","ip":"192.168.3.2","ipAliases":[],"mac":"42:01:c0:a8:03:02","mtu":8244,"network":"projects/628944397724/networks/aojea-dra-net-3","subnetmask":"255.255.255.0","targetInstanceIps":[]},{"accessConfigs":[{"externalIp":"","type":"ONE_TO_ONE_NAT"}],"dnsServers":["169.254.169.254"],"forwardedIps":[],"gateway":"192.168.4.1","ip":"192.168.4.2","ipAliases":[],"mac":"42:01:c0:a8:04:02","mtu":8244,"network":"projects/628944397724/networks/aojea-dra-net-4","subnetmask":"255.255.255.0","targetInstanceIps":[]}]
	gceInterfacesRaw, err := metadata.GetWithContext(ctx, "instance/network-interfaces/?recursive=true&alt=json")
	if err != nil {
		klog.Infof("could not get network interfaces on GCE .... skipping GCE network interface attributes: %v", err)
	} else {
		klog.Infof("Getting GCE accelerator attributes for instance type %s", instanceType)
		if err = json.Unmarshal([]byte(gceInterfacesRaw), &gceInterfaces); err != nil {
			klog.Infof("could not get network interfaces on GCE .... skipping GCE network interface attributes: %v", err)
		}
	}
	return gceInterfaces
}
