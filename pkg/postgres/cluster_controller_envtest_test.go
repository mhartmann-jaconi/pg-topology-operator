package postgres

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TestEnvtest_ClusterReconcile_PopulatesState verifies that the cluster controller's
// Reconcile method reads a Cluster from the real API server and populates the
// internal state with the correct primary, desired primary, and instance count.
func TestEnvtest_ClusterReconcile_PopulatesState(t *testing.T) {
	g := NewWithT(t)
	defer guardEnvTest(t, g)()

	// 1. Create a Cluster in the API server with 3 instances
	pgCluster := createEnvtestCluster(g, clusterName, clusterNamespace, 3)

	// 2. Set the status to simulate CNPG populating the primary info
	patchClusterStatus(g, pgCluster, reconcileInstanceName("1"), reconcileInstanceName("1"))

	// 3. Run the Reconciler against the real API server
	rec := &Reconciler{Client: k8sClient}
	result, err := rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: clusterName, Namespace: clusterNamespace},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(reconcile.Result{}))

	// 4. Verify internal state was populated from the real Cluster object
	clus := GetCluster(clusterNamespace, clusterName)
	g.Expect(clus).ToNot(BeNil())
	g.Expect(clus.GetCurrentPrimaryInstanceId()).To(Equal("1"))
	g.Expect(clus.GetName()).To(Equal(clusterName))
	g.Expect(clus.GetNamespace()).To(Equal(clusterNamespace))
}

// TestEnvtest_ClusterReconcile_DeletionDropsState verifies that when a Cluster has a
// DeletionTimestamp, the Reconciler removes it from internal state.
func TestEnvtest_ClusterReconcile_DeletionDropsState(t *testing.T) {
	g := NewWithT(t)
	defer guardEnvTest(t, g)()

	// 1. Create a Cluster in the API server and reconcile it to populate internal state.
	//    This establishes the precondition: state must exist before we can verify deletion removes it.
	pgCluster := createEnvtestCluster(g, clusterName, clusterNamespace, 2)
	patchClusterStatus(g, pgCluster, clusterName+"-1", clusterName+"-1")

	rec := &Reconciler{Client: k8sClient}
	_, err := rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: clusterName, Namespace: clusterNamespace},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(GetCluster(clusterNamespace, clusterName)).ToNot(BeNil())

	// 2. Delete the Cluster. The API server sets DeletionTimestamp when finalizers are present.
	//    We add a finalizer first so the object enters the "terminating" state instead of vanishing.
	pgCluster.Finalizers = []string{"test.finalizer/cleanup"}
	err = k8sClient.Update(context.Background(), pgCluster)
	g.Expect(err).ToNot(HaveOccurred())

	err = k8sClient.Delete(context.Background(), pgCluster)
	g.Expect(err).ToNot(HaveOccurred())

	// 3. Reconcile again — the controller should detect DeletionTimestamp and drop the state
	result, err := rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: clusterName, Namespace: clusterNamespace},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(reconcile.Result{}))

	// 4. Internal state should be gone
	g.Expect(GetCluster(clusterNamespace, clusterName)).To(BeNil())
}

// TestEnvtest_ClusterReconcile_UpdateRefreshesState verifies that when a Cluster's
// TargetPrimary is updated in the API server, a subsequent Reconcile call refreshes
// the internal state with the new desired primary instance ID.
func TestEnvtest_ClusterReconcile_UpdateRefreshesState(t *testing.T) {
	g := NewWithT(t)
	defer guardEnvTest(t, g)()

	// 1. Create a Cluster and set initial status: CurrentPrimary=1, TargetPrimary=1
	pgCluster := createEnvtestCluster(g, clusterName, clusterNamespace, 3)
	patchClusterStatus(g, pgCluster, clusterName+"-1", clusterName+"-1")

	// 2. First reconcile — populate internal state
	rec := &Reconciler{Client: k8sClient}
	_, err := rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: clusterName, Namespace: clusterNamespace},
	})
	g.Expect(err).ToNot(HaveOccurred())

	clus := GetCluster(clusterNamespace, clusterName)
	g.Expect(clus).ToNot(BeNil())
	g.Expect(clus.GetDesiredPrimaryInstanceId()).To(Equal("1"))

	// 3. Update TargetPrimary to instance 2 (simulates CNPG switchover)
	patchClusterStatus(g, pgCluster, clusterName+"-1", clusterName+"-2")

	// 4. Reconcile again — internal state should reflect the updated TargetPrimary
	result, err := rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: clusterName, Namespace: clusterNamespace},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(reconcile.Result{}))

	// 5. Verify the desired primary was refreshed
	clus = GetCluster(clusterNamespace, clusterName)
	g.Expect(clus).ToNot(BeNil())
	g.Expect(clus.GetDesiredPrimaryInstanceId()).To(Equal("2"),
		"Reconcile must refresh desiredPrimary when TargetPrimary changes in the API server")
	g.Expect(clus.GetCurrentPrimaryInstanceId()).To(Equal("1"),
		"CurrentPrimary must remain unchanged")
}

func TestEnvtest_ClusterReconcile_NotFoundDoesDropsState(t *testing.T) {
	g := NewWithT(t)
	defer guardEnvTest(t, g)()

	// 1. Create and reconcile a Cluster to populate internal state
	pgCluster := createEnvtestCluster(g, clusterName, clusterNamespace, 2)
	patchClusterStatus(g, pgCluster, clusterName+"-1", clusterName+"-1")

	rec := &Reconciler{Client: k8sClient}
	_, err := rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: clusterName, Namespace: clusterNamespace},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(GetCluster(clusterNamespace, clusterName)).ToNot(BeNil())

	// 2. Fully delete the Cluster (no finalizers → immediate removal)
	err = k8sClient.Delete(context.Background(), pgCluster)
	g.Expect(err).ToNot(HaveOccurred())

	// 3. Reconcile again — NotFound path is hit
	result, err := rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: clusterName, Namespace: clusterNamespace},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(reconcile.Result{}))

	// 4. Internal state is NOT cleaned up because DropCluster was called with
	//    empty namespace/name instead of the request's namespace/name.
	g.Expect(GetCluster(clusterNamespace, clusterName)).To(BeNil(), "NotFound error drops cluster state")
}

// reconcileInstanceName builds the full CNPG instance name (e.g. "reconcile-test-1")
// used in Cluster.Status.CurrentPrimary / TargetPrimary fields.
func reconcileInstanceName(id string) string {
	return fmt.Sprintf("%s-%s", clusterName, id)
}
