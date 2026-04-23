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
	LabelLeader = "topology.label_leader"

	PodName = "pod.name"
	PodPort = "pod.port"

	ServiceName      = "service.name"
	ServiceNamespace = "service.namespace"

	CertificateOrganization = "certificate.organization"

	MetricsEnabled = "metrics.enabled"
	MetricsPort    = "metrics.port"

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

	rootCmd.Flags().Bool(MetricsEnabled, true, "expose prometheus metrucs")
	rootCmd.Flags().Int(MetricsPort, 9000, "port to expose prometheus metrics on")
	rootCmd.Flags().Bool(DebugEnabled, false, "expose golang runtime debug information")
	rootCmd.Flags().Int(DebugPort, 1234, "port to expose debug information on")

	rootCmd.Flags().String(CertificateOrganization, "", "name of organization for webhook certificates")

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
	// Allow flags containing dashes / dots to be set by environment variables which use underscores instead of dashes /
	// dots.
	viper.SetConfigFile("/config/application.yaml")
	err := viper.ReadInConfig()
	if err != nil {
		panic(err)
	}
	replacer := strings.NewReplacer("-", "_", ".", "_")
	viper.SetEnvKeyReplacer(replacer)
}

func init() {
	cobra.OnInitialize(initConfig, viper.AutomaticEnv, InitLogging)
}
