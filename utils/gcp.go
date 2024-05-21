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
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"cloud.google.com/go/compute/metadata"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
)

type GcpConfig struct {
	Project          string
	Zone             string
	GceInstanceGroup string
	AliasNetwork     string
	WaitSeconds      uint
}

var (
	ctx            = context.Background()
	computeService *compute.Service
)

type GceInstance struct {
	Name               string
	NetworkInterface   string
	NetworkFingerprint string
	AliasNetwork       string
	AliasIps           *[]string
	OtherNetworks      []Network
}

type Network struct {
	Name string
	Cidr string
}

func ConnectCompute() {
	c, err := google.DefaultClient(ctx, compute.CloudPlatformScope)
	if err != nil {
		log.Printf("Error getting Default GCP client: %v", err)
	}
	computeService, err = compute.New(c)
	if err != nil {
		log.Fatalf("Error conencting to GCP compute service: %v", err)
		os.Exit(1)
	}
}

// GetProject gets the GCP project ID from GCP credentials.
func ChooseProject(cfg *GcpConfig) {
	if cfg.Project != "" {
		return
	}
	log.Printf("Get project from GCP credentials.")
	credentials, err :=
		google.FindDefaultCredentials(ctx, compute.ComputeScope)
	// TODO(leffler): Explain how to specify credentials.
	msg := "Failed to get project id. Please specify using command line."
	if err != nil {
		log.Fatalf("%s err: %v\n", msg, err)
	}
	cfg.Project = credentials.ProjectID
}

func ChooseZone(cfg *GcpConfig) {
	if cfg.Zone != "" {
		return
	}
	cfg.Zone, _ = metadata.Zone()
}

func ListInstanceGroups(cfg *GcpConfig) (names []string, err error) {
	req := computeService.InstanceGroups.List(cfg.Project, cfg.Zone)
	err = req.Pages(ctx, func(page *compute.InstanceGroupList) error {
		for _, instanceGroup := range page.Items {
			names = append(names, instanceGroup.Name)
		}
		return nil
	})
	if err != nil {
		log.Printf("Error listing instance groups: %v", err)
		return names, err
	}
	return names, nil
}

func ListInstancesInGroup(cfg *GcpConfig) (names []string, err error) {
	rb := &compute.InstanceGroupsListInstancesRequest{
		InstanceState: "RUNNING",
	}
	req := computeService.InstanceGroups.ListInstances(cfg.Project, cfg.Zone, cfg.GceInstanceGroup, rb)
	err = req.Pages(ctx, func(page *compute.InstanceGroupsListInstances) error {
		for _, instance := range page.Items {
			url := strings.Split(instance.Instance, "/")
			names = append(names, url[len(url)-1])
		}
		return nil
	})
	if err != nil {
		log.Printf("Error listing instances: %v", err)
		return names, err
	}
	return names, nil
}

func GetInstance(cfg *GcpConfig, name string) (*GceInstance, error) {
	resp, err := computeService.Instances.Get(cfg.Project, cfg.Zone, name).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("Error getting instance %s: %v", name, err)
	}
	instance := GceInstance{
		Name:     resp.Name,
		AliasIps: &[]string{},
	}
	interfaces := resp.NetworkInterfaces
	for _, i := range interfaces {
		instance.NetworkInterface = i.Name
		instance.NetworkFingerprint = i.Fingerprint
		for _, alias := range i.AliasIpRanges {
			if alias.SubnetworkRangeName == cfg.AliasNetwork {
				// Manage our alias network.
				instance.AliasNetwork = alias.SubnetworkRangeName
				ips, err := ExpandNetworkPrefix(alias.IpCidrRange)
				if err != nil {
					log.Printf("Failed to expand network prefix: %v", err)
				}
				for _, ip := range ips {
					*instance.AliasIps = append(*instance.AliasIps, ip.String())
				}
			} else {
				// Track other alias networks.
				network := Network{
					Name: alias.SubnetworkRangeName,
					Cidr: alias.IpCidrRange,
				}
				instance.OtherNetworks = append(instance.OtherNetworks, network)
			}
		}
	}
	return &instance, nil
}

func GetInstancesFromMIG(cfg *GcpConfig) (map[string]*GceInstance, error) {
	instances := map[string]*GceInstance{}
	names, err := ListInstancesInGroup(cfg)
	if err != nil {
		log.Printf("Error listing instances in group: %v", err)
		return instances, err
	}
	for _, name := range names {
		instance, err := GetInstance(cfg, name)
		if err != nil {
			log.Printf("Error getting instance: %v", err)
			continue
		}
		instances[name] = instance
	}
	return instances, nil
}

func UpdateAliasIPs(cfg *GcpConfig, instance *GceInstance, ips []string) error {
	ipRanges := []*compute.AliasIpRange{}
	for _, ip := range ips {
		ipRanges = append(ipRanges, &compute.AliasIpRange{
			IpCidrRange:         ip + "/32",
			SubnetworkRangeName: cfg.AliasNetwork,
		})
	}
	for _, network := range instance.OtherNetworks {
		ipRanges = append(ipRanges, &compute.AliasIpRange{
			IpCidrRange:         network.Cidr,
			SubnetworkRangeName: network.Name,
		})
	}
	rb := &compute.NetworkInterface{
		Fingerprint:   instance.NetworkFingerprint,
		AliasIpRanges: ipRanges,
	}

	_, err := computeService.Instances.UpdateNetworkInterface(
		cfg.Project, cfg.Zone, instance.Name, instance.NetworkInterface, rb).Context(ctx).Do()

	if err != nil {
		log.Printf("Error updating network interfaces: %v", err)
		return err
	}

	// TODO: Change code below to process the `resp` object:
	// fmt.Printf("Response: %#v\n", resp)
	return nil
}
