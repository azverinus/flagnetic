// Package subnet groups client addresses the way limits count them.
package subnet

import "net/netip"

// Of returns the group a client address is counted in: an IPv4 address
// alone, an IPv6 address with the rest of its /64, which one subscriber
// usually holds whole and can rotate through at will. Without an address,
// the group is the unspecified one.
func Of(ip netip.Addr) netip.Prefix {
	ip = ip.Unmap()
	switch {
	case !ip.IsValid():
		return netip.PrefixFrom(netip.IPv4Unspecified(), 32)
	case ip.Is4():
		return netip.PrefixFrom(ip, 32)
	}
	p, _ := ip.WithZone("").Prefix(64)
	return p
}
