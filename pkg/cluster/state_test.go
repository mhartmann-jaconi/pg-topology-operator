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

	count := 0
	events.AddListenerForAZChange(func(ctx context.Context, s string) { count = count + 1 })

	Store("node1", "east-1", "east", false)
	Store("node2", "east-2", "east", false)
	Store("node3", "west-1", "west", false)
	Store("node4", "west-2", "west", false)
	g.Expect(count).To(Equal(0))

	g.Expect(GetPrimaryZone()).To(BeEmpty())

	Store("node2", "east-2", "east", true)
	g.Expect(GetPrimaryZone()).To(Equal("east-2"))
	g.Expect(count).To(Equal(1))

	Store("node3", "west-1", "west", true)
	g.Expect(GetPrimaryZone()).To(Equal("west-1"))
	g.Expect(count).To(Equal(2))
}

func TestGetPrimaryZone(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	events.AddListenerForAZChange(func(ctx context.Context, s string) {})

	Store("node1", "east-1", "east", false)
	Store("node2", "east-2", "east", false)
	Store("node3", "west-1", "west", false)
	Store("node4", "west-2", "west", false)
	g.Expect(GetPrimaryZone()).To(BeEmpty())

	// make east-2 the primary az
	Store("node2", "east-2", "east", true)
	g.Expect(GetPrimaryZone()).To(Equal("east-2"))

	// drop only node from east-2 and make this AZ unhealthy
	Drop("node2")
	g.Expect(GetPrimaryZone()).To(BeEmpty())
}

func TestGetHealthyZones(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	events.AddListenerForAZChange(func(ctx context.Context, s string) {})

	Store("node1", "east-1", "east", false)
	Store("node2", "east-2", "east", false)
	Store("node3", "east-2", "east", false)

	g.Expect(GetHealthyZones()).To(ContainElements("east-1", "east-2"))

	Drop("node2")
	g.Expect(GetHealthyZones()).To(ContainElements("east-1", "east-2"))

	Drop("node3")
	g.Expect(GetHealthyZones()).To(ContainElements("east-1"))

	Drop("node1")
	g.Expect(GetHealthyZones()).To(BeEmpty())
	g.Expect(nodes).To(BeEmpty())
}

func cleanup() {
	nodes = map[string]node{}
	zones = map[string]Zone{}
	viper.Reset()
}
