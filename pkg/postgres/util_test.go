package postgres

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestIsPrimary(t *testing.T) {
	g := NewWithT(t)

	// Test primary instance role
	labels := map[string]string{LabelPGInstanceRole: PGRolePrimary}
	g.Expect(IsPrimary(labels)).To(BeTrue())

	// Test primary legacy role
	labels = map[string]string{LabelPGRole: PGRolePrimary}
	g.Expect(IsPrimary(labels)).To(BeTrue())

	// Test initdb job role
	labels = map[string]string{LabelPGJobRole: PGRoleInitDB}
	g.Expect(IsPrimary(labels)).To(BeTrue())

	// Test other job role
	labels = map[string]string{LabelPGJobRole: "foo"}
	g.Expect(IsPrimary(labels)).To(BeFalse())

	// Test replica instance role
	labels = map[string]string{LabelPGInstanceRole: PGRoleReplica}
	g.Expect(IsPrimary(labels)).To(BeFalse())

	// Test replica legacy role
	labels = map[string]string{LabelPGRole: PGRoleReplica}
	g.Expect(IsPrimary(labels)).To(BeFalse())

	// Test other instance role
	labels = map[string]string{LabelPGInstanceRole: "foo"}
	g.Expect(IsPrimary(labels)).To(BeFalse())

	// Test empty
	labels = map[string]string{}
	g.Expect(IsPrimary(labels)).To(BeFalse())
}

func TestGetInstanceId(t *testing.T) {
	g := NewWithT(t)

	labels := map[string]string{
		LabelPGClusterName:  "my-cluster",
		LabelPGInstanceName: "my-cluster-42",
	}
	g.Expect(GetInstanceId(labels)).To(Equal("42"))

	// Empty instance name
	labels = map[string]string{
		LabelPGClusterName: "my-cluster",
	}
	g.Expect(GetInstanceId(labels)).To(Equal(""))
}

func TestExtractInstanceId(t *testing.T) {
	g := NewWithT(t)

	g.Expect(ExtractInstanceId("cluster-example", "cluster-example-1")).To(Equal("1"))
	g.Expect(ExtractInstanceId("pg", "pg-15")).To(Equal("15"))
	g.Expect(ExtractInstanceId("pg-cluster", "pg-cluster-99")).To(Equal("99"))
	g.Expect(ExtractInstanceId("pg", "")).To(Equal(""))
}

func TestGetClusterName(t *testing.T) {
	g := NewWithT(t)

	labels := map[string]string{
		LabelPGClusterName: "test-cluster",
	}
	g.Expect(GetClusterName(labels)).To(Equal("test-cluster"))

	labels = map[string]string{}
	g.Expect(GetClusterName(labels)).To(Equal(""))
}
