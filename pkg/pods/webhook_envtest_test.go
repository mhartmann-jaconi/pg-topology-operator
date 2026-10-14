package pods

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/cluster"
	postgres "vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/postgres"
)

// TestEnvtestPodWebhook_NodeSelectorInjection creates a PG replica pod and verifies
// that the webhook injects the correct nodeSelector based on the instance's region.
func TestEnvtestPodWebhook_NodeSelectorInjection(t *testing.T) {
	g := NewWithT(t)
	defer guardPodEnvTest(t, g)()
	ensureNamespace(g, clusterNamespace)

	// 1. Set up topology: dc-01 is the primary region.
	cluster.Store("node1", "dc-01", "dc-01-01", true)
	cluster.Store("node2", "dc-02", "dc-02-01", false)

	// Register the cluster and instance 2 in dc-02.
	clus := postgres.RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 3, 2)
	clus.RegisterOrUpdateInstance("2", "dc-02", "zone", ip, true)

	// 2. Create a replica pod for instance 2.
	pod := buildPGPod("pg-2", "2")
	err := podK8sClient.Create(context.Background(), pod)
	g.Expect(err).NotTo(HaveOccurred(), "Pod creation should succeed")

	// 3. Read back and verify the webhook injected the correct nodeSelector.
	saved := readPod(g, "pg-2", clusterNamespace)
	g.Expect(saved.Spec.NodeSelector).To(HaveLen(1),
		"Webhook must set exactly one nodeSelector key")
	g.Expect(saved.Spec.NodeSelector).To(HaveKeyWithValue(regionLabel, "dc-02"),
		"Webhook must inject nodeSelector with the instance's region")
}

// TestEnvtestPodWebhook_PreservesExistingNodeSelector creates a pod that already has
// a nodeSelector set. The webhook must not overwrite it.
func TestEnvtestPodWebhook_PreservesExistingNodeSelector(t *testing.T) {
	g := NewWithT(t)
	defer guardPodEnvTest(t, g)()
	ensureNamespace(g, clusterNamespace)

	// 1. Set up topology.
	cluster.Store("node1", "dc-01", "dc-01-01", true)

	clus := postgres.RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 3, 2)
	clus.RegisterOrUpdateInstance("2", "dc-01", "zone", ip, true)

	// 2. Create a pod that already has a nodeSelector from another system.
	pod := buildPGPod("pg-2-existing", "2", withNodeSelector("custom-label", "custom-value"))
	err := podK8sClient.Create(context.Background(), pod)
	g.Expect(err).NotTo(HaveOccurred(), "Pod creation should succeed")

	// 3. Verify the original selector is preserved and no region selector was added.
	saved := readPod(g, "pg-2-existing", clusterNamespace)
	g.Expect(saved.Spec.NodeSelector).To(HaveKeyWithValue("custom-label", "custom-value"),
		"Original nodeSelector must be preserved")
	g.Expect(saved.Spec.NodeSelector).NotTo(HaveKey(regionLabel),
		"Webhook must not inject region when nodeSelector already exists")
}

// TestEnvtestPodWebhook_InitialPrimaryPinnedToLeaderRegion creates the initial
// primary setup pod (instance 1 with jobRole=initdb). The webhook must pin it to
// the primary region via cluster.GetPrimaryRegion(), not to the instance's own region.
func TestEnvtestPodWebhook_InitialPrimaryPinnedToLeaderRegion(t *testing.T) {
	g := NewWithT(t)
	defer guardPodEnvTest(t, g)()
	ensureNamespace(g, clusterNamespace)

	// 1. Set up topology: dc-01 is the primary region.
	cluster.Store("node1", "dc-01", "dc-01-01", true)
	cluster.Store("node2", "dc-02", "dc-02-01", false)

	// Register the cluster — but do NOT register instance 1 yet.
	// The initdb setup pod runs before the instance is known.
	postgres.RegisterOrUpdateCluster(clusterNamespace, clusterName, "", "", 3, 2)

	// 2. Create the initial primary setup pod.
	// jobRole=initdb + instance=1 triggers the isInitialPrimary() path in the webhook,
	// which uses cluster.GetPrimaryRegion() instead of looking up the instance's region.
	pod := buildPGPod("pg-1-initdb", "1", withPrimaryRole(), withJobRole("initdb"))
	err := podK8sClient.Create(context.Background(), pod)
	g.Expect(err).NotTo(HaveOccurred(), "Initial primary pod creation should succeed")

	// 3. Verify the pod is pinned to the primary region.
	saved := readPod(g, "pg-1-initdb", clusterNamespace)
	g.Expect(saved.Spec.NodeSelector).To(HaveKeyWithValue(regionLabel, "dc-01"),
		"Initial primary must be pinned to the primary region, not to instance region")
}

// TestEnvtestPodWebhook_RejectsUnregisteredCluster creates a PG pod that references
// a cluster not registered in the operator's internal state. The webhook must return
// an error, causing the API server to deny the Pod CREATE request entirely.
func TestEnvtestPodWebhook_RejectsUnregisteredCluster(t *testing.T) {
	g := NewWithT(t)
	defer guardPodEnvTest(t, g)()
	ensureNamespace(g, clusterNamespace)

	// 1. Set up topology but do NOT register any cluster.
	cluster.Store("node1", "dc-01", "dc-01-01", true)

	// 2. Create a pod referencing a cluster that doesn't exist in internal state.
	// The webhook calls GetCluster() → nil → returns error → API server denies CREATE.
	pod := buildPGPod("pg-orphan", "1")
	err := podK8sClient.Create(context.Background(), pod)

	// 3. The Pod CREATE must be denied by the API server.
	g.Expect(err).To(HaveOccurred(), "Pod CREATE must be denied for unregistered cluster")
	g.Expect(err.Error()).To(ContainSubstring("denied"),
		"Error must indicate admission denial")
}
