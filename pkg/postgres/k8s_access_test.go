package postgres

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestPatchClusterWithRetry_RetriesPermanentForbiddenError demonstrates that
// patchClusterWithRetry retries forbidden errors, caused by invalid rbac rules.
func TestPatchClusterWithRetry_RetriesPermanentForbiddenError(t *testing.T) {
	g := NewWithT(t)

	// Track how many times the patch is attempted
	patchAttempts := atomic.Int32{}
	ctx, cancel := context.WithCancel(context.Background())

	// Build a fake client that intercepts Status().Patch() and returns 403 Forbidden
	apiCluster := &pgapi.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "retry-test",
			Namespace: "default",
		},
		Status: pgapi.ClusterStatus{
			TargetPrimary: "retry-test-1",
		},
	}
	scheme := runtime.NewScheme()
	pgapi.SchemeBuilder.AddToScheme(scheme)

	cli = fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(apiCluster).
		WithObjects(apiCluster).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				patchAttempts.Add(1)
				// Simulate a permanent RBAC error — the operator's ServiceAccount
				// does not have permission to patch cluster status
				return errors.NewForbidden(
					schema.GroupResource{Group: "postgresql.cnpg.io", Resource: "clusters"},
					"retry-test",
					fmt.Errorf("forbidden: service account lacks patch permission"),
				)
			},
		}).
		Build()
	defer func() { cli = nil }()

	// Execute the retry loop
	go patchClusterWithRetry(ctx, "default", "retry-test",
		func(cluster *pgapi.Cluster) (*pgapi.Cluster, bool) {
			cluster.Status.TargetPrimary = "retry-test-2"
			return cluster, true
		},
	)

	// check that retry continues
	g.Eventually(patchAttempts.Load, 1*time.Second).Should(BeNumerically(">", 0))
	g.Eventually(patchAttempts.Load, 1*time.Second).Should(BeNumerically(">", 1))

	cancel()
	currentValue := patchAttempts.Load()
	compareValue := patchAttempts.Load()
	g.Expect(currentValue).To(Equal(compareValue))
}

func TestPatchClusterWithRetry_NotFoundError(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	pgapi.SchemeBuilder.AddToScheme(scheme)

	cli = fake.NewClientBuilder().
		WithScheme(scheme).
		Build()
	defer func() { cli = nil }()

	err := patchClusterWithRetry(context.Background(), "default", "not-existing",
		func(cluster *pgapi.Cluster) (*pgapi.Cluster, bool) {
			return cluster, true
		},
	)
	g.Expect(err).NotTo(BeNil())
	g.Expect(errors.IsNotFound(err)).To(BeTrue())
}

func TestPatchClusterWithRetry_ContextCanceled(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	pgapi.SchemeBuilder.AddToScheme(scheme)

	cli = fake.NewClientBuilder().
		WithScheme(scheme).
		Build()
	defer func() { cli = nil }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := patchClusterWithRetry(ctx, "default", "not-existing",
		func(cluster *pgapi.Cluster) (*pgapi.Cluster, bool) {
			return cluster, true
		},
	)
	g.Expect(err).NotTo(BeNil())
	_, isCancelError := err.(ContextCanceled)
	g.Expect(isCancelError).To(BeTrue())
}
