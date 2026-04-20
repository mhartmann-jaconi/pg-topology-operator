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
	"github.com/thejerf/slogassert"

	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var webhook = defaulter{}

func TestDefault(t *testing.T) {

	g := NewWithT(t)
	ctx := context.TODO()

	handler := slogassert.New(t, config.LevelTrace, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(slog.New(handler))

	events.SetListenersForRegionChange(func(ctx context.Context, s string) {})

	// not a cluster object
	pod := &v1.Pod{}
	err := webhook.Default(ctx, pod)
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Failed to call cluster webhook")
	handler.Reset()

	clus := &pgapi.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "clus",
			Namespace: "default",
		},
		Status: pgapi.ClusterStatus{},
	}
	// no desired target set
	err = webhook.Default(ctx, clus)
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Current primary not set yet for cluster")
	handler.Reset()

	// current == desired
	clus.Status.CurrentPrimary = "clus-1"
	clus.Status.TargetPrimary = "clus-1"
	err = webhook.Default(ctx, clus)
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Current primary is already target primary on cluster")
	handler.Reset()

	// no cluster internally registered
	clus.Status.TargetPrimary = "clus-2"
	err = webhook.Default(ctx, clus)
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Cluster is not registered yet")
	handler.Reset()

	internalCluster := RegisterOrUpdateCluster("default", "clus", "1", "1", 3, 2)

	// instance not registered
	err = webhook.Default(ctx, clus)
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Instance clus-2 not registered for cluster")
	handler.Reset()

	internalCluster.RegisterOrUpdateInstance("1", "region1", true)
	internalCluster.RegisterOrUpdateInstance("2", "region1", true)
	internalCluster.RegisterOrUpdateInstance("3", "region2", true)
	cluster.Store("x", "region1", true)
	cluster.Store("y", "region2", false)

	// instance already eligible
	err = webhook.Default(ctx, clus)
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Desired primary clus-2 is already eligible primary")
	handler.Reset()

	cluster.Store("z", "region3", true)

	// no eligible instance found
	err = webhook.Default(ctx, clus)
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Intercept primary election to not use clus-2: not in primary region")
	handler.AssertMessage("No eligible target instance found to be promoted to primary")
	handler.Reset()

	cluster.Store("y", "region2", true)

	// change desired primary to clus-3
	err = webhook.Default(ctx, clus)
	g.Expect(err).To(BeNil())
	handler.AssertMessage("Intercept primary election to not use clus-2: not in primary region")
	handler.AssertMessage("Update cluster desired primary to clus-3")
	handler.Reset()
	g.Expect(clus.Status.TargetPrimary).To(Equal("clus-3"))
}
