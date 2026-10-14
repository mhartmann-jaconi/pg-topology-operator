package postgres

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/h2non/gock"
	. "github.com/onsi/gomega"
	"github.com/thejerf/slogassert"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/config"
)

var (
	url = fmt.Sprintf("https://%s:8000/pg/status", ip)
)

func TestCallStatus_error(t *testing.T) {
	g := NewWithT(t)

	gock.Intercept()
	defer gock.Off()

	httpClient = http.Client{
		Timeout:   time.Duration(1) * time.Second,
		Transport: http.DefaultTransport,
	}

	gock.New(url).
		Get("").
		Reply(400).
		JSON(map[string]string{"error": "error"})

	_, err := callStatusEndpoint(ip)
	g.Expect(err).NotTo(BeNil())
}

func TestCallStatus_invalidObject(t *testing.T) {
	g := NewWithT(t)

	gock.Intercept()
	defer gock.Off()

	httpClient = http.Client{
		Timeout:   time.Duration(1) * time.Second,
		Transport: http.DefaultTransport,
	}

	// response body does match struct
	gock.New(url).
		Get("/").
		Reply(200).
		JSON(map[string]string{"value": "different"})

	response, err := callStatusEndpoint(ip)
	g.Expect(err).To(BeNil())
	g.Expect(response.ReplicationInfo).To(BeEmpty())
}

func TestCallStatus_success(t *testing.T) {
	g := NewWithT(t)

	gock.Intercept()
	defer gock.Off()

	httpClient = http.Client{
		Timeout:   time.Duration(1) * time.Second,
		Transport: http.DefaultTransport,
	}

	gock.New(url).
		Get("").
		Reply(200).
		JSON(pgStatusResponse{
			ReplicationInfo: []replicationInfo{{
				ApplicationName: "foo-1",
				SyncState:       "",
			}, {
				ApplicationName: "foo-2",
				SyncState:       "sync",
			}, {
				ApplicationName: "foo-3",
				SyncState:       "potential",
			}},
		})

	response, err := callStatusEndpoint(ip)
	g.Expect(err).To(BeNil())
	g.Expect(response.ReplicationInfo[0].ApplicationName).To(Equal("foo-1"))
	g.Expect(response.ReplicationInfo[0].SyncState).To(BeEmpty())
	g.Expect(response.ReplicationInfo[1].ApplicationName).To(Equal("foo-2"))
	g.Expect(response.ReplicationInfo[1].SyncState).To(Equal("sync"))
	g.Expect(response.ReplicationInfo[2].ApplicationName).To(Equal("foo-3"))
	g.Expect(response.ReplicationInfo[2].SyncState).To(Equal("potential"))
}

func TestRebalanceCluster_skip(t *testing.T) {
	clus := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 3, 2)
	defer DropCluster(clusterNamespace, clusterName)

	handler := slogassert.New(t, config.LevelTrace, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(slog.New(handler))

	// not all instances registered yet
	rebalanceCluster(clus)
	handler.AssertMessage("Skip Balancing, as cluster is not fully registered")
	handler.Reset()

	// not all instances healthy
	clus.RegisterOrUpdateInstance("1", "region", "zone", ip, true)
	clus.RegisterOrUpdateInstance("2", "region", "zone", ip, true)
	clus.RegisterOrUpdateInstance("3", "region", "zone", ip, false)

	rebalanceCluster(clus)
	handler.AssertMessage("Skip Balancing, as not all instances are healthy")
	handler.Reset()

	// all instances healthy, but no response from status endpoint
	clus.RegisterOrUpdateInstance("3", "region", "zone", ip, true)

	rebalanceCluster(clus)
	handler.AssertMessage("Skip Balancing, as replication state is unknown")
	handler.Reset()
}

