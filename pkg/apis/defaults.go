package apis

import (
	"hash/fnv"
)

// Default applies default values to the NetworkConfig.
func (c *NetworkConfig) Default() {
	c.Interface.Default()
	if c.Interface.Type == InterfaceTypeIPVLAN {
		if c.Interface.IPVlan == nil {
			c.Interface.IPVlan = &IPVlanConfig{}
		}
		c.Interface.IPVlan.Default()
	}
	if c.Interface.VRF != nil {
		c.Interface.VRF.Default()
	}
}

// Default applies default values to the InterfaceConfig.
func (c *InterfaceConfig) Default() {
	// Fold the deprecated DHCP field into Addressing when Addressing is unset.
	if c.Addressing == "" && c.DHCP != nil && *c.DHCP {
		c.Addressing = AddressingModeDHCP
	}
}

// Default applies default values to the VRFConfig.
func (c *VRFConfig) Default() {
	if c.Table == nil && c.Name != "" {
		// Derive a deterministic table ID from the VRF name to ensure interfaces
		// joining the same VRF automatically share the same table ID.
		h := fnv.New32a()
		h.Write([]byte(c.Name))
		// Use the constant from this package
		tableID := int((h.Sum32() % 1000) + RouteTableOffset)
		c.Table = &tableID
	}
}

// Default applies default values to the IPVlanConfig.
func (c *IPVlanConfig) Default() {
	if c.Mode == "" {
		c.Mode = IPVlanModeL2
	}
	if c.Flag == "" {
		c.Flag = IPVlanFlagBridge
	}
}
