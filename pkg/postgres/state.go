package postgres

import (
	"fmt"
	"log/slog"
	"strconv"

	nodes "github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
)

// just use a high number here, which is bigger than the max amount of
// instances in a cluster
const MaxInt = 500000

type Cluster struct {
	name      string
	namespace string
	instances map[string]instance
	primary   string
	standbies int
}

type instance struct {
	instanceId string
	zone       string
	active     bool
}

// cluster id -> cluster object
var clusters = map[string]Cluster{}

func CreateOrUpdateCluster(clusterName, clusterNamespace, primaryInstanceId string, numStandby int) Cluster {
	if clus, ok := clusters[clusterName]; ok {
		clus.primary = primaryInstanceId
		clus.standbies = numStandby
		clus.namespace = clusterNamespace
		clusters[clusterName] = clus
	} else {
		clusters[clusterName] = Cluster{
			instances: map[string]instance{},
			namespace: clusterNamespace,
			name:      clusterName,
			primary:   primaryInstanceId,
			standbies: numStandby,
		}
	}
	return clusters[clusterName]
}

func dropCluster(clusterName string) {
	delete(clusters, clusterName)
}

// GetLeastUsedZone returns the name of the AZ, where currently the least amount of
// instances for the cluster are running in.
func GetLeastUsedZone(clusterName string) string {
	instancesPerZone := make(map[string]int)
	for _, zn := range nodes.GetHealthyZones() {
		// init map with 0 instances
		instancesPerZone[zn] = 0
	}
	if clus, ok := clusters[clusterName]; ok {
		for _, inst := range clus.instances {
			if inst.active {
				// increase number of instances per zone for each active instance
				instancesPerZone[inst.zone] = instancesPerZone[inst.zone] + 1
			}
		}
	}
	min := MaxInt
	// iterate through all zones and check, which one has the least amount of instances
	for _, numberOfInstances := range instancesPerZone {
		if numberOfInstances < min {
			min = numberOfInstances
		}
	}
	// check all zones again and return the one, where the actual count equals the
	// calculated minimum
	for zn, count := range instancesPerZone {
		if count == min {
			return zn
		}
	}
	// should never happen
	return ""
}

// GetCurrentPrimaryInstanceId returns the ID string for the current primary instance
// of the cluster. An empty string is returned if no primary instance is set yet.
func GetCurrentPrimaryInstanceId(clusterName string) string {
	if clus, ok := clusters[clusterName]; ok {
		return clus.primary
	}
	return ""
}

// isSyncReplica tells if the instance with the given ID is a sync-replica in the
// cluster
func isSyncReplica(clusterName, instanceId string) bool {
	if clus, ok := clusters[clusterName]; ok {
		// get the maximum possible index of a sync-replica instance; PG-operator takes the
		// first instances after the primary as sync
		maxStandbyIndex := clus.standbies + 1
		if instanceIndex, err := strconv.Atoi(instanceId); err == nil {
			return instanceIndex <= maxStandbyIndex
		}
	}
	return false
}

// GetZoneForInstanceId returns the name of the AZ, where the instance with given
// identifier is running in (empty string, if no AZ assigned yet).
func GetZoneForInstanceId(clusterName, instanceId string) string {
	if clus, ok := clusters[clusterName]; ok {
		if inst, ok := clus.instances[instanceId]; ok {
			return inst.zone
		}
	}
	return ""
}

// GetOrAssignZoneToInstance will either return the name of a currently assigned AZ
// for the instance, or it will assign the currently least-used AZ to the instance and
// and return the new AZ name.
func GetOrAssignZoneToInstance(clusterName, instanceId string) (string, error) {
	zn := GetZoneForInstanceId(clusterName, instanceId)
	if zn != "" {
		return zn, nil
	}
	return SetZoneForInstanceId(clusterName, instanceId, GetLeastUsedZone(clusterName), false)
}

// SetZoneForInstanceId will assign the given AZ name to the instance in the internal
// state map.
func SetZoneForInstanceId(clusterName, instanceId, zoneName string, isHealthy bool) (string, error) {
	return setZoneForInstanceIdInternal(clusterName, instanceId, zoneName, isHealthy, false)
}

func SetZoneForInstanceIdIgnoreHealth(clusterName, instanceId, zoneName string) (string, error) {
	return setZoneForInstanceIdInternal(clusterName, instanceId, zoneName, true, true)
}

func setZoneForInstanceIdInternal(clusterName, instanceId, zoneName string, isHealthy, ignoreHealth bool) (string, error) {
	if _, ok := clusters[clusterName]; !ok {
		slog.Debug(fmt.Sprintf("Cluster %s not registered yet; creating it...", clusterName))
		CreateOrUpdateCluster(clusterName, "", "", 0)
	}
	clus := clusters[clusterName]
	if inst, ok := clus.instances[instanceId]; ok {
		if inst.zone != zoneName {
			slog.Info(fmt.Sprintf("Updating zone for instance %s from %s to %s", instanceId, inst.zone, zoneName))
		}
		inst.zone = zoneName
		if !ignoreHealth {
			inst.active = isHealthy
		}
		clus.instances[instanceId] = inst
	} else {
		slog.Info(fmt.Sprintf("Assiging zone %s to instance %s", zoneName, instanceId))
		clus.instances[instanceId] = instance{
			instanceId: instanceId,
			zone:       zoneName,
			active:     isHealthy,
		}
	}
	clusters[clusterName] = clus
	return zoneName, nil
}

// DropInstanceId will remove an instance-AZ assignment from the internal state.
func DropInstanceId(clusterName, instanceId string) {
	if clus, ok := clusters[clusterName]; ok {
		if inst, ok := clus.instances[instanceId]; ok {
			if inst.active {
				slog.Info(fmt.Sprintf("Deleting instance %s from internal state", instanceId))
			}
			inst.active = false
			clus.instances[instanceId] = inst
		}
		clusters[clusterName] = clus
	}
}
