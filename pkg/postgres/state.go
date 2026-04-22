package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
)

// just use a high number here, which is bigger than the max amount of
// instances in a cluster
const MaxInt = 500000

type Cluster struct {
	name             string
	namespace        string
	desiredInstances int
	instances        map[string]*Instance
	primary          string
	desiredPrimary   string
	syncNum          int
}

type Instance struct {
	name       string
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
func RegisterOrUpdateCluster(clusterNamespace, clusterName, primaryInstanceId, desiredPrimaryId string, desiredInstances, numStandby int) *Cluster {
	key := clusterKey(clusterNamespace, clusterName)
	if clus, ok := clusters[key]; ok {
		clus.primary = primaryInstanceId
		clus.desiredInstances = desiredInstances
		clus.syncNum = numStandby
		clus.namespace = clusterNamespace
		clus.desiredPrimary = desiredPrimaryId
	} else {
		clusters[key] = &Cluster{
			instances:        map[string]*Instance{},
			namespace:        clusterNamespace,
			name:             clusterName,
			primary:          primaryInstanceId,
			desiredPrimary:   desiredPrimaryId,
			desiredInstances: desiredInstances,
			syncNum:          numStandby,
		}
		ctx := config.EnrichContext(context.Background(), clusterName)
		slog.InfoContext(ctx, "Registered cluster")
	}
	return clusters[key]
}

func DropCluster(clusterNamespace, clusterName string) {
	if _, ok := clusters[clusterKey(clusterNamespace, clusterName)]; !ok {
		ctx := config.EnrichContext(context.Background(), clusterName)
		slog.InfoContext(ctx, "Remove cluster from internal state")
	}
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

func (clus *Cluster) RegistrationComplete() bool {
	return clus.desiredInstances <= len(clus.instances)
}

func (clus *Cluster) RegisterOrUpdateInstance(instanceId, region string, isHealthy bool) *Instance {
	ctx := config.EnrichContext(context.Background(), clus.name)
	if _, err := strconv.Atoi(instanceId); err != nil {
		slog.ErrorContext(ctx, fmt.Sprintf("Instance could not be created, ID %s is not a number", instanceId), "err", err)
		return nil
	}
	if inst, ok := clus.instances[instanceId]; ok {
		inst.active = isHealthy
		inst.region = region
		return inst
	} else {
		slog.InfoContext(ctx, fmt.Sprintf("Register new instance %s in region %s", instanceId, region))
	}
	inst := &Instance{
		name:       clus.name + "-" + instanceId,
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

func (clus *Cluster) initiateSwitchover(ctx context.Context) error {
	ctx = config.EnrichContext(ctx, clus.name)
	if !clus.RegistrationComplete() {
		// can't do switchover as long as not all instances are registered
		slog.WarnContext(ctx, "Not all instances registered yet, skip switchover")
		return fmt.Errorf("cluster not fully registered yet")
	}
	if err := initiateSwitchover(ctx, clus); err != nil {
		slog.ErrorContext(ctx, "Failed to change primary instance for cluster", "err", err)
		return err
	}
	return nil
}

func (inst *Instance) GetRegion() string {
	return inst.region
}

func (inst *Instance) IsActive() bool {
	return inst.active
}

func (inst *Instance) MarkInactive() {
	if inst.active {
		ctx := config.EnrichContext(context.Background(), inst.cluster.name)
		slog.InfoContext(ctx, fmt.Sprintf("Marking instance %s as not active", inst.instanceId))
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

	limit := min(len(activeReplicas), inst.cluster.syncNum+1)
	for i := 0; i < limit; i++ {
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
