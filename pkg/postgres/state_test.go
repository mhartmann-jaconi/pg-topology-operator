package postgres

import (
	"context"
	"log/slog"
	"os"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/thejerf/slogassert"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/config"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/events"
)

func init() {
	// Suppress background K8s patch goroutines aggressively globally for unit tests
	// unless a specific integration test configures a fake client or envtest.
	// This definitively resolving async "nil pointer dereference" background panics across the suite.
	events.SetListenersForRegionChange(func(context.Context, string) {})
}

func TestGetInstances(t *testing.T) {
	g := NewWithT(t)
	defer teardown()

	// tests the sorting of the return value
	clus := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 5, 2)
	clus.RegisterOrUpdateInstance("3", "", "zone", ip, true)
	clus.RegisterOrUpdateInstance("5", "", "zone", ip, true)
	clus.RegisterOrUpdateInstance("2", "", "zone", ip, true)
	clus.RegisterOrUpdateInstance("4", "", "zone", ip, true)
	clus.RegisterOrUpdateInstance("1", "", "zone", ip, true)

	instances := clus.GetInstances()
	g.Expect(instances[0].instanceId).To(Equal("1"))
	g.Expect(instances[1].instanceId).To(Equal("2"))
	g.Expect(instances[2].instanceId).To(Equal("3"))
	g.Expect(instances[3].instanceId).To(Equal("4"))
	g.Expect(instances[4].instanceId).To(Equal("5"))
}

func TestRegisterOrUpdateInstance(t *testing.T) {
	g := NewWithT(t)
	defer teardown()

	// test that instances are updated inline and not replaced
	clus := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 2, 2)
	inst := clus.RegisterOrUpdateInstance("1", "", "zone", ip, true)

	clus.RegisterOrUpdateInstance("1", "region", "zone", ip, false)
	g.Expect(inst.IsActive()).To(BeFalse())
	g.Expect(inst.GetRegion()).To(Equal("region"))

	// verify that only 'numerical' values are allowed
	inst2 := clus.RegisterOrUpdateInstance("two", "region", "zone", ip, true)
	g.Expect(inst2).To(BeNil())
	g.Expect(clus.GetInstanceByID("2")).To(BeNil())
	g.Expect(clus.GetInstanceByID("two")).To(BeNil())
}

func TestMarkInactive(t *testing.T) {
	g := NewWithT(t)
	defer teardown()

	clus := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 1, 2)
	inst := clus.RegisterOrUpdateInstance("1", "", "zone", ip, true)
	g.Expect(inst.IsActive()).To(BeTrue())

	clus.MarkInactive("1")
	g.Expect(inst.IsActive()).To(BeFalse())
}

func TestRegistrationComplete(t *testing.T) {
	g := NewWithT(t)
	defer teardown()

	clus := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 2, 2)
	g.Expect(clus.RegistrationComplete()).To(BeFalse())

	clus.RegisterOrUpdateInstance("1", "", "zone", ip, true)
	g.Expect(clus.RegistrationComplete()).To(BeFalse())

	clus.RegisterOrUpdateInstance("2", "", "zone", ip, true)
	g.Expect(clus.RegistrationComplete()).To(BeTrue())
}

func TestIsSyncReplica(t *testing.T) {
	g := NewWithT(t)
	defer teardown()

	clus := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 6, 2)
	inst1 := clus.RegisterOrUpdateInstance("1", "", "zone", ip, true)
	inst2 := clus.RegisterOrUpdateInstance("2", "", "zone", ip, true)
	inst3 := clus.RegisterOrUpdateInstance("3", "", "zone", ip, false)
	inst4 := clus.RegisterOrUpdateInstance("4", "", "zone", ip, false)
	inst5 := clus.RegisterOrUpdateInstance("5", "", "zone", ip, true)
	inst6 := clus.RegisterOrUpdateInstance("6", "", "zone", ip, true)

	g.Expect(inst1.IsSyncReplica()).To(BeTrue())  // primary
	g.Expect(inst2.IsSyncReplica()).To(BeTrue())  // sync-replica
	g.Expect(inst3.IsSyncReplica()).To(BeFalse()) // unhealthy
	g.Expect(inst4.IsSyncReplica()).To(BeFalse()) // unhealthy
	g.Expect(inst5.IsSyncReplica()).To(BeTrue())  // next sync-replica in line
	g.Expect(inst6.IsSyncReplica()).To(BeFalse()) // async

	// mark instance 3 as healthy again, then it should become a sync-replica
	clus.RegisterOrUpdateInstance("3", "", "zone", ip, true)

	g.Expect(inst3.IsSyncReplica()).To(BeTrue())  // now a sync one
	g.Expect(inst5.IsSyncReplica()).To(BeFalse()) // not sync anymore
}

