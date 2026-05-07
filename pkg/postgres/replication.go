package postgres

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
)

var httpClient http.Client

type pgStatusResponse struct {
	ReplicationInfo []replicationInfo `json:"replicationInfo,omitempty"`
}

type replicationInfo struct {
	// syncState (sync|quorum|potential|async|)
	SyncState       string `json:"syncState,omitempty"`
	ApplicationName string `json:"applicationName"`
}

func isSyncState(stateString string) bool {
	return strings.EqualFold(stateString, "sync") || strings.EqualFold(stateString, "quorum")
}

// callStatusEndpoint will do a HTTP call to the status endpoint of the postgres
// instance with the given IP.
// Note that this will only return a response with sync information, if the given
// IP is the one of the primary instance.
func callStatusEndpoint(primaryIP string) (pgStatusResponse, error) {
	resp, err := httpClient.Get(fmt.Sprintf("https://%s:8000/pg/status", primaryIP))
	if err != nil {
		return pgStatusResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return pgStatusResponse{}, fmt.Errorf("Unexpected status code %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return pgStatusResponse{}, err
	}
	statResponse := &pgStatusResponse{}
	err = json.Unmarshal(body, statResponse)
	if err != nil {
		return pgStatusResponse{}, err
	}
	return *statResponse, nil
}

func runRebalancing() {
	for {
		time.Sleep(10 * time.Minute)
		rebalanceAll()
	}
}

func rebalanceAll() {
	slog.Info("Start instance-AZ rebalancing...")
	for _, clus := range GetClusters() {
		go rebalanceCluster(clus)
	}
}

// rebalanceCluster checks the replication-state and AZ of every instance and makes
// sure that maximum 1 sync or primary is running per AZ.
// If this is not the case, this will either initiate a switchover or drop the second
// sync pod in the affected AZ.
func rebalanceCluster(clus PGCluster) {
	ctx := config.EnrichContext(context.Background(), clus.GetName())
	slog.InfoContext(ctx, "Check sync replica balancing for cluster")
	if !clus.RegistrationComplete() {
		slog.DebugContext(ctx, "Skip Balancing, as cluster is not fully registered")
		return
	}
	if !clus.AllInstancesHealthy() {
		slog.DebugContext(ctx, "Skip Balancing, as not all instances are healthy")
		return
	}
	stateMap := clus.GetReplicationState()
	if len(stateMap) < 1 {
		slog.DebugContext(ctx, "Skip Balancing, as replication state is unknown")
		return
	}

	// get active replicas from state map and sort them by ID
	activeReplicas := make([]string, 0)
	for instId := range stateMap {
		activeReplicas = append(activeReplicas, instId)
	}
	slices.Sort(activeReplicas)

	// temporary map to store AZ names where a sync replica or primary was detected
	zonesWithSyncReplicas := map[string]struct{}{}

	for _, instId := range activeReplicas {
		isSyncState := isSyncState(stateMap[instId])

		if clus.GetCurrentPrimaryInstanceId() == instId {
			inst := clus.GetPrimaryInstance()
			if inst == nil {
				slog.ErrorContext(ctx, "Invalid state: cluster has current primary ID set, but no primary instance registered")
				return
			}
			zone := inst.GetZone()
			if _, ok := zonesWithSyncReplicas[zone]; ok {
				// instance with lower ID already a sync replica, so this instance must
				// not be primary
				slog.InfoContext(ctx, "Found sync and primary instance in same region. Initiate switchover...")
				if err := clus.initiateSwitchover(ctx); err != nil {
					slog.ErrorContext(ctx, "Failed to initiate switchover", "err", err)
				}
				return
			}
			zonesWithSyncReplicas[zone] = struct{}{}
			continue
		} else if !isSyncState {
			// ignore non-sync replicas
			continue
		}
		inst := clus.GetInstanceByID(instId)
		if _, ok := zonesWithSyncReplicas[inst.GetZone()]; ok {
			slog.InfoContext(ctx, fmt.Sprintf("Found two sync instances in zone %s. Removing %s...", inst.GetZone(), inst.name))
			if err := deletePod(ctx, clus.GetNamespace(), inst.name); err != nil {
				slog.ErrorContext(ctx, "Failed to delete pod", "err", err)
			}
			return
		}
		zonesWithSyncReplicas[inst.GetZone()] = struct{}{}
	}
	slog.InfoContext(ctx, "Cluster is already balanced over AZs")
}

func init() {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	httpClient = http.Client{
		Timeout:   time.Duration(1) * time.Second,
		Transport: tr,
	}

	go runRebalancing()
}
