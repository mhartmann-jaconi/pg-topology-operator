package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/events"
	. "github.com/onsi/gomega"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// --- Shared envtest infrastructure ---
// These variables and functions are used by both webhook and election engine integration tests.

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

const (
	clusterName      = "pg"
	clusterNamespace = "default"
	ip               = "1.2.3.4"
)

type TestCluster struct {
	Cluster
	shouldSwitchoverFail bool
	switchoverCounter    atomic.Int32
}

func (tc *TestCluster) getSwitchoverCount() int32 {
	return tc.switchoverCounter.Load()
}

func (tc *TestCluster) initiateSwitchover(ctx context.Context) error {
	tc.switchoverCounter.Add(1)
	if tc.shouldSwitchoverFail {
		return fmt.Errorf("always fails")
	}
	return nil
}

// setupEnvTest boots the envtest framework with CRDs and webhook server.
// Isolated from TestMain so that standard unit tests remain fast.
func setupEnvTest() error {
	ctx, cancel = context.WithCancel(context.Background())
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	viper.BindEnv(config.MetricsAddr, "0")

	// Silence the background async event loop to prevent interference between tests
	events.SetListenersForRegionChange(func(context.Context, string) {})

	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		return fmt.Errorf("KUBEBUILDER_ASSETS not found. Skipping envtest.")
	}

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("testdata", "webhooks")},
		},
	}

	var err error
	cfg, err = testEnv.Start()
	if err != nil {
		return err
	}

	scheme := runtime.NewScheme()
	err = corev1.AddToScheme(scheme)
	if err != nil {
		return err
	}
	err = pgapi.AddToScheme(scheme)
	if err != nil {
		return err
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	// Bootstrap Manager strictly for spinning up the webhook server bindings
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:         scheme,
		LeaderElection: false,
		Metrics:        metricsserver.Options{BindAddress: "0"},
		WebhookServer: ctrlwebhook.NewServer(ctrlwebhook.Options{
			CertDir: testEnv.WebhookInstallOptions.LocalServingCertDir,
			Host:    testEnv.WebhookInstallOptions.LocalServingHost,
			Port:    testEnv.WebhookInstallOptions.LocalServingPort,
		}),
	})
	if err != nil {
		return err
	}

	// Register a raw admission handler directly, bypassing WithDefaulter's builder.
	// This avoids any potential GVK filtering the builder may impose on subresource requests.
	mgr.GetWebhookServer().Register(mutationEndpoint, &ctrlwebhook.Admission{
		Handler: admission.HandlerFunc(func(ctx context.Context, req admission.Request) admission.Response {
			d := &defaulter{}
			cluster := &pgapi.Cluster{}
			if err := json.Unmarshal(req.Object.Raw, cluster); err != nil {
				return admission.Errored(400, err)
			}
			if err := d.Default(ctx, cluster); err != nil {
				return admission.Denied(err.Error())
			}
			marshalled, err := json.Marshal(cluster)
			if err != nil {
				return admission.Errored(500, err)
			}
			return admission.PatchResponseFromRaw(req.Object.Raw, marshalled)
		}),
	})

	// Start Manager
	go func() {
		if err := mgr.Start(ctx); err != nil {
			panic(fmt.Sprintf("Failed to start manager: %v", err))
		}
	}()

	// Wait for Webhook endpoint to become physically active on the randomized port
	d := &net.Dialer{}
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for webhook server to become healthy")
		case <-ticker.C:
			conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", testEnv.WebhookInstallOptions.LocalServingHost, testEnv.WebhookInstallOptions.LocalServingPort))
			if err == nil {
				conn.Close()
				return nil
			}
		}
	}
}

func teardownEnvTest() {
	if cancel != nil {
		cancel()
	}
	if testEnv != nil {
		_ = testEnv.Stop()
	}
	// Wipe cluster memory state cleanly
	teardown()
}

// --- Helpers to reduce boilerplate across envtest tests ---

// guardEnvTest skips the test if KUBEBUILDER_ASSETS is not set,
// then boots the envtest framework. Returns a teardown function.
func guardEnvTest(t *testing.T, g Gomega) func() {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set. Skipping envtest invocation.")
	}
	err := setupEnvTest()
	g.Expect(err).NotTo(HaveOccurred(), "Envtest framework failed to boot")
	return teardownEnvTest
}

// createEnvtestCluster creates a namespace and an empty Cluster object in the API server.
// Returns the created Cluster object for subsequent patching.
func createEnvtestCluster(g Gomega, name, namespace string, instances int) *pgapi.Cluster {
	// Ensure namespace exists
	err := k8sClient.Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	})
	if client.IgnoreAlreadyExists(err) != nil {
		g.Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")
	}

	pgCluster := &pgapi.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       pgapi.ClusterSpec{Instances: instances},
	}
	err = k8sClient.Create(context.Background(), pgCluster)
	g.Expect(err).NotTo(HaveOccurred(), "Failed to create base cluster")
	return pgCluster
}

// patchClusterStatus patches the status subresource with the given currentPrimary and targetPrimary.
// Status().Patch() is required because envtest routes PATCH (but not PUT) through webhooks.
func patchClusterStatus(g Gomega, pgCluster *pgapi.Cluster, currentPrimary, targetPrimary string) {
	patch := []byte(fmt.Sprintf(`{"status":{"currentPrimary":"%s","targetPrimary":"%s"}}`,
		currentPrimary, targetPrimary))
	err := k8sClient.Status().Patch(context.Background(), pgCluster, client.RawPatch(types.MergePatchType, patch))
	g.Expect(err).NotTo(HaveOccurred(), "Failed to patch cluster status")
}

// readClusterStatus reads the Cluster object back from the API server.
func readClusterStatus(g Gomega, name, namespace string) *pgapi.Cluster {
	saved := &pgapi.Cluster{}
	err := k8sClient.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, saved)
	g.Expect(err).NotTo(HaveOccurred(), "Failed to read cluster")
	return saved
}

func teardown() {
	viper.Reset()
	cluster.ClearState()
	clusters = map[string]PGCluster{}
}
