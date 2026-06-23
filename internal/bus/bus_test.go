package bus

import (
	"sync"
	"testing"
)

func TestPubSub(t *testing.T) {
	b := New()
	var got string
	stop := b.Sub("test", func(topic string, msg Msg) {
		got = msg.(string)
	})
	defer stop()

	b.Pub("test", "hello")
	if got != "hello" {
		t.Fatalf("expected 'hello', got %q", got)
	}
}

func TestMultipleSubscribers(t *testing.T) {
	b := New()
	var mu sync.Mutex
	var msgs []string
	b.Sub("t", func(_ string, m Msg) {
		mu.Lock()
		msgs = append(msgs, "a:"+m.(string))
		mu.Unlock()
	})
	b.Sub("t", func(_ string, m Msg) {
		mu.Lock()
		msgs = append(msgs, "b:"+m.(string))
		mu.Unlock()
	})
	b.Pub("t", "x")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d: %v", len(msgs), msgs)
	}
}

func TestNoSubscribers(t *testing.T) {
	b := New()
	b.Pub("nonexistent", "data")
}

func TestUnsubscribe(t *testing.T) {
	b := New()
	count := 0
	stop := b.Sub("t", func(_ string, _ Msg) { count++ })
	b.Pub("t", 1)
	stop()
	b.Pub("t", 2)
	if count != 1 {
		t.Fatalf("expected 1 delivery, got %d", count)
	}
}

func TestUnsubscribeDuringDelivery(t *testing.T) {
	b := New()
	var stop1 func()
	count := 0
	stop1 = b.Sub("t", func(_ string, _ Msg) {
		count++
		stop1()
	})
	b.Pub("t", 1)
	b.Pub("t", 2)
	if count != 1 {
		t.Fatalf("expected 1 delivery, got %d", count)
	}
}

func TestPanicInSubscriber(t *testing.T) {
	b := New()
	ok := false
	b.Sub("t", func(_ string, _ Msg) { panic("boom") })
	b.Sub("t", func(_ string, _ Msg) { ok = true })
	b.Pub("t", "x")
	if !ok {
		t.Fatal("second subscriber was not called after panic in first")
	}
}

func TestTopics(t *testing.T) {
	b := New()
	stop := b.Sub("t1", func(_ string, _ Msg) {})
	defer stop()
	b.Sub("t2", func(_ string, _ Msg) {})
	topics := b.Topics()
	if len(topics) != 2 {
		t.Fatalf("expected 2 topics, got %d: %v", len(topics), topics)
	}
}

func TestSubscriberCount(t *testing.T) {
	b := New()
	s1 := b.Sub("t1", func(_ string, _ Msg) {})
	s2 := b.Sub("t1", func(_ string, _ Msg) {})
	b.Sub("t2", func(_ string, _ Msg) {})
	defer s1()
	defer s2()
	if n := b.SubscriberCount(); n != 3 {
		t.Fatalf("expected 3 subscribers, got %d", n)
	}
}

func TestConcurrentPubSub(t *testing.T) {
	b := New()
	var mu sync.Mutex
	count := 0
	b.Sub("t", func(_ string, _ Msg) {
		mu.Lock()
		count++
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Pub("t", nil)
		}()
	}
	wg.Wait()
	if count != 100 {
		t.Fatalf("expected 100 deliveries, got %d", count)
	}
}

func TestStopIdempotent(t *testing.T) {
	b := New()
	count := 0
	stop := b.Sub("t", func(_ string, _ Msg) { count++ })
	b.Pub("t", nil)
	stop()
	stop()
	stop()
	if count != 1 {
		t.Fatalf("expected 1 delivery, got %d", count)
	}
	if b.SubscriberCount() != 0 {
		t.Fatalf("expected 0 subscribers after stop, got %d", b.SubscriberCount())
	}
}
