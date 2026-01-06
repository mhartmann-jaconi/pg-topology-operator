package pods

import (
	"testing"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	postgres "github.com/jaconi-io/ipoque-pg-webhook/pkg/postgres"
	. "github.com/onsi/gomega"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const clusterName = "pg"
const zoneLabel = "topology.kubernetes.io/zone"

func TestApplyNodeSelector(t *testing.T) {
	g := NewWithT(t)

	viper.Set(config.LabelZone, zoneLabel)
	defer viper.Reset()

	// nothing should change, as the node selector is already applied
	pod1 := createPod("1", "zone", false)
	err := applyNodeSelector(pod1)
	g.Expect(err).To(BeNil())
	g.Expect(pod1.Spec.NodeSelector[zoneLabel]).To(Equal("zone"))

	// already a zone registered for instance, which should get applied
	postgres.CreateOrUpdateCluster(clusterName, "", "", 2)
	postgres.SetZoneForInstanceId(clusterName, "2", "zone2", true)
	pod2 := createPod("2", "", false)
	err = applyNodeSelector(pod2)
	g.Expect(err).To(BeNil())
	g.Expect(pod2.Spec.NodeSelector[zoneLabel]).To(Equal("zone2"))

	// primary instance should get primary zone
	cluster.Store("node3", "zone3", "region", true)
	cluster.Store("node4", "zone4", "region", false)
	postgres.SetZoneForInstanceId(clusterName, "3", "zone3", true)
	pod3 := createPod("3", "", true)
	err = applyNodeSelector(pod3)
	g.Expect(err).To(BeNil())
	g.Expect(pod3.Spec.NodeSelector[zoneLabel]).To(Equal("zone3"))

	// a non-primary pod should get the least used zone applied
	postgres.SetZoneForInstanceId(clusterName, "4", "zone4", true)
	pod4 := createPod("4", "", false)
	err = applyNodeSelector(pod4)
	g.Expect(err).To(BeNil())
	g.Expect(pod4.Spec.NodeSelector[zoneLabel]).To(Equal("zone4"))
}

func createPod(idx, zone string, primary bool) *corev1.Pod {
	pd := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName + "-" + idx,
			Labels: map[string]string{
				postgres.LabelPGClusterName:  clusterName,
				postgres.LabelPGInstanceRole: postgres.PGRoleReplica,
				postgres.LabelPGInstanceName: clusterName + "-" + idx,
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	if zone != "" {
		pd.Spec = corev1.PodSpec{
			NodeSelector: map[string]string{
				zoneLabel: zone,
			},
		}
	}
	if primary {
		pd.ObjectMeta.Labels[postgres.LabelPGInstanceRole] = postgres.PGRolePrimary
	}
	return pd
}
