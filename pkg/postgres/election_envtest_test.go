package postgres

import (
	"context"
	"testing"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	. "github.com/onsi/gomega"
)

// TestEnvtestElection_InitiateSwitchover triggers initiateSwitchover() against
// the envtest API server and verifies that all status fields set by setNewTarget()
// (TargetPrimary, TargetPrimaryTimestamp, Phase, PhaseReason, Conditions)
// are applied correctly via patchClusterWithRetry().
func TestEnvtestElection_InitiateSwitchover(t *testing.T) {
	g := NewWithT(t)
	defer guardEnvTest(t, g)()

	// 1. Topology: dc-02 is the leading region.
	// The election engine will search for an eligible node in dc-02.
	cluster.Store("node1", "dc-01", "dc-01-01", false)
	cluster.Store("node2", "dc-02", "dc-02-01", true)

	// Register the cluster internally. Nodes 1 and 3 (dc-01) are healthy but in the wrong region,
	// node 2 (dc-02) is a healthy sync replica eligible for promotion.
	internalCluster := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 3, 2)
	internalCluster.RegisterOrUpdateInstance("1", "dc-01", "zone", ip, true)
	internalCluster.RegisterOrUpdateInstance("2", "dc-02", "zone", ip, true)
	internalCluster.RegisterOrUpdateInstance("3", "dc-01", "zone", ip, true)

	// 2. Create the Cluster in the API server and set initial status.
	// CurrentPrimary=1, TargetPrimary=1 (stable state before switchover).
	pgCluster := createEnvtestCluster(g, clusterName, clusterNamespace, 3)
	patchClusterStatus(g, pgCluster, instanceName("1"), instanceName("1"))

	// 3. Point the election engine's K8s client at the real envtest API server.
	// This replaces the package-level `cli` variable that patchClusterWithRetry() uses.
	cli = k8sClient

	// 4. Execute the election engine's switchover.
	// initiateSwitchover() will:
	//   - Check if the current primary (node 1) is eligible → false (wrong region)
	//   - Search for a new target → find node 2 (dc-02, active, sync)
	//   - Call applyNewPrimary() → patchClusterWithRetry() → Status().Patch()
	//   - setNewTarget() sets: TargetPrimary, TargetPrimaryTimestamp, Phase, PhaseReason, Condition
	err := initiateSwitchover(context.Background(), internalCluster)
	g.Expect(err).NotTo(HaveOccurred(), "Switchover should succeed")

	// 5. Read back the persisted state and validate ALL fields set by setNewTarget().
	saved := readClusterStatus(g, clusterName, clusterNamespace)

	// Core mutation: TargetPrimary redirected to the eligible node in dc-02
	g.Expect(saved.Status.TargetPrimary).To(Equal(instanceName("2")),
		"Election engine should set TargetPrimary to the eligible node")

	// setNewTarget() also sets the switchover metadata fields.
	// These are validated against the real CRD schema — the fake client never checks this.
	g.Expect(saved.Status.Phase).To(Equal(string(pgapi.PhaseSwitchover)),
		"Phase must be set to Switchover")
	g.Expect(saved.Status.PhaseReason).To(ContainSubstring(instanceName("2")),
		"PhaseReason must reference the new target instance")
	g.Expect(saved.Status.TargetPrimaryTimestamp).NotTo(BeEmpty(),
		"TargetPrimaryTimestamp must be set")

	// Condition: the cluster should be marked as not ready during switchover
	g.Expect(saved.Status.Conditions).NotTo(BeEmpty(),
		"Conditions must contain the not-ready marker")

	// Unchanged fields
	g.Expect(saved.Status.CurrentPrimary).To(Equal(instanceName("1")),
		"Election engine must not alter CurrentPrimary")
	g.Expect(saved.Spec.Instances).To(Equal(3),
		"Election engine must not alter Spec")
}

