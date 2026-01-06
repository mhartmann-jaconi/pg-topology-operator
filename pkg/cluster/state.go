package cluster

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/events"
	"github.com/spf13/viper"
)

type node struct {
	name     string
	zoneName string
}

type Zone struct {
	name      string
	isLeader  bool                // tells whether the zone is the primary one
	region    string              // region, the zone belongs to
	isHealthy bool                // false, if no healthy nodes are assigned to this zone
	nodes     map[string]struct{} // all node-ids belonging to this zone
}

// map of all existing K8s nodes by name
var nodes = map[string]node{}

// map of all existing Availability Zones by name
var zones = map[string]Zone{}

// Store will create or update a node with given information in the internal state.
func Store(nodeName, zoneName, regionName string, leader bool) {
	if zoneName == "" || regionName == "" {
		slog.Debug(fmt.Sprintf("Missing labels for node %s", nodeName))
		return
	}

	if GetStorageClassForRegion(regionName) == "" {
		slog.Warn(fmt.Sprintf("Region %s has no storage class assinged. Skip node %s", regionName, nodeName))
		return
	}

	slog.Debug(fmt.Sprintf("Storing node %s (%s/%s), leader: %v", nodeName, regionName, zoneName, leader))

	var primaryChanged bool = false
	if zn, ok := zones[zoneName]; !ok {
		// initial state
		slog.Info(fmt.Sprintf("Create new zone %s", zoneName))
		zones[zoneName] = Zone{
			name:      zoneName,
			isLeader:  leader,
			region:    regionName,
			isHealthy: true,
			nodes:     map[string]struct{}{},
		}
		zones[zoneName].nodes[nodeName] = struct{}{}
	} else {
		if leader && !zn.isLeader {
			primaryChanged = true
		}
		zn.isLeader = leader
		if _, ok := zn.nodes[nodeName]; !ok {
			zn.nodes[nodeName] = struct{}{}
		}
		zones[zoneName] = zn
	}
	if existing, ok := nodes[nodeName]; ok {
		existing.name = nodeName
		existing.zoneName = zoneName
		nodes[nodeName] = existing
	} else {
		nodes[nodeName] = node{
			name:     nodeName,
			zoneName: zoneName,
		}
	}
	if primaryChanged {
		// we need to make all other zones to non-primaries
		for n, zn := range zones {
			if n == zoneName {
				continue
			}
			zn.isLeader = false
			zones[n] = zn
		}

		// there is a new leader, so we need to change the primary instance
		slog.Info(fmt.Sprintf("zone %s changed to become the new leading zone", zoneName))
		events.PrimaryAZChanged.Emit(context.TODO(), zoneName)
	}
	slog.Debug(fmt.Sprintf("current primary zone is '%s'", GetPrimaryZone()))
}

func Drop(nodeName string) {
	slog.Debug(fmt.Sprintf("Dropping node %s from internal state", nodeName))
	nd, ok := nodes[nodeName]
	if !ok {
		return
	}
	zn, ok := zones[nd.zoneName]
	if !ok {
		return
	}
	delete(zn.nodes, nodeName)
	if len(zn.nodes) < 1 {
		zn.isHealthy = false
	}
	zones[nd.zoneName] = zn
	delete(nodes, nodeName)
}

func LogZones() {
	result := make(map[string][]string)
	for _, zn := range zones {
		if zns, ok := result[zn.region]; ok {
			result[zn.region] = append(zns, zn.name)
		} else {
			result[zn.region] = []string{zn.name}
		}
	}
	slog.Debug(fmt.Sprintf("Region-Zone-Map: %v", result))
}

func GetRegionForZone(zoneName string) string {
	for _, z := range zones {
		if z.name == zoneName {
			return z.region
		}
	}
	return ""
}

func GetHealthyZones() []string {
	result := []string{}
	for name, zn := range zones {
		if !zn.isHealthy {
			continue
		}
		result = append(result, name)
	}
	return result
}

func GetPrimaryZone() string {
	for n, z := range zones {
		if z.isLeader && z.isHealthy {
			return n
		}
	}
	return ""
}

func GetStorageClassForZone(zone string) string {
	return GetStorageClassForRegion(GetRegionForZone(zone))
}

func GetStorageClassForRegion(region string) string {
	volumeMap := viper.GetStringMapString(config.PgVolumes)
	if storageClass, ok := volumeMap[region]; ok {
		return storageClass
	} else {
		return ""
	}
}
