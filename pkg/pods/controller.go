package pods

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	postgres "github.com/jaconi-io/ipoque-pg-webhook/pkg/postgres"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/util"
	"github.com/spf13/viper"
	v1 "k8s.io/api/core/v1"
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

// Reconcile will fill the internal postgres state map with instance-AZ-mapping
// information
func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	slog.Debug(fmt.Sprintf("Reconciling pod %s...", request.Name))
	var pod = &v1.Pod{}
	if err := r.Client.Get(ctx, request.NamespacedName, pod); err != nil {
		if errors.IsNotFound(err) {
			// deleted
			return reconcile.Result{}, nil
		}
		slog.Error(fmt.Sprintf("failed to read pod: %v", err))
		return reconcile.Result{}, err
	}
	_, ok := pod.Labels[postgres.LabelPGInstanceName]
	if !ok {
		// not a PG instance
		return reconcile.Result{}, nil
	}

	clusterName := postgres.GetClusterName(pod.Labels)
	instanceId := postgres.GetInstanceId(pod.Labels)

	if pod.DeletionTimestamp != nil {
		// delete instance from internal state
		postgres.DropInstanceId(clusterName, instanceId)
		return reconcile.Result{}, nil
	}
	if len(pod.Spec.NodeSelector) < 1 {
		// no node selector set, ignore events until node selector got applied
		return reconcile.Result{}, nil
	}

	zoneName := pod.Spec.NodeSelector[viper.GetString(config.LabelZone)]
	_, err := postgres.SetZoneForInstanceId(clusterName, instanceId, zoneName, util.PodIsHealthy(pod))

	return reconcile.Result{}, err
}

func InitController(mgr manager.Manager) {
	ctrl.NewControllerManagedBy(mgr).
		For(&v1.Pod{}).
		Complete(&Reconciler{Client: mgr.GetClient()})
}
