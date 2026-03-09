package postgres

import (
	"context"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var cli client.Client = nil

func readCluster(namespace, name string) (*pgapi.Cluster, error) {
	clus := &pgapi.Cluster{}
	err := getClient().Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, clus)
	return clus, err
}

func listPodsForCluster(namespace, name string) ([]corev1.Pod, error) {
	podList := &v1.PodList{}
	err := getClient().List(context.TODO(), podList, client.MatchingLabels{LabelPGClusterName: name}, client.InNamespace(namespace))
	if err != nil {
		return make([]corev1.Pod, 0), err
	}
	return podList.Items, nil
}

func patchObject(origin, updated client.Object) error {
	return getClient().Status().Patch(
		context.TODO(),
		updated,
		client.MergeFromWithOptions(origin, client.MergeFromWithOptimisticLock{}),
	)
}

func getClient() client.Client {
	if cli != nil {
		return cli
	}
	cli = mgr.GetClient()
	return cli
}
