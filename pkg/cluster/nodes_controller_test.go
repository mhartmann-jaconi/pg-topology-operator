package cluster

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/events"
)

func TestReconcile(t *testing.T) {
	g := NewWithT(t)

	var primaryChangeCount atomic.Int32
	events.SetListenersForRegionChange(func(ctx context.Context, s string) { primaryChangeCount.Add(1) })
	getChangeCount := func() int32 { return primaryChangeCount.Load() }

	setup()
	defer cleanup()

	node1 := createNode("node1", "dc-01", "01-01", true, true)
	node2 := createNode("node2", "dc-01", "01-02", true, true)
	node3 := createNode("node3", "dc-02", "02-01", true, false)
	node4 := createNode("node4", "dc-02", "02-02", true, false)

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
	g.Eventually(getChangeCount, 20*time.Millisecond).Should(Equal(int32(1)))

	node4.ObjectMeta.Labels["lead"] = "yep"
	_, err = runReconcile(node4)

	// check that primary region changed
	g.Expect(GetPrimaryRegion()).To(Equal("dc-02"))
	g.Eventually(getChangeCount, 20*time.Millisecond).Should(Equal(int32(2)))
}

// TestReconcile_GetError verifies that when the API server returns a non-NotFound
// error (e.g. a transient connection failure), the error is propagated to the caller
// for controller-runtime to requeue. Internal state must remain untouched.
func TestReconcile_GetError(t *testing.T) {
	g := NewWithT(t)

	setup()
	defer cleanup()

	// Pre-populate state with a non-leader node to verify it remains untouched
	// after the error. We avoid leader nodes here to prevent firing async
	// PrimaryRegionChanged events that can leak into subsequent tests.
	// See TestPrimaryRegionChanged_staleEventAfterListenerReset in events_test.go.
	Store("existing-node", "dc-01", "zone", false)
	g.Expect(GetRegionOfNode("existing-node")).To(Equal("dc-01"))

	// Use a fake client with an interceptor that returns a generic error on Get.
	// This simulates a transient API server failure (not a NotFound).
	interceptedClient := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			return fmt.Errorf("simulated API server connection error")
		},
	}).Build()

	rec := Reconciler{Client: interceptedClient}
	_, err := rec.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "any-node"},
	})

	// The error must propagate — the controller-runtime will requeue
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("simulated API server connection error"))

	// Internal state must remain untouched
	g.Expect(GetRegionOfNode("existing-node")).To(Equal("dc-01"))
}

// TestReconcile_NodeNotFound verifies that reconciling a node that doesn't exist
// in the API server returns no error and does not modify the internal state.
func TestReconcile_NodeNotFound(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	events.SetListenersForRegionChange(func(ctx context.Context, s string) {})

	// Reconcile a node name that doesn't exist in the fake client
	rec := Reconciler{Client: fake.NewFakeClient()}
	result, err := rec.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "ghost-node"},
	})

	g.Expect(err).To(BeNil())
	g.Expect(result).To(Equal(reconcile.Result{}))
	g.Expect(GetRegionOfNode("ghost-node")).To(BeEmpty())
	g.Expect(GetPrimaryRegion()).To(BeEmpty())
}

// TestReconcile_StoresHealthyNodeRegion verifies the full happy path of the Reconcile
// method: a healthy node with correct labels is read from the API server, and its
// region is stored via Store().
// Note: We intentionally use a non-leader node to avoid firing a PrimaryRegionChanged
// event, which would leave a pending message in the async event channel and interfere
// with subsequent tests (e.g. TestStore) that count region change events.
// See TestPrimaryRegionChanged_staleEventAfterListenerReset in events_test.go.
func TestReconcile_StoresHealthyNodeRegion(t *testing.T) {
	g := NewWithT(t)

	setup()
	defer cleanup()

	// Create a healthy non-leader node in dc-02
	node := createNode("replica-node", "dc-02", "zone", true, false)

	result, err := runReconcile(node)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(reconcile.Result{}))

	// Verify the node's region was stored
	g.Expect(GetRegionOfNode("replica-node")).To(Equal("dc-02"))
}

func runReconcile(node *corev1.Node) (reconcile.Result, error) {
	rec := Reconciler{Client: fake.NewFakeClient(node)}
	return rec.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: node.Name}})
}

func createNode(name, region, zone string, healthy, leader bool) *corev1.Node {
	nd := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				regionLabel: region,
				zoneLabel:   zone,
			},
		},
	}
	if !healthy {
		nd.Spec.Taints = []corev1.Taint{{
			Effect: corev1.TaintEffectNoSchedule,
		}}
	}
	if leader {
		nd.ObjectMeta.Labels[leaderLabel] = "yep"
	}
	return nd
}
