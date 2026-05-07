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
	"sync"

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
	instancesLock    sync.RWMutex
	primary          string
	desiredPrimary   string
	syncNum          int
}

type Instance struct {
	name       string
	instanceId string
	podIP      string
	region     string
	zone       string
	active     bool
	cluster    PGCluster
}

type PGCluster interface {
	GetName() string
	GetNamespace() string
	GetCurrentPrimaryInstanceId() string
	GetDesiredPrimaryInstanceId() string
	GetSyncInstanceCount() int
	GetInstances() []*Instance
	GetInstanceByName(instanceName string) *Instance
	GetInstanceByID(instanceId string) *Instance
	GetPrimaryInstance() *Instance
	// GetReplicationState returns map of instanceIDs to their replication
	// state, gotten from PG status endpoint
	GetReplicationState() map[string]string
	// RegistrationComplete tells if all desired instances were created
	RegistrationComplete() bool
	// AllInstancesHealthy tells if all created instances are healthy
	AllInstancesHealthy() bool
	RegisterOrUpdateInstance(instanceId, region, zone, podIP string, isHealthy bool) *Instance
	MarkInactive(instanceId string)
	initiateSwitchover(ctx context.Context) error
}

// cluster_ns#cluster_name -> cluster object
var clusters = map[string]PGCluster{}
var clustersLock = sync.RWMutex{}

func clusterKey(clusterNamespace, clusterName string) (clusterKey string) {
	return clusterNamespace + "#" + clusterName
}

func GetClusters() map[string]PGCluster {
	clustersLock.RLock()
	defer clustersLock.RUnlock()
	result := map[string]PGCluster{}
	maps.Copy(result, clusters)
	return result
}

func GetCluster(clusterNamespace, clusterName string) PGCluster {
	clustersLock.RLock()
	defer clustersLock.RUnlock()
	return clusters[clusterKey(clusterNamespace, clusterName)]
}

// RegisterOrUpdateCluster will register a cluster if it does not exist yet or update
// an existing cluster.
func RegisterOrUpdateCluster(clusterNamespace, clusterName, primaryInstanceId, desiredPrimaryId string, desiredInstances, numStandby int) PGCluster {
	key := clusterKey(clusterNamespace, clusterName)
	clustersLock.Lock()
	defer clustersLock.Unlock()
	if pgclus, ok := clusters[key]; ok {
		clus := pgclus.(*Cluster)
		clus.primary = primaryInstanceId
		clus.desiredInstances = desiredInstances
		clus.syncNum = numStandby
		clus.namespace = clusterNamespace
		clus.desiredPrimary = desiredPrimaryId
		//clusters[key] = clus
	} else {
		clusters[key] = &Cluster{
			instances:        map[string]*Instance{},
			instancesLock:    sync.RWMutex{},
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
	clustersLock.Lock()
	defer clustersLock.Unlock()
	clusterKey := clusterKey(clusterNamespace, clusterName)
	if _, ok := clusters[clusterKey]; ok {
		ctx := config.EnrichContext(context.Background(), clusterName)
		slog.InfoContext(ctx, "Remove cluster from internal state")
	}
	delete(clusters, clusterKey)
}

func (clus *Cluster) GetName() string {
	return clus.name
}

func (clus *Cluster) GetNamespace() string {
	return clus.namespace
}

// GetCurrentPrimaryInstanceId returns the ID string for the current primary instance
// of the cluster. An empty string is returned if no primary instance is set yet.
func (clus *Cluster) GetCurrentPrimaryInstanceId() string {
	return clus.primary
}

func (clus *Cluster) GetDesiredPrimaryInstanceId() string {
	return clus.desiredPrimary
}

func (clus *Cluster) GetSyncInstanceCount() int {
	return clus.syncNum
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

func (clus *Cluster) GetReplicationState() map[string]string {
	prim := clus.GetPrimaryInstance()
	ctx := config.EnrichContext(context.Background(), clus.name)
	if prim == nil || !prim.active {
		slog.DebugContext(ctx, "Primary instance not found or not healthy")
		return map[string]string{}
	}
	stat, err := callStatusEndpoint(prim.podIP)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to call replication status", "err", err)
		return map[string]string{}
	}
	result := map[string]string{}
	for _, ri := range stat.ReplicationInfo {
		result[ExtractInstanceId(clus.name, ri.ApplicationName)] = ri.SyncState
	}
	return result
}

func (clus *Cluster) RegistrationComplete() bool {
	return clus.desiredInstances <= len(clus.instances)
}

func (clus *Cluster) AllInstancesHealthy() bool {
	for _, inst := range clus.instances {
		if !inst.active {
			return false
		}
	}
	return true
}

func (clus *Cluster) RegisterOrUpdateInstance(instanceId, region, zone, podIP string, isHealthy bool) *Instance {
	ctx := config.EnrichContext(context.Background(), clus.name)
	if _, err := strconv.Atoi(instanceId); err != nil {
		slog.ErrorContext(ctx, fmt.Sprintf("Instance could not be created, ID %s is not a number", instanceId), "err", err)
		return nil
	}
	clus.instancesLock.Lock()
	defer clus.instancesLock.Unlock()
	if inst, ok := clus.instances[instanceId]; ok {
		inst.active = isHealthy
		inst.podIP = podIP
		inst.region = region
		return inst
	} else {
		slog.InfoContext(ctx, fmt.Sprintf("Register new instance %s in region %s", instanceId, region))
	}
	inst := &Instance{
		name:       clus.name + "-" + instanceId,
		instanceId: instanceId,
		podIP:      podIP,
		region:     region,
		zone:       zone,
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

func (inst *Instance) GetZone() string {
	return inst.zone
}

func (inst *Instance) IsActive() bool {
	return inst.active
}

func (inst *Instance) MarkInactive() {
	if inst.active {
		ctx := config.EnrichContext(context.Background(), inst.cluster.GetName())
		slog.InfoContext(ctx, fmt.Sprintf("Marking instance %s as not active", inst.instanceId))
	}
	inst.active = false
}

// IsSyncReplica tells if the instance is a sync-replica in the
// cluster (which also includes the primary)
func (inst *Instance) IsSyncReplica() bool {
	states := inst.cluster.GetReplicationState()
	if state, ok := states[inst.instanceId]; ok && len(state) > 0 {
		slog.DebugContext(config.EnrichContext(context.Background(), inst.cluster.GetName()), fmt.Sprintf("Found sync entry in state map for %s", inst.instanceId))
		return isSyncState(state)
	}
	// fallback
	activeReplicas := make([]string, 0)
	for _, insta := range inst.cluster.GetInstances() {
		if !insta.active {
			continue
		}
		activeReplicas = append(activeReplicas, insta.instanceId)
	}
	slices.Sort(activeReplicas)

	limit := min(len(activeReplicas), inst.cluster.GetSyncInstanceCount()+1)
	for i := range limit {
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
