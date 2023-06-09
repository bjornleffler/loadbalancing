package utils

// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
