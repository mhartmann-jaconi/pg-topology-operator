package postgres

import (
	"context"
	"time"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
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

func patchClusterWithRetry(namespace, name string, change func(*pgapi.Cluster) (*pgapi.Cluster, bool)) error {
	return retry.OnError(
		wait.Backoff{
			Duration: 1 * time.Second,
			Factor:   1.0,
			Steps:    10,
			Jitter:   0.0,
		},
		func(err error) bool {
			return true
		},
		func() error {
			cluster, err := readCluster(namespace, name)
			if err != nil {
				return err
			}
			modified, changed := change(cluster)
			if !changed {
				return nil
			}
			return getClient().Status().Patch(
				context.TODO(),
				modified,
				client.MergeFrom(cluster),
			)
		},
	)
}

func getClient() client.Client {
	if cli != nil {
		return cli
	}
	cli = mgr.GetClient()
	return cli
}
