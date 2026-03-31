package postgres

import (
	"testing"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	. "github.com/onsi/gomega"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const clusterName = "pg"
const clusterNamespace = "default"

var clus *Cluster

func Test_changePrimaryInstances(t *testing.T) {
	g := NewWithT(t)

	defer viper.Reset()

	viper.Set(config.LogLevel, "debug")
	config.InitLogging()

	cluster.Store("node1", "dc-01", false)
	cluster.Store("node2", "dc-02", true)

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

	clusters = map[string]*Cluster{
		clusterKey(clusterNamespace, clusterName):  clus1,
		clusterKey(clusterNamespace, clusterName2): clus2,
	}

	clus1.RegisterOrUpdateInstance("1", "dc-01", true)
	clus1.RegisterOrUpdateInstance("2", "dc-02", false)
	clus2.RegisterOrUpdateInstance("1", "dc-01", true)
	clus2.RegisterOrUpdateInstance("2", "dc-02", true)

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

	err := changePrimaryInstances("dc-02")
	g.Expect(err).NotTo(BeNil())
	g.Expect(err.Error()).To(ContainSubstring("no eligible target instance found to be promoted to primary"))
}

func Test_isEligibleForID(t *testing.T) {
	g := NewWithT(t)

	defer viper.Reset()

	setup()

	inst1 := clus.RegisterOrUpdateInstance("1", "dc-01", true)
	inst2 := clus.RegisterOrUpdateInstance("2", "dc-01", true)
	inst3 := clus.RegisterOrUpdateInstance("3", "dc-02", true)
	inst4 := clus.RegisterOrUpdateInstance("4", "dc-01", false)
	inst5 := clus.RegisterOrUpdateInstance("5", "dc-02", false)
	inst6 := clus.RegisterOrUpdateInstance("6", "dc-01", true)
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
	cluster.Store("node1", "dc-01", true)
	cluster.Store("node2", "dc-01", true)
	cluster.Store("node3", "dc-02", false)

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
