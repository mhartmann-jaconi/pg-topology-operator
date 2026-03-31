package cluster

import (
	"context"
	"testing"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/events"
	. "github.com/onsi/gomega"
	"github.com/spf13/viper"
)

func TestStore(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	eventCounter := 0
	events.AddListenerForRegionChange(func(ctx context.Context, s string) { eventCounter = eventCounter + 1 })

	Store("node1", "dc-01", false)
	Store("node2", "dc-02", false)
	g.Expect(eventCounter).To(Equal(0))

	g.Expect(GetPrimaryRegion()).To(BeEmpty())

	Store("node1", "dc-01", true)
	g.Expect(GetPrimaryRegion()).To(Equal("dc-01"))
	g.Expect(eventCounter).To(Equal(1))

	Store("node2", "dc-02", true)
	g.Expect(GetPrimaryRegion()).To(Equal("dc-02"))
	g.Expect(eventCounter).To(Equal(2))
}

func TestGetPrimaryRegion(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	events.AddListenerForRegionChange(func(ctx context.Context, s string) {})

	Store("node1", "dc-01", false)
	Store("node2", "dc-02", false)

	g.Expect(GetPrimaryRegion()).To(BeEmpty())

	// make dc-02 the primary region
	Store("node2", "dc-02", true)
	g.Expect(GetPrimaryRegion()).To(Equal("dc-02"))
}

func cleanup() {
	nodes = map[string]string{}
	regions = map[string]region{}
	viper.Reset()
}
