package pods

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	postgres "github.com/jaconi-io/ipoque-pg-webhook/pkg/postgres"
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
	var pod = &v1.Pod{}
	if err := r.Client.Get(ctx, request.NamespacedName, pod); err != nil {
		if errors.IsNotFound(err) {
			// deleted
			return reconcile.Result{}, nil
		}
		slog.Error(fmt.Sprintf("failed to read pod: %v", err))
		return reconcile.Result{}, err
	}
	if _, ok := pod.Labels[postgres.LabelPGInstanceName]; !ok {
		// not a PG instance
		return reconcile.Result{}, nil
	}
	if _, ok := pod.Labels[postgres.LabelPGJobRole]; ok {
		// a setup job, ignore
		return reconcile.Result{}, nil
	}

	clusterName := postgres.GetClusterName(pod.Labels)
	instanceId := postgres.GetInstanceId(pod.Labels)

	clus := postgres.GetCluster(pod.Namespace, clusterName)
	if clus == nil {
		// cluster not existing in internal state
		slog.Warn(fmt.Sprintf("Cluster %s not existing, skip processing of %s", clusterName, pod.Name))
		return reconcile.Result{}, nil
	}

	if pod.DeletionTimestamp != nil {
		// inactive instance from internal state
		clus.MarkInactive(instanceId)
		// check if we need to initiate a switchover
		if postgres.IsPrimary(pod.Labels) && postgres.IsHealthy(clusterName, pod.Namespace) {
			return reconcile.Result{}, doSwitchover(pod.Name, clus)
		}
		return reconcile.Result{}, nil
	}
	if len(pod.Spec.NodeName) < 1 {
		// no node applied, ignore events until node name got applied
		return reconcile.Result{}, nil
	}

	nodeName := pod.Spec.NodeName
	region := cluster.GetRegionOfNode(nodeName)
	podIsHealthy := isHealthy(pod)

	clus.RegisterOrUpdateInstance(instanceId, region, podIsHealthy)

	if !podIsHealthy &&
		postgres.IsPrimary(pod.Labels) &&
		postgres.IsHealthy(clusterName, pod.Namespace) {
		// initiate switchover proactive
		return reconcile.Result{}, doSwitchover(pod.Name, clus)
	}

	return reconcile.Result{}, nil
}

func doSwitchover(currentPrimary string, clus *postgres.Cluster) error {
	slog.Info(fmt.Sprintf("Primary instance %s unhealthy in cluster %s. Initiating switchover", currentPrimary, clus.GetName()))
	if err := postgres.InitiateSwitchover(clus); err != nil {
		slog.Error(fmt.Sprintf("Failed to initate switchover for cluster %s: %v", clus.GetName(), err))
		return err
	}
	return nil
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

func InitController(mgr manager.Manager) {
	ctrl.NewControllerManagedBy(mgr).
		For(&v1.Pod{}).
		Complete(&Reconciler{Client: mgr.GetClient()})
}
