package postgres

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestGetInstances(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	// tests the sorting of the return value
	clus := RegisterOrUpdateCluster("", "foo", "1", "1", 5, 2)
	clus.RegisterOrUpdateInstance("3", "", true)
	clus.RegisterOrUpdateInstance("5", "", true)
	clus.RegisterOrUpdateInstance("2", "", true)
	clus.RegisterOrUpdateInstance("4", "", true)
	clus.RegisterOrUpdateInstance("1", "", true)

	instances := clus.GetInstances()
	g.Expect(instances[0].instanceId).To(Equal("1"))
	g.Expect(instances[1].instanceId).To(Equal("2"))
	g.Expect(instances[2].instanceId).To(Equal("3"))
	g.Expect(instances[3].instanceId).To(Equal("4"))
	g.Expect(instances[4].instanceId).To(Equal("5"))
}

func TestRegisterOrUpdateInstance(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	// test that instances are updated inline and not replaced
	clus := RegisterOrUpdateCluster("", "foo", "1", "1", 2, 2)
	inst := clus.RegisterOrUpdateInstance("1", "", true)

	clus.RegisterOrUpdateInstance("1", "region", false)
	g.Expect(inst.IsActive()).To(BeFalse())
	g.Expect(inst.GetRegion()).To(Equal("region"))

	// verify that only 'numerical' values are allowed
	inst2 := clus.RegisterOrUpdateInstance("two", "region", true)
	g.Expect(inst2).To(BeNil())
	g.Expect(clus.GetInstanceByID("2")).To(BeNil())
	g.Expect(clus.GetInstanceByID("two")).To(BeNil())
}

func TestMarkInactive(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	clus := RegisterOrUpdateCluster("", "foo", "1", "1", 1, 2)
	inst := clus.RegisterOrUpdateInstance("1", "", true)
	g.Expect(inst.IsActive()).To(BeTrue())

	clus.MarkInactive("1")
	g.Expect(inst.IsActive()).To(BeFalse())
}

func TestRegistrationComplete(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	clus := RegisterOrUpdateCluster("", "foo", "1", "1", 2, 2)
	g.Expect(clus.RegistrationComplete()).To(BeFalse())

	clus.RegisterOrUpdateInstance("1", "", true)
	g.Expect(clus.RegistrationComplete()).To(BeFalse())

	clus.RegisterOrUpdateInstance("2", "", true)
	g.Expect(clus.RegistrationComplete()).To(BeTrue())
}

func TestIsSyncReplica(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	clus := RegisterOrUpdateCluster("", "foo", "1", "1", 6, 2)
	inst1 := clus.RegisterOrUpdateInstance("1", "", true)
	inst2 := clus.RegisterOrUpdateInstance("2", "", true)
	inst3 := clus.RegisterOrUpdateInstance("3", "", false)
	inst4 := clus.RegisterOrUpdateInstance("4", "", false)
	inst5 := clus.RegisterOrUpdateInstance("5", "", true)
	inst6 := clus.RegisterOrUpdateInstance("6", "", true)

	g.Expect(inst1.IsSyncReplica()).To(BeTrue())  // primary
	g.Expect(inst2.IsSyncReplica()).To(BeTrue())  // sync-replica
	g.Expect(inst3.IsSyncReplica()).To(BeFalse()) // unhealthy
	g.Expect(inst4.IsSyncReplica()).To(BeFalse()) // unhealthy
	g.Expect(inst5.IsSyncReplica()).To(BeTrue())  // next sync-replica in line
	g.Expect(inst6.IsSyncReplica()).To(BeFalse()) // async

	// mark instance 3 as healthy again, then it should become a sync-replica
	clus.RegisterOrUpdateInstance("3", "", true)

	g.Expect(inst3.IsSyncReplica()).To(BeTrue())  // now a sync one
	g.Expect(inst5.IsSyncReplica()).To(BeFalse()) // not sync anymore
}

func cleanup() {
	clusters = make(map[string]*Cluster)
}
