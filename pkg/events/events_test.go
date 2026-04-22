package events

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

var stateMap = make(map[string]string)

func TestPrimaryRegionChanged_preventParallelRuns(t *testing.T) {
	g := NewWithT(t)

	// Verify that still running listener calls are canceled, if a
	// new event was emited and that a new listener starts up.

	SetListenersForRegionChange(functionWithRetry)
	defer SetListenersForRegionChange()

	getRunForeverState := func() string { return stateMap["run_forever"] }
	getStopImmediatelyState := func() string { return stateMap["stop_immediately"] }

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
	stateMap[value] = "started"
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
				stateMap[value] = "canceled"
				return nil
			}
			if value == "stop_immediately" {
				stateMap[value] = "finished"
				return nil
			}
			stateMap[value] = "running"
			return fmt.Errorf("this will always return an error")
		},
	)
}
