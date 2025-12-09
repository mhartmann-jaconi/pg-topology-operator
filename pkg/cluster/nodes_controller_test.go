package cluster

import (
	"context"
	"testing"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/events"
	. "github.com/onsi/gomega"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcile(t *testing.T) {
	g := NewWithT(t)

	primaryChangeCount := 0
	events.AddListenerForAZChange(func(ctx context.Context, s string) { primaryChangeCount++ })

	viper.Set(config.LabelLeader, "lead")
	viper.Set(config.LabelRegion, "region")
	viper.Set(config.LabelZone, "zone")
	defer cleanup()

	node1 := createNode("node1", "east-1", "east", true, true)
	node2 := createNode("node2", "east-2", "east", true, false)
	node3 := createNode("node3", "west-1", "west", true, false)
	node4 := createNode("node4", "west-2", "west", true, false)

	_, err := runReconcile(node1)
	g.Expect(err).To(BeNil())
	_, err = runReconcile(node2)
	g.Expect(err).To(BeNil())
	_, err = runReconcile(node3)
	g.Expect(err).To(BeNil())
	_, err = runReconcile(node4)
	g.Expect(err).To(BeNil())

	// reconcile all nodes and check that east-1 is primary and all other mappings exist
	g.Expect(GetPrimaryZone()).To(Equal("east-1"))
	g.Expect(GetHealthyZones()).To(ContainElements("east-1", "east-2", "west-1", "west-2"))
	g.Expect(GetRegionForZone("east-1")).To(Equal("east"))
	g.Expect(GetRegionForZone("east-2")).To(Equal("east"))
	g.Expect(GetRegionForZone("west-1")).To(Equal("west"))
	g.Expect(GetRegionForZone("west-2")).To(Equal("west"))

	// make zone west-1 unhealthy
	node3.Spec.Taints = []corev1.Taint{{Effect: corev1.TaintEffectNoSchedule}}
	_, err = runReconcile(node3)
	g.Expect(err).To(BeNil())
	// check that zone is not healthy anymore, but that region mapping still exists
	g.Expect(GetHealthyZones()).To(ContainElements("east-1", "east-2", "west-2"))
	g.Expect(GetRegionForZone("west-1")).To(Equal("west"))

	// make east-2 to the new leading zone
	node2.ObjectMeta.Labels["lead"] = "yep"
	_, err = runReconcile(node2)
	g.Expect(err).To(BeNil())
	// check that primary zone changed
	g.Expect(GetPrimaryZone()).To(Equal("east-2"))
	g.Expect(GetHealthyZones()).To(ContainElements("east-1", "east-2", "west-2"))
	g.Expect(primaryChangeCount).To(Equal(1))
}

func runReconcile(node *corev1.Node) (reconcile.Result, error) {
	rec := Reconciler{Client: fake.NewFakeClient(node)}
	return rec.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: node.Name}})
}

func createNode(name, zone, region string, healthy, leader bool) *corev1.Node {
	nd := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"zone":   zone,
				"region": region,
			},
		},
	}
	if !healthy {
		nd.Spec.Taints = []corev1.Taint{{
			Effect: corev1.TaintEffectNoSchedule,
		}}
	}
	if leader {
		nd.ObjectMeta.Labels["lead"] = "yep"
	}
	return nd
}
