// Package events provides an event like system, realised with go channels internally.
// One can register listeners (callbacks) in case an event get's triggered. In this
// case, the callback gets called with a cancelable context. The callback function
// MUST exit when the given context is canceled.
package events

import (
	"context"
	"log/slog"
	"sync"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
)

// RegionChangeListener function gets a context and the name of the new primary
// region as input.
// Note that the given context is cancelable and the function MUST exit if the
// context was canceled.
type RegionChangeListener func(cancelable context.Context, newRegion string)

const EventTypeRegionChanged = "PrimaryRegionChanged"

var (
	parentContext = config.GetParentContext()

	regionChangeChannel   = make(chan string)
	regionChangeListeners = make([]RegionChangeListener, 0)
	regionChangeLock      = sync.RWMutex{}
	regionChangeCtx       = context.WithValue(parentContext, "event", EventTypeRegionChanged)

	initialized = false
)

// AddListenerForRegionChange adds a new event listener which will get notified in
// case the primary region has changed.
func AddListenerForRegionChange(listener RegionChangeListener) {
	regionChangeLock.Lock()
	defer regionChangeLock.Unlock()
	regionChangeListeners = append(regionChangeListeners, listener)
}

// SetListenersForRegionChange overwrites all event listeners for primary region change
// with the given ones.
func SetListenersForRegionChange(newListeners ...RegionChangeListener) {
	regionChangeLock.Lock()
	defer regionChangeLock.Unlock()
	regionChangeListeners = make([]RegionChangeListener, len(newListeners))
	copy(regionChangeListeners, newListeners)
}

// PrimaryRegionChanged notifies all listeners that the primary region has changed.
func PrimaryRegionChanged(newRegion string) {
	regionChangeChannel <- newRegion
}

func initRegionChangeEvents() {
	if initialized {
		return
	}
	initialized = true
	ctx, cancel := context.WithCancel(regionChangeCtx)
	go func() {
		// forever listen to regionChangeChannel until main context is closed
		for {
			select {
			case <-regionChangeCtx.Done():
				// application shutdown
				slog.Info("Closing event channels")
				close(regionChangeChannel)
				return
			case newRegion := <-regionChangeChannel:
				// cancel the old context, so that still running goroutines exit
				cancel()
				// create new context and pass it to new goroutines
				ctx, cancel = context.WithCancel(regionChangeCtx)
				notifyListeners(ctx, newRegion)
			}
		}
	}()
}

func notifyListeners(ctx context.Context, newRegion string) {
	regionChangeLock.RLock()
	defer regionChangeLock.RUnlock()
	for _, listener := range regionChangeListeners {
		go listener(ctx, newRegion)
	}
}

func init() {
	initRegionChangeEvents()
}
