package postgres

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/events"
	. "github.com/onsi/gomega"
	"github.com/spf13/viper"
	"github.com/thejerf/slogassert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var clus *Cluster

func Test_changePrimaryInstances(t *testing.T) {
	g := NewWithT(t)

	viper.Set(config.LogLevel, "debug")
	defer teardown()

	events.SetListenersForRegionChange(func(context.Context, string) {})

	cluster.Store("node1", "dc-01", "dc-01-01", false)
	cluster.Store("node2", "dc-02", "dc-02-01", true)

	clus1 := &Cluster{
		name:           clusterName,
		namespace:      clusterNamespace,
		syncNum:        2,
		primary:        "1",
		desiredPrimary: "1",
		instances:      map[string]*Instance{},
	}
	clusterName2 := clusterName + "2"
	clus2 := &Cluster{
		name:           clusterName2,
		namespace:      clusterNamespace,
		syncNum:        2,
		primary:        "1",
		desiredPrimary: "1",
		instances:      map[string]*Instance{},
	}

	clusters = map[string]PGCluster{
		clusterKey(clusterNamespace, clusterName):  clus1,
		clusterKey(clusterNamespace, clusterName2): clus2,
	}

	clus1.RegisterOrUpdateInstance("1", "dc-01", "zone", ip, true)
	clus1.RegisterOrUpdateInstance("2", "dc-02", "zone", ip, true)
	clus2.RegisterOrUpdateInstance("1", "dc-01", "zone", ip, true)
	clus2.RegisterOrUpdateInstance("2", "dc-02", "zone", ip, true)

	apiCluster1 := &pgapi.Cluster{
		ObjectMeta: v1.ObjectMeta{
			Name:      clusterName,
			Namespace: clusterNamespace,
		},
		Status: pgapi.ClusterStatus{},
	}
	apiCluster2 := &pgapi.Cluster{
		ObjectMeta: v1.ObjectMeta{
			Name:      clusterName2,
			Namespace: clusterNamespace,
		},
		Status: pgapi.ClusterStatus{},
	}
	scheme, _ := pgapi.SchemeBuilder.Build()
	cli = fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(apiCluster1, apiCluster2).
		WithObjects(apiCluster1, apiCluster2).Build()

	err := changePrimaryInstances(context.Background(), "dc-02")
	g.Expect(err).To(BeNil())
}

