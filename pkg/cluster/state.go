package cluster

import (
	"fmt"
	"log/slog"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/events"
)

type region struct {
	name     string
	isLeader bool
}

// map of node names to their region
var nodes = map[string]string{}

// map of all existing Regions by name
var regions = map[string]region{}

// Store will put region and primary-region information into internal state
func Store(nodeName, regionName string, leader bool) {

	slog.Debug(fmt.Sprintf("Storing node %s, region %s, leader: %v", nodeName, regionName, leader))

	var primaryChanged bool = false

	nodes[nodeName] = regionName

	if reg, ok := regions[regionName]; !ok {
		slog.Info(fmt.Sprintf("Create new region %s", regionName))
		regions[regionName] = region{name: regionName, isLeader: leader}
		if leader {
			primaryChanged = true
		}
	} else {
		if leader && !reg.isLeader {
			primaryChanged = true
		}
		reg.isLeader = leader
		regions[regionName] = reg
	}

	if primaryChanged {
		// we need to make all other regions to non-primaries
		for n, rg := range regions {
			if n == regionName {
				continue
			}
			rg.isLeader = false
			regions[n] = rg
		}

		// there is a new leader, so we need to change the primary instance
		slog.Info(fmt.Sprintf("region %s changed to become the new leading region", regionName))
		events.PrimaryRegionChanged(regionName)
	}
}

// GetRegionOfNode retrieves the the region, where the given node resides in
func GetRegionOfNode(nodeName string) string {
	return nodes[nodeName]
}

// GetPrimaryRegion retrieves the name of the current primary region
func GetPrimaryRegion() string {
	for n, r := range regions {
		if r.isLeader {
			return n
		}
	}
	return ""
}

func ClearState() {
	nodes = map[string]string{}
	regions = map[string]region{}
}
