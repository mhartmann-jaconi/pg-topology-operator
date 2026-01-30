package events

import (
	"context"

	"github.com/maniartech/signals"
)

// Event-Stream, notifying that the primary AZ has changed
var PrimaryAZChanged *signals.AsyncSignal[string]

func AddListenerForAZChange(listener func(ctx context.Context, s string)) {
	if PrimaryAZChanged == nil {
		PrimaryAZChanged = signals.New[string]()
	}
	PrimaryAZChanged.AddListener(listener)
}

func SetListenersForAZChange(listeners ...func(ctx context.Context, s string)) {
	PrimaryAZChanged = signals.New[string]()
	for _, l := range listeners {
		AddListenerForAZChange(l)
	}
}
