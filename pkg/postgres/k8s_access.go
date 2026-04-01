package postgres

import (
	"context"
	"time"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var cli client.Client = nil

func readCluster(namespace, name string) (*pgapi.Cluster, error) {
	clus := &pgapi.Cluster{}
	err := getClient().Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, clus)
	return clus, err
}

func patchObject(origin, updated client.Object) error {
	// always do retries for K8s write requests to avoud conflict errors
	return retryOnConflict(func() error {
		return getClient().Status().Patch(
			context.TODO(),
			updated,
			client.MergeFromWithOptions(origin, client.MergeFromWithOptimisticLock{}),
		)
	})
}

func retryOnConflict(function func() error) error {
	return retry.OnError(
		wait.Backoff{
			Duration: 1 * time.Second,
			Factor:   1.0,
			Steps:    10,
			Jitter:   0.0,
		},
		func(err error) bool {
			return errors.IsConflict(err)
		},
		function,
	)
}

func getClient() client.Client {
	if cli != nil {
		return cli
	}
	cli = mgr.GetClient()
	return cli
}
