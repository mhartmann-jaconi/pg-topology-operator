package events

import (
	"context"

	"github.com/maniartech/signals"
)

// Event-Stream, notifying that the primary region has changed
var PrimaryRegionChanged *signals.AsyncSignal[string]

func AddListenerForRegionChange(listener func(ctx context.Context, s string)) {
	if PrimaryRegionChanged == nil {
		PrimaryRegionChanged = signals.New[string]()
	}
	PrimaryRegionChanged.AddListener(listener)
}

func SetListenersForRegionChange(listeners ...func(ctx context.Context, s string)) {
	PrimaryRegionChanged = signals.New[string]()
	for _, l := range listeners {
		AddListenerForRegionChange(l)
	}
}
