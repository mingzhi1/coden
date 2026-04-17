package events

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

// ringCap is the number of recent events retained for since_seq replay (R-01).
// A power-of-two keeps the modulo cheap.
const ringCap = 256

type Bus struct {
	seq         uint64
	mu          sync.Mutex
	subscribers map[uint64]subscriber
	nextID      uint64
	// ring buffer — guarded by mu
	ring      [ringCap]model.Event
	ringHead  int // index of the oldest slot
	ringCount int // number of valid entries (0..ringCap)
}

type subscriber struct {
	sessionID string
	ch        chan model.Event
}

func NewBus() *Bus {
	return &Bus{
		subscribers: make(map[uint64]subscriber),
	}
}

// Seq returns the sequence number of the most recently emitted event.
// Zero means no events have been emitted yet.
func (b *Bus) Seq() uint64 {
	return atomic.LoadUint64(&b.seq)
}

func (b *Bus) Subscribe(sessionID string) (<-chan model.Event, func()) {
	ch := make(chan model.Event, 128)

	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subscribers[id] = subscriber{
		sessionID: sessionID,
		ch:        ch,
	}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		sub, ok := b.subscribers[id]
		if ok {
			delete(b.subscribers, id)
			close(sub.ch)
		}
		b.mu.Unlock()
	}

	return ch, cancel
}

func (b *Bus) Emit(sessionID, topic string, payload any) model.Event {
	event := model.Event{
		Seq:       atomic.AddUint64(&b.seq, 1),
		SessionID: sessionID,
		Topic:     topic,
		Timestamp: time.Now(),
		Payload:   model.EncodePayload(payload),
	}

	b.mu.Lock()
	// Write to ring buffer (N-09).
	idx := (b.ringHead + b.ringCount) % ringCap
	b.ring[idx] = event
	if b.ringCount < ringCap {
		b.ringCount++
	} else {
		b.ringHead = (b.ringHead + 1) % ringCap
	}
	// Forward to live subscribers.
	for _, sub := range b.subscribers {
		if sub.sessionID != "" && sub.sessionID != sessionID {
			continue
		}
		select {
		case sub.ch <- event:
		default:
		}
	}
	b.mu.Unlock()

	return event
}

// Close closes all subscriber channels and removes them.
// Safe to call multiple times; subsequent cancel funcs returned by Subscribe
// will be no-ops because the subscriber entry is already deleted.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, sub := range b.subscribers {
		close(sub.ch)
		delete(b.subscribers, id)
	}
}

// SubscribeSince is like Subscribe but also replays all buffered events whose
// Seq is greater than sinceSeq. The subscription is registered before the
// replay so that no event can be missed between the replayed history and the
// start of live delivery.
//
// Use sinceSeq=0 to replay the entire retained buffer.
// Use sinceSeq equal to the value returned by Seq() (or SessionSnapshot.LastEventSeq)
// to start receiving only future events with no replay.
func (b *Bus) SubscribeSince(sessionID string, sinceSeq uint64) (<-chan model.Event, func()) {
	// Allocate a channel large enough to absorb the ring buffer replay burst
	// without blocking the caller, plus room for live events.
	ch := make(chan model.Event, ringCap+128)

	b.mu.Lock()
	// Register subscriber first so live events are captured immediately.
	id := b.nextID
	b.nextID++
	b.subscribers[id] = subscriber{sessionID: sessionID, ch: ch}

	// Replay buffered events (oldest → newest).
	for i := 0; i < b.ringCount; i++ {
		ev := b.ring[(b.ringHead+i)%ringCap]
		if ev.Seq <= sinceSeq {
			continue
		}
		if sessionID != "" && ev.SessionID != "" && ev.SessionID != sessionID {
			continue
		}
		select {
		case ch <- ev:
		default: // channel full — skip (shouldn't happen with ringCap+128 capacity)
		}
	}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		sub, ok := b.subscribers[id]
		if ok {
			delete(b.subscribers, id)
			close(sub.ch)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}
