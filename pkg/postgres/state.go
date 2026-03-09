package postgres

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// just use a high number here, which is bigger than the max amount of
// instances in a cluster
const MaxInt = 500000

type Cluster struct {
	name           string
	namespace      string
	instances      map[string]*Instance
	primary        string
	desiredPrimary string
	syncNum        int
}

type Instance struct {
	instanceId string
	region     string
	active     bool
	cluster    *Cluster
}

// cluster_ns#cluster_name -> cluster object
var clusters = map[string]*Cluster{}

func clusterKey(clusterNamespace, clusterName string) (clusterKey string) {
	return clusterNamespace + "#" + clusterName
}

// RegisterOrUpdateCluster will register a cluster if it does not exist yet or update
// an existing cluster.
func RegisterOrUpdateCluster(clusterNamespace, clusterName, primaryInstanceId, desiredPrimaryId string, numStandby int) *Cluster {
	key := clusterKey(clusterNamespace, clusterName)
	if clus, ok := clusters[key]; ok {
		clus.primary = primaryInstanceId
		clus.syncNum = numStandby
		clus.namespace = clusterNamespace
		clus.desiredPrimary = desiredPrimaryId
	} else {
		clusters[key] = &Cluster{
			instances:      map[string]*Instance{},
			namespace:      clusterNamespace,
			name:           clusterName,
			primary:        primaryInstanceId,
			desiredPrimary: desiredPrimaryId,
			syncNum:        numStandby,
		}
	}
	return clusters[key]
}

func dropCluster(clusterNamespace, clusterName string) {
	delete(clusters, clusterKey(clusterNamespace, clusterName))
}

func GetCluster(clusterNamespace, clusterName string) *Cluster {
	return clusters[clusterKey(clusterNamespace, clusterName)]
}

func (clus *Cluster) GetName() string {
	return clus.name
}

// GetCurrentPrimaryInstanceId returns the ID string for the current primary instance
// of the cluster. An empty string is returned if no primary instance is set yet.
func (clus *Cluster) GetCurrentPrimaryInstanceId() string {
	return clus.primary
}

func (clus *Cluster) GetDesiredPrimaryInstanceId() string {
	return clus.desiredPrimary
}

func (clus *Cluster) GetInstances() []*Instance {
	instances := slices.Collect(maps.Values(clus.instances))
	sort.Slice(instances, func(i, j int) bool {
		return strings.Compare(instances[i].instanceId, instances[j].instanceId) <= 0
	})
	return instances
}
func (clus *Cluster) GetInstanceByName(instanceName string) *Instance {
	instanceId := ExtractInstanceId(clus.name, instanceName)
	return clus.instances[instanceId]
}

func (clus *Cluster) GetInstanceByID(instanceId string) *Instance {
	return clus.instances[instanceId]
}

func (clus *Cluster) GetPrimaryInstance() *Instance {
	return clus.instances[clus.primary]
}

func (clus *Cluster) RegisterOrUpdateInstance(instanceId, region string, isHealthy bool) *Instance {
	if _, err := strconv.Atoi(instanceId); err != nil {
		slog.Error(fmt.Sprintf("Instance could not be created, ID %s is not a number: %v", instanceId, err))
		return nil
	}
	if inst, ok := clus.instances[instanceId]; ok {
		inst.active = isHealthy
		inst.region = region
		return inst
	}
	inst := &Instance{
		instanceId: instanceId,
		region:     region,
		active:     isHealthy,
		cluster:    clus,
	}
	clus.instances[instanceId] = inst
	return inst
}

func (clus *Cluster) MarkInactive(instanceId string) {
	inst := clus.GetInstanceByID(instanceId)
	if inst != nil {
		inst.MarkInactive()
	}
}

func (inst *Instance) GetRegion() string {
	return inst.region
}

func (inst *Instance) IsActive() bool {
	return inst.active
}

func (inst *Instance) MarkInactive() {
	if inst.active {
		slog.Info(fmt.Sprintf("Marking instance %s as not active", inst.instanceId))
	}
	inst.active = false
}

// IsSyncReplica tells if the instance is a sync-replica in the
// cluster (which also includes the primary)
func (inst *Instance) IsSyncReplica() bool {
	activeReplicas := make([]string, 0)
	for _, insta := range inst.cluster.GetInstances() {
		if !insta.active {
			continue
		}
		activeReplicas = append(activeReplicas, insta.instanceId)
	}
	slices.Sort(activeReplicas)

	for i := 0; i < inst.cluster.syncNum+1; i++ {
		if activeReplicas[i] == inst.instanceId {
			return true
		}
	}
	return false
}

// ----- Methods, required for testing -----

func (clus *Cluster) SetPrimary(instanceId string) {
	clus.primary = instanceId
}
