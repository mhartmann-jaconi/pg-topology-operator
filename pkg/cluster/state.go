package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/events"
)

// map of node names to their region
var nodeRegions = map[string]string{}
var nodeRegionsLock = sync.RWMutex{}

// map of node names to their AZs
var nodeZones = map[string]string{}
var nodeZonesLock = sync.RWMutex{}

// map of all existing Regionnames to the info, if they are the leading region
var regionLeader = map[string]bool{}
var regionsLock = sync.RWMutex{}

// Store will put region and primary-region information into internal state
func Store(nodeName, regionName, zoneName string, shouldBeLeader bool) {

	slog.Log(context.Background(), config.LevelTrace, fmt.Sprintf("Storing node %s, region %s, zone: %s, leader: %v", nodeName, regionName, zoneName, shouldBeLeader))

	var primaryChanged bool = false

	saveNode(nodeName, regionName, zoneName)

	regionsLock.Lock()
	defer regionsLock.Unlock()
	if isCurrentyLeader, ok := regionLeader[regionName]; !ok {
		slog.Info(fmt.Sprintf("Create new region %s", regionName))
		regionLeader[regionName] = shouldBeLeader
		if shouldBeLeader {
			primaryChanged = true
		}
	} else {
		if shouldBeLeader && !isCurrentyLeader {
			primaryChanged = true
		}
		regionLeader[regionName] = shouldBeLeader
	}

	if primaryChanged {
		// we need to make all other regions to non-primaries
		for n := range regionLeader {
			if n == regionName {
				continue
			}
			regionLeader[n] = false
		}

		// there is a new leader, so we need to change the primary instance
		slog.Info(fmt.Sprintf("region %s changed to become the new leading region", regionName))
		events.PrimaryRegionChanged(regionName)
	}
}

func saveNode(nodeName, regionName, zoneName string) {
	nodeRegionsLock.Lock()
	defer nodeRegionsLock.Unlock()
	nodeRegions[nodeName] = regionName

	nodeZonesLock.Lock()
	defer nodeZonesLock.Unlock()
	nodeZones[nodeName] = zoneName
}

// GetRegionOfNode retrieves the the region, where the given node resides in
func GetRegionOfNode(nodeName string) string {
	nodeRegionsLock.RLock()
	defer nodeRegionsLock.RUnlock()
	return nodeRegions[nodeName]
}

func GetZoneOfNode(nodeName string) string {
	nodeZonesLock.RLock()
	defer nodeZonesLock.RUnlock()
	return nodeZones[nodeName]
}

func GetZoneCount() int {
	nodeZonesLock.RLock()
	defer nodeZonesLock.RUnlock()
	temp := map[string]struct{}{}
	for _, zone := range nodeZones {
		temp[zone] = struct{}{}
	}
	return len(temp)
}

// GetPrimaryRegion retrieves the name of the current primary region
func GetPrimaryRegion() string {
	regionsLock.RLock()
	defer regionsLock.RUnlock()
	for region, isLeader := range regionLeader {
		if isLeader {
			return region
		}
	}
	return ""
}

// -- HELPER methods for testing --

// ClearState resets all internal maps to their zero state.
// It is not thread-safe and should only be called from test teardown (defer cleanup).
func ClearState() {
	nodeRegions = map[string]string{}
	nodeZones = map[string]string{}
	regionLeader = map[string]bool{}
}
