package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/thejerf/slogassert"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/cluster"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/config"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/events"

	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var webhook = defaulter{}

func TestDefault(t *testing.T) {
	g := NewWithT(t)

	ctx := context.Background()
	defer teardown()

	handler := slogassert.New(t, config.LevelTrace, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(slog.New(handler))

	events.SetListenersForRegionChange(func(context.Context, string) {})

	// not a cluster object
	pod := &v1.Pod{}
	err := webhook.Default(ctx, pod)
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Failed to call cluster webhook")
	handler.Reset()

	clus := &pgapi.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: clusterNamespace,
		},
		Status: pgapi.ClusterStatus{},
	}
	// no desired target set
	err = webhook.Default(ctx, clus)
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Current primary not set yet for cluster")
	handler.Reset()

	// current == desired
	clus.Status.CurrentPrimary = instanceName("1")
	clus.Status.TargetPrimary = instanceName("1")
	err = webhook.Default(ctx, clus)
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Current primary is already target primary on cluster")
	handler.Reset()

	// no cluster internally registered
	clus.Status.TargetPrimary = instanceName("2")
	err = webhook.Default(ctx, clus)
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Cluster is not registered yet")
	handler.Reset()

	internalCluster := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 3, 2)

	internalCluster.RegisterOrUpdateInstance("1", "region1", "zone", ip, true)
	internalCluster.RegisterOrUpdateInstance("2", "region1", "zone", ip, true)
	internalCluster.RegisterOrUpdateInstance("3", "region2", "zone", ip, true)
	cluster.Store("x", "region1", "zone1", true)
	cluster.Store("y", "region2", "zone2", false)

	// instance already eligible
	err = webhook.Default(ctx, clus)
	g.Expect(err).To(BeNil())
	handler.AssertMessage(fmt.Sprintf("Desired primary %s is already eligible primary", instanceName("2")))
	handler.Reset()

	cluster.Store("z", "region3", "zone3", true)

	// no eligible instance found
	err = webhook.Default(ctx, clus)
	g.Expect(err).To(BeNil())
	handler.AssertMessage(fmt.Sprintf("Intercept primary election to not use %s: not in primary region", instanceName("2")))
	handler.AssertMessage("No eligible target instance found to be promoted to primary")
	handler.Reset()

	cluster.Store("y", "region2", "zone2", true)

	// change desired primary to clus-3
	err = webhook.Default(ctx, clus)
	g.Expect(err).To(BeNil())
	handler.AssertMessage(fmt.Sprintf("Intercept primary election to not use %s: not in primary region", instanceName("2")))
	handler.AssertMessage(fmt.Sprintf("Update cluster desired primary to %s", instanceName("3")))
	handler.Reset()
	g.Expect(clus.Status.TargetPrimary).To(Equal(instanceName("3")))
}

func TestDefault_PermitsFallbackFailoverWhenLocalUnavailable(t *testing.T) {
	g := NewWithT(t)

	ctx := context.Background()
	defer teardown()

	events.SetListenersForRegionChange(func(context.Context, string) {})

	// Simulate CloudNativePG attempting a cross-region failover during dc-01 brownout
	clus := buildCluster(
		"1", // Original primary in dc-01
		"6", // CloudNativePG natively elected dc-02 node
	)

	// Register the topology internals (simulate dc-01 nodes as offline/missing by not registering them)
	internalCluster := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 3, 2)
	internalCluster.RegisterOrUpdateInstance("6", "dc-02", "zone", ip, false)

	// Set dc-01 as the leading zone in the topology tracker
	cluster.Store("dc-01-node", "dc-01", "dc-01-01", true)
	cluster.Store("dc-02-node", "dc-02", "dc-02-01", false)

	// EXECUTE WEBHOOK
	err := webhook.Default(ctx, clus)

	// Expect the webhook to permit the cross-region failover by gracefully returning nil
	g.Expect(err).To(BeNil())

	// Verify the original cross-region fallback target was NOT modified
	g.Expect(clus.Status.TargetPrimary).To(Equal(instanceName("6")))
}

func TestDefault_UnregisteredTarget(t *testing.T) {
	g := NewWithT(t)

	ctx := context.Background()
	defer teardown()

	events.SetListenersForRegionChange(func(context.Context, string) {})

	// Simulate CloudNativePG electing a target in dc-02
	clus := buildCluster(
		"1",
		"4",
	)

	// Register topology: The new target '4' has NOT been registered by the Pod Watcher yet!
	// It is a race condition. dc-01 has a perfectly healthy node ready to take over.
	internalCluster := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 3, 2)
	internalCluster.RegisterOrUpdateInstance("1", "dc-01", "zone", ip, true)
	internalCluster.RegisterOrUpdateInstance("2", "dc-01", "zone", ip, true) // Preferred fallback target

	cluster.Store("dc-01-node", "dc-01", "dc-01-01", true)
	cluster.Store("dc-02-node", "dc-02", "dc-02-01", false)

	// Execute webhook
	err := webhook.Default(ctx, clus)

	g.Expect(err).To(BeNil())
	g.Expect(clus.Status.TargetPrimary).To(Equal(instanceName("2")))
}

