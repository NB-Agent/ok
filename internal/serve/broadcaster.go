package serve

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/eventpipe"
)

// Broadcaster is the event sink the controller emits to in server mode.
// It marshals each event to JSON and fans it out to every connected SSE
// subscriber. A slow subscriber's buffer drops rather than back-pressure the
// agent goroutine — a browser that can't keep up loses intermediate frames,
// not the whole session (it can refetch /history).
//
// Broadcaster implements both the old event.Sink (via Emit) and the new
// eventpipe.Sink (via EmitTyped). It uses the shared eventpipe.ToWire for
// serialization, replacing the previously duplicated toWire logic.
type Broadcaster struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
	// cache the Eventizer for converting old→typed events
	ez *eventpipe.Eventizer
}

// NewBroadcaster returns an empty Broadcaster ready to accept subscribers.
func NewBroadcaster() *Broadcaster {
	b := &Broadcaster{subs: map[chan []byte]struct{}{}}
	b.ez = eventpipe.NewEventizer(eventpipe.FuncSink(func(ev eventpipe.Event) {
		b.marshalAndFanOut(ev)
	}))
	return b
}

// Emit implements event.Sink (old interface) by converting to typed event
// and broadcasting. This preserves backward compatibility while using the
// shared eventpipe.ToWire format.
func (b *Broadcaster) Emit(e *event.Event) {
	b.ez.Emit(e)
}

// EmitTyped implements eventpipe.Sink by broadcasting the typed event
// directly using the shared eventpipe.ToWire format.
func (b *Broadcaster) EmitTyped(ev eventpipe.Event) {
	b.marshalAndFanOut(ev)
}

func (b *Broadcaster) marshalAndFanOut(ev eventpipe.Event) {
	data, err := json.Marshal(eventpipe.ToWire(ev))
	if err != nil {
		fmt.Fprintf(os.Stderr, "broadcaster: marshal %v event: %v\n", ev.Type(), err)
		return
	}
	b.mu.Lock()
	chans := make([]chan []byte, 0, len(b.subs))
	for ch := range b.subs {
		chans = append(chans, ch)
	}
	b.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- data:
		default:
		}
	}
}

// Subscribe registers a new SSE client and returns its channel plus an
// unsubscribe func the handler must call (defer) when the client disconnects.
func (b *Broadcaster) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

// Subscribers reports the current connection count (for diagnostics/tests).
func (b *Broadcaster) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
