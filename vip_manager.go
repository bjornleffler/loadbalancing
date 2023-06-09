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

// VIP Manager is a utility to manage a set of virtual ("alias") IPs with a
// GCE Managed Instance Group.

import (
	"flag"
	"log"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/bjornleffler/loadbalancing/utils"
)

type Config struct {
	Gcp          *utils.GcpConfig
	VIPs         []string
	Workers      uint
	SleepSeconds uint
}

const (
	DefaultWorkers      = 10
	DefaultSleepSeconds = 10
	DefaultWaitSeconds  = 60
)

var (
	cfg = Config{
		Gcp: &utils.GcpConfig{},
	}
)

func parseArgs() *Config {
	vips := ""
	fs := flag.CommandLine
	fs.StringVar(&cfg.Gcp.Project, "project", "", "GCP project name.")
	fs.StringVar(&cfg.Gcp.Zone, "zone", "", "GCE zone name.")
	fs.StringVar(&cfg.Gcp.GceInstanceGroup, "gce_instance_group", "", "GCE instance group.")
	fs.StringVar(&cfg.Gcp.AliasNetwork, "alias_network", "", "Alias network name.")
	fs.StringVar(&vips, "vips", "", "Virtual IPv4 addresses, specified as list of ips or prefixes.")
	fs.UintVar(&cfg.Workers, "workers", DefaultWorkers, "Worker: max concurrent requests.")
	fs.UintVar(&cfg.SleepSeconds, "sleep", DefaultSleepSeconds, "Seconds to sleep during inactivity.")
	fs.UintVar(&cfg.Gcp.WaitSeconds, "wait", DefaultWaitSeconds, "Seconds to wait for changes to occur.")
	flag.Parse()
	cfg.VIPs = parseVIPs(vips)
	return &cfg
}

func checkArgs(cfg *Config) {
	if cfg.Gcp.Zone == "" {
		log.Fatalf("Please specify GCE zone using -zone")
	}
	if cfg.Gcp.GceInstanceGroup == "" {
		log.Fatalf("Please specify GCE instance group using -gce_instance_group")
	}
	if cfg.Gcp.AliasNetwork == "" {
		log.Fatalf("Please specify alias network group using -alias_network")
	}
	if len(cfg.VIPs) == 0 {
		log.Fatalf("Please specify virtual ips using -vips")
	}
	if cfg.Workers == 0 {
		cfg.Workers = 1
	}
}

func parseVIPs(input string) []string {
	input = strings.ReplaceAll(input, ",", " ")
	addrs := []netip.Addr{}
	for _, network := range strings.Split(input, " ") {
		if network == "" {
			continue
		}
		// Try parsing as a single IP.
		ip, err := netip.ParseAddr(network)
		if err == nil {
			addrs = append(addrs, ip)
			continue
		}

		// If that didn't work, parse as network prefix: a.b.c.d/e
		ips, err := utils.ExpandNetworkPrefix(network)
		if err != nil {
			log.Fatalf("Failed to parse prefix: %v", network)
		}
		addrs = append(addrs, ips...)
	}
	// Sort for readability. Not strictly necessary.
	sort.Slice(addrs, func(i, j int) bool {
		return addrs[i].Compare(addrs[j]) < 1
	})
	ips := []string{}
	for _, addr := range addrs {
		ips = append(ips, addr.String())
	}
	return ips
}

func PrintConfig(cfg *Config) {
	log.Printf("Configuration:")
	log.Printf(" - GCP project: %v", cfg.Gcp.Project)
	log.Printf(" - GCE zone: %v", cfg.Gcp.Zone)
	log.Printf(" - Virtual IPs: %v", cfg.VIPs)
	log.Printf(" - Worker: %v", cfg.Workers)
	log.Printf(" - Wait seconds: %v", cfg.Gcp.WaitSeconds)
}

func PrintInstances(cfg *Config) {
	instances, err := utils.GetInstancesFromMIG(cfg.Gcp)
	if err != nil {
		log.Printf("Error getting instances: %v", err)
		return
	}
	log.Printf("Current state:")
	for name, instance := range instances {
		log.Printf(" - Instance: %s ips: %v", name, *instance.AliasIps)
	}
}

func minAliasIps(instances map[string]*utils.GceInstance, operations map[string]utils.Operation) int {
	min := -1
	for name, instance := range instances {
		ips := len(*instance.AliasIps) + len(operations[name].Ips)
		if min < 0 || ips < min {
			min = ips
		}
	}
	return min
}

