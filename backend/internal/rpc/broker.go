package rpc

import (
	"log/slog"
	"sync"
	"sync/atomic"

	"mailand/backend/internal/rpc/pb"
)

const subscriberBuffer = 256

// Broker fans daemon events out to all SubscribeEvents streams. Publish never
// blocks: a subscriber whose buffer is full loses the event (and it is counted).
type Broker struct {
	mu      sync.Mutex
	subs    map[*subscriber]struct{}
	closed  bool
	dropped atomic.Uint64
	log     *slog.Logger
}

type subscriber struct{ ch chan *pb.DaemonEvent }

// NewBroker returns an empty broker.
func NewBroker() *Broker {
	return &Broker{subs: map[*subscriber]struct{}{}, log: slog.Default()}
}

// Publish delivers ev to every subscriber without blocking.
func (b *Broker) Publish(ev *pb.DaemonEvent) {
	if ev == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for s := range b.subs {
		select {
		case s.ch <- ev:
		default:
			if n := b.dropped.Add(1); n == 1 || n%100 == 0 {
				b.log.Warn("event subscriber too slow, dropping events", "dropped_total", n)
			}
		}
	}
}

// Dropped returns the total number of events dropped for slow subscribers.
func (b *Broker) Dropped() uint64 { return b.dropped.Load() }

// Subscribe returns a channel of events and a cancel func (idempotent). The
// channel is closed on cancel or when the broker is closed.
func (b *Broker) Subscribe() (<-chan *pb.DaemonEvent, func()) {
	s := &subscriber{ch: make(chan *pb.DaemonEvent, subscriberBuffer)}
	b.mu.Lock()
	if b.closed {
		close(s.ch)
		b.mu.Unlock()
		return s.ch, func() {}
	}
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s.ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[s]; ok {
			delete(b.subs, s)
			close(s.ch)
		}
	}
}

// Close closes all subscriber channels; later Publish calls are no-ops.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for s := range b.subs {
		close(s.ch)
	}
	b.subs = map[*subscriber]struct{}{}
}
