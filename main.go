package main

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"time"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/healthcheck"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/pods"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/postgres"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/webhook"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	pgtls "github.com/jaconi-io/ipoque-pg-webhook/pkg/tls"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
)

var scheme = runtime.NewScheme()

func init() {
	// CNPG API objects.
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	// Standard Kubernetes objects.
	utilruntime.Must(pgapi.AddToScheme(scheme))
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "pg-operator-webhook",
		Short: "PG Operator Webhook",
		Long:  "Webhook to set node selectors to PG pods as well as storage classes for their PVCs.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Get a config to talk to the apiserver
			cfg := ctrl.GetConfigOrDie()

			metricsServerOptions := metricsserver.Options{
				BindAddress: viper.GetString(config.MetricsAddr),
			}

			var err error
			var mgr manager.Manager
			// connecting to the k8s api server fails if an e.g. istio sidecar has not yet finished starting up
			retry.OnError(wait.Backoff{
				Duration: 1 * time.Second,
				Factor:   1.0,
				Steps:    30,
				Jitter:   0.0,
			}, func(err error) bool { return true }, func() error {
				mgr, err = manager.New(cfg, manager.Options{
					HealthProbeBindAddress: ":8081",
					Scheme:                 scheme,
					Metrics:                metricsServerOptions,
					WebhookServer: ctrlwebhook.NewServer(ctrlwebhook.Options{
						// listen on configured port for HTTPS requests for mutation
						Port:    viper.GetInt(config.PodPort),
						CertDir: pgtls.CertDir(),
					}),
				})
				return err
			})
			if err != nil {
				return err
			}

			if err = pods.SetupWebhook(mgr); err != nil {
				return fmt.Errorf("failed to setup pod webhook: %w", err)
			}
			if err = postgres.SetupWebhook(mgr); err != nil {
				return fmt.Errorf("failed to setup cluster webhook: %w", err)
			}

			watchdog, err := healthcheck.Setup(mgr)
			if err != nil {
				return fmt.Errorf("failed to setup healthcheck watchdog: %w", err)
			}

			// add a health check which verifies that at least the primary region is known
			mgr.AddReadyzCheck("watch-primary", func(req *http.Request) error {
				if cluster.GetPrimaryRegion() == "" {
					return errors.New("no primary region yet")
				}
				return nil
			})
			mgr.AddHealthzCheck("watch-events", watchdog.HealthzCheck)

			go exposeDebugInfo()

			caCert, err := pgtls.Generate()
			if err != nil {
				slog.Error("Failed to generate webhook certificates", "err", err)
				return err
			}

			client, err := kubernetes.NewForConfig(mgr.GetConfig())
			if err != nil {
				return err
			}

			// Update the webhook configurations inside the cluster.
			err = webhook.Update(cmd.Context(), client, caCert, pods.MutationConfigName, postgres.MutationConfigName)
			if err != nil {
				return err
			}

			// initialize all controllers
			cluster.InitController(mgr)
			pods.InitController(mgr)
			postgres.InitController(mgr)

			return mgr.Start(config.GetParentContext())
		},
	}
	config.Bootstrap(rootCmd)
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func exposeDebugInfo() {
	if !viper.GetBool(config.DebugEnabled) {
		return
	}
	port := viper.GetInt(config.DebugPort)
	slog.Info(fmt.Sprintf("Expose debug information on port %d", port))
	mux := http.NewServeMux()
	// Add pprof endpoints
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	//mux.HandleFunc("/debug/pprof/heap", pprof.Heap)
	//mux.HandleFunc("/debug/pprof/goroutine", pprof.Goroutine)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), mux))
}
