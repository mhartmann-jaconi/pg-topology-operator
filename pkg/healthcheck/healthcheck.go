package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/spf13/viper"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/config"
)

const (
	leaseName      = "pg-topology-operator-watchdog"
	writeInterval  = 15 * time.Second
	livenessMaxAge = 1 * time.Minute
)

// Reconcile for K8s leases
type LeaseReconciler struct {
	client.Client
	watchdog *Watchdog
}

var podName string

var _ reconcile.Reconciler = &LeaseReconciler{}

func Setup(mgr manager.Manager) (*Watchdog, error) {
	namespace := viper.GetString(config.ServiceNamespace)
	if namespace == "" {
		return nil, errors.New("service namespace must be configured for healthcheck watchdog")
	}

	podName = viper.GetString(config.PodName)
	if podName == "" {
		return nil, fmt.Errorf("missing pod.name")
	}

	watchdog := &Watchdog{namespace: namespace}

	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&coordinationv1.Lease{}).
		WithOptions(controller.TypedOptions[reconcile.Request]{MaxConcurrentReconciles: 1}).
		Build(&LeaseReconciler{
			Client:   mgr.GetClient(),
			watchdog: watchdog,
		})
	if err != nil {
		return nil, fmt.Errorf("failed to build watchdog controller: %w", err)
	}

	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		return runLeaseUpdater(ctx, mgr.GetClient(), namespace)
	})); err != nil {
		return nil, fmt.Errorf("failed to add watchdog lease updater: %w", err)
	}

	return watchdog, nil
}

// Reconciles the operator lease and writes lastSeen time to Watchdog
func (r *LeaseReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	if request.Namespace != r.watchdog.namespace || request.Name != leaseName {
		return reconcile.Result{}, nil
	}

	lease := &coordinationv1.Lease{}
	if err := r.Client.Get(ctx, request.NamespacedName, lease); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	r.watchdog.MarkSeen(time.Now())

	return reconcile.Result{}, nil
}

// runLeaseUpdater starts an endless loop and writes every 'writeInterval'
// seconds the current timestamp to the K8s lease
func runLeaseUpdater(ctx context.Context, c client.Client, namespace string) error {
	ticker := time.NewTicker(writeInterval)
	defer ticker.Stop()

	if err := writeToLease(ctx, c, namespace, time.Now()); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := writeToLease(ctx, c, namespace, time.Now()); err != nil {
				slog.Error(fmt.Sprintf("Failed to update watchdog lease %s/%s", namespace, leaseName), "err", err)
			}
		}
	}
}

// writeToLease updates the operator's lease with the current timestamp
func writeToLease(ctx context.Context, c client.Client, namespace string, now time.Time) error {
	leaseKey := types.NamespacedName{Name: leaseName, Namespace: namespace}
	lease := &coordinationv1.Lease{}
	err := c.Get(ctx, leaseKey, lease)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		lease = &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      leaseName,
				Namespace: namespace,
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity: &podName,
				RenewTime:      &metav1.MicroTime{Time: now},
			},
		}
		return c.Create(ctx, lease)
	}

	base := lease.DeepCopy()
	lease.Spec.RenewTime = &metav1.MicroTime{Time: now}
	lease.Spec.HolderIdentity = &podName
	return c.Patch(ctx, lease, client.MergeFrom(base))
}

var _ healthz.Checker = healthz.Checker(func(req *http.Request) error { return nil })