func TestInterfacePointerRoundtrip(t *testing.T) {
	g := NewWithT(t)
	defer teardown()

	clus := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 1, 2)
	inst := clus.RegisterOrUpdateInstance("1", "", "zone", ip, true)
	g.Expect(inst.IsActive()).To(BeTrue())
	g.Expect(inst.GetRegion()).To(BeEmpty())

	clus.MarkInactive("1")
	g.Expect(inst.IsActive()).To(BeFalse())
	g.Expect(inst.GetRegion()).To(BeEmpty())

	clusx := GetCluster(clusterNamespace, clusterName)
	instx := clusx.GetInstanceByID("1")

	g.Expect(instx.IsActive()).To(BeFalse())
	g.Expect(instx.GetRegion()).To(BeEmpty())

	clusx.RegisterOrUpdateInstance("1", "region", "zone", ip, true)

	g.Expect(instx.IsActive()).To(BeTrue())
	g.Expect(instx.GetRegion()).To(Equal("region"))
	g.Expect(inst.IsActive()).To(BeTrue())
	g.Expect(inst.GetRegion()).To(Equal("region"))

	instx.MarkInactive()

	g.Expect(instx.IsActive()).To(BeFalse())
	g.Expect(inst.IsActive()).To(BeFalse())
}

// TestSetPrimary verifies that SetPrimary correctly updates the cluster's current primary
// instance ID without affecting other cluster state.
func TestSetPrimary(t *testing.T) {
	g := NewWithT(t)
	defer teardown()

	clus := RegisterOrUpdateCluster("", "foo", "1", "1", 3, 2)

	// Verify initial primary
	g.Expect(clus.GetCurrentPrimaryInstanceId()).To(Equal("1"))

	// Change primary to instance "2" and verify the update took effect
	clus.(*Cluster).SetPrimary("2")
	g.Expect(clus.GetCurrentPrimaryInstanceId()).To(Equal("2"))
}

// TestRegisterOrUpdateCluster_UpdatePath verifies that calling RegisterOrUpdateCluster
// a second time updates the existing cluster's fields instead of creating a new one.
func TestRegisterOrUpdateCluster_UpdatePath(t *testing.T) {
	g := NewWithT(t)
	defer teardown()

	// Register initial cluster
	clus := RegisterOrUpdateCluster("ns", "bar", "1", "1", 3, 2)
	clus.RegisterOrUpdateInstance("1", "region1", "zone", ip, true)

	// Update the cluster with new values
	clus2 := RegisterOrUpdateCluster("ns", "bar", "2", "3", 5, 4)

	// All updated fields should reflect the new values
	g.Expect(clus2.GetCurrentPrimaryInstanceId()).To(Equal("2"))
	g.Expect(clus2.GetDesiredPrimaryInstanceId()).To(Equal("3"))
	g.Expect(clus2.GetSyncInstanceCount()).To(Equal(4))
	g.Expect(clus2.GetNamespace()).To(Equal("ns"))
	g.Expect(clus2.GetName()).To(Equal("bar"))

	// Verify desiredInstances updated from 3 to 5: register 4 instances (would be
	// complete with old value 3, but incomplete with new value 5)
	clus2.RegisterOrUpdateInstance("2", "region1", "zone", ip, true)
	clus2.RegisterOrUpdateInstance("3", "region1", "zone", ip, true)
	clus2.RegisterOrUpdateInstance("4", "region1", "zone", ip, true)
	g.Expect(clus2.RegistrationComplete()).To(BeFalse())

	// Adding a 5th instance completes registration, proving desiredInstances is 5
	clus2.RegisterOrUpdateInstance("5", "region1", "zone", ip, true)
	g.Expect(clus2.RegistrationComplete()).To(BeTrue())
}

// TestDropCluster verifies that dropping a cluster removes it from the internal state
// and that dropping a non-existing cluster does not panic or corrupt remaining state.
func TestDropCluster(t *testing.T) {
	g := NewWithT(t)
	defer teardown()

	// Register two clusters
	RegisterOrUpdateCluster("ns", "drop-me", "1", "1", 3, 2)
	RegisterOrUpdateCluster("ns", "keep-me", "1", "1", 3, 2)
	g.Expect(GetCluster("ns", "drop-me")).ToNot(BeNil())
	g.Expect(GetCluster("ns", "keep-me")).ToNot(BeNil())

	// Drop one cluster and verify only that one is gone
	DropCluster("ns", "drop-me")
	g.Expect(GetCluster("ns", "drop-me")).To(BeNil())
	g.Expect(GetCluster("ns", "keep-me")).ToNot(BeNil())

	// Dropping a non-existing cluster should not panic or affect the remaining cluster
	DropCluster("ns", "does-not-exist")
	g.Expect(GetCluster("ns", "keep-me")).ToNot(BeNil())
}

