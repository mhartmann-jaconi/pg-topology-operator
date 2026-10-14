package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	nodes "vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/cluster"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/events"
)

// changePrimaryInstances will change the desired primary instance for all PG clusters
// based on the current leading AZ. This will loop through all existing pods of the
// cluster and check which instance is a sync-replica and in the desired AZ. This instance
// is then set as desired new primary on the cluster.
func changePrimaryInstances(ctx context.Context, newLeadingZone string) error {
	existingClusters := GetClusters()
	slog.Info(fmt.Sprintf("Received event for primary AZ change to %s. Updating %d clusters", newLeadingZone, len(existingClusters)))
	clustersLeft := make([]PGCluster, len(existingClusters))
	count := 0
	for _, clus := range existingClusters {
		clustersLeft[count] = clus
		count++
	}
	return retry.OnError(
		wait.Backoff{
			Duration: 1 * time.Second,
			Factor:   1.5,
			Steps:    60,
			Jitter:   0.0,
		},
		func(err error) bool {
			return true
		},
		func() error {
			if ctx.Err() != nil {
				slog.Debug("Primary change function canceled")
				return nil
			}
			var errx error
			clustersError := make([]PGCluster, 0)
			slog.Info(fmt.Sprintf("Starting update of %d clusters", len(clustersLeft)))
			for i := range clustersLeft {
				clus := clustersLeft[i]
				if err := clus.initiateSwitchover(ctx); err != nil {
					errx = err
					clustersError = append(clustersError, clus)
				}
			}
			if len(clustersError) > 0 {
				slog.Warn(fmt.Sprintf("Errors occurred for switchover in %d clusters. Retrying...", len(clustersError)))
				clustersLeft = clustersError
				return errx
			}
			return nil
		},
	)
}

// initiateSwitchover will set a new desired primary instance on the cluster in K8s,
// if necessary. It will first check if the actual desired instance is already
// eligible to become the new primary. If not, this will try to find another instance
// to get promoted and set this as desired primary.
// If no instance could be found, this will return an error.
func initiateSwitchover(ctx context.Context, clus PGCluster) error {
	if alreadyChanged, _ := isEligible(clus.GetPrimaryInstance(), true); alreadyChanged {
		slog.DebugContext(ctx, "Eligible target primary already set on cluster")
		return nil
	} else if clus.GetDesiredPrimaryInstanceId() == "" {
		slog.DebugContext(ctx, "Desired primary not yet existing for cluster")
		return nil
	}

	slog.InfoContext(ctx, fmt.Sprintf("Changing primary instance for cluster. Current: %s, Desired: %s",
		clus.GetCurrentPrimaryInstanceId(), clus.GetDesiredPrimaryInstanceId()))
	newInstance := findNewEligibleTargetInstance(ctx, clus)
	if newInstance == "" {
		return fmt.Errorf("no eligible target instance found to be promoted to primary")
	}
	return applyNewPrimary(ctx, clus.GetName(), clus.GetNamespace(), newInstance)
}

// findNewEligibleTargetInstance will return the instance-name of the instance,
// which should currently become the primary instance.
// If no such instance could be found, an empty string is retrieved.
func findNewEligibleTargetInstance(ctx context.Context, clus PGCluster) string {

	// iterate through pods (healthy instances) and decide, which one is eligible
	// for being promoted to new primary
	for _, inst := range clus.GetInstances() {
		slog.DebugContext(ctx, fmt.Sprintf("Checking instance %s...", inst.name))
		if inst.instanceId == clus.GetCurrentPrimaryInstanceId() {
			slog.DebugContext(ctx, fmt.Sprintf("Skip instance %s as it is the current primary", inst.name))
			continue
		}
		if is, reason := isEligible(inst, true); is {
			slog.InfoContext(ctx, fmt.Sprintf("Found %s to get promoted to new primary instance", inst.name))
			return inst.name
		} else {
			slog.DebugContext(ctx, fmt.Sprintf("Skip instance %s as it is %s", inst.name, reason))
		}
	}
	return ""
}

// isEligible tells if the given instance is eligible to become the new primary instance.
// The checkIfSync parameter determines if the instance needs to be a sync replica or not.
// If the instance is not eligible, this will also return a reason string, why it isn't.
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
func applyNewPrimary(ctx context.Context, clusterName, clusterNamespace, newPrimary string) error {
	slog.InfoContext(ctx, fmt.Sprintf("Change cluster to have new primary instance %s", newPrimary))

	return patchClusterWithRetry(ctx, clusterNamespace, clusterName, func(cluster *pgapi.Cluster) (*pgapi.Cluster, bool) {
		if cluster.Status.TargetPrimary == newPrimary {
			// already set, nothing to do here
			slog.DebugContext(ctx, fmt.Sprintf("Desired primary %s already set", newPrimary))
			return nil, false
		}
		return setNewTarget(cluster.DeepCopy(), newPrimary), true
	})
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
	// call changePrimaryInstances in case the primary region changes
	events.AddListenerForRegionChange(func(ctx context.Context, s string) {
		changePrimaryInstances(ctx, s)
	})
}
