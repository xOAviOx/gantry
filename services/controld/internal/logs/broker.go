package logs

import "sync"

// broker is a tiny fan-out primitive: many subscribers, non-blocking publish.
// A subscriber whose buffer is full is considered "slow": it is removed and its
// Dropped() channel is closed so the reader can react (SPEC.md §10 sends a `gap`
// event and disconnects the client). Zero external dependencies, safe for
// concurrent use.
type broker[T any] struct {
	mu   sync.Mutex
	subs map[*sub[T]]struct{}
}

func newBroker[T any]() *broker[T] {
	return &broker[T]{subs: make(map[*sub[T]]struct{})}
}

// sub is one subscription. C delivers values in publish order; dropped is closed
// exactly once if the subscriber overflows or is torn down by the broker.
type sub[T any] struct {
	C       chan T
	dropped chan struct{}
	once    sync.Once
}

func (s *sub[T]) markDropped() { s.once.Do(func() { close(s.dropped) }) }

// subscribe registers a subscriber with a buffered channel of the given size.
func (b *broker[T]) subscribe(buf int) *sub[T] {
	s := &sub[T]{C: make(chan T, buf), dropped: make(chan struct{})}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// unsubscribe removes a subscriber. Idempotent.
func (b *broker[T]) unsubscribe(s *sub[T]) {
	b.mu.Lock()
	delete(b.subs, s)
	b.mu.Unlock()
}

// publish delivers v to every subscriber without blocking. Any subscriber whose
// buffer is full is dropped (removed + signaled) so one slow reader can never
// stall the producer or other readers.
func (b *broker[T]) publish(v T) {
	b.mu.Lock()
	var drop []*sub[T]
	for s := range b.subs {
		select {
		case s.C <- v:
		default:
			drop = append(drop, s)
		}
	}
	for _, s := range drop {
		delete(b.subs, s)
		s.markDropped()
	}
	b.mu.Unlock()
}

// len reports the current subscriber count (used for broker GC).
func (b *broker[T]) len() int {
	b.mu.Lock()
	n := len(b.subs)
	b.mu.Unlock()
	return n
}
