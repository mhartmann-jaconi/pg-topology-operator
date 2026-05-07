package cluster

import (
	"context"
	"fmt"
	"log"
	"log/slog"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/spf13/viper"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	client.Client
}

var _ reconcile.Reconciler = &Reconciler{}

// Reconcile will store information about existing zones, regions and which
// zone is the primary one, based on node labels
func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	var node = &v1.Node{}
	if err := r.Client.Get(ctx, request.NamespacedName, node); err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}

		slog.Error(fmt.Sprintf("failed to read node: %s", request.Name), "err", err)
		return reconcile.Result{}, err
	}

	if !isHealthy(node) {
		// ignore
		slog.Debug(fmt.Sprintf("Ignore node %s as it is not healthy", node.Name))
		return reconcile.Result{}, nil
	}

	region := node.Labels[viper.GetString(config.LabelRegion)]
	zone := node.Labels[viper.GetString(config.LabelZone)]
	_, isLeader := node.Labels[viper.GetString(config.LabelLeader)]

	Store(node.Name, region, zone, isLeader)

	return reconcile.Result{}, nil
}

func isHealthy(node *v1.Node) bool {
	if node.DeletionTimestamp != nil {
		return false
	}
	for _, taint := range node.Spec.Taints {
		if taint.Effect == v1.TaintEffectNoSchedule {
			return false
		}
	}
	return !node.Spec.Unschedulable
}

func InitController(mgr manager.Manager) reconcile.Reconciler {
	reco, err := ctrl.NewControllerManagedBy(mgr).
		For(&v1.Node{}).
		WithOptions(controller.TypedOptions[reconcile.Request]{MaxConcurrentReconciles: 2}).
		Build(&Reconciler{Client: mgr.GetClient()})
	if err != nil {
		log.Fatal(err)
	}
	return reco
}
