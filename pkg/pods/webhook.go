package pods

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/postgres"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/webhook"

	"github.com/spf13/viper"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
)

const mutationEndpoint string = "/mutate-pods"
const mutationConfigName string = "postgres-pod-update"

func BuildWebhook(caCert *bytes.Buffer) *admissionregistrationv1.MutatingWebhookConfiguration {
	apiGroup := corev1.SchemeGroupVersion.Group
	apiVersion := corev1.SchemeGroupVersion.Version
	apiResource := string(corev1.ResourcePods)
	return webhook.Build(caCert, mutationEndpoint, mutationConfigName, apiGroup, apiVersion, apiResource)
}

func Setup(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &corev1.Pod{}).
		WithDefaulter(&defaulter{}).
		WithDefaulterCustomPath(mutationEndpoint).
		Complete()
}

type defaulter struct {
}

func (defaulter) Default(_ context.Context, pod *corev1.Pod) error {
	if _, ok := pod.Labels[postgres.LabelPGClusterName]; !ok {
		return nil
	}

	if err := applyNodeSelector(pod); err != nil {
		slog.Error(fmt.Sprintf("failed to mutate pod: %v", err))
		return err
	}
	return nil
}

func applyNodeSelector(pod *corev1.Pod) error {
	clusterName := postgres.GetClusterName(pod.Labels)
	instanceId := postgres.GetInstanceId(pod.Labels)

	labelZone := viper.GetString(config.LabelZone)

	if len(pod.Spec.NodeSelector) > 0 {
		slog.Info(fmt.Sprintf("pod %s already has node selector applied", pod.Name))
		return nil
	}

	// We need to wait for PVC to get it's zone assigned. If we would do that for pvc and
	// pod, we could run into race conditions, where both get a different zone applied.
	return retry.OnError(wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   1.0,
		Steps:    30,
		Jitter:   0.0,
	}, func(err error) bool { return true }, func() error {
		if zoneName := postgres.GetZoneForInstanceId(clusterName, instanceId); zoneName != "" {
			// PVC mutation already ran through, apply the zone to pod
			attachTopologyLabels(pod, zoneName)
			pod.Spec.NodeSelector = map[string]string{
				labelZone: zoneName,
			}
			return nil
		}
		return fmt.Errorf("instance %s has no zone assigned yet", instanceId)
	})
}

func attachTopologyLabels(pod *corev1.Pod, zone string) {
	pod.ObjectMeta.Labels[viper.GetString(config.LabelZone)] = zone
	pod.ObjectMeta.Labels[viper.GetString(config.LabelRegion)] = cluster.GetRegionForZone(zone)
}
