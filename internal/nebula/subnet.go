package nebula

import (
	"fmt"
	"net/netip"
)

// SubnetIP returns a specific host address within a /24 subnet.
// hostIndex 1 = first usable host (e.g. 10.42.1.1 for pulumi-ui),
// hostIndex 2 = second host (e.g. 10.42.1.2 for the first agent), etc.
func SubnetIP(subnet string, hostIndex int) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(subnet)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("parse subnet %q: %w", subnet, err)
	}
	base := prefix.Addr().As4()
	base[3] = byte(hostIndex)
	addr := netip.AddrFrom4(base)
	return netip.PrefixFrom(addr, 24), nil
}

// UIAddress returns the pulumi-ui Nebula IP within the stack's subnet (.1).
func UIAddress(subnet string) (netip.Prefix, error) {
	return SubnetIP(subnet, 1)
}

// AgentAddress returns the first agent Nebula IP within the stack's subnet (.2).
func AgentAddress(subnet string) (netip.Prefix, error) {
	return SubnetIP(subnet, 2)
}