func GetSpareIps(cfg *Config, instances map[string]*utils.GceInstance) []string {
	used := map[string]bool{}
	for _, ip := range cfg.VIPs {
		used[ip] = false
	}
	for _, instance := range instances {
		for _, ip := range *instance.AliasIps {
			if _, ok := used[ip]; ok {
				used[ip] = true
			}
		}
	}
	spare := []string{}
	for ip, inuse := range used {
		if !inuse {
			spare = append(spare, ip)
		}
	}
	if len(spare) > 0 {
		log.Printf("Spare IPs: %v", spare)
	}
	return spare
}

// Return number of operations executed.
func AllocateIps(cfg *Config) int {
	instances, err := utils.GetInstancesFromMIG(cfg.Gcp)
	if err != nil {
		log.Printf("Error getting instances: %v", err)
		return 0
	}
	spare := GetSpareIps(cfg, instances)
	if len(spare) == 0 || len(instances) == 0 {
		return 0
	}
	operations := map[string]utils.Operation{}
	for name, instance := range instances {
		operations[name] = utils.Operation{
			Type:     utils.Add,
			Instance: instance,
			Ips:      []string{},
		}
	}
	// Assign spare IPs to instances with min number of IPs.
	for _, ip := range spare {
		min := minAliasIps(instances, operations)
		for name, instance := range instances {
			if len(*instance.AliasIps)+len(operations[name].Ips) == min {
				// Workaround for golang not supporting map[value].Thing = ...
				if operation, ok := operations[name]; ok {
					operation.Ips = append(operation.Ips, ip)
					operations[name] = operation
				}
				break
			}
		}
	}
	return utils.ExecuteParallel(cfg.Gcp, operations)
}

func minValue(values map[string]int) int {
	min := -1
	for _, v := range values {
		if min < 0 || v < min {
			min = v
		}
	}
	return min
}

func maxValue(values map[string]int) int {
	max := 0
	for _, v := range values {
		if max < v {
			max = v
		}
	}
	return max
}

func ReduceIps(cfg *Config) int {
	instances, err := utils.GetInstancesFromMIG(cfg.Gcp)
	if err != nil {
		log.Printf("Error getting instances: %v", err)
		return 0
	}
	if len(instances) == 0 {
		return 0
	}
	target := map[string]int{}
	for name, instance := range instances {
		ips := len(*instance.AliasIps)
		target[name] = ips
		if ips == 0 {
			log.Printf("Detected new instance: %s", name)
		}
	}

	// "Robin Hood" algorithm: Take from the rich and give to the poor,
	// until the difference is small enough: less than 2.
	min := minValue(target)
	max := maxValue(target)
	for max-min > 1 {
		// Decrement target for an instance with max targets
		for name, v := range target {
			if v == max {
				target[name] = target[name] - 1
				break
			}
		}
		// Increment target for an instance with min targets
		for name, v := range target {
			if v == min {
				target[name] = target[name] + 1
				break
			}
		}
		min = minValue(target)
		max = maxValue(target)
	}

	// Generate operations.
	operations := map[string]utils.Operation{}
	for name, instance := range instances {
		reduction := len(*instance.AliasIps) - target[name]
		if reduction > 0 {
			ips := *instance.AliasIps
			operations[name] = utils.Operation{
				Type:     utils.Remove,
				Instance: instance,
				Ips:      ips[:reduction],
			}
		}
	}
	return utils.ExecuteParallel(cfg.Gcp, operations)
}

func main() {
	// Configure and print initial state.
	log.Printf("Start VIP Manager.")
	cfg := parseArgs()
	utils.ConnectCompute()
	utils.ChooseProject(cfg.Gcp)
	utils.ChooseZone(cfg.Gcp)
	checkArgs(cfg)
	utils.StartWorkers(cfg.Gcp, cfg.Workers)
	PrintConfig(cfg)
	PrintInstances(cfg)

	// Main logic:
	// 1. Allocate unused / spare IPs.
	// 2. Remove IPs from nodes with too many IPs.
	// 3. Sleep when there is nothing to do.
	for {
		changes := AllocateIps(cfg)
		changes += ReduceIps(cfg)
		if changes > 0 {
			PrintInstances(cfg)
		} else {
			time.Sleep(time.Duration(cfg.SleepSeconds) * time.Second)
		}
	}
}
