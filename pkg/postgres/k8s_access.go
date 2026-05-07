package postgres

import (
	"context"
	"log/slog"
	"time"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var cli client.Client = nil

type ContextCanceled struct {
	origin error
}

func (cc ContextCanceled) Error() string {
	return cc.origin.Error()
}

func readCluster(ctx context.Context, namespace, name string) (*pgapi.Cluster, error) {
	clus := &pgapi.Cluster{}
	err := getClient().Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, clus)
	return clus, err
}

func patchClusterWithRetry(ctx context.Context, namespace, name string, change func(*pgapi.Cluster) (*pgapi.Cluster, bool)) error {
	return retry.OnError(
		wait.Backoff{
			Duration: 500 * time.Millisecond,
			Factor:   1.0,
			Steps:    10,
			Jitter:   0.0,
		},
		func(err error) bool {
			if errors.IsNotFound(err) {
				slog.InfoContext(ctx, "Cluster is gone, give up patching")
				return false
			}
			if _, ok := err.(ContextCanceled); ok {
				slog.InfoContext(ctx, "Context deadline exceeded, give up patching")
				return false
			}
			slog.WarnContext(ctx, "Error patching cluster, retrying", "err", err)
			return true
		},
		func() error {
			if ctx.Err() != nil {
				return ContextCanceled{origin: ctx.Err()}
			}
			cluster, err := readCluster(ctx, namespace, name)
			if err != nil {
				return err
			}
			modified, changed := change(cluster)
			if !changed {
				return nil
			}
			return getClient().Status().Patch(
				ctx,
				modified,
				client.MergeFrom(cluster),
			)
		},
	)
}

func deletePod(ctx context.Context, namespace, name string) error {
	pod := &v1.Pod{}
	clint := getClient()
	err := clint.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if pod.ObjectMeta.DeletionTimestamp != nil {
		// already in deletion
		return nil
	}
	return clint.Delete(ctx, pod)
}

func getClient() client.Client {
	if cli != nil {
		return cli
	}
	cli = mgr.GetClient()
	return cli
}
