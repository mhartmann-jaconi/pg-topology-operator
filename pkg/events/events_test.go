package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

var stateMap = make(map[string]string)
var testLock = sync.RWMutex{}

func TestPrimaryRegionChanged_preventParallelRuns(t *testing.T) {
	g := NewWithT(t)

	// Verify that still running listener calls are canceled, if a
	// new event was emited and that a new listener starts up.

	SetListenersForRegionChange(functionWithRetry)
	defer SetListenersForRegionChange()

	getRunForeverState := func() string {
		testLock.Lock()
		defer testLock.Unlock()
		return stateMap["run_forever"]
	}
	getStopImmediatelyState := func() string {
		testLock.Lock()
		defer testLock.Unlock()
		return stateMap["stop_immediately"]
	}

	// emit event and start a listener which will run forever
	PrimaryRegionChanged("run_forever")

	// verify that listener is still running
	g.Eventually(getRunForeverState, 50*time.Millisecond).Should(Equal("running"))

	// emit another event, which must stop the existing listener and start a new one
	PrimaryRegionChanged("stop_immediately")

	// verify that forever-running listener was canceled
	g.Eventually(getRunForeverState, 50*time.Millisecond).Should(Equal("canceled"))
	// verify that second listener just finished
	g.Eventually(getStopImmediatelyState, 50*time.Millisecond).Should(Equal("finished"))
}

// functionWithRetry has a retry logic inside and writes its current
// status into the stateMap. The key in this map is the given
// value string.
// It will stop immediately with state 'finished' if the given value
// is 'stop_immediately'.
// It will stop with state 'canceled' if the context was canceled.
// It will keep running and also write state 'running' in any other cases.
func functionWithRetry(ctx context.Context, value string) {
	mapWrite(value, "started")
	retry.OnError(
		wait.Backoff{
			Duration: 10 * time.Millisecond,
			Factor:   1,
			Steps:    5000,
			Jitter:   0.0,
		},
		func(err error) bool {
			return true
		},
		func() error {
			if ctx.Err() != nil {
				slog.Debug("Primary change function canceled")
				mapWrite(value, "canceled")
				return nil
			}
			if value == "stop_immediately" {
				mapWrite(value, "finished")
				return nil
			}
			mapWrite(value, "running")
			return fmt.Errorf("this will always return an error")
		},
	)
}

func mapWrite(key, value string) {
	testLock.Lock()
	defer testLock.Unlock()
	stateMap[key] = value
}

// TestPrimaryRegionChanged_subscriberPersistsUntilUnsubscribed validates
// that event listeners stay in the internal list until they are removed.
func TestPrimaryRegionChanged_subscriberPersistsUntilUnsubscribed(t *testing.T) {
	g := NewWithT(t)

	// Step 1: Caller-A registers a counter listener and fires an event.
	counterA := atomic.Int32{}
	SetListenersForRegionChange(func(ctx context.Context, s string) {
		counterA.Add(1)
	})

	PrimaryRegionChanged("event-from-A")
	getCounterA := func() int { return int(counterA.Load()) }
	g.Eventually(getCounterA, 50*time.Millisecond).Should(Equal(1))

	// Step 2: Caller-B adds its own counter via AddListenerForRegionChange,
	// without knowing that listener-A is still registered (no cleanup happened).
	counterB := atomic.Int32{}
	AddListenerForRegionChange(func(ctx context.Context, s string) {
		counterB.Add(1)
	})

	// Step 3: Caller-B fires its own event.
	PrimaryRegionChanged("event-from-B")
	getCounterB := func() int { return int(counterB.Load()) }
	g.Eventually(getCounterB, 50*time.Millisecond).Should(Equal(1))

	// Step 4: Verify the stale listener leaked.
	// counterA is now 2 — it received "event-from-B" even though caller-A
	// is no longer active.
	valueA := getCounterA()
	g.Expect(valueA).To(Equal(2), "Stale listener-A received event-from-B")
	t.Logf("counterA: %d (expected 1 if properly cleaned up, got 2 — stale listener active)", valueA)
	t.Logf("counterB: %d (correct)", getCounterB())

	// Cleanup
	SetListenersForRegionChange()
}