func Test_changePrimaryInstances_canceledContext(t *testing.T) {
	g := NewWithT(t)

	defer teardown()

	handler := slogassert.New(t, config.LevelTrace, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(slog.New(handler))

	// create cancelable context to pass into function
	ctx, cancel := context.WithCancel(context.Background())

	// no clusters existing, but function should run through
	err := changePrimaryInstances(ctx, "dc-02")
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Starting update of 0 clusters")
	handler.Reset()

	// cancel context
	cancel()

	// context canceled, exit function without further processing
	err = changePrimaryInstances(ctx, "dc-02")
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Primary change function canceled")
	handler.Reset()
}

func Test_initiateSwitchover(t *testing.T) {
	g := NewWithT(t)

	viper.Set(config.LogLevel, "debug")

	defer teardown()

	ctx := context.Background()
	events.SetListenersForRegionChange(func(context.Context, string) {})

	cluster.Store("node1", "dc-01", "dc-01-01", false)
	cluster.Store("node2", "dc-02", "dc-02-01", true)

	clus1 := &Cluster{
		name:           clusterName,
		namespace:      clusterNamespace,
		syncNum:        2,
		primary:        "1",
		desiredPrimary: "1",
		instances:      map[string]*Instance{},
	}
	clusterName2 := clusterName + "2"
	clus2 := &Cluster{
		name:           clusterName2,
		namespace:      clusterNamespace,
		syncNum:        2,
		primary:        "1",
		desiredPrimary: "1",
		instances:      map[string]*Instance{},
	}

	clusters = map[string]PGCluster{
		clusterKey(clusterNamespace, clusterName):  clus1,
		clusterKey(clusterNamespace, clusterName2): clus2,
	}

	clus1.RegisterOrUpdateInstance("1", "dc-01", "zone", ip, true)
	clus1.RegisterOrUpdateInstance("2", "dc-02", "zone", ip, false)
	clus2.RegisterOrUpdateInstance("1", "dc-01", "zone", ip, true)
	clus2.RegisterOrUpdateInstance("2", "dc-02", "zone", ip, true)

	apiCluster1 := &pgapi.Cluster{
		ObjectMeta: v1.ObjectMeta{
			Name:      clusterName,
			Namespace: clusterNamespace,
		},
		Status: pgapi.ClusterStatus{},
	}
	apiCluster2 := &pgapi.Cluster{
		ObjectMeta: v1.ObjectMeta{
			Name:      clusterName2,
			Namespace: clusterNamespace,
		},
		Status: pgapi.ClusterStatus{},
	}
	scheme, _ := pgapi.SchemeBuilder.Build()
	cli = fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(apiCluster1, apiCluster2).
		WithObjects(apiCluster1, apiCluster2).Build()

	err := clus2.initiateSwitchover(ctx)
	g.Expect(err).To(BeNil())
	err = clus1.initiateSwitchover(ctx)
	g.Expect(err).NotTo(BeNil())
	g.Expect(err.Error()).To(ContainSubstring("no eligible target instance found to be promoted to primary"))
}

func Test_isEligibleForID(t *testing.T) {
	g := NewWithT(t)

	setup()
	defer teardown()

	inst1 := clus.RegisterOrUpdateInstance("1", "dc-01", "zone", ip, true)
	inst2 := clus.RegisterOrUpdateInstance("2", "dc-01", "zone", ip, true)
	inst3 := clus.RegisterOrUpdateInstance("3", "dc-02", "zone", ip, true)
	inst4 := clus.RegisterOrUpdateInstance("4", "dc-01", "zone", ip, false)
	inst5 := clus.RegisterOrUpdateInstance("5", "dc-02", "zone", ip, false)
	inst6 := clus.RegisterOrUpdateInstance("6", "dc-01", "zone", ip, true)
	clus.desiredPrimary = "1"
	clus.primary = "1"

	// current primary
	val, _ := isEligible(inst1, false)
	g.Expect(val).To(BeTrue())

	// other sync instance in same region
	val, _ = isEligible(inst2, false)
	g.Expect(val).To(BeTrue())

	// non-sync instance in same region, but don't check for sync status
	val, _ = isEligible(inst6, false)
	g.Expect(val).To(BeTrue())

	// non-sync instance in same region, but sync status must be checked
	val, _ = isEligible(inst6, true)
	g.Expect(val).To(BeFalse())

	// unhealthy instance in same region
	val, _ = isEligible(inst4, false)
	g.Expect(val).To(BeFalse())

	// sync instance in different region
	val, _ = isEligible(inst3, false)
	g.Expect(val).To(BeFalse())

	// unhealthy instance in different region
	val, _ = isEligible(inst5, false)
	g.Expect(val).To(BeFalse())
}

// TestChangePrimaryInstances_RetryDrain evaluates the orchestrator's "retry funnel" behavior.
// When a cross-region switchover mandate is issued, the engine loops over all active clusters.
// If one cluster fails to switch (due to missing resources), it is tracked in `clusterErrors`.
// This test validates that `clustersLeft = clustersError` properly isolates broken clusters,
// ensuring healthy clusters aren't erroneously re-evaluated during backoff retries.
func TestChangePrimaryInstances_RetryDrain(t *testing.T) {
	g := NewWithT(t)

	defer teardown()

	workingCluster := &TestCluster{
		shouldSwitchoverFail: false,
	}
	clusters[clusterKey(clusterNamespace, "working")] = workingCluster
	brokenCluster := &TestCluster{
		shouldSwitchoverFail: true,
	}
	clusters[clusterKey(clusterNamespace, "broken")] = brokenCluster

	ctx, cancelFunc := context.WithCancel(context.Background())
	go changePrimaryInstances(ctx, "")

	g.Eventually(workingCluster.getSwitchoverCount, 100*time.Millisecond).Should(Equal(int32(1)))
	g.Eventually(brokenCluster.getSwitchoverCount, 2*time.Second).Should(BeNumerically(">", int32(1)))

	cancelFunc()

	// make sure, workingCluster switchover was still only called 1 time
	g.Expect(workingCluster.getSwitchoverCount()).To(Equal(int32(1)))
}

// TestInitiateSwitchover_WaitDeadlock evaluates resilience against missing targets.
// It verifies that `initiateSwitchover` decisively throws an error when no eligible
// replicas exist in the target region. This intentionally triggers a native retry loop
// to wait for sync, rather than failing over to an asynchronous replica.
func TestInitiateSwitchover_WaitDeadlock(t *testing.T) {
	g := NewWithT(t)

	viper.Set(config.LogLevel, "debug")
	config.InitLogging()

	defer viper.Reset()

	clus := RegisterOrUpdateCluster("ns2", "deadlock-db", "1", "1", 2, 1)
	defer DropCluster("ns2", "deadlock-db")
	clus.RegisterOrUpdateInstance("1", "region1", "zone", ip, true)

	cluster.Store("reset", "region2", "zone2", false)
	cluster.Store("mock", "region2", "zone2", true)

	err := initiateSwitchover(context.Background(), clus)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("no eligible target instance found to be promoted to primary"))
}