func TestDefault_PermitsFailoverDuringColdCacheInconsistency(t *testing.T) {
	g := NewWithT(t)

	ctx := context.Background()
	defer teardown()

	// CloudNativePG submits a cross-region failover mutation request
	clus := buildCluster(
		"1",
		"2", // Cross-region node
	)

	// NOTE: OPERATOR COLD-CACHE BEHAVIOR
	// If the PG Topology Operator pod restarts, its memory cache is cleared.
	// When CloudNativePG attempts a failover simultaneously, `GetCluster` evaluates to `nil`
	// because the operator has not yet populated its internal state.
	//
	// The result handler implementation is up to the workload, which creates the resource
	// (in this case: CNPG). As this does not have a retry-logic implemented, an error at
	// this point, would be a stop-the-world event.

	// Do NOT call `RegisterOrUpdateCluster` to physically simulate the unhydrated cold cache.

	// Execute webhook
	err := webhook.Default(ctx, clus)

	g.Expect(err).To(BeNil())
	g.Expect(clus.Status.TargetPrimary).To(Equal(instanceName("2")))
}

func TestDefault_IgnoresLocalNodesDueToNaiveSyncReplicaSorting(t *testing.T) {
	g := NewWithT(t)

	ctx := context.Background()
	defer teardown()

	events.SetListenersForRegionChange(func(context.Context, string) {})

	// Simulate CloudNativePG natively electing a target in dc-02 (e.g., node 2)
	clus := buildCluster(
		"1",
		"2", // Cross-region failover attempt
	)

	// In this topology, dc-01 is the active leader.
	cluster.Store("dc-01-node", "dc-01", "dc-01-01", true)
	cluster.Store("dc-02-node", "dc-02", "dc-02-01", false)

	// Set syncNum = 0. This restricts our internal operator logic to only assign 1 Sync status slot (limit=1).
	internalCluster := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 3, 0)

	internalCluster.RegisterOrUpdateInstance("1", "dc-01", "zone", ip, false) // Node 1 (Primary) is DEAD
	internalCluster.RegisterOrUpdateInstance("2", "dc-02", "zone", ip, true)  // Node 2 (Cross-region) is HEALTHY
	internalCluster.RegisterOrUpdateInstance("3", "dc-01", "zone", ip, true)  // Node 3 (Domestic-region) is HEALTHY

	// Execute webhook
	err := webhook.Default(ctx, clus)

	// NOTE: ALPHANUMERIC SYNC REPLICA SORTING LOGIC
	// The PG Topology Operator (`IsSyncReplica()` in state.go) assigns the "Sync Replica" status
	// by sorting the active instances alphanumerically by their IDs (e.g., `["2", "3"]`).
	// Given `limit=1` in this topology, the operator assigns the sole "Sync Replica" status to node "2"
	// because "2" < "3".
	//
	// When the webhook runs `findNewEligibleTargetInstance()` to evaluate potential fallback targets in the
	// domestic region (`dc-01`), it filters using `checkIfSync=true`.
	// Because the alphanumeric sort algorithm assigned the sync status to node "2", node "3" computes as an Async replica.
	// Consequently, the webhook does not select node "3", exits the search cleanly, and permits the initial
	// cross-region failover to proceed.
	g.Expect(err).To(BeNil())
	g.Expect(clus.Status.TargetPrimary).To(Equal(instanceName("2")))
}

func TestDefault_ForciblyFailsBackToPrimaryRegionDuringSecondaryFailover(t *testing.T) {
	g := NewWithT(t)

	ctx := context.Background()
	defer teardown()

	events.SetListenersForRegionChange(func(context.Context, string) {})

	// Simulate CloudNativePG operating in DR (dc-02) and natively attempting a local failover within dc-02
	clus := buildCluster(
		"4", // Currently operating in DR
		"5", // Attempting failover to another node in DR
	)

	// Kubernetes natively labels `dc-01` as the immutable primary region
	cluster.Store("dc-01-node", "dc-01", "dc-01-01", true)
	cluster.Store("dc-02-node", "dc-02", "dc-02-01", false)

	// syncNum = 2 (allows 3 total active sync replica slots)
	internalCluster := RegisterOrUpdateCluster(clusterNamespace, clusterName, "4", "5", 3, 2)

	internalCluster.RegisterOrUpdateInstance("1", "dc-01", "zone", ip, true)  // Node 1 (dc-01) recovered and is healthy
	internalCluster.RegisterOrUpdateInstance("4", "dc-02", "zone", ip, false) // Node 4 (Current DR Primary) crashing
	internalCluster.RegisterOrUpdateInstance("5", "dc-02", "zone", ip, true)  // Node 5 (Target DR Secondary) healthy

	// Execute webhook
	err := webhook.Default(ctx, clus)

	// NOTE: AUTOMATED FAILBACK ENGINE MECHANICS
	// The webhook evaluates the CloudNativePG target ("test-cluster-5") using `isEligible()`.
	// The eligibility function strictly compares the target's region against `nodes.GetPrimaryRegion()`.
	// Because K8s statically designates `dc-01` as the primary region, "test-cluster-5" evaluates as `false`.
	//
	// Following the rejection, the webhook searches the active topology for an alternative eligible instance.
	// It discovers the recently recovered "test-cluster-1" in `dc-01` and assigns it as the new target.
	// Consequently, any secondary failover event inside the DR region acts as an automated catalyst
	// to pull the primary database role back to the primary datacenter, provided healthy nodes exist there.
	g.Expect(err).To(BeNil())
	g.Expect(clus.Status.TargetPrimary).To(Equal(instanceName("1")))
}

func buildCluster(currentPrimary, desiredPrimary string) *pgapi.Cluster {
	return &pgapi.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: clusterNamespace,
		},
		Status: pgapi.ClusterStatus{
			CurrentPrimary: instanceName(currentPrimary),
			TargetPrimary:  instanceName(desiredPrimary),
		},
	}
}

func instanceName(instanceId string) string {
	return fmt.Sprintf("%s-%s", clusterName, instanceId)
}
