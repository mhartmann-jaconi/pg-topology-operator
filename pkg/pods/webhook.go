package pods

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/postgres"

	"github.com/spf13/viper"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const mutationEndpoint string = "/mutate-pods"
const MutationConfigName string = "postgres-pod-update"

func Setup(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &corev1.Pod{}).
		WithDefaulter(&defaulter{}).
		WithDefaulterCustomPath(mutationEndpoint).
		Complete()
}

type defaulter struct {
}

func (defaulter) Default(_ context.Context, pod *corev1.Pod) error {
	slog.Debug(fmt.Sprintf("Check if we can apply node selector to pod %s...", pod.Name))
	if pod.Name == "" {

	}
	if _, ok := pod.Labels[postgres.LabelPGClusterName]; !ok {
		slog.Debug(fmt.Sprintf("Skip pod %s as it is not a PG instance", pod.Name))
		for k, v := range pod.Labels {
			slog.Debug(fmt.Sprintf("NO_PG: %s: %s", k, v))
		}
		return nil
	}

	if err := applyNodeSelector(pod); err != nil {
		slog.Error(fmt.Sprintf("failed to mutate pod: %v", err))
		return err
	}
	return nil
}

// applyNodeSelector attaches a node selector to the first occurrence of a primary pod
// of the PG cluster. All other topology constraints are set by K8s mechanics itself.
func applyNodeSelector(pod *corev1.Pod) error {
	clusterName := postgres.GetClusterName(pod.Labels)
	clus := postgres.GetCluster(pod.Namespace, clusterName)
	if clus == nil {
		slog.Warn(fmt.Sprintf("Cluster %s not existing yet, skip instance %s", clusterName, pod.Name))
		return fmt.Errorf("cluster %s not found", clusterName)
	}
	if clus.GetCurrentPrimaryInstanceId() != "" {
		slog.Debug(fmt.Sprintf("Ignore pod %s in %s for webhook, as a primary instance is already stored", pod.Name, clusterName))
		// already processed
		return nil
	}
	if !isInitialPrimary(pod) {
		// ignore
		slog.Debug(fmt.Sprintf("Ignore pod %s in %s for webhook, as it is not a primary instance", pod.Name, clusterName))
		return nil
	}

	labelRegion := viper.GetString(config.LabelRegion)
	primaryRegion := cluster.GetPrimaryRegion()

	slog.Info(fmt.Sprintf("Applying selector for region %s on pod %s", primaryRegion, pod.Name))

	pod.Spec.NodeSelector = map[string]string{
		labelRegion: primaryRegion,
	}
	return nil
}

func isInitialPrimary(pod *corev1.Pod) bool {
	if postgres.IsPrimary(pod.Labels) {
		return true
	}
	// on initial setup, first PG pod does not have primary label attached, so we need to check the instance number
	return postgres.GetInstanceId(pod.Labels) == "1"
}
