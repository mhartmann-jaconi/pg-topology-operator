// TODO refactor the whole config handling to use cobra instead
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	LabelRegion = "topology.label_region"
	LabelLeader = "topology.label_leader"

	PodPort = "pod.port"

	ServiceName      = "service.name"
	ServiceNamespace = "service.namespace"

	CertificateOrganization = "certificate.organization"

	LogJson  = "log.json"
	LogLevel = "log.level"
)

func Bootstrap(rootCmd *cobra.Command) {
	rootCmd.Flags().String(LabelRegion, "topology.kubernetes.io/region", "node label for region")
	rootCmd.Flags().String(LabelLeader, "zone-leader", "node label to identify primary zone")

	rootCmd.Flags().Int(PodPort, 9443, "container port, service TLS traffic for webhook")

	rootCmd.Flags().String(ServiceName, "", "name of the webhook implementation K8s service")
	rootCmd.Flags().String(ServiceNamespace, "", "namespace of the webhook implementation K8s service")

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

func InitLogging() {
	var level slog.Level
	err := level.UnmarshalText([]byte(viper.GetString(LogLevel)))
	if err != nil {
		panic(fmt.Errorf("invalid log level %q: %w", viper.GetString(LogLevel), err))
	}

	options := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	if viper.GetBool(LogJson) {
		handler = slog.NewJSONHandler(os.Stdout, options)
	} else {
		handler = slog.NewTextHandler(os.Stdout, options)
	}

	slog.SetDefault(slog.New(handler))
	log.SetLogger(logr.FromSlogHandler(handler))
}

func init() {
	cobra.OnInitialize(initConfig, viper.AutomaticEnv, InitLogging)
}
