package healthcheck

import (
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// Watchdog stores information about last lease reconcilation
type Watchdog struct {
	namespace string
	lastSeen  atomic.Int64
}

func (w *Watchdog) HealthzCheck(req *http.Request) error {
	return w.Healthy(livenessMaxAge)
}

func (w *Watchdog) MarkSeen(now time.Time) {
	w.lastSeen.Store(now.UnixNano())
}

func (w *Watchdog) Healthy(maxAge time.Duration) error {
	lastSeenNanos := w.lastSeen.Load()
	if lastSeenNanos == 0 {
		return errors.New("watchdog has not yet observed a lease event")
	}

	lastSeen := time.Unix(0, lastSeenNanos)
	age := time.Since(lastSeen)
	if age > maxAge {
		return fmt.Errorf("watchdog last observed lease event %s ago", age.Round(time.Second))
	}

	return nil
}
