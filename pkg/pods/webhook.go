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

func SetupWebhook(mgr ctrl.Manager) error {
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

// applyNodeSelector attaches a region-based node selector to
//  1. the initial setup job for PG instance 1
//  2. all "real" PG pods, which are not instance setup related
//
// The region to set is for the initial setup pod the current primary region.
// For all other PG instance it is the region from their own setup job.
func applyNodeSelector(ctx context.Context, pod *corev1.Pod) error {
	clusterName := postgres.GetClusterName(pod.Labels)
	clus := postgres.GetCluster(pod.Namespace, clusterName)
	if clus == nil {
		return fmt.Errorf("cluster %s not found", clusterName)
	}
	if len(pod.Spec.NodeSelector) > 0 {
		// never touch existing node selectors
		return nil
	}
	if !isSetupPod(pod) {
		inst := clus.GetInstanceByID(postgres.GetInstanceId(pod.Labels))
		if inst == nil {
			slog.WarnContext(ctx, fmt.Sprintf("Instance %s not registered yet", pod.Name))
			return nil
		}
		setSelector(ctx, pod, inst.GetRegion())
		return nil
	}
	if !isInitialPrimary(pod) {
		// ignore
		slog.Log(ctx, config.LevelTrace, fmt.Sprintf("Ignore pod %s for webhook, as it is not a primary instance", pod.Name))
		return nil
	}

	primaryRegion := cluster.GetPrimaryRegion()
	if primaryRegion == "" {
		return fmt.Errorf("primary region not set yet")
	}

	setSelector(ctx, pod, primaryRegion)
	return nil
}

func setSelector(ctx context.Context, pod *corev1.Pod, region string) {
	labelRegion := viper.GetString(config.LabelRegion)
	if pod.Spec.NodeSelector[labelRegion] == region {
		return
	}
	slog.InfoContext(ctx, fmt.Sprintf("Applying selector for region %s on pod %s", region, pod.Name))
	pod.Spec.NodeSelector = map[string]string{
		labelRegion: region,
	}
}

func isSetupPod(pod *corev1.Pod) bool {
	return pod.Labels[postgres.LabelPGJobRole] != ""
}

func isInitialPrimary(pod *corev1.Pod) bool {
	if postgres.IsPrimary(pod.Labels) {
		return true
	}
	// on initial setup, first PG pod might not have primary label attached, so we need to check the instance number
	return postgres.GetInstanceId(pod.Labels) == "1"
}
