package pods

import (
	"context"
	"fmt"
	"testing"

	"log/slog"
	"os"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	postgres "github.com/jaconi-io/ipoque-pg-webhook/pkg/postgres"
	. "github.com/onsi/gomega"
	"github.com/spf13/viper"
	"github.com/thejerf/slogassert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var webhook = defaulter{}

func TestDefault(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	viper.Set(config.LabelRegion, regionLabel)
	defer teardown()

	handler := slogassert.New(t, config.LevelTrace, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(slog.New(handler))

	// not a pod object
	pgcluster := &pgapi.Cluster{}
	err := webhook.Default(ctx, pgcluster)
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Failed to call pod webhook")
	handler.Reset()

	// pod with no cluster name label
	pod1 := createPod("1", "", true)
	delete(pod1.Labels, postgres.LabelPGClusterName)
	err = webhook.Default(ctx, pod1)
	g.Expect(err).To(BeNil())
	// slogassert uses substring matching by default for some checks, but it's safe to just check for "Skip pod"
	handler.AssertMessage("Skip pod pg-1 as it is not a PG instance")
	handler.Reset()

	// valid pod, applyNodeSelector fails (because cluster not found)
	pod2 := createPod("2", "", true)
	err = webhook.Default(ctx, pod2)
	g.Expect(err).To(HaveOccurred())
	handler.AssertMessage("failed to mutate pod")
	handler.Reset()

	// valid pod, applyNodeSelector succeeds
	cluster.Store("node1", "region1", "zone1", true)
	clus := postgres.RegisterOrUpdateCluster(clusterNamespace, clusterName, "", "", 3, 2)
	clus.RegisterOrUpdateInstance("2", "region1", "zone", ip, true)

	err = webhook.Default(ctx, pod2)
	g.Expect(err).To(BeNil())
	g.Expect(pod2.Spec.NodeSelector[regionLabel]).To(Equal("region1"))
	handler.AssertMessage("Applying selector for region region1 on pod pg-2")
	handler.Reset()
}

func TestApplyNodeSelector(t *testing.T) {
	g := NewWithT(t)

	viper.Set(config.LabelRegion, regionLabel)
	defer teardown()

	cluster.Store("node1", "region1", "zone1", true)
	cluster.Store("node2", "region2", "zone2", false)

	ctx := config.EnrichContext(context.Background(), clusterName)

	// cluster not existing
	pod := createPod("1", "region1", false)
	err := applyNodeSelector(ctx, pod)
	g.Expect(err).To(HaveOccurred())

	clus := postgres.RegisterOrUpdateCluster(clusterNamespace, clusterName, "", "", 3, 2)

	// nothing should change, as the node selector is already applied
	err = applyNodeSelector(ctx, pod)
	g.Expect(err).To(BeNil())
	g.Expect(pod.Spec.NodeSelector[regionLabel]).To(Equal("region1"))

	// initial setup pod
	pod = createPod("1", "", false)
	pod.ObjectMeta.Labels[postgres.LabelPGJobRole] = "initdb"
	err = applyNodeSelector(ctx, pod)
	g.Expect(err).To(BeNil())
	g.Expect(pod.Spec.NodeSelector[regionLabel]).To(Equal("region1"))

	// 2. follow-up setup pod
	pod = createPod("3", "", false)
	pod.ObjectMeta.Labels[postgres.LabelPGJobRole] = "update"
	err = applyNodeSelector(ctx, pod)
	g.Expect(err).To(BeNil())
	g.Expect(pod.Spec.NodeSelector[regionLabel]).To(BeEmpty())

	// instance not registered
	pod = createPod("3", "", false)
	err = applyNodeSelector(ctx, pod)
	g.Expect(err).To(BeNil())
	g.Expect(pod.Spec.NodeSelector[regionLabel]).To(BeEmpty())

	// instance with region set
	clus.RegisterOrUpdateInstance("3", "region2", "zone", ip, true)
	err = applyNodeSelector(ctx, pod)
	g.Expect(err).To(BeNil())
	g.Expect(pod.Spec.NodeSelector[regionLabel]).To(Equal("region2"))
}

func TestApplyNodeSelector_RejectsInitialPodWhenPrimaryRegionNotSet(t *testing.T) {
	g := NewWithT(t)

	viper.Set(config.LabelRegion, regionLabel)
	defer teardown()

	// Wipe global region state lingering from previous test executions
	cluster.Store("node", "region1", "zone1", false)

	ctx := config.EnrichContext(context.Background(), clusterName)

	// Register cluster natively, but do NOT register any nodes as leaders (simulating missing label)
	postgres.RegisterOrUpdateCluster(clusterNamespace, clusterName, "", "", 3, 2)

	// Boot the initial primary pod
	pod1 := createPod("1", "", true)
	pod1.ObjectMeta.Labels[postgres.LabelPGJobRole] = "initdb"

	// Because no region is set, the pod webhook fatally rejects the initial pod creation.
	err := applyNodeSelector(ctx, pod1)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("primary region not set yet"))
}

// TestSetSelector_NoOpWhenRegionAlreadyMatches verifies that setSelector is a no-op
// when the pod's existing nodeSelector already contains the correct region value.
func TestSetSelector_NoOpWhenRegionAlreadyMatches(t *testing.T) {
	g := NewWithT(t)

	viper.Set(config.LabelRegion, regionLabel)
	defer teardown()

	ctx := config.EnrichContext(context.Background(), clusterName)

	// Create a pod that already has the correct region selector
	pod := createPod("1", "region1", false)
	g.Expect(pod.Spec.NodeSelector[regionLabel]).To(Equal("region1"))

	// Call setSelector with the same region — should be a no-op
	setSelector(ctx, pod, "region1")

	// The selector must remain unchanged (same map, same value)
	g.Expect(pod.Spec.NodeSelector[regionLabel]).To(Equal("region1"))

	// Now call with a different region — should overwrite
	setSelector(ctx, pod, "region2")
	g.Expect(pod.Spec.NodeSelector[regionLabel]).To(Equal("region2"))
}

func createPod(idx, region string, primary bool) *corev1.Pod {
	pd := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", clusterName, idx),
			Namespace: clusterNamespace,
			Labels: map[string]string{
				postgres.LabelPGClusterName:  clusterName,
				postgres.LabelPGInstanceRole: postgres.PGRoleReplica,
				postgres.LabelPGInstanceName: clusterName + "-" + idx,
			},
		},
		Spec: corev1.PodSpec{
			TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{{
						Key:      postgres.LabelPGClusterName,
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{clusterName},
					}, {
						Key:      postgres.LabelPGPodRole,
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{"instance"},
					}},
				},
			}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	if region != "" {
		pd.Spec.NodeSelector = map[string]string{
			regionLabel: region,
		}
	}
	if primary {
		pd.ObjectMeta.Labels[postgres.LabelPGInstanceRole] = postgres.PGRolePrimary
	}
	return pd
}
