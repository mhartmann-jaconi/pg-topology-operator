package pvcs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	postgres "github.com/jaconi-io/ipoque-pg-webhook/pkg/postgres"
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
	slog.Debug(fmt.Sprintf("Reconciling pvc %s...", request.Name))
	var pvc = &v1.PersistentVolumeClaim{}
	if err := r.Client.Get(ctx, request.NamespacedName, pvc); err != nil {
		if errors.IsNotFound(err) {
			// deleted
			return reconcile.Result{}, nil
		}
		slog.Error(fmt.Sprintf("failed to read PVC: %v", err))
		return reconcile.Result{}, err
	}
	_, ok := pvc.Labels[postgres.LabelPGInstanceName]
	if !ok {
		// not a PG instance
		return reconcile.Result{}, nil
	}

	clusterName := postgres.GetClusterName(pvc.Labels)
	instanceId := postgres.GetInstanceId(pvc.Labels)
	zoneName := pvc.Labels[viper.GetString(config.LabelZone)]

	postgres.SetZoneForInstanceIdIgnoreHealth(clusterName, instanceId, zoneName)

	return reconcile.Result{}, nil
}

func InitController(mgr manager.Manager) {
	ctrl.NewControllerManagedBy(mgr).
		For(&v1.PersistentVolumeClaim{}).
		Complete(&Reconciler{Client: mgr.GetClient()})
}
