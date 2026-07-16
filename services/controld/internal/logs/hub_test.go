package logs

import (
	"testing"

	"github.com/avishuklacode/gantry/services/controld/internal/store"
)

// Two subscribers on the same broker both see every published value, in order.
func TestBrokerFanoutOrdered(t *testing.T) {
	b := newBroker[store.LogLine]()
	s1 := b.subscribe(16)
	s2 := b.subscribe(16)

	const n = 10
	for i := 1; i <= n; i++ {
		b.publish(store.LogLine{Seq: int64(i)})
	}

	for _, s := range []*sub[store.LogLine]{s1, s2} {
		for i := 1; i <= n; i++ {
			select {
			case ll := <-s.C:
				if ll.Seq != int64(i) {
					t.Fatalf("out of order: got seq %d want %d", ll.Seq, i)
				}
			default:
				t.Fatalf("missing value seq %d", i)
			}
		}
	}
}

// A full subscriber is dropped on the next publish and its Dropped channel closes;
// other subscribers keep flowing (one slow reader can't stall the producer).
func TestBrokerOverflowDropsSlowSubscriber(t *testing.T) {
	b := newBroker[store.LogLine]()
	slow := b.subscribe(2) // tiny buffer
	fast := b.subscribe(64)

	// Overfill the slow subscriber: 2 fit, the 3rd overflows and drops it.
	for i := 1; i <= 3; i++ {
		b.publish(store.LogLine{Seq: int64(i)})
	}

	select {
	case <-slow.dropped:
		// expected
	default:
		t.Fatal("slow subscriber should have been dropped on overflow")
	}

	// The fast subscriber is unaffected and got all three.
	if got := len(fast.C); got != 3 {
		t.Fatalf("fast subscriber got %d values, want 3", got)
	}

	// Further publishes never reach the dropped subscriber's (now unread) buffer
	// beyond what already fit, and don't panic.
	b.publish(store.LogLine{Seq: 4})
	if got := len(fast.C); got != 4 {
		t.Fatalf("fast subscriber got %d values after extra publish, want 4", got)
	}
}

// Unsubscribing stops delivery and lets the broker be considered empty.
func TestBrokerUnsubscribe(t *testing.T) {
	b := newBroker[store.LogLine]()
	s := b.subscribe(4)
	if b.len() != 1 {
		t.Fatalf("len = %d, want 1", b.len())
	}
	b.unsubscribe(s)
	if b.len() != 0 {
		t.Fatalf("len after unsubscribe = %d, want 0", b.len())
	}
	// Publish after unsubscribe must not deliver.
	b.publish(store.LogLine{Seq: 1})
	if got := len(s.C); got != 0 {
		t.Fatalf("unsubscribed channel got %d values, want 0", got)
	}
}
