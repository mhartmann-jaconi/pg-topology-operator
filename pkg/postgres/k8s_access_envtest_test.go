package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestEnvtest_DeletePod_Exists verifies that deletePod removes an existing pod
// from the API server.
func TestEnvtest_DeletePod_Exists(t *testing.T) {
	g := NewWithT(t)
	ns, teardown := setupDeletePodEnvtest(t, g)
	defer teardown()

	ctx := context.Background()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-1",
			Namespace: ns,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "postgres",
				Image: "postgres:16",
			}},
		},
	}
	g.Expect(k8sClient.Create(ctx, pod)).To(Succeed())

	// Verify pod exists
	found := &corev1.Pod{}
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), found)).To(Succeed())

	// Delete it
	err := deletePod(ctx, ns, "test-pod-1")
	g.Expect(err).NotTo(HaveOccurred())

	// Verify pod is gone (or has DeletionTimestamp set)
	g.Eventually(func() bool {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), found)
		if err != nil {
			return true // NotFound
		}
		return found.DeletionTimestamp != nil
	}, 5*time.Second).Should(BeTrue())
}

// TestEnvtest_DeletePod_NotFound verifies that deletePod returns nil when the
// target pod does not exist.
func TestEnvtest_DeletePod_NotFound(t *testing.T) {
	g := NewWithT(t)
	ns, teardown := setupDeletePodEnvtest(t, g)
	defer teardown()

	err := deletePod(context.Background(), ns, "nonexistent-pod")
	g.Expect(err).NotTo(HaveOccurred())
}

// TestEnvtest_DeletePod_AlreadyDeleting verifies that deletePod returns nil when
// the pod already has a DeletionTimestamp, skipping the redundant delete call.
func TestEnvtest_DeletePod_AlreadyDeleting(t *testing.T) {
	g := NewWithT(t)
	ns, teardown := setupDeletePodEnvtest(t, g)
	defer teardown()

	ctx := context.Background()

	// Create a pod with a finalizer so it stays in "Terminating" state
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pod-terminating",
			Namespace:  ns,
			Finalizers: []string{"test/block-deletion"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "postgres",
				Image: "postgres:16",
			}},
		},
	}
	g.Expect(k8sClient.Create(ctx, pod)).To(Succeed())

	// Trigger deletion — pod will be stuck in Terminating due to the finalizer
	g.Expect(k8sClient.Delete(ctx, pod)).To(Succeed())

	// Verify DeletionTimestamp is now set
	found := &corev1.Pod{}
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), found)).To(Succeed())
	g.Expect(found.DeletionTimestamp).ToNot(BeNil())

	// deletePod should return nil without attempting another delete
	err := deletePod(ctx, ns, "test-pod-terminating")
	g.Expect(err).NotTo(HaveOccurred())
}

// setupDeletePodEnvtest boots envtest, sets the global client, and creates a
// unique test namespace. Returns the namespace name and a teardown function.
func setupDeletePodEnvtest(t *testing.T, g Gomega) (string, func()) {
	teardownEnv := guardEnvTest(t, g)
	cli = k8sClient

	// Derive a DNS-compatible namespace name from the test name
	nsName := strings.ToLower("dp-" + t.Name())
	nsName = strings.ReplaceAll(nsName, "_", "-")
	nsName = strings.ReplaceAll(nsName, "/", "-")
	if len(nsName) > 63 {
		nsName = nsName[:63]
	}

	g.Expect(k8sClient.Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: nsName},
	})).To(Succeed())
	return nsName, teardownEnv
}