// TestInitiateSwitchover_IgnoresUninitializedClusters evaluates the architectural race condition
// during asynchronous cluster bootstrapping.
// If a region failover event fires while a Postgres cluster is cold-booting (before CNPG operator
// natively populates `TargetPrimary`), `initiateSwitchover` detects `desiredPrimary == ""`
// and silently returns `nil` (swallowing the event).
func TestInitiateSwitchover_IgnoresUninitializedClusters(t *testing.T) {
	g := NewWithT(t)

	viper.Set(config.LogLevel, "debug")
	config.InitLogging()
	defer viper.Reset()

	clusBoot := &Cluster{
		name:           "booting-cluster",
		namespace:      "default",
		syncNum:        2,
		primary:        "1",
		desiredPrimary: "", // Uninitialized
		instances:      map[string]*Instance{},
	}
	err := initiateSwitchover(context.Background(), clusBoot)
	g.Expect(err).To(BeNil())
}

// TestIsEligible_RejectsGhostNodes evaluates the operator's defense against dying pods.
// When pods are deleted or evacuated during scaling, the cache marks them `active: false`.
// This asserts that `isEligible` respects this boolean, blocking a "ghost node"
// from winning the failover election and preventing an immediate secondary outage.
func TestIsEligible_RejectsGhostNodes(t *testing.T) {
	g := NewWithT(t)
	cluster.Store("mock", "region2", "zone2", true)

	inst := &Instance{
		name:   "ghost-pod",
		region: "region2",
		active: false, // Node is terminating
	}

	eligible, reason := isEligible(inst, true)
	g.Expect(eligible).To(BeFalse())
	g.Expect(reason).To(Equal("not active"))
}

func TestInitiateSwitchover_SkipsElectionWhenCurrentPrimaryIsEligible(t *testing.T) {
	g := NewWithT(t)

	// Wipe global region state lingering from previous executions
	cluster.Store("reset-node", "region1", "zone1", false)

	ctx := config.EnrichContext(context.Background(), clusterName)

	// Create cluster with desired primary matching current primary, perfectly active in the correct region
	clus := RegisterOrUpdateCluster("", clusterName, "1", "1", 3, 2)
	defer DropCluster("", clusterName)

	// Designate the target leading datacenter exactly where our node is
	cluster.Store("mock-leading-node", "region1", "zone1", true)

	// Initialize the primary instance natively in the correct region and healthy
	clus.RegisterOrUpdateInstance("1", "region1", "zone", ip, true)

	// The engine should immediately recognize the topology is perfect and abort the switchover natively
	err := initiateSwitchover(ctx, clus)

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(clus.GetCurrentPrimaryInstanceId()).To(Equal("1"))
	g.Expect(clus.GetDesiredPrimaryInstanceId()).To(Equal("1"))
}

