package postgres

import (
	"testing"

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

func Test_findNewEligibleTargetInstance(t *testing.T) {
	g := NewWithT(t)

	defer viper.Reset()

	setup()

	// test region fallback
	pd1 := createPod("1", "east-1", "east", true)
	pd1.Status.Phase = corev1.PodFailed
	pd2 := createPod("2", "east-2", "east", false)

	cli = fake.NewClientBuilder().WithObjects(pd1, pd2).Build()

	eligiblePod, err := findNewEligibleTargetInstance(clusters[clusterName], false)
	g.Expect(err).To(BeNil())
	g.Expect(eligiblePod).To(Equal("pg-2"))
}

func Test_findNewEligibleTargetInstanceInternal(t *testing.T) {
	g := NewWithT(t)

	defer viper.Reset()

	setup()

	eligiblePod, err := findNewEligibleTargetInstanceInternal(clusters[clusterName], true, false)
	g.Expect(err).To(BeNil())
	g.Expect(eligiblePod).To(Equal("pg-1"))

	eligiblePod, err = findNewEligibleTargetInstanceInternal(clusters[clusterName], false, false)
	g.Expect(err).To(BeNil())
	g.Expect(eligiblePod).To(Equal("pg-1"))

	eligiblePod, err = findNewEligibleTargetInstanceInternal(clusters[clusterName], false, true)
	g.Expect(err).To(BeNil())
	g.Expect(eligiblePod).To(Equal("pg-2"))

	eligiblePod, err = findNewEligibleTargetInstanceInternal(clusters[clusterName], true, true)
	g.Expect(err).To(BeNil())
	g.Expect(eligiblePod).To(BeEmpty())

	// simulate that the primary primary zone changed
	cluster.Store("node3", "west-1", "west", true)

	eligiblePod, err = findNewEligibleTargetInstanceInternal(clusters[clusterName], true, false)
	g.Expect(err).To(BeNil())
	g.Expect(eligiblePod).To(Equal("pg-3"))

	// simualte that pg-3 is not a sync-replica anymore, then no result should be returned
	clus := clusters[clusterName]
	clus.standbies = 1
	clusters[clusterName] = clus

	eligiblePod, err = findNewEligibleTargetInstanceInternal(clusters[clusterName], true, false)
	g.Expect(err).To(BeNil())
	g.Expect(eligiblePod).To(BeEmpty())
}

func setup() {
	viper.Set(config.LogLevel, "debug")
	viper.Set(config.LabelRegion, "topology.kubernetes.io/region")
	viper.Set(config.LabelZone, "topology.kubernetes.io/zone")
	config.InitLogging()
	viper.Set(config.PgVolumes, map[string]string{
		"east": "sc-east",
		"west": "sc-west",
	})
	cluster.Store("node1", "east-1", "east", true)
	cluster.Store("node2", "east-2", "east", false)
	cluster.Store("node3", "west-1", "west", false)

	clusters = map[string]Cluster{
		clusterName: {
			name:      clusterName,
			namespace: clusterNamespace,
			standbies: 2,
			instances: map[string]instance{},
		},
	}

	pd1 := createPod("1", "east-1", "east", true)
	pd2 := createPod("2", "east-2", "east", false)
	pd3 := createPod("3", "west-1", "west", false)
	pd4 := createPod("4", "east-1", "east", false)

	cli = fake.NewClientBuilder().WithObjects(pd1, pd2, pd3, pd4).Build()
}

func createPod(idx, zone, region string, primary bool) *corev1.Pod {
	pd := &corev1.Pod{
		ObjectMeta: v1.ObjectMeta{
			Name:      clusterName + "-" + idx,
			Namespace: clusterNamespace,
			Labels: map[string]string{
				LabelPGClusterName:              clusterName,
				LabelPGInstanceRole:             PGRoleReplica,
				LabelPGInstanceName:             clusterName + "-" + idx,
				"topology.kubernetes.io/region": region,
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"topology.kubernetes.io/zone": zone,
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	clus := clusters[clusterName]
	clus.instances[idx] = instance{instanceId: idx, zone: zone, active: true}
	if primary {
		pd.ObjectMeta.Labels[LabelPGInstanceRole] = PGRolePrimary
		clus.primary = idx
	}
	clusters[clusterName] = clus
	return pd
}
