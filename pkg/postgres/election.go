package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	nodes "github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/events"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/util"
	"github.com/spf13/viper"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var mgr manager.Manager
var cli client.Client = nil

// changePrimaryInstances will change the deisred primary instance for all PG clusters
// based on the current leading AZ. This will loop through all existing pods of the
// cluster and check which instance is a sync-replica and in the desired AZ. This instance
// is then set as desired new primary on the cluster.
func changePrimaryInstances(newLeadingZone string) error {
	slog.Info(fmt.Sprintf("Received event for primary AZ change to %s", newLeadingZone))
	for name, clus := range clusters {
		if err := changePrimaryFor(clus); err != nil {
			slog.Error(fmt.Sprintf("Failed to change primary instance for cluster %s: %v", name, err))
			return err
		}
	}
	return nil
}

func changePrimaryFor(clus Cluster) error {
	slog.Info(fmt.Sprintf("Changing primary instance for cluster %s", clus.name))
	newInstance, err := findNewEligibleTargetInstance(clus, false)
	if err != nil {
		return err
	}
	if newInstance == "" {
		return fmt.Errorf("no eligible target instance found to be promoted to primary")
	}
	newInstanceId := ExtractInstanceId(clus.name, newInstance)
	if newInstanceId == clus.primary {
		slog.Debug(fmt.Sprintf("Skip primary change for cluster %s as most eligible instance is already set", clus.name))
		return nil
	}
	return applyNewPrimary(clus.name, clus.namespace, newInstance)
}

func findNewEligibleTargetInstance(clus Cluster, skipCurrent bool) (string, error) {
	// first, try to find new primary instance on the primary AZ
	instId, err := findNewEligibleTargetInstanceInternal(clus, true, skipCurrent)
	if err != nil {
		return "", err
	}
	if instId == "" {
		// as a fallback, try to get one in the same region
		return findNewEligibleTargetInstanceInternal(clus, false, skipCurrent)
	}
	return instId, nil
}

// findNewEligibleTargetInstanceInternal will return the instance-name of the instance,
// which should currently become the primary instance. The behaviour can be controlled
// by parameters, where we you can define:
// - if the instance must be on the primary zone or just in the same region as the
// . primary zone
// - if the current primary instance should not be returned, even if it is the most
// . eligible one
func findNewEligibleTargetInstanceInternal(clus Cluster, onPrimaryZone, skipCurrent bool) (string, error) {

	primaryZone := nodes.GetPrimaryZone()
	slog.Info(fmt.Sprintf("Changing primary instance to zone %s", primaryZone))

	podList := &v1.PodList{}
	err := getClient().List(context.TODO(), podList, client.MatchingLabels{LabelPGClusterName: clus.name})
	if err != nil {
		return "", err
	}
	slog.Info(fmt.Sprintf("Found %d pods overall", len(podList.Items)))

	// iterate through pods (healthy instances) and decide, which one is eligible
	// for being promoted to new primary
	for _, pod := range podList.Items {
		slog.Debug(fmt.Sprintf("Checking pod %s...", pod.Name))
		if _, ok := pod.Labels[LabelPGInstanceRole]; !ok {
			slog.Debug(fmt.Sprintf("Skip pod %s, as it has no instance role set", pod.Name))
			continue
		}
		if onPrimaryZone {
			// check for pod in the primary zone
			if selector := pod.Spec.NodeSelector[viper.GetString(config.LabelZone)]; selector != primaryZone {
				slog.Debug(fmt.Sprintf("Skip pod %s, as its zone %s does not match required zone %s", pod.Name, selector, primaryZone))
				continue
			}
		} else {
			// check for same region
			primaryRegion := nodes.GetRegionForZone(primaryZone)
			if podRegion := pod.Labels[viper.GetString(config.LabelRegion)]; podRegion != primaryRegion {
				slog.Debug(fmt.Sprintf("Skip pod %s, as its region %s does not match required region %s", pod.Name, podRegion, primaryRegion))
				continue
			}
		}
		instanceId := GetInstanceId(pod.Labels)
		if skipCurrent && instanceId == GetCurrentPrimaryInstanceId(clus.name) {
			slog.Debug(fmt.Sprintf("Skip pod %s, as it is the actual primary", pod.Name))
			continue
		}
		if !isSyncReplica(clus.name, instanceId) {
			slog.Debug(fmt.Sprintf("Skip pod %s as it is aynchronous replicated", pod.Name))
			continue
		}

		if !util.PodIsHealthy(&pod) {
			slog.Debug(fmt.Sprintf("Skip pod %s as it is not healthy", pod.Name))
			continue
		}
		slog.Info(fmt.Sprintf("Found %s to get promoted to new primary instance", pod.Name))
		return pod.Name, nil
	}
	return "", nil
}

// applyNewPrimary sets the given instance name as the new desired primary on the cluster
func applyNewPrimary(clusterName, clusterNamespace, newPrimary string) error {
	slog.Info(fmt.Sprintf("Change cluster to have new primary instance %s", newPrimary))
	var cluster = &pgapi.Cluster{}
	err := getClient().Get(context.TODO(), types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, cluster)
	if err != nil {
		return err
	}

	mcluster := setNewTarget(cluster.DeepCopy(), newPrimary)

	return getClient().Status().Patch(
		context.TODO(),
		mcluster,
		client.MergeFromWithOptions(cluster, client.MergeFromWithOptimisticLock{}),
	)
}

func setNewTarget(clus *pgapi.Cluster, newTarget string) *pgapi.Cluster {
	clus.Status.TargetPrimary = newTarget
	clus.Status.TargetPrimaryTimestamp = getCurrentTimestamp()
	clus.Status.Phase = pgapi.PhaseSwitchover
	clus.Status.PhaseReason = fmt.Sprintf("Switching over to %v", newTarget)
	// tell PG-operator that cluster is not ready, so a change needs to be made
	setClusterNotReadyCondition(clus)
	return clus
}

func setClusterNotReadyCondition(cluster *pgapi.Cluster) {
	if cluster.Status.Conditions == nil {
		cluster.Status.Conditions = []metav1.Condition{}
	}

	condition := metav1.Condition{
		Type:    string(pgapi.ConditionClusterReady),
		Status:  metav1.ConditionFalse,
		Reason:  string(pgapi.ClusterIsNotReady),
		Message: "Cluster Is Not Ready",
	}

	meta.SetStatusCondition(&cluster.Status.Conditions, condition)
}

func getCurrentTimestamp() string {
	t := time.Now()
	return t.Format(metav1.RFC3339Micro)
}

func getClient() client.Client {
	if cli != nil {
		return cli
	}
	cli = mgr.GetClient()
	return cli
}

func init() {
	// call changePrimaryInstances in case the primary AZ changes
	events.AddListenerForAZChange(func(ctx context.Context, s string) {
		changePrimaryInstances(s)
	})
}