// TestInitiateSwitchover_IncompleteRegistration verifies that initiateSwitchover
// returns an error when the cluster's instance registration is not yet complete.
// The election engine must not attempt a switchover before all expected instances
// have registered, as it would make decisions with incomplete topology information.
func TestInitiateSwitchover_IncompleteRegistration(t *testing.T) {
	g := NewWithT(t)
	defer teardown()

	// Create a cluster that expects 3 instances but register only 1
	clus := RegisterOrUpdateCluster("ns", "incomplete", "1", "1", 3, 2)
	clus.RegisterOrUpdateInstance("1", "region-a", "zone", ip, true)
	g.Expect(clus.RegistrationComplete()).To(BeFalse())

	// initiateSwitchover should refuse because the cluster is not fully registered
	err := clus.(*Cluster).initiateSwitchover(context.Background())
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cluster not fully registered yet"))
}

// TestAllInstancesHealthy verifies that AllInstancesHealthy returns true only
// when every registered instance is active, and false when any instance is inactive.
func TestAllInstancesHealthy(t *testing.T) {
	g := NewWithT(t)
	defer teardown()

	clus := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 3, 2)

	// No instances registered → vacuously true (empty map iteration)
	g.Expect(clus.AllInstancesHealthy()).To(BeTrue())

	// All instances healthy
	clus.RegisterOrUpdateInstance("1", "region", "zone", ip, true)
	clus.RegisterOrUpdateInstance("2", "region", "zone", ip, true)
	clus.RegisterOrUpdateInstance("3", "region", "zone", ip, true)
	g.Expect(clus.AllInstancesHealthy()).To(BeTrue())

	// One instance unhealthy → false
	clus.MarkInactive("2")
	g.Expect(clus.AllInstancesHealthy()).To(BeFalse())

	// Re-activate → true again
	clus.RegisterOrUpdateInstance("2", "region", "zone", ip, true)
	g.Expect(clus.AllInstancesHealthy()).To(BeTrue())
}

// TestGetPrimaryInstance verifies that GetPrimaryInstance returns the correct
// instance when a valid primary is set, and nil when the primary ID doesn't
// match any registered instance.
func TestGetPrimaryInstance(t *testing.T) {
	g := NewWithT(t)
	defer teardown()

	clus := RegisterOrUpdateCluster(clusterNamespace, clusterName, "2", "2", 3, 2)
	clus.RegisterOrUpdateInstance("1", "region-a", "zone-a", ip, true)
	clus.RegisterOrUpdateInstance("2", "region-a", "zone-b", ip, true)

	// Primary is instance "2"
	primary := clus.GetPrimaryInstance()
	g.Expect(primary).ToNot(BeNil())
	g.Expect(primary.GetRegion()).To(Equal("region-a"))
	g.Expect(primary.GetZone()).To(Equal("zone-b"))

	// Set primary to a non-existent instance → nil
	clus.(*Cluster).SetPrimary("99")
	g.Expect(clus.GetPrimaryInstance()).To(BeNil())
}

func TestConcurrent_RegisterAndGetCluster(t *testing.T) {

	defer teardown()

	done := make(chan struct{})

	// Writer goroutine: continuously registers clusters
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			RegisterOrUpdateCluster("ns", "race-test", "1", "1", 3, 2)
		}
	}()

	// Reader goroutine: continuously reads the cluster
	for i := 0; i < 1000; i++ {
		GetCluster("ns", "race-test")
	}

	<-done
}

func TestConcurrent_RegisterAndDropCluster(t *testing.T) {
	defer teardown()

	done := make(chan struct{})

	// Writer goroutine: continuously registers clusters
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			RegisterOrUpdateCluster("ns", "race-drop", "1", "1", 3, 2)
		}
	}()

	// Deleter goroutine: continuously drops the same cluster
	for i := 0; i < 1000; i++ {
		DropCluster("ns", "race-drop")
	}

	<-done
}

func TestDropCluster_LogMessage(t *testing.T) {

	g := NewWithT(t)
	defer teardown()

	handler := slogassert.New(t, config.LevelTrace, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(slog.New(handler))

	// Register a cluster and drop it — should log the removal
	RegisterOrUpdateCluster("ns", "log-test", "1", "1", 3, 2)
	DropCluster("ns", "log-test")
	handler.AssertMessage("Remove cluster from internal state")
	handler.Reset()

	// Drop a non-existing cluster — should NOT log the removal
	DropCluster("ns", "never-existed")
	for _, lm := range handler.Unasserted() {
		g.Expect(lm.Message).ToNot(Equal("Remove cluster from internal state"))
	}
}