func TestApplyNewPrimary_IgnoresDuplicateAssignments(t *testing.T) {
	g := NewWithT(t)

	ctx := config.EnrichContext(context.Background(), clusterName)

	// Setup a fake k8s client with a Cluster object that ALREADY has TargetPrimary assigned to our desired target ("2")
	apiCluster := &pgapi.Cluster{
		ObjectMeta: v1.ObjectMeta{
			Name:      clusterName,
			Namespace: clusterNamespace,
		},
		Status: pgapi.ClusterStatus{
			TargetPrimary: "2",
		},
	}
	scheme, _ := pgapi.SchemeBuilder.Build()
	cli = fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(apiCluster).
		WithObjects(apiCluster).Build()

	// Instruct the engine to apply "2" as the new primary
	err := applyNewPrimary(ctx, clusterName, clusterNamespace, "2")

	// It should cleanly swallow the patch update and return nil without modifying k8s
	g.Expect(err).ToNot(HaveOccurred())
}

// TestPatchClusterWithRetry_NoChange verifies that patchClusterWithRetry short-circuits
// without sending a Status().Patch() when the change function returns `changed: false`.
// This covers the `!changed` early-return code path in patchClusterWithRetry.
func TestPatchClusterWithRetry_NoChange(t *testing.T) {
	g := NewWithT(t)

	defer teardown()

	// Create a Cluster object in the fake API server
	apiCluster := &pgapi.Cluster{
		ObjectMeta: v1.ObjectMeta{
			Name:      clusterName,
			Namespace: clusterNamespace,
		},
		Status: pgapi.ClusterStatus{
			CurrentPrimary: clusterName + "-1",
			TargetPrimary:  clusterName + "-1",
		},
	}
	scheme, _ := pgapi.SchemeBuilder.Build()
	cli = fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(apiCluster).
		WithObjects(apiCluster).Build()

	// Call patchClusterWithRetry with a change function that always returns changed=false.
	// This proves the !changed short-circuit: no Status().Patch() is sent.
	changeCalled := false
	err := patchClusterWithRetry(context.Background(), clusterNamespace, clusterName,
		func(cluster *pgapi.Cluster) (*pgapi.Cluster, bool) {
			changeCalled = true
			return nil, false
		},
	)
	g.Expect(err).ToNot(HaveOccurred(), "patchClusterWithRetry must succeed when no change is needed")
	g.Expect(changeCalled).To(BeTrue(), "Change function must still be called to evaluate the condition")

	// Verify the API server state is completely untouched
	saved := &pgapi.Cluster{}
	err = cli.Get(context.Background(), types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, saved)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(saved.Status.TargetPrimary).To(Equal(clusterName+"-1"),
		"TargetPrimary must be untouched when change returns false")
	g.Expect(saved.Status.Phase).To(BeEmpty(),
		"Phase must remain empty when no patch is sent")
}

// ----- HELPERS ------

func setup() {
	viper.Set(config.LogLevel, "debug")
	viper.Set(config.LabelRegion, "topology.kubernetes.io/region")
	viper.Set(config.LabelZone, "topology.kubernetes.io/zone")

	events.SetListenersForRegionChange(func(context.Context, string) {})

	cluster.Store("node1", "dc-01", "dc-01-01", true)
	cluster.Store("node2", "dc-01", "dc-01-02", true)
	cluster.Store("node3", "dc-02", "dc-02-01", false)

	clus = &Cluster{
		name:      clusterName,
		namespace: clusterNamespace,
		syncNum:   2,
		instances: map[string]*Instance{},
	}
}
