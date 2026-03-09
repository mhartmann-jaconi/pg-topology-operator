package postgres

import (
	"testing"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	. "github.com/onsi/gomega"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const clusterName = "pg"
const clusterNamespace = "default"

var clus *Cluster

func Test_isEligibleForID(t *testing.T) {
	g := NewWithT(t)

	defer viper.Reset()

	setup()

	inst1 := clus.RegisterOrUpdateInstance("1", "east", true)
	inst2 := clus.RegisterOrUpdateInstance("2", "east", true)
	inst3 := clus.RegisterOrUpdateInstance("3", "west", true)
	inst4 := clus.RegisterOrUpdateInstance("4", "east", false)
	inst5 := clus.RegisterOrUpdateInstance("5", "west", false)
	inst6 := clus.RegisterOrUpdateInstance("6", "east", true)
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

// ----- HELPERS ------

func setup() {
	viper.Set(config.LogLevel, "debug")
	viper.Set(config.LabelRegion, "topology.kubernetes.io/region")
	config.InitLogging()
	cluster.Store("node1", "east", true)
	cluster.Store("node2", "east", true)
	cluster.Store("node3", "west", false)

	clus = &Cluster{
		name:      clusterName,
		namespace: clusterNamespace,
		syncNum:   2,
		instances: map[string]*Instance{},
	}
}

func createPod(idx, region string, primary bool) *corev1.Pod {
	pd := &corev1.Pod{
		ObjectMeta: v1.ObjectMeta{
			Name:      clusterName + "-" + idx,
			Namespace: clusterNamespace,
			Labels: map[string]string{
				LabelPGClusterName:  clusterName,
				LabelPGInstanceRole: PGRoleReplica,
				LabelPGInstanceName: clusterName + "-" + idx,
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"topology.kubernetes.io/region": region,
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	clus.RegisterOrUpdateInstance(idx, region, true)
	if primary {
		pd.ObjectMeta.Labels[LabelPGInstanceRole] = PGRolePrimary
		clus.primary = idx
		clus.desiredPrimary = idx
	}
	return pd
}
