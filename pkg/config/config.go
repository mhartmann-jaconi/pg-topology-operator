// TODO refactor the whole config handling to use cobra instead
package config

import (
	"context"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

const (
	LabelRegion = "topology.label_region"
	LabelZone   = "topology.label_zone"
	LabelLeader = "topology.label_leader"

	PodName = "pod.name"
	PodPort = "pod.port"

	ServiceName      = "service.name"
	ServiceNamespace = "service.namespace"

	CertificateOrganization = "certificate.organization"
	CertificateDirectory    = "certificate.directory"

	MetricsAddr = "metrics.address"

	DebugEnabled = "debug.enabled"
	DebugPort    = "debug.port"

	LogJson  = "log.json"
	LogLevel = "log.level"
)

var mutex *sync.Mutex = new(sync.Mutex)
var parentContext context.Context

func Bootstrap(rootCmd *cobra.Command) {
	rootCmd.Flags().String(LabelRegion, "topology.kubernetes.io/region", "node label for region")
	rootCmd.Flags().String(LabelLeader, "zone-leader", "node label to identify primary zone")

	rootCmd.Flags().String(PodName, "", "name of the operator pod")
	rootCmd.Flags().Int(PodPort, 9443, "container port, service TLS traffic for webhook")

	rootCmd.Flags().String(ServiceName, "", "name of the webhook implementation K8s service")
	rootCmd.Flags().String(ServiceNamespace, "", "namespace of the webhook implementation K8s service")

	rootCmd.Flags().String(MetricsAddr, ":9000", "address to expose prometheus metrics on, 0 to disable")
	rootCmd.Flags().Bool(DebugEnabled, false, "expose golang runtime debug information")
	rootCmd.Flags().Int(DebugPort, 1234, "port to expose debug information on")

	rootCmd.Flags().String(CertificateOrganization, "", "name of organization for webhook certificates")
	rootCmd.Flags().String(CertificateDirectory, "/etc/webhook/certs/", "directory to store generated certificates in")

	rootCmd.Flags().Bool(LogJson, false, "output logs in JSON format")
	rootCmd.Flags().String(LogLevel, "info", "log level")

	viper.BindPFlags(rootCmd.Flags())

	cobra.OnInitialize(unmarkRequired(rootCmd))
}

// unmarkRequired works around an issue with cobra and viper, where required flags - set via environment variables - are
// not recognized. See https://github.com/spf13/viper/issues/397
func unmarkRequired(cmd *cobra.Command) func() {
	return func() {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if viper.IsSet(f.Name) {
				cmd.Flags().SetAnnotation(f.Name, cobra.BashCompOneRequiredFlag, []string{"false"})
			}
		})
	}
}

func GetParentContext() context.Context {
	mutex.Lock()
	defer mutex.Unlock()
	if parentContext == nil {
		parentContext = signals.SetupSignalHandler()
	}
	return parentContext
}

func initConfig() {
	viper.SetConfigFile("/config/application.yaml")
	err := viper.ReadInConfig()
	if err != nil {
		panic(err)
	}

	// Allow flags containing dashes / dots to be set by environment variables which use underscores instead of dashes /
	// dots.
	replacer := strings.NewReplacer("-", "_", ".", "_")
	viper.SetEnvKeyReplacer(replacer)
}

func init() {
	cobra.OnInitialize(initConfig, viper.AutomaticEnv, InitLogging)
}
