// Package events notifies live subscribers about committed hub events and
// carries transient, never-persisted updates such as streaming text.
//
// Committed events are the source of truth: subscribers are woken by Notify
// and then read the events table from their cursor, so a slow or reconnecting
// client never depends on having received every in-memory signal.
package events

import (
	"encoding/json"
	"sync"
	"sync/atomic"
)

// Transient is an ephemeral update (e.g. coalesced token deltas). It has no
// sequence and is never replayed; clients rely on canonical snapshots.
type Transient struct {
	Type       string          `json:"type"`
	RoomID     string          `json:"roomId,omitempty"`
	ThreadID   string          `json:"threadId,omitempty"`
	JobID      string          `json:"jobId,omitempty"`
	RunID      string          `json:"runId,omitempty"`
	EngineerID string          `json:"engineerId,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

// Bus fans out commit notifications and transient updates.
type Bus struct {
	mu      sync.Mutex
	changed chan struct{}
	subs    map[*Subscription]struct{}
}

func NewBus() *Bus {
	return &Bus{changed: make(chan struct{}), subs: map[*Subscription]struct{}{}}
}

// Notify wakes everyone waiting on Changed.
func (b *Bus) Notify() {
	b.mu.Lock()
	close(b.changed)
	b.changed = make(chan struct{})
	b.mu.Unlock()
}

// Changed returns a channel closed at the next Notify.
func (b *Bus) Changed() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.changed
}

// Subscription receives transient updates through a bounded buffer. When a
// subscriber falls behind, it is marked slow and should be disconnected with
// a recoverable reason rather than blocking other clients.
type Subscription struct {
	C    chan Transient
	slow atomic.Bool
	bus  *Bus
}

func (b *Bus) Subscribe(buffer int) *Subscription {
	s := &Subscription{C: make(chan Transient, buffer), bus: b}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

func (s *Subscription) Close() {
	s.bus.mu.Lock()
	delete(s.bus.subs, s)
	s.bus.mu.Unlock()
}

// Slow reports whether the subscriber dropped updates.
func (s *Subscription) Slow() bool { return s.slow.Load() }

// Publish delivers a transient update without blocking.
func (b *Bus) Publish(t Transient) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		select {
		case s.C <- t:
		default:
			s.slow.Store(true)
		}
	}
}

// Subscribers returns the number of live subscriptions.
func (b *Bus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
