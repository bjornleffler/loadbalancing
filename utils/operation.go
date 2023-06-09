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

// Operation abstracts operations to add/remove alias IPs to GCE VMs.

import (
	"log"
	"time"

	"golang.org/x/exp/slices"
)

type Type int

const (
	Add Type = iota
	Remove
)

var (
	in  = make(chan Operation)
	out = make(chan int)
)

type Operation struct {
	Type     Type
	Instance *GceInstance
	Ips      []string
}

func (t Type) String() string {
	switch t {
	case Add:
		return "Add"
	case Remove:
		return "Remove"
	default:
		return "Unknown"
	}
}

func StartWorkers(cfg *GcpConfig, workers uint) {
	for i := 0; i < int(workers); i++ {
		go Worker(i, cfg, in, out)
	}
}

func Worker(i int, cfg *GcpConfig, in chan Operation, out chan int) {
	for {
		operation := <-in
		out <- Execute(cfg, operation)
	}
}

func ExecuteParallel(cfg *GcpConfig, operations map[string]Operation) int {
	changes := 0
	inFlight := 0
	for _, operation := range operations {
		if len(operation.Ips) > 0 {
			log.Printf("Instance: %v %v ips: %v",
				operation.Instance.Name, operation.Type.String(), operation.Ips)
			in <- operation
			inFlight++
		}
	}
	for i := 0; i < inFlight; i++ {
		changes += <-out
	}
	return changes
}

func Execute(cfg *GcpConfig, operation Operation) int {
	instance, err := GetInstance(cfg, operation.Instance.Name)
	if err != nil {
		log.Printf("Error getting instance: %v", err)
		return 0
	}
	var newState []string
	switch operation.Type {
	case Add:
		newState = *instance.AliasIps
		for _, ip := range operation.Ips {
			if !slices.Contains(newState, ip) {
				newState = append(newState, ip)
			}
		}
	case Remove:
		newState = []string{}
		for _, ip := range *instance.AliasIps {
			if !slices.Contains(operation.Ips, ip) {
				newState = append(newState, ip)
			}
		}
	}
	if len(*instance.AliasIps) == len(newState) {
		// No actual changes.
		return 0
	}
	err = UpdateAliasIPs(cfg, instance, newState)
	if err != nil {
		log.Printf("Error updating alias ips for instance %s", instance.Name)
		return 0
	}
	WaitForUpdate(cfg, instance.Name, newState)
	return 1
}

func exponentialBackoff(elapsedSeconds int) time.Duration {
	switch {
	case elapsedSeconds < 5:
		return time.Duration(1) * time.Second
	case elapsedSeconds < 15:
		return time.Duration(2) * time.Second
	case elapsedSeconds < 60:
		return time.Duration(5) * time.Second
	default:
		return time.Duration(10) * time.Second
	}
}

func WaitForUpdate(cfg *GcpConfig, instanceName string, newState []string) {
	start := time.Now()
	elapsedSeconds := 0
	for uint(elapsedSeconds) < cfg.WaitSeconds {
		instance, err := GetInstance(cfg, instanceName)
		if err != nil {
			log.Printf("Error waiting for operation to complete. Ignoring: %v", err)
			return
		}
		if len(newState) == len(*instance.AliasIps) {
			log.Printf("Instance: %s updated in %v.", instance.Name, time.Since(start))
			return
		}
		time.Sleep(exponentialBackoff(elapsedSeconds))
		elapsedSeconds = int(time.Since(start).Seconds())
	}
	log.Printf("Waited %d seconds for instance update, then gave up.", elapsedSeconds)
}
