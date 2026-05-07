package cluster

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/events"
	. "github.com/onsi/gomega"
)

func TestStore(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	var eventCounter = atomic.Int32{}
	events.SetListenersForRegionChange(func(ctx context.Context, s string) {
		eventCounter.Add(1)
	})
	getEventCounter := func() int32 { return eventCounter.Load() }

	Store("node1", "dc-01", "dc-01-01", false)
	Store("node2", "dc-02", "dc-02-01", false)

	g.Expect(GetPrimaryRegion()).To(BeEmpty())
	g.Eventually(getEventCounter, 20*time.Millisecond).Should(Equal(int32(0)))

	Store("node1", "dc-01", "dc-01-01", true)
	g.Expect(GetPrimaryRegion()).To(Equal("dc-01"))
	g.Eventually(getEventCounter, 20*time.Millisecond).Should(Equal(int32(1)))

	Store("node2", "dc-02", "dc-02-01", true)
	g.Expect(GetPrimaryRegion()).To(Equal("dc-02"))
	g.Eventually(getEventCounter, 20*time.Millisecond).Should(Equal(int32(2)))
}

func TestGetPrimaryRegion(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	events.AddListenerForRegionChange(func(ctx context.Context, s string) {})

	Store("node1", "dc-01", "dc-01-01", false)
	Store("node2", "dc-02", "dc-02-01", false)

	g.Expect(GetPrimaryRegion()).To(BeEmpty())

	// make dc-02 the primary region
	Store("node2", "dc-02", "dc-02-01", true)
	g.Expect(GetPrimaryRegion()).To(Equal("dc-02"))
}

// TestClearState verifies that ClearState removes all stored nodes and regions,
// resetting the internal state to its initial empty condition.
func TestClearState(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	events.AddListenerForRegionChange(func(ctx context.Context, s string) {})

	// Populate state with nodes and a leader region
	Store("node1", "dc-01", "dc-01-01", true)
	Store("node2", "dc-02", "dc-02-01", false)
	g.Expect(GetPrimaryRegion()).To(Equal("dc-01"))
	g.Expect(GetRegionOfNode("node1")).To(Equal("dc-01"))
	g.Expect(GetRegionOfNode("node2")).To(Equal("dc-02"))

	// Clear everything
	ClearState()

	// All state should be empty
	g.Expect(GetPrimaryRegion()).To(BeEmpty())
	g.Expect(GetRegionOfNode("node1")).To(BeEmpty())
	g.Expect(GetRegionOfNode("node2")).To(BeEmpty())
}

// TestGetZoneOfNode verifies that Store() persists the zone name for each
// node and that GetZoneOfNode() retrieves it correctly.
func TestGetZoneOfNode(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	// Store nodes with distinct zones
	Store("node1", "dc-01", "dc-01-az-a", false)
	Store("node2", "dc-01", "dc-01-az-b", false)

	g.Expect(GetZoneOfNode("node1")).To(Equal("dc-01-az-a"))
	g.Expect(GetZoneOfNode("node2")).To(Equal("dc-01-az-b"))

	// Updating a node should overwrite its zone
	Store("node1", "dc-01", "dc-01-az-c", false)
	g.Expect(GetZoneOfNode("node1")).To(Equal("dc-01-az-c"))

	// Unknown node should return empty string (zero value)
	g.Expect(GetZoneOfNode("unknown")).To(BeEmpty())
}

// TestGetZoneCount verifies that GetZoneCount() returns the number of
// distinct availability zones across all stored nodes.
func TestGetZoneCount(t *testing.T) {
	g := NewWithT(t)
	defer cleanup()

	// No nodes stored → zero zones
	g.Expect(GetZoneCount()).To(Equal(0))

	// Two nodes in the same zone → one unique zone
	Store("node1", "dc-01", "az-a", false)
	Store("node2", "dc-01", "az-a", false)
	g.Expect(GetZoneCount()).To(Equal(1))

	// Third node in a different zone → two unique zones
	Store("node3", "dc-02", "az-b", false)
	g.Expect(GetZoneCount()).To(Equal(2))

	// Fourth node in yet another zone → three unique zones
	Store("node4", "dc-02", "az-c", false)
	g.Expect(GetZoneCount()).To(Equal(3))
}
