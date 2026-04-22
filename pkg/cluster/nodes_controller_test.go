package cluster

import (
	"context"
	"testing"
	"time"

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
	events.SetListenersForRegionChange(func(ctx context.Context, s string) { primaryChangeCount++ })
	getChangeCount := func() int { return primaryChangeCount }

	viper.Set(config.LabelLeader, "lead")
	viper.Set(config.LabelRegion, "region")
	defer cleanup()

	node1 := createNode("node1", "dc-01", true, true)
	node2 := createNode("node2", "dc-01", true, true)
	node3 := createNode("node3", "dc-02", true, false)
	node4 := createNode("node4", "dc-02", true, false)

	_, err := runReconcile(node1)
	g.Expect(err).To(BeNil())
	_, err = runReconcile(node2)
	g.Expect(err).To(BeNil())
	_, err = runReconcile(node3)
	g.Expect(err).To(BeNil())
	_, err = runReconcile(node4)
	g.Expect(err).To(BeNil())

	// reconcile all nodes and check that dc-01 is primary and all other mappings exist
	g.Expect(GetPrimaryRegion()).To(Equal("dc-01"))
	g.Eventually(getChangeCount, 20*time.Millisecond).Should(Equal(1))

	// make node3 unhealthy and try to reconcile -> no change
	node3.Spec.Taints = []corev1.Taint{{Effect: corev1.TaintEffectNoSchedule}}
	node3.ObjectMeta.Labels["lead"] = "yep"
	_, err = runReconcile(node3)
	g.Expect(err).To(BeNil())
	g.Expect(GetPrimaryRegion()).To(Equal("dc-01"))
	g.Eventually(getChangeCount, 20*time.Millisecond).Should(Equal(1))

	node4.ObjectMeta.Labels["lead"] = "yep"
	_, err = runReconcile(node4)

	// check that primary region changed
	g.Expect(GetPrimaryRegion()).To(Equal("dc-02"))
	g.Eventually(getChangeCount, 20*time.Millisecond).Should(Equal(2))
}

func runReconcile(node *corev1.Node) (reconcile.Result, error) {
	rec := Reconciler{Client: fake.NewFakeClient(node)}
	return rec.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: node.Name}})
}

func createNode(name, region string, healthy, leader bool) *corev1.Node {
	nd := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
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
