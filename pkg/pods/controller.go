package pods

import (
	"context"
	"fmt"
	"log"
	"log/slog"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	postgres "github.com/jaconi-io/ipoque-pg-webhook/pkg/postgres"
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

// Reconcile will fill the internal postgres state map with instance-region-mapping-
// and healthiness-information
func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	var pod = &v1.Pod{}
	if err := r.Client.Get(ctx, request.NamespacedName, pod); err != nil {
		if errors.IsNotFound(err) {
			// deleted
			return reconcile.Result{}, nil
		}
		slog.Error(fmt.Sprintf("failed to read pod: %s", request.Name), "err", err)
		return reconcile.Result{}, err
	}
	if _, ok := pod.Labels[postgres.LabelPGInstanceName]; !ok {
		// not a PG instance
		return reconcile.Result{}, nil
	}

	clusterName := postgres.GetClusterName(pod.Labels)
	instanceId := postgres.GetInstanceId(pod.Labels)

	ctx = config.EnrichContext(ctx, clusterName)

	clus := postgres.GetCluster(pod.Namespace, clusterName)
	if clus == nil {
		// cluster not existing yet in internal state, retry
		slog.WarnContext(ctx, fmt.Sprintf("Cluster is not existing, skip processing of %s", pod.Name))
		return reconcile.Result{}, fmt.Errorf("cluster not regstered yet")
	}

	isSetupJob := false
	_, isSetupJob = pod.Labels[postgres.LabelPGJobRole]

	if pod.DeletionTimestamp != nil {
		if !isSetupJob {
			// inactive instance from internal state
			clus.MarkInactive(instanceId)
		}
		return reconcile.Result{}, nil
	}
	if len(pod.Spec.NodeName) < 1 {
		// no node applied, ignore events until node name got applied
		slog.DebugContext(ctx, fmt.Sprintf("Ignore pod %s, as node is not set yet", pod.Name))
		return reconcile.Result{}, nil
	}

	nodeName := pod.Spec.NodeName
	region := cluster.GetRegionOfNode(nodeName)

	if region == "" {
		slog.DebugContext(ctx, fmt.Sprintf("Skip %s as node is %s not registered yet", pod.Name, nodeName))
		return reconcile.Result{}, fmt.Errorf("node not registered yet")
	}

	// as long as setup is running, instance can not be considered as healthy
	instanceIsHealthy := isHealthy(pod) && !isSetupJob

	clus.RegisterOrUpdateInstance(instanceId, region, instanceIsHealthy)

	return reconcile.Result{}, nil
}

func isHealthy(pod *v1.Pod) bool {
	if pod.Status.Phase != v1.PodRunning {
		return false
	}
	for _, con := range pod.Status.ContainerStatuses {
		if !con.Ready {
			return false
		}
	}
	return true
}

func InitController(mgr manager.Manager) reconcile.Reconciler {
	reco, err := ctrl.NewControllerManagedBy(mgr).
		For(&v1.Pod{}).
		WithOptions(controller.TypedOptions[reconcile.Request]{MaxConcurrentReconciles: 2}).
		Build(&Reconciler{Client: mgr.GetClient()})
	if err != nil {
		log.Fatal(err)
	}
	return reco
}
