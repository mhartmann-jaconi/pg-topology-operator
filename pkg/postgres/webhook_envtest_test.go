package postgres

import (
	"testing"

	. "github.com/onsi/gomega"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/cluster"
)

func TestEnvtestWebhook_AutomatedDRFailback(t *testing.T) {
	g := NewWithT(t)
	defer guardEnvTest(t, g)()

	// 1. State Mapping: Simulate dc-01 recovering and healthy, while dc-02 leadership suffers
	// dc-01 is the primary region — node 5 in dc-02 should be rejected as ineligible
	cluster.Store("node1", "dc-01", "dc-01-01", true)
	cluster.Store("node2", "dc-02", "dc-02-01", false)
	cluster.Store("node3", "dc-02", "dc-02-02", false) // dc-02 is NOT the leader

	// Build the mock instances
	//  - dc-01 instance 1 healthy
	//  - dc-02 instance 4 (current primary) crashes
	//  - dc-02 instance 5 (desired primary) healthy
	internalCluster := RegisterOrUpdateCluster(clusterNamespace, clusterName, "4", "5", 3, 2)
	internalCluster.RegisterOrUpdateInstance("1", "dc-01", "zone", ip, true)
	internalCluster.RegisterOrUpdateInstance("4", "dc-02", "zone", ip, false)
	internalCluster.RegisterOrUpdateInstance("5", "dc-02", "zone", ip, true)

	// 2. Create the Cluster object and patch its status to simulate CNPG failover
	pgCluster := createEnvtestCluster(g, clusterName, clusterNamespace, 3)
	patchClusterStatus(g, pgCluster, instanceName("4"), instanceName("5"))

	// 3. Read back and assert: webhook should redirect to node 1 in dc-01
	saved := readClusterStatus(g, clusterName, clusterNamespace)
	g.Expect(saved.Status.TargetPrimary).To(Equal(instanceName("1")),
		"Webhook should redirect failover to the healthy node in the primary region")
	g.Expect(saved.Status.CurrentPrimary).To(Equal(instanceName("4")),
		"Webhook must not alter CurrentPrimary")
	g.Expect(saved.Spec.Instances).To(Equal(3),
		"Webhook must not alter Spec")
}

func TestEnvtestWebhook_SyncReplicaAffinityEnforcement(t *testing.T) {
	g := NewWithT(t)
	defer guardEnvTest(t, g)()

	// 1. Topology: dc-01 is leading
	cluster.Store("node1", "dc-01", "dc-01-01", true)
	cluster.Store("node1", "dc-02", "dc-02-01", false)

	// Build the mock instances with syncNum = 2 (the cluster under test has 2 sync replicas).
	// IsSyncReplica() computes: limit = min(activeReplicas, syncNum + 1) = min(4, 3) = 3
	// The +1 accounts for the primary, which is part of the sync group.
	// With 4 active replicas (nodes 2,3,4,5) and limit=3, node 5 becomes async.
	internalCluster := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 5, 2)

	// 1 (Primary) is DEAD.
	// 2 (Cross-region) is HEALTHY. Nodes 3, 4, 5 (Domestic) are HEALTHY.
	internalCluster.RegisterOrUpdateInstance("1", "dc-01", "zone", ip, false)
	internalCluster.RegisterOrUpdateInstance("2", "dc-02", "zone", ip, true)
	internalCluster.RegisterOrUpdateInstance("3", "dc-01", "zone", ip, true)
	internalCluster.RegisterOrUpdateInstance("4", "dc-01", "zone", ip, true)
	internalCluster.RegisterOrUpdateInstance("5", "dc-01", "zone", ip, true)

	// Assert the Sync Replica sorting allocation physically within the test
	// activeReplicas = [2, 3, 4, 5]. limit=3.
	// Since limit=3, nodes 2, 3, 4 receive sync status. Node 5 drops to async!
	g.Expect(internalCluster.GetInstanceByID("2").IsSyncReplica()).To(BeTrue(), "Node 2 receives sync")
	g.Expect(internalCluster.GetInstanceByID("3").IsSyncReplica()).To(BeTrue(), "Node 3 receives sync")
	g.Expect(internalCluster.GetInstanceByID("4").IsSyncReplica()).To(BeTrue(), "Node 4 receives sync")
	g.Expect(internalCluster.GetInstanceByID("5").IsSyncReplica()).To(BeFalse(), "Node 5 must compute as async because limit=3")

	// 2. Create Cluster object and patch status to simulate CNPG failover to cross-region node 2
	pgCluster := createEnvtestCluster(g, clusterName, clusterNamespace, 5)
	patchClusterStatus(g, pgCluster, instanceName("1"), instanceName("2"))

	// 3. Evaluate Mutating Webhook Processing
	// The webhook processes nodes sequentially [1, 2, 3, 4, 5].
	// - 1 is dead.
	// - 2 is cross-region.
	// - 3 is domestic + sync -> ELIGIBLE.
	// The webhook aggressively mutates the target to Node 3 instead of Node 2.
	saved := readClusterStatus(g, clusterName, clusterNamespace)
	g.Expect(saved.Status.TargetPrimary).To(Equal(instanceName("3")),
		"Webhook should redirect failover to the first eligible domestic sync replica")
	g.Expect(saved.Status.CurrentPrimary).To(Equal(instanceName("1")),
		"Webhook must not alter CurrentPrimary")
	g.Expect(saved.Spec.Instances).To(Equal(5),
		"Webhook must not alter Spec")
}
