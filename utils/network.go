package utils

import (
	"net"
	"net/netip"
)

func increment(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] > 0 {
			break
		}
	}
}

func ExpandNetworkPrefix(prefix string) (addrs []netip.Addr, err error) {
	ipv4, ipv4Net, err := net.ParseCIDR(prefix)
	if err != nil {
		return addrs, err
	}

	for ip := ipv4.Mask(ipv4Net.Mask); ipv4Net.Contains(ip); increment(ip) {
		if addr, ok := netip.AddrFromSlice(ip); ok {
			addrs = append(addrs, addr)
		}
	}
	return addrs, nil
}
