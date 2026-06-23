package eventpipe

// Sink consumes a typed event stream. Sinks are composable via wrappers
// (Filter, Map, FanOut, etc.) forming a middleware chain.
type Sink interface {
	Emit(Event)
}

// FuncSink adapts a function to Sink.
type FuncSink func(Event)

func (f FuncSink) Emit(ev Event) { f(ev) }

// Discard drops every event.
var Discard Sink = FuncSink(func(Event) {})

// --- Middleware: composable Sink wrappers ---

// Filter returns a Sink that only passes events matching the predicate.
func Filter(next Sink, fn func(Event) bool) Sink {
	if next == nil {
		return Discard
	}
	return FuncSink(func(ev Event) {
		if fn(ev) {
			next.Emit(ev)
		}
	})
}

// ByType returns a predicate that matches the given type strings.
// Usage: Filter(sink, ByType("model.delta", "model.final"))
func ByType(types ...string) func(Event) bool {
	set := make(map[string]struct{}, len(types))
	for _, t := range types {
		set[t] = struct{}{}
	}
	return func(ev Event) bool {
		_, ok := set[ev.Type()]
		return ok
	}
}

// SkipType returns a predicate that excludes the given type strings.
func SkipType(types ...string) func(Event) bool {
	set := make(map[string]struct{}, len(types))
	for _, t := range types {
		set[t] = struct{}{}
	}
	return func(ev Event) bool {
		_, ok := set[ev.Type()]
		return !ok
	}
}

// Map transforms each event before passing it to the next sink.
// Returning nil drops the event.
func Map(next Sink, fn func(Event) Event) Sink {
	if next == nil {
		return Discard
	}
	return FuncSink(func(ev Event) {
		if mapped := fn(ev); mapped != nil {
			next.Emit(mapped)
		}
	})
}

// FanOut broadcasts each event to all sinks. If a sink is nil it's skipped.
// All sinks see the same event; there is no transformation.
func FanOut(sinks ...Sink) Sink {
	live := make([]Sink, 0, len(sinks))
	for _, s := range sinks {
		if s != nil {
			live = append(live, s)
		}
	}
	if len(live) == 0 {
		return Discard
	}
	if len(live) == 1 {
		return live[0]
	}
	return FuncSink(func(ev Event) {
		for _, s := range live {
			s.Emit(ev)
		}
	})
}

// Pipe chains sinks left to right: Pipe(a, b, c) emits to a→b→c.
func Pipe(sinks ...Sink) Sink {
	if len(sinks) == 0 {
		return Discard
	}
	// Build the chain from right to left: each sink wraps the next.
	chain := sinks[len(sinks)-1]
	for i := len(sinks) - 2; i >= 0; i-- {
		prev := sinks[i]
		if prev == nil {
			continue
		}
		inner := chain
		chain = FuncSink(func(ev Event) {
			prev.Emit(ev)
			inner.Emit(ev)
		})
	}
	return chain
}

// Tap calls fn as a side-effect, then forwards to next. The event is not
// modified and fn cannot drop it.
func Tap(next Sink, fn func(Event)) Sink {
	if next == nil {
		return Discard
	}
	return FuncSink(func(ev Event) {
		fn(ev)
		next.Emit(ev)
	})
}

// FanOutByType creates a router that sends each event to the matching sink
// based on its Type(). Events whose type doesn't match any key go to fallback
// (which may be nil to drop unmatched).
func FanOutByType(routes map[string]Sink, fallback Sink) Sink {
	return FuncSink(func(ev Event) {
		if s, ok := routes[ev.Type()]; ok && s != nil {
			s.Emit(ev)
		} else if fallback != nil {
			fallback.Emit(ev)
		}
	})
}
