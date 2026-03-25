package postgres

import (
	"context"
	"fmt"
	"log/slog"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
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

func (defaulter) Default(_ context.Context, obj runtime.Object) error {
	cluster, ok := obj.(*pgapi.Cluster)
	if !ok {
		slog.Warn(fmt.Sprintf("Invalid object type: %s", obj.GetObjectKind().GroupVersionKind().Kind))
		return nil
	}
	currentPrimary := cluster.Status.CurrentPrimary
	desiredPrimary := cluster.Status.TargetPrimary
	if currentPrimary == "" {
		slog.Debug(fmt.Sprintf("Current primary not set yet for cluster %s", cluster.Name))
		return nil
	}
	if currentPrimary == desiredPrimary {
		slog.Debug(fmt.Sprintf("Current primary is already target primary on cluster %s", cluster.Name))
		return nil
	}
	clus := GetCluster(cluster.Namespace, cluster.Name)
	if clus == nil {
		slog.Debug(fmt.Sprintf("Cluster %s not registered yet", cluster.Name))
		return nil
	}
	inst := clus.GetInstanceByName(desiredPrimary)
	if inst == nil {
		slog.Error(fmt.Sprintf("Instance %s not registered for cluster %s", desiredPrimary, clus.name))
		return nil
	}
	if eligible, reason := isEligible(inst, false); eligible {
		slog.Debug(fmt.Sprintf("Desired primary %s is already eligible cluster %s", desiredPrimary, cluster.Name))
		return nil
	} else {
		slog.Info(fmt.Sprintf("Intercept primary election from %s: %s", desiredPrimary, reason))
	}
	newPrimary, err := findNewEligibleTargetInstance(clus)
	if err != nil {
		slog.Error(fmt.Sprintf("Failed to find new target instance for cluster %s: %v", clus.name, err))
		return nil
	}
	slog.Info(fmt.Sprintf("Update cluster desired primary to %s", newPrimary))
	cluster.Status.TargetPrimary = newPrimary
	return nil
}
