package pods

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/cluster"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/events"
	postgres "vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/postgres"
)

var nodeName = "node"

func TestReconcile(t *testing.T) {
	t.Run("returns nil for deleted pods", func(t *testing.T) {
		g := NewWithT(t)

		err := runReconcileWithPod()

		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("ignores pods without postgres instance labels", func(t *testing.T) {
		g := NewWithT(t)

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "plain-pod",
				Namespace: "default",
			},
		}

		err := runReconcileWithPod(pod)

		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("returns an error when cluster state is not registered yet", func(t *testing.T) {
		g := NewWithT(t)

		pod := newReconcilePod(clusterNamespace, clusterName, "1")

		err := runReconcileWithPod(pod)

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("cluster not regstered yet"))
	})

	t.Run("marks deleting non-job instances as inactive", func(t *testing.T) {
		g := NewWithT(t)
		events.SetListenersForRegionChange(func(ctx context.Context, s string) {})

		clus := createTestCluster(t)
		inst := clus.RegisterOrUpdateInstance("1", "region-a", "zone", "1.2.3.4", true)

		timestamp := metav1.Now()
		pod := newReconcilePod(clusterNamespace, clusterName, "1")
		pod.DeletionTimestamp = &timestamp
		pod.Finalizers = []string{"test.finalizer/cleanup"}

		err := runReconcileWithPod(pod)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(inst.IsActive()).To(BeFalse())
	})

	t.Run("ignores pods until a node name is assigned", func(t *testing.T) {
		g := NewWithT(t)

		clus := createTestCluster(t)
		pod := newReconcilePod(clusterNamespace, clusterName, "1")
		pod.Spec.NodeName = ""

		err := runReconcileWithPod(pod)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(clus.GetInstanceByID("1")).To(BeNil())
	})

	t.Run("returns an error when the node region is not known yet", func(t *testing.T) {
		g := NewWithT(t)

		clus := createTestCluster(t)
		pod := newReconcilePod(clusterNamespace, clusterName, "1")
		pod.Spec.NodeName = "unknown-node"

		err := runReconcileWithPod(pod)

		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("node not registered yet"))
		g.Expect(clus.GetInstanceByID("1")).To(BeNil())
	})

	t.Run("registers healthy postgres instances with their region", func(t *testing.T) {
		g := NewWithT(t)

		clus := createTestCluster(t)
		cluster.Store(nodeName, "region", "zone", false)

		pod := newReconcilePod(clusterNamespace, clusterName, "1")
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Ready: true}}

		err := runReconcileWithPod(pod)

		g.Expect(err).ToNot(HaveOccurred())
		inst := clus.GetInstanceByID("1")
		g.Expect(inst).ToNot(BeNil())
		g.Expect(inst.IsActive()).To(BeTrue())
		g.Expect(inst.GetRegion()).To(Equal("region"))
	})

	t.Run("registers unhealthy postgres instances with their region", func(t *testing.T) {
		g := NewWithT(t)

		clus := createTestCluster(t)
		cluster.Store(nodeName, "region", "zone", false)

		pod := newReconcilePod(clusterNamespace, clusterName, "1")
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Ready: false}}

		err := runReconcileWithPod(pod)

		g.Expect(err).ToNot(HaveOccurred())
		inst := clus.GetInstanceByID("1")
		g.Expect(inst).ToNot(BeNil())
		g.Expect(inst.IsActive()).To(BeFalse())
		g.Expect(inst.GetRegion()).To(Equal("region"))
	})

	t.Run("change health state of instance", func(t *testing.T) {
		g := NewWithT(t)

		clus := createTestCluster(t)
		inst := clus.RegisterOrUpdateInstance("1", "region", "zone", "1.2.3.4", true)

		cluster.Store(nodeName, "region", "zone", false)

		pod := newReconcilePod(clusterNamespace, clusterName, "1")
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Ready: false}}

		err := runReconcileWithPod(pod)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(inst).ToNot(BeNil())
		g.Expect(inst.IsActive()).To(BeFalse())
		g.Expect(inst.GetRegion()).To(Equal("region"))
	})

	t.Run("registers setup jobs as inactive even when the pod is running", func(t *testing.T) {
		g := NewWithT(t)

		clus := createTestCluster(t)
		cluster.Store(nodeName, "region", "zone", false)

		pod := newReconcilePod(clusterNamespace, clusterName, "1")
		pod.Labels[postgres.LabelPGJobRole] = postgres.PGRoleInitDB
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Ready: true}}

		err := runReconcileWithPod(pod)

		g.Expect(err).ToNot(HaveOccurred())
		inst := clus.GetInstanceByID("1")
		g.Expect(inst).ToNot(BeNil())
		g.Expect(inst.IsActive()).To(BeFalse())
	})
}

// TestIsHealthy_ContainerNotReady verifies that a pod with multiple containers
// is unhealthy when at least one container is not ready (e.g. a crashing sidecar).
func TestIsHealthy_ContainerNotReady(t *testing.T) {
	g := NewWithT(t)

	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "postgres", Ready: true},
				{Name: "sidecar", Ready: false}, // sidecar is crashing
			},
		},
	}

	// Pod should be unhealthy because the sidecar is not ready
	g.Expect(isHealthy(pod)).To(BeFalse())

	// All containers ready → healthy
	pod.Status.ContainerStatuses[1].Ready = true
	g.Expect(isHealthy(pod)).To(BeTrue())

	// Pod not running → unhealthy regardless of container status
	pod.Status.Phase = corev1.PodPending
	g.Expect(isHealthy(pod)).To(BeFalse())
}

func createTestCluster(t *testing.T) postgres.PGCluster {
	clus := postgres.RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 1, 0)
	t.Cleanup(func() {
		postgres.DropCluster(clusterNamespace, clusterName)
		cluster.ClearState()
	})
	return clus
}

func runReconcileWithPod(objects ...*corev1.Pod) error {
	builder := fake.NewClientBuilder()
	if len(objects) > 0 {
		clientObjects := make([]client.Object, 0, len(objects))
		for _, obj := range objects {
			clientObjects = append(clientObjects, obj)
		}
		builder = builder.WithObjects(clientObjects...)
	}

	rec := Reconciler{Client: builder.Build()}

	request := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "missing",
		},
	}
	if len(objects) > 0 {
		request.NamespacedName = types.NamespacedName{
			Namespace: objects[0].Namespace,
			Name:      objects[0].Name,
		}
	}

	_, err := rec.Reconcile(context.Background(), request)
	return err
}

func newReconcilePod(namespace, clusterName, instanceID string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", clusterName, instanceID),
			Namespace: namespace,
			Labels: map[string]string{
				postgres.LabelPGClusterName:  clusterName,
				postgres.LabelPGInstanceName: fmt.Sprintf("%s-%s", clusterName, instanceID),
				postgres.LabelPGInstanceRole: postgres.PGRoleReplica,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
}
