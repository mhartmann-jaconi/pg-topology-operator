package pods

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/cluster"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/config"
	postgres "vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/postgres"
)

// --- Shared envtest infrastructure for pod webhook tests ---

var (
	podCfg       *rest.Config
	podK8sClient client.Client
	podTestEnv   *envtest.Environment
	podCtx       context.Context
	podCancel    context.CancelFunc
)

const (
	regionLabel      = "topology.kubernetes.io/region"
	clusterName      = "pg"
	clusterNamespace = "pod-envtest-namespace"
	ip               = "1.2.3.4"
)

// setupPodEnvTest boots envtest with only core API (no CRDs needed) and the pod webhook.
func setupPodEnvTest() error {
	podCtx, podCancel = context.WithCancel(context.Background())
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	viper.BindEnv(config.MetricsAddr, "0")

	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		return fmt.Errorf("KUBEBUILDER_ASSETS not found. Skipping envtest.")
	}

	// Configure region label as in production
	viper.Set(config.LabelRegion, regionLabel)

	podTestEnv = &envtest.Environment{
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("testdata", "webhooks")},
		},
	}

	var err error
	podCfg, err = podTestEnv.Start()
	if err != nil {
		return err
	}

	scheme := runtime.NewScheme()
	if err = corev1.AddToScheme(scheme); err != nil {
		return err
	}

	podK8sClient, err = client.New(podCfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	mgr, err := ctrl.NewManager(podCfg, ctrl.Options{
		Scheme:         scheme,
		LeaderElection: false,
		Metrics:        metricsserver.Options{BindAddress: "0"},
		WebhookServer: ctrlwebhook.NewServer(ctrlwebhook.Options{
			CertDir: podTestEnv.WebhookInstallOptions.LocalServingCertDir,
			Host:    podTestEnv.WebhookInstallOptions.LocalServingHost,
			Port:    podTestEnv.WebhookInstallOptions.LocalServingPort,
		}),
	})
	if err != nil {
		return err
	}

	// Register the pod webhook handler at the same path as production.
	mgr.GetWebhookServer().Register(mutationEndpoint, &ctrlwebhook.Admission{
		Handler: admission.HandlerFunc(func(ctx context.Context, req admission.Request) admission.Response {
			d := &defaulter{}
			pod := &corev1.Pod{}
			if err := json.Unmarshal(req.Object.Raw, pod); err != nil {
				return admission.Errored(400, err)
			}
			if err := d.Default(ctx, pod); err != nil {
				return admission.Denied(err.Error())
			}
			marshalled, err := json.Marshal(pod)
			if err != nil {
				return admission.Errored(500, err)
			}
			return admission.PatchResponseFromRaw(req.Object.Raw, marshalled)
		}),
	})

	go func() {
		if err := mgr.Start(podCtx); err != nil {
			panic(fmt.Sprintf("Failed to start manager: %v", err))
		}
	}()

	// Wait for webhook endpoint to become active
	d := &net.Dialer{}
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for webhook server to become healthy")
		case <-ticker.C:
			conn, err := d.DialContext(podCtx, "tcp", fmt.Sprintf("%s:%d",
				podTestEnv.WebhookInstallOptions.LocalServingHost,
				podTestEnv.WebhookInstallOptions.LocalServingPort))
			if err == nil {
				conn.Close()
				return nil
			}
		}
	}
}

func teardownPodEnvTest() {
	if podCancel != nil {
		podCancel()
	}
	if podTestEnv != nil {
		_ = podTestEnv.Stop()
	}
	teardown()
}

// --- Helpers ---

// guardPodEnvTest skips the test if KUBEBUILDER_ASSETS is not set,
// boots the pod envtest framework, and returns a teardown function.
func guardPodEnvTest(t *testing.T, g Gomega) func() {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set. Skipping envtest invocation.")
	}
	err := setupPodEnvTest()
	g.Expect(err).NotTo(HaveOccurred(), "Pod envtest framework failed to boot")
	return teardownPodEnvTest
}

// ensureNamespace creates the test namespace if it doesn't exist.
func ensureNamespace(g Gomega, namespace string) {
	err := podK8sClient.Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	})
	if client.IgnoreAlreadyExists(err) != nil {
		g.Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")
	}
}

// buildPGPod creates a Pod struct with CNPG labels but does NOT submit it to the API server.
// Use the options to customize the pod's role and labels.
func buildPGPod(name, instanceId string, opts ...podOption) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: clusterNamespace,
			Labels: map[string]string{
				postgres.LabelPGClusterName:  clusterName,
				postgres.LabelPGInstanceRole: postgres.PGRoleReplica,
				postgres.LabelPGInstanceName: clusterName + "-" + instanceId,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "postgres",
				Image: "ghcr.io/cloudnative-pg/postgresql:16",
			}},
		},
	}
	for _, opt := range opts {
		opt(pod)
	}
	return pod
}

// podOption is a functional option for customizing test pods.
type podOption func(*corev1.Pod)

// withPrimaryRole marks the pod as the primary instance.
func withPrimaryRole() podOption {
	return func(pod *corev1.Pod) {
		pod.Labels[postgres.LabelPGInstanceRole] = postgres.PGRolePrimary
	}
}

// withJobRole sets the cnpg.io/jobRole label (e.g. "initdb" for setup pods).
func withJobRole(role string) podOption {
	return func(pod *corev1.Pod) {
		pod.Labels[postgres.LabelPGJobRole] = role
	}
}

// withNodeSelector pre-sets a nodeSelector on the pod.
func withNodeSelector(label, value string) podOption {
	return func(pod *corev1.Pod) {
		pod.Spec.NodeSelector = map[string]string{label: value}
	}
}

// readPod reads a Pod back from the API server.
func readPod(g Gomega, name, namespace string) *corev1.Pod {
	pod := &corev1.Pod{}
	err := podK8sClient.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, pod)
	g.Expect(err).NotTo(HaveOccurred(), "Failed to read pod")
	return pod
}

func teardown() {
	postgres.DropCluster(clusterNamespace, clusterName)
	cluster.ClearState()
	viper.Reset()
}