// TestEnvtestElection_InitiateSwitchoverNoEligibleTarget triggers initiateSwitchover()
// when no eligible target exists (all nodes are in the wrong region).
// The function must return an error and leave the API server state unchanged.
func TestEnvtestElection_InitiateSwitchoverNoEligibleTarget(t *testing.T) {
	g := NewWithT(t)
	defer guardEnvTest(t, g)()

	// 1. Topology: dc-02 is the leading region, but all nodes are in dc-01.
	cluster.Store("node1", "dc-01", "dc-01-01", false)
	cluster.Store("node2", "dc-02", "dc-02-01", true)

	// All instances are in dc-01 — none are eligible for dc-02 leadership.
	internalCluster := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 3, 2)
	internalCluster.RegisterOrUpdateInstance("1", "dc-01", "zone", ip, true)
	internalCluster.RegisterOrUpdateInstance("2", "dc-01", "zone", ip, true)
	internalCluster.RegisterOrUpdateInstance("3", "dc-01", "zone", ip, true)

	// 2. Create the Cluster and set stable initial status.
	pgCluster := createEnvtestCluster(g, clusterName, clusterNamespace, 3)
	patchClusterStatus(g, pgCluster, instanceName("1"), instanceName("1"))

	// 3. Point the election engine at the real API server.
	cli = k8sClient

	// 4. Execute switchover — must fail because no node is in dc-02.
	err := initiateSwitchover(context.Background(), internalCluster)
	g.Expect(err).To(HaveOccurred(), "Switchover must fail when no eligible target exists")
	g.Expect(err.Error()).To(ContainSubstring("no eligible target instance found"))

	// 5. Read back and verify the API server state is completely untouched.
	saved := readClusterStatus(g, clusterName, clusterNamespace)
	g.Expect(saved.Status.TargetPrimary).To(Equal(instanceName("1")),
		"TargetPrimary must remain unchanged after failed switchover")
	g.Expect(saved.Status.CurrentPrimary).To(Equal(instanceName("1")),
		"CurrentPrimary must remain unchanged")
	g.Expect(saved.Status.Phase).To(BeEmpty(),
		"Phase must not be set after failed switchover")
	g.Expect(saved.Status.Conditions).To(BeEmpty(),
		"Conditions must not be set after failed switchover")
}

// TestEnvtestElection_InitiateSwitchoverIdempotent calls initiateSwitchover() twice
// with the same topology. The second call should detect that TargetPrimary is already
// set to the correct value and skip the patch without overwriting metadata fields.
func TestEnvtestElection_InitiateSwitchoverIdempotent(t *testing.T) {
	g := NewWithT(t)
	defer guardEnvTest(t, g)()

	// 1. Topology: dc-02 is the leading region.
	cluster.Store("node1", "dc-01", "dc-01-01", false)
	cluster.Store("node2", "dc-02", "dc-02-01", true)

	// Node 2 in dc-02 is the only eligible candidate.
	internalCluster := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 3, 2)
	internalCluster.RegisterOrUpdateInstance("1", "dc-01", "zone", ip, true)
	internalCluster.RegisterOrUpdateInstance("2", "dc-02", "zone", ip, true)
	internalCluster.RegisterOrUpdateInstance("3", "dc-01", "zone", ip, true)

	// 2. Create the Cluster and set stable initial status.
	pgCluster := createEnvtestCluster(g, clusterName, clusterNamespace, 3)
	patchClusterStatus(g, pgCluster, instanceName("1"), instanceName("1"))

	// 3. Point the election engine at the real API server.
	cli = k8sClient

	// 4. First switchover — applies the patch.
	err := initiateSwitchover(context.Background(), internalCluster)
	g.Expect(err).NotTo(HaveOccurred(), "First switchover should succeed")

	// Verify the first switchover produced the expected state.
	firstSaved := readClusterStatus(g, clusterName, clusterNamespace)
	g.Expect(firstSaved.Status.TargetPrimary).To(Equal(instanceName("2")),
		"First switchover must set TargetPrimary to node 2")
	g.Expect(firstSaved.Status.Phase).To(Equal(string(pgapi.PhaseSwitchover)),
		"First switchover must set Phase to Switchover")
	g.Expect(firstSaved.Status.PhaseReason).To(ContainSubstring(instanceName("2")),
		"First switchover must set PhaseReason referencing node 2")
	g.Expect(firstSaved.Status.TargetPrimaryTimestamp).NotTo(BeEmpty(),
		"First switchover must set TargetPrimaryTimestamp")
	g.Expect(firstSaved.Status.Conditions).NotTo(BeEmpty(),
		"First switchover must set Conditions")
	firstTimestamp := firstSaved.Status.TargetPrimaryTimestamp

	// 5. Second switchover with the same topology.
	// applyNewPrimary() reads the current state, sees TargetPrimary == newPrimary,
	// and returns without patching (k8s_access.go:L37-L40).
	err = initiateSwitchover(context.Background(), internalCluster)
	g.Expect(err).NotTo(HaveOccurred(), "Second switchover should succeed (no-op)")

	// 6. Read back and verify the state is identical to the first call.
	secondSaved := readClusterStatus(g, clusterName, clusterNamespace)
	g.Expect(secondSaved.Status.TargetPrimary).To(Equal(instanceName("2")),
		"TargetPrimary must remain node 2")
	g.Expect(secondSaved.Status.TargetPrimaryTimestamp).To(Equal(firstTimestamp),
		"Timestamp must not change on idempotent call")
	g.Expect(secondSaved.Status.Phase).To(Equal(string(pgapi.PhaseSwitchover)),
		"Phase must remain Switchover")
	g.Expect(len(secondSaved.Status.Conditions)).To(Equal(len(firstSaved.Status.Conditions)),
		"Conditions must not accumulate duplicate entries")
}