func TestRebalanceCluster_switchoverRequired(t *testing.T) {
	g := NewWithT(t)

	clusters = map[string]PGCluster{}
	clus := &TestCluster{
		Cluster: Cluster{
			namespace:        clusterNamespace,
			name:             clusterName,
			desiredInstances: 3,
			syncNum:          2,
			primary:          "3",
			desiredPrimary:   "3",
			instances:        map[string]*Instance{},
			instancesLock:    sync.RWMutex{},
		},
		shouldSwitchoverFail: false,
	}
	clusters[clusterKey(clusterNamespace, clusterName)] = clus
	defer DropCluster(clusterNamespace, clusterName)

	// instance 3 is primary, but is in same zone as sync instance 1
	clus.RegisterOrUpdateInstance("1", "region", "zone1", ip, true)
	clus.RegisterOrUpdateInstance("2", "region", "zone2", ip, true)
	clus.RegisterOrUpdateInstance("3", "region", "zone1", ip, true)

	handler := slogassert.New(t, config.LevelTrace, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(slog.New(handler))

	mockStatus(t, "1", "sync", "2", "sync", "3", "")

	rebalanceCluster(clus)
	handler.AssertMessage("Found sync and primary instance in same region. Initiate switchover...")
	handler.Reset()

	g.Expect(clus.getSwitchoverCount()).To(Equal(int32(1)))
}

func TestRebalanceCluster_podDeletion(t *testing.T) {
	clus := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 4, 2)
	defer DropCluster(clusterNamespace, clusterName)

	// instance 3 is primary, but is in same zone as sync instance 1
	clus.RegisterOrUpdateInstance("1", "region", "zone1", ip, true)
	clus.RegisterOrUpdateInstance("2", "region", "zone2", ip, true)
	clus.RegisterOrUpdateInstance("3", "region", "zone2", ip, true)
	clus.RegisterOrUpdateInstance("4", "region", "zone3", ip, true)

	handler := slogassert.New(t, config.LevelTrace, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(slog.New(handler))

	mockStatus(t, "1", "", "2", "sync", "3", "sync", "4", "potential")

	cli = fake.NewClientBuilder().Build()

	rebalanceCluster(clus)
	handler.AssertMessage(fmt.Sprintf("Found two sync instances in zone zone2. Removing %s-%s...", clusterName, "3"))
	handler.Reset()
}

// TestRebalanceCluster_alreadyBalanced verifies that no action is taken when
// all sync replicas and the primary are in different zones.
func TestRebalanceCluster_alreadyBalanced(t *testing.T) {
	g := NewWithT(t)

	clusters = map[string]PGCluster{}
	clus := &TestCluster{
		Cluster: Cluster{
			namespace:        clusterNamespace,
			name:             clusterName,
			desiredInstances: 3,
			syncNum:          2,
			primary:          "1",
			desiredPrimary:   "1",
			instances:        map[string]*Instance{},
			instancesLock:    sync.RWMutex{},
		},
		shouldSwitchoverFail: false,
	}
	clusters[clusterKey(clusterNamespace, clusterName)] = clus
	defer DropCluster(clusterNamespace, clusterName)

	// Each instance in a different zone → already balanced
	clus.RegisterOrUpdateInstance("1", "region", "zone1", ip, true)
	clus.RegisterOrUpdateInstance("2", "region", "zone2", ip, true)
	clus.RegisterOrUpdateInstance("3", "region", "zone3", ip, true)

	handler := slogassert.New(t, config.LevelTrace, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(slog.New(handler))

	mockStatus(t, "1", "", "2", "sync", "3", "sync")

	rebalanceCluster(clus)
	handler.AssertMessage("Cluster is already balanced over AZs")

	// No switchover should have been triggered
	g.Expect(clus.getSwitchoverCount()).To(Equal(int32(0)))
}

// TestRebalanceCluster_switchoverFails verifies that a switchover error is
// logged gracefully without panicking or corrupting state.
func TestRebalanceCluster_switchoverFails(t *testing.T) {
	clusters = map[string]PGCluster{}
	clus := &TestCluster{
		Cluster: Cluster{
			namespace:        clusterNamespace,
			name:             clusterName,
			desiredInstances: 3,
			syncNum:          2,
			primary:          "3",
			desiredPrimary:   "3",
			instances:        map[string]*Instance{},
			instancesLock:    sync.RWMutex{},
		},
		shouldSwitchoverFail: true, // switchover will return an error
	}
	clusters[clusterKey(clusterNamespace, clusterName)] = clus
	defer DropCluster(clusterNamespace, clusterName)

	// Primary (3) and sync (1) in same zone → triggers switchover
	clus.RegisterOrUpdateInstance("1", "region", "zone1", ip, true)
	clus.RegisterOrUpdateInstance("2", "region", "zone2", ip, true)
	clus.RegisterOrUpdateInstance("3", "region", "zone1", ip, true)

	handler := slogassert.New(t, config.LevelTrace, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(slog.New(handler))

	mockStatus(t, "1", "sync", "2", "sync", "3", "")

	// Should not panic despite the switchover error
	rebalanceCluster(clus)
	handler.AssertMessage("Failed to initiate switchover")
}

// TestRebalanceCluster_quorumState verifies that "quorum" replication state
// is treated as a sync replica, same as "sync".
func TestRebalanceCluster_quorumState(t *testing.T) {
	clus := RegisterOrUpdateCluster(clusterNamespace, clusterName, "1", "1", 3, 2)
	defer DropCluster(clusterNamespace, clusterName)

	// Instance 2 has "quorum" state and shares zone with instance 3 (also quorum)
	clus.RegisterOrUpdateInstance("1", "region", "zone1", ip, true)
	clus.RegisterOrUpdateInstance("2", "region", "zone2", ip, true)
	clus.RegisterOrUpdateInstance("3", "region", "zone2", ip, true)

	handler := slogassert.New(t, config.LevelTrace, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(slog.New(handler))

	// Use "quorum" instead of "sync" — should still trigger pod deletion
	mockStatus(t, "1", "", "2", "quorum", "3", "quorum")

	cli = fake.NewClientBuilder().Build()

	rebalanceCluster(clus)
	handler.AssertMessage(fmt.Sprintf("Found two sync instances in zone zone2. Removing %s-%s...", clusterName, "3"))
}

func TestRebalanceCluster_primaryNotRegistered(t *testing.T) {
	clusters = map[string]PGCluster{}
	clus := &nilPrimaryCluster{
		TestCluster: TestCluster{
			Cluster: Cluster{
				namespace:        clusterNamespace,
				name:             clusterName,
				desiredInstances: 2,
				syncNum:          2,
				primary:          "1", // primary ID exists but instance is NOT registered
				desiredPrimary:   "1",
				instances:        map[string]*Instance{},
				instancesLock:    sync.RWMutex{},
			},
		},
	}
	clusters[clusterKey(clusterNamespace, clusterName)] = clus
	defer DropCluster(clusterNamespace, clusterName)

	clus.RegisterOrUpdateInstance("2", "region", "zone1", ip, true)
	clus.RegisterOrUpdateInstance("3", "region", "zone2", ip, true)

	rebalanceCluster(clus)
}

// TestRebalanceAll verifies that rebalanceAll dispatches rebalancing for
// every registered cluster.
func TestRebalanceAll(t *testing.T) {
	g := NewWithT(t)
	clusters = map[string]PGCluster{}
	defer func() { clusters = map[string]PGCluster{} }()

	// Register two clusters, both fully registered but with no HTTP mock,
	// so both will hit "replication state is unknown" — proving dispatch happened.
	clus1 := RegisterOrUpdateCluster("ns", "cluster1", "1", "1", 2, 1)
	clus1.RegisterOrUpdateInstance("1", "region", "zone1", ip, true)
	clus1.RegisterOrUpdateInstance("2", "region", "zone2", ip, true)

	clus2 := RegisterOrUpdateCluster("ns", "cluster2", "1", "1", 2, 1)
	clus2.RegisterOrUpdateInstance("1", "region", "zone1", ip, true)
	clus2.RegisterOrUpdateInstance("2", "region", "zone2", ip, true)

	handler := slogassert.New(t, config.LevelTrace, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(slog.New(handler))

	rebalanceAll()

	// Give goroutines time to complete
	time.Sleep(100 * time.Millisecond)

	handler.AssertMessage("Start instance-AZ rebalancing...")

	// Each call to AssertMessage consumes one match — two calls verify two dispatches
	handler.AssertMessage("Check sync replica balancing for cluster")
	handler.AssertMessage("Check sync replica balancing for cluster")

	// Verify no third dispatch exists — exactly 2 clusters were processed
	for _, lm := range handler.Unasserted() {
		g.Expect(lm.Message).ToNot(Equal("Check sync replica balancing for cluster"))
	}
}

func TestConcurrent_RebalanceAllAndRegister(t *testing.T) {
	clusters = map[string]PGCluster{}
	defer func() { clusters = map[string]PGCluster{} }()

	done := make(chan struct{})

	// Writer goroutine: continuously registers clusters
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			RegisterOrUpdateCluster("ns", fmt.Sprintf("clus-%d", i), "1", "1", 1, 1)
		}
	}()

	// Reader goroutine: continuously iterates via rebalanceAll
	for i := 0; i < 100; i++ {
		rebalanceAll()
	}

	<-done
}

// nilPrimaryCluster overrides GetReplicationState to return a state map that
// includes the primary ID, simulating the race where PG still reports the
// primary even though it was deregistered from internal state.
type nilPrimaryCluster struct {
	TestCluster
}

func (c *nilPrimaryCluster) GetReplicationState() map[string]string {
	return map[string]string{
		"1": "", // primary — but not registered as an Instance
		"2": "sync",
		"3": "sync",
	}
}

func mockStatus(t *testing.T, values ...string) {
	gock.Intercept()
	httpClient = http.Client{
		Timeout:   time.Duration(1) * time.Second,
		Transport: http.DefaultTransport,
	}
	if len(values)%2 != 0 {
		t.Error("Arguments must be dividable by 2")
	}
	replInfo := make([]replicationInfo, len(values)/2)
	counter := 0
	for i := 0; i < len(values); i = i + 2 {
		replInfo[counter] = replicationInfo{
			ApplicationName: fmt.Sprintf("%s-%s", clusterName, values[i]),
			SyncState:       values[i+1],
		}
		counter++
	}
	gock.New(url).
		Get("").
		Reply(200).
		JSON(pgStatusResponse{
			ReplicationInfo: replInfo,
		})
	t.Cleanup(func() {
		gock.Off()
	})
}
