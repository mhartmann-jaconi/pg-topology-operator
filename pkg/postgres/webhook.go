package postgres

import (
	"context"
	"fmt"
	"log/slog"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

const mutationEndpoint string = "/mutate-clusters"
const MutationConfigName string = "postgres-cluster-update"

func Setup(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&pgapi.Cluster{}).
		WithDefaulter(&defaulter{}).
		WithDefaulterCustomPath(mutationEndpoint).
		Complete()
}

type defaulter struct {
}

func (defaulter) Default(ctx context.Context, obj runtime.Object) error {
	cluster, ok := obj.(*pgapi.Cluster)
	if !ok {
		slog.Warn(fmt.Sprintf("Invalid object type: %s", obj.GetObjectKind().GroupVersionKind().Kind))
		return nil
	}
	ctx = config.EnrichContext(ctx, cluster.Name)
	currentPrimary := cluster.Status.CurrentPrimary
	desiredPrimary := cluster.Status.TargetPrimary
	if currentPrimary == "" {
		slog.DebugContext(ctx, "Current primary not set yet for cluster")
		return nil
	}
	if currentPrimary == desiredPrimary {
		slog.DebugContext(ctx, "Current primary is already target primary on cluster")
		return nil
	}
	clus := GetCluster(cluster.Namespace, cluster.Name)
	if clus == nil {
		slog.DebugContext(ctx, "Cluster is not registered yet")
		return nil
	}
	inst := clus.GetInstanceByName(desiredPrimary)
	if inst == nil {
		slog.ErrorContext(ctx, fmt.Sprintf("Instance %s not registered for cluster", desiredPrimary))
		return nil
	}
	if eligible, reason := isEligible(inst, false); eligible {
		slog.DebugContext(ctx, fmt.Sprintf("Desired primary %s is already eligible cluster", desiredPrimary))
		return nil
	} else {
		slog.InfoContext(ctx, fmt.Sprintf("Intercept primary election from %s: %s", desiredPrimary, reason))
	}
	newPrimary, err := findNewEligibleTargetInstance(ctx, clus)
	if err != nil {
		slog.ErrorContext(ctx, fmt.Sprintf("Failed to find new target instance for cluster: %v", err))
		return nil
	}
	slog.InfoContext(ctx, fmt.Sprintf("Update cluster desired primary to %s", newPrimary))
	cluster.Status.TargetPrimary = newPrimary
	return nil
}
