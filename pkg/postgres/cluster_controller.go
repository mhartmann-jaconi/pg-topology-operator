package postgres

import (
	"context"
	"fmt"
	"log/slog"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	client.Client
}

var _ reconcile.Reconciler = &Reconciler{}

// Reconcile gets notified of all changes for a PG cluster and put's its information
// into the internal state map (such as current primary, number of standbies, etc)
func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	cluster := &pgapi.Cluster{}
	err := r.Client.Get(context.TODO(), request.NamespacedName, cluster)
	if err != nil {
		if errors.IsNotFound(err) {
			// cluster was deleted
			dropCluster(cluster.Name)
			return reconcile.Result{}, nil
		}
		slog.Error(fmt.Sprintf("Failed to read cluster: %v", err))
		return reconcile.Result{}, err
	}

	if cluster.DeletionTimestamp != nil {
		// cluster is in deletion
		dropCluster(cluster.Name)
		return reconcile.Result{}, nil
	}

	clusterName := cluster.Name
	primaryInstanceId := ExtractInstanceId(clusterName, cluster.Status.CurrentPrimary)
	standbies := getStandbyInstanceCount(cluster)

	clus := CreateOrUpdateCluster(clusterName, request.Namespace, primaryInstanceId, standbies)

	if isClusterHealthy(cluster) {
		// cluster is healthy, try to re-shift primary instance if necessary
		if err = changePrimaryFor(clus); err != nil {
			slog.Error(fmt.Sprintf("Failed to change primary for cluster %s: %v", clus.name, err))
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func getStandbyInstanceCount(cluster *pgapi.Cluster) int {
	syncNum := cluster.Spec.PostgresConfiguration.Synchronous.Number
	maxStandby := cluster.Spec.PostgresConfiguration.Synchronous.MaxStandbyNamesFromCluster

	if maxStandby != nil && *maxStandby > syncNum {
		return *maxStandby
	} else {
		return syncNum
	}
}

func InitController(mangr manager.Manager) {
	mgr = mangr
	ctrl.NewControllerManagedBy(mangr).
		For(&pgapi.Cluster{}).
		Complete(&Reconciler{Client: mangr.GetClient()})
}
