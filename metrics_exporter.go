package main

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

// Metrics Exporter exports prometheus metrics to facilitate advanced load balancing.

// This is currently mostly useless, as the kernel NFS server doesn't report the
// number of connections. This makes NFS session balancing impossible with the
// kernel NFS server.

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/cakturk/go-netstat/netstat"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rafacas/sysstats"
	"golang.org/x/exp/slices"
)

const (
	Prefix = "metrics_exporter_"
	// Nfs4Port    = 2049
	DefaultPort = 9001
)

var (
	cpuUsagePercent = promauto.NewGauge(prometheus.GaugeOpts{
		Name: Prefix + "cpu_usage_percent",
		Help: "CPU usage in percent.",
	})
	memoryUsagePercent = promauto.NewGauge(prometheus.GaugeOpts{
		Name: Prefix + "memory_usage_percent",
		Help: "Memory usage in percent.",
	})
	systemLoad = promauto.NewGauge(prometheus.GaugeOpts{
		Name: Prefix + "system_load",
		Help: "System load (number of waiting threads).",
	})
	ingressTcpTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: Prefix + "ingress_tcp_connections_total",
		Help: "Total number of ingress TCP connections.",
	})
	egressTcpTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: Prefix + "egress_tcp_connections_total",
		Help: "Total number of egress TCP connections.",
	})
	ingressTcpByPort = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: Prefix + "ingress_tcp_connections_by_port",
		Help: "Total number of ingress TCP connections, per port",
	}, []string{"port"})
	egressTcpByPort = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: Prefix + "egress_tcp_connections_by_port",
		Help: "Number of egress TCP connections, per port.",
	}, []string{"port"})
	// nfsConnections = promauto.NewGauge(prometheus.GaugeOpts{
	//	Name: Prefix + "nfs_connections_total",
	//	Help: "Total number of inbound NFS TCP connections.",
	//})
	//nfs4Connections = promauto.NewGauge(prometheus.GaugeOpts{
	//	Name: Prefix + "nfs_v4_connections_total",
	//	Help: "Total number of inbound NFSv4 TCP connections.",
	//})
)

func getCPUPercent() (float64, error) {
	average, err := sysstats.GetCpuStatsInterval(1)
	if err != nil {
		return 0, err
	}
	// Use total used cpu as the indicator of how busy the system is.
	cpu := average["cpu"]
	// log.Printf("CPU Total: %v%%", cpu["total"])
	return cpu["total"], nil
}

func getMemoryPercent() (float64, error) {
	stats, err := sysstats.GetMemStats()
	if err != nil {
		return 0, err
	}
	used := float64(stats["memtotal"] - stats["realfree"])
	usagePercent := 100.0 * used / float64(stats["memtotal"])
	// log.Printf("Memory usage: %v%%", usagePercent)
	return usagePercent, nil
}

func getLoad() (sysstats.LoadAvg, error) {
	load, err := sysstats.GetLoadAvg()
	if err != nil {
		return load, err
	}
	return load, nil
}

func listLocalIPs() (ips []netip.Addr, err error) {
	ifaces, err := net.Interfaces()
	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			return ips, err
		}
		for _, addr := range addrs {
			switch v := addr.(type) {
			case *net.IPNet:
				if ip, ok := netip.AddrFromSlice(v.IP); ok {
					ips = append(ips, ip)
				}
			case *net.IPAddr:
				if ip, ok := netip.AddrFromSlice(v.IP); ok {
					ips = append(ips, ip)
				}
			}
		}
	}
	return ips, err
}

func getTcpCounts() (ingress, egress map[uint16]int64, err error) {
	// Filter for established not loopback connections.
	establishedNotLoopback := func(s *netstat.SockTabEntry) bool {
		return s.State == netstat.Established && !s.LocalAddr.IP.IsLoopback()
	}
	// List established sockets.
	socks4, err := netstat.TCPSocks(establishedNotLoopback)
	if err != nil {
		return ingress, egress, err
	}
	socks6, err := netstat.TCP6Socks(establishedNotLoopback)
	if err != nil {
		return ingress, egress, err
	}
	socks := append(socks4, socks6...)

	localIPs, err := listLocalIPs()
	if err != nil {
		return ingress, egress, err
	}

	ingress = map[uint16]int64{}
	egress = map[uint16]int64{}
	for _, s := range socks {
		ip, ok := netip.AddrFromSlice(s.LocalAddr.IP)
		if !ok {
			return ingress, egress, fmt.Errorf("Failed to parse %v", s.LocalAddr.IP)
		}
		if slices.Contains(localIPs, ip) {
			egress[s.RemoteAddr.Port] += 1
		} else {
			ingress[s.LocalAddr.Port] += 1
		}
	}
	return ingress, egress, nil
}

func exportMetrics() {
	go func() {
		for {
			cpu, err := getCPUPercent()
			if err != nil {
				log.Printf("Error getting CPU usage: %v", err)
			}
			cpuUsagePercent.Set(cpu)

			memory, err := getMemoryPercent()
			if err != nil {
				log.Printf("Error getting Memory usage: %v", err)
			}
			memoryUsagePercent.Set(memory)

			load, err := getLoad()
			if err != nil {
				log.Printf("Error getting Load value: %v", err)
			}
			systemLoad.Set(load.Avg1)

			// TCP connections.
			ingress, egress, err := getTcpCounts()
			if err != nil {
				log.Printf("Error getting Load value: %v", err)
			}
			ingressTotal, egressTotal := 0, 0
			for k, v := range ingress {
				ingressTotal += 1
				port := strconv.FormatUint(uint64(k), 10)
				ingressTcpByPort.WithLabelValues(port).Set(float64(v))
			}
			for k, v := range egress {
				egressTotal += 1
				port := strconv.FormatUint(uint64(k), 10)
				egressTcpByPort.WithLabelValues(port).Set(float64(v))
			}
			ingressTcpTotal.Set(float64(ingressTotal))
			egressTcpTotal.Set(float64(egressTotal))
			//nfs4 := float64(ingress[Nfs4Port])
			//nfs4Connections.Set(nfs4)
			//nfsConnections.Set(nfs4)

			time.Sleep(15 * time.Second)
		}
	}()
}

func main() {
	port := DefaultPort
	fs := flag.CommandLine
	fs.IntVar(&port, "p", DefaultPort, "TCP port for metrics export.")
	flag.Parse()
	log.Printf("Start Metrics Exporter on port %d", port)
	exportMetrics()
	http.Handle("/metrics", promhttp.Handler())
	err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
	log.Printf("Failed to start Metrics Exporter: %v", err)
}
