package postgres

import (
	"context"
	"log/slog"
	"os"
	"testing"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/events"
	. "github.com/onsi/gomega"
	"github.com/spf13/viper"
	"github.com/thejerf/slogassert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const clusterName = "pg"
const clusterNamespace = "default"

var clus *Cluster

func Test_changePrimaryInstances(t *testing.T) {
	g := NewWithT(t)

	defer teardown()

	viper.Set(config.LogLevel, "debug")

	events.SetListenersForRegionChange(func(context.Context, string) {})

	cluster.Store("node1", "dc-01", false)
	cluster.Store("node2", "dc-02", true)

	clus1 := &Cluster{
		name:           clusterName,
		namespace:      clusterNamespace,
		syncNum:        2,
		primary:        "1",
		desiredPrimary: "1",
		instances:      map[string]*Instance{},
	}
	clusterName2 := clusterName + "2"
	clus2 := &Cluster{
		name:           clusterName2,
		namespace:      clusterNamespace,
		syncNum:        2,
		primary:        "1",
		desiredPrimary: "1",
		instances:      map[string]*Instance{},
	}

	clusters = map[string]*Cluster{
		clusterKey(clusterNamespace, clusterName):  clus1,
		clusterKey(clusterNamespace, clusterName2): clus2,
	}

	clus1.RegisterOrUpdateInstance("1", "dc-01", true)
	clus1.RegisterOrUpdateInstance("2", "dc-02", true)
	clus2.RegisterOrUpdateInstance("1", "dc-01", true)
	clus2.RegisterOrUpdateInstance("2", "dc-02", true)

	apiCluster1 := &pgapi.Cluster{
		ObjectMeta: v1.ObjectMeta{
			Name:      clusterName,
			Namespace: clusterNamespace,
		},
		Status: pgapi.ClusterStatus{},
	}
	apiCluster2 := &pgapi.Cluster{
		ObjectMeta: v1.ObjectMeta{
			Name:      clusterName2,
			Namespace: clusterNamespace,
		},
		Status: pgapi.ClusterStatus{},
	}
	scheme, _ := pgapi.SchemeBuilder.Build()
	cli = fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(apiCluster1, apiCluster2).
		WithObjects(apiCluster1, apiCluster2).Build()

	err := changePrimaryInstances(context.Background(), "dc-02")
	g.Expect(err).To(BeNil())
}

func Test_changePrimaryInstances_canceledContext(t *testing.T) {
	g := NewWithT(t)

	defer teardown()

	handler := slogassert.New(t, config.LevelTrace, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(slog.New(handler))

	// create cancelable context to pass into function
	ctx, cancel := context.WithCancel(context.Background())

	// no clusters existing, but function should run through
	err := changePrimaryInstances(ctx, "dc-02")
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Starting update of 0 clusters")
	handler.Reset()

	// cancel context
	cancel()

	// context canceled, exit function without further processing
	err = changePrimaryInstances(ctx, "dc-02")
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Primary change function canceled")
	handler.Reset()
}

func Test_initiateSwitchover(t *testing.T) {
	g := NewWithT(t)

	defer teardown()

	ctx := context.Background()
	viper.Set(config.LogLevel, "debug")

	events.SetListenersForRegionChange(func(context.Context, string) {})

	cluster.Store("node1", "dc-01", false)
	cluster.Store("node2", "dc-02", true)

	clus1 := &Cluster{
		name:           clusterName,
		namespace:      clusterNamespace,
		syncNum:        2,
		primary:        "1",
		desiredPrimary: "1",
		instances:      map[string]*Instance{},
	}
	clusterName2 := clusterName + "2"
	clus2 := &Cluster{
		name:           clusterName2,
		namespace:      clusterNamespace,
		syncNum:        2,
		primary:        "1",
		desiredPrimary: "1",
		instances:      map[string]*Instance{},
	}

	clusters = map[string]*Cluster{
		clusterKey(clusterNamespace, clusterName):  clus1,
		clusterKey(clusterNamespace, clusterName2): clus2,
	}

	clus1.RegisterOrUpdateInstance("1", "dc-01", true)
	clus1.RegisterOrUpdateInstance("2", "dc-02", false)
	clus2.RegisterOrUpdateInstance("1", "dc-01", true)
	clus2.RegisterOrUpdateInstance("2", "dc-02", true)

	apiCluster1 := &pgapi.Cluster{
		ObjectMeta: v1.ObjectMeta{
			Name:      clusterName,
			Namespace: clusterNamespace,
		},
		Status: pgapi.ClusterStatus{},
	}
	apiCluster2 := &pgapi.Cluster{
		ObjectMeta: v1.ObjectMeta{
			Name:      clusterName2,
			Namespace: clusterNamespace,
		},
		Status: pgapi.ClusterStatus{},
	}
	scheme, _ := pgapi.SchemeBuilder.Build()
	cli = fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(apiCluster1, apiCluster2).
		WithObjects(apiCluster1, apiCluster2).Build()

	err := clus2.initiateSwitchover(ctx)
	g.Expect(err).To(BeNil())
	err = clus1.initiateSwitchover(ctx)
	g.Expect(err).NotTo(BeNil())
	g.Expect(err.Error()).To(ContainSubstring("no eligible target instance found to be promoted to primary"))
}

func Test_isEligibleForID(t *testing.T) {
	g := NewWithT(t)

	setup()

	defer teardown()

	inst1 := clus.RegisterOrUpdateInstance("1", "dc-01", true)
	inst2 := clus.RegisterOrUpdateInstance("2", "dc-01", true)
	inst3 := clus.RegisterOrUpdateInstance("3", "dc-02", true)
	inst4 := clus.RegisterOrUpdateInstance("4", "dc-01", false)
	inst5 := clus.RegisterOrUpdateInstance("5", "dc-02", false)
	inst6 := clus.RegisterOrUpdateInstance("6", "dc-01", true)
	clus.desiredPrimary = "1"
	clus.primary = "1"

	// current primary
	val, _ := isEligible(inst1, false)
	g.Expect(val).To(BeTrue())

	// other sync instance in same region
	val, _ = isEligible(inst2, false)
	g.Expect(val).To(BeTrue())

	// non-sync instance in same region, but don't check for sync status
	val, _ = isEligible(inst6, false)
	g.Expect(val).To(BeTrue())

	// non-sync instance in same region, but sync status must be checked
	val, _ = isEligible(inst6, true)
	g.Expect(val).To(BeFalse())

	// unhealthy instance in same region
	val, _ = isEligible(inst4, false)
	g.Expect(val).To(BeFalse())

	// sync instance in different region
	val, _ = isEligible(inst3, false)
	g.Expect(val).To(BeFalse())

	// unhealthy instance in different region
	val, _ = isEligible(inst5, false)
	g.Expect(val).To(BeFalse())
}

// ----- HELPERS ------

func setup() {
	viper.Set(config.LogLevel, "debug")
	viper.Set(config.LabelRegion, "topology.kubernetes.io/region")

	events.SetListenersForRegionChange(func(context.Context, string) {})

	cluster.Store("node1", "dc-01", true)
	cluster.Store("node2", "dc-01", true)
	cluster.Store("node3", "dc-02", false)

	clus = &Cluster{
		name:      clusterName,
		namespace: clusterNamespace,
		syncNum:   2,
		instances: map[string]*Instance{},
	}
}

func teardown() {
	viper.Reset()
	cluster.ClearState()
	clusters = map[string]*Cluster{}
}
