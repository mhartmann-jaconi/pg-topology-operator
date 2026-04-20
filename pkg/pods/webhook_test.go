package pods

import (
	"context"
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
const regionLabel = "topology.kubernetes.io/region"

func TestApplyNodeSelector(t *testing.T) {
	g := NewWithT(t)

	viper.Set(config.LabelRegion, regionLabel)
	defer viper.Reset()
	cluster.Store("node1", "region1", true)
	cluster.Store("node2", "region2", false)

	ctx := config.EnrichContext(context.TODO(), clusterName)

	// cluster not existing
	pod1 := createPod("1", "region1", false)
	err := applyNodeSelector(ctx, pod1)
	g.Expect(err).To(HaveOccurred())

	// nothing should change, as the node selector is already applied
	clus := postgres.RegisterOrUpdateCluster("", clusterName, "", "", 3, 2)
	err = applyNodeSelector(ctx, pod1)
	g.Expect(err).To(BeNil())
	g.Expect(pod1.Spec.NodeSelector[regionLabel]).To(Equal("region1"))

	// pod has neither primary label nor is it instance ID '1'
	pod2 := createPod("2", "", false)
	err = applyNodeSelector(ctx, pod2)
	g.Expect(err).To(BeNil())
	g.Expect(pod2.Spec.NodeSelector).To(BeEmpty())

	// pod has primary label applied so it gets a nodeselector
	pod3 := createPod("3", "", true)
	err = applyNodeSelector(ctx, pod3)
	g.Expect(err).To(BeNil())
	g.Expect(pod3.Spec.NodeSelector[regionLabel]).To(Equal("region1"))

	// pod has no primary label, but instance ID '1'; so it should get the label
	pod1 = createPod("1", "", false)
	err = applyNodeSelector(ctx, pod1)
	g.Expect(err).To(BeNil())
	g.Expect(pod1.Spec.NodeSelector[regionLabel]).To(Equal("region1"))

	// cluster has primary initialized, so nothing should change at all from now on
	clus.SetPrimary("1")
	pod1 = createPod("1", "", false)
	err = applyNodeSelector(ctx, pod1)
	g.Expect(err).To(BeNil())
	g.Expect(pod1.Spec.NodeSelector).To(BeEmpty())
}

func createPod(idx, region string, primary bool) *corev1.Pod {
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
	if region != "" {
		pd.Spec = corev1.PodSpec{
			NodeSelector: map[string]string{
				regionLabel: region,
			},
		}
	}
	if primary {
		pd.ObjectMeta.Labels[postgres.LabelPGInstanceRole] = postgres.PGRolePrimary
	}
	return pd
}
