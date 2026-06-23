// Package bus is a lightweight publish-subscribe message bus. It decouples
// components by letting them communicate through typed topics instead of direct
// method calls. Thread-safe; zero allocations on the publish path when there are
// no subscribers for a topic.
package bus

import (
	"log"
	"sync"
)

type Msg = any
type Handler func(topic string, msg Msg)

type subEntry struct {
	id uint64
	fn Handler
}

type Bus struct {
	mu     sync.RWMutex
	subs   map[string][]subEntry
	nextID uint64
}

func New() *Bus {
	return &Bus{subs: map[string][]subEntry{}}
}

func (b *Bus) Pub(topic string, msg Msg) {
	b.mu.RLock()
	entries := b.subs[topic]
	handlers := make([]Handler, len(entries))
	for i, e := range entries {
		handlers[i] = e.fn
	}
	b.mu.RUnlock()

	for _, fn := range handlers {
		safeCall(fn, topic, msg)
	}
}

func (b *Bus) Sub(topic string, fn Handler) (stop func()) {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subs[topic] = append(b.subs[topic], subEntry{id: id, fn: fn})
	b.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			entries := b.subs[topic]
			for i, e := range entries {
				if e.id == id {
					b.subs[topic] = append(entries[:i], entries[i+1:]...)
					break
				}
			}
			if len(b.subs[topic]) == 0 {
				delete(b.subs, topic)
			}
			b.mu.Unlock()
		})
	}
}

func (b *Bus) Topics() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, 0, len(b.subs))
	for t := range b.subs {
		out = append(out, t)
	}
	return out
}

func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := 0
	for _, es := range b.subs {
		n += len(es)
	}
	return n
}

func safeCall(fn Handler, topic string, msg Msg) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("bus: panic in handler for %q (swallowed): %v", topic, r)
		}
	}()
	fn(topic, msg)
}
