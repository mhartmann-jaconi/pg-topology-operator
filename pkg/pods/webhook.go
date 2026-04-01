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
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

const mutationEndpoint string = "/mutate-pods"
const MutationConfigName string = "postgres-pod-update"

func Setup(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&corev1.Pod{}).
		WithDefaulter(&defaulter{}).
		WithDefaulterCustomPath(mutationEndpoint).
		Complete()
}

type defaulter struct {
}

func (defaulter) Default(ctx context.Context, obj runtime.Object) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		err := fmt.Errorf("Object not of type pod: %s", obj.GetObjectKind().GroupVersionKind().Kind)
		slog.Warn("Failed to call pod webhook", "err", err)
		return nil
	}
	slog.Debug(fmt.Sprintf("Check if we can apply node selector to pod %s...", pod.Name))
	if _, ok := pod.Labels[postgres.LabelPGClusterName]; !ok {
		slog.Debug(fmt.Sprintf("Skip pod %s as it is not a PG instance", pod.Name))
		return nil
	}

	clusterName := postgres.GetClusterName(pod.Labels)
	ctx = config.EnrichContext(ctx, clusterName)
	if err := applyNodeSelector(ctx, pod); err != nil {
		slog.ErrorContext(ctx, "failed to mutate pod", "err", err)
		return err
	}
	return nil
}

// applyNodeSelector attaches a node selector to the first occurrence of a primary pod
// of the PG cluster. All other topology constraints are set by K8s mechanics itself.
func applyNodeSelector(ctx context.Context, pod *corev1.Pod) error {
	clusterName := postgres.GetClusterName(pod.Labels)
	clus := postgres.GetCluster(pod.Namespace, clusterName)
	if clus == nil {
		return fmt.Errorf("cluster %s not found", clusterName)
	}
	if clus.GetCurrentPrimaryInstanceId() != "" {
		slog.DebugContext(ctx, fmt.Sprintf("Ignore pod %s for webhook, as a primary instance is already stored", pod.Name))
		// already processed
		return nil
	}
	if !isInitialPrimary(pod) {
		// ignore
		slog.DebugContext(ctx, fmt.Sprintf("Ignore pod %s for webhook, as it is not a primary instance", pod.Name))
		return nil
	}

	labelRegion := viper.GetString(config.LabelRegion)
	primaryRegion := cluster.GetPrimaryRegion()

	slog.InfoContext(ctx, fmt.Sprintf("Applying selector for region %s on pod %s", primaryRegion, pod.Name))

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
