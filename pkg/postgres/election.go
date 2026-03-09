package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	nodes "github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/events"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// changePrimaryInstances will change the desired primary instance for all PG clusters
// based on the current leading AZ. This will loop through all existing pods of the
// cluster and check which instance is a sync-replica and in the desired AZ. This instance
// is then set as desired new primary on the cluster.
func changePrimaryInstances(newLeadingZone string) error {
	slog.Info(fmt.Sprintf("Received event for primary AZ change to %s", newLeadingZone))
	for _, clus := range clusters {
		if err := InitiateSwitchover(clus); err != nil {
			slog.Error(fmt.Sprintf("Failed to change primary instance for cluster %s: %v", clus.name, err))
			return err
		}
	}
	return nil
}

func InitiateSwitchover(clus *Cluster) error {
	// TODO when we trust cnpg-operator in choosing the new primary, we might end up in a
	//  state, where all sync-replicas run in a single region. So for now we require the
	//  new primary to be a sync one and therfore we might end up with an additional switch-back
	if alreadyChanged, _ := isEligible(clus.GetPrimaryInstance(), true); alreadyChanged {
		slog.Debug(fmt.Sprintf("Eligible target primary already set on cluster %s", clus.name))
		return nil
	} else if clus.desiredPrimary == "" {
		slog.Debug(fmt.Sprintf("Desired primary not yet existing for cluster %s", clus.name))
		return nil
	}

	slog.Info(fmt.Sprintf("Changing primary instance for cluster %s. Current: %s, Desired: %s", clus.name, clus.primary, clus.desiredPrimary))
	newInstance, err := findNewEligibleTargetInstance(clus)
	if err != nil {
		return err
	}
	if newInstance == "" {
		return fmt.Errorf("no eligible target instance found to be promoted to primary")
	}
	return applyNewPrimary(clus.name, clus.namespace, newInstance)
}

// findNewEligibleTargetInstance will return the instance-name of the instance,
// which should currently become the primary instance.
func findNewEligibleTargetInstance(clus *Cluster) (string, error) {

	podList, err := listPodsForCluster(clus.namespace, clus.name)
	if err != nil {
		return "", err
	}
	slog.Info(fmt.Sprintf("Found %d pods overall", len(podList)))

	// iterate through pods (healthy instances) and decide, which one is eligible
	// for being promoted to new primary
	for _, pod := range podList {
		slog.Debug(fmt.Sprintf("Checking pod %s...", pod.Name))
		if is, reason := isEligible(clus.GetInstanceByName(pod.Name), true); is {
			slog.Info(fmt.Sprintf("Found %s to get promoted to new primary instance", pod.Name))
			return pod.Name, nil
		} else {
			slog.Debug(fmt.Sprintf("Skip instance %s as it is %s", pod.Name, reason))
		}
	}
	return "", nil
}

func isEligible(inst *Instance, checkIfSync bool) (eligible bool, reason string) {
	if inst == nil {
		return false, "not existing"
	}
	if !inst.IsActive() {
		return false, "not active"
	}
	if nodes.GetPrimaryRegion() != inst.GetRegion() {
		return false, "not in primary region"
	}
	if checkIfSync && !inst.IsSyncReplica() {
		// if another one already set the target primary we trust him, that this was intentional
		// and that this instance is in sync
		return false, "not a sync replica"
	}
	return true, ""
}

// applyNewPrimary sets the given instance name as the new desired primary on the cluster
func applyNewPrimary(clusterName, clusterNamespace, newPrimary string) error {
	slog.Info(fmt.Sprintf("Change cluster to have new primary instance %s", newPrimary))

	cluster, err := readCluster(clusterNamespace, clusterName)
	if err != nil {
		return err
	}
	if cluster.Status.TargetPrimary == newPrimary {
		// already set, nothing to do here
		return nil
	}

	mcluster := setNewTarget(cluster.DeepCopy(), newPrimary)

	return patchObject(cluster, mcluster)
}

func setNewTarget(clus *pgapi.Cluster, newTarget string) *pgapi.Cluster {
	// from https://github.com/cloudnative-pg/cloudnative-pg/blob/main/internal/cmd/plugin/promote/promote.go
	clus.Status.TargetPrimary = newTarget
	clus.Status.TargetPrimaryTimestamp = getCurrentTimestamp()
	clus.Status.Phase = pgapi.PhaseSwitchover
	clus.Status.PhaseReason = fmt.Sprintf("Switching over to %v", newTarget)
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

func init() {
	// call changePrimaryInstances in case the primary AZ changes
	events.AddListenerForRegionChange(func(ctx context.Context, s string) {
		changePrimaryInstances(s)
	})
}
