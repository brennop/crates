package events

import (
	"context"
	"sync"
)

// Bus delivers events to its one consumer, the UI. Publish never blocks.
// Change events are queued in order and never dropped; state events keep only
// the latest value per kind.
type Bus struct {
	mu      sync.Mutex
	changes []Event
	state   [numSlots]StateEvent
	wake    chan struct{}
}

func NewBus() *Bus {
	return &Bus{wake: make(chan struct{}, 1)}
}

// Publish implements Sink.
func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	if s, ok := e.(StateEvent); ok {
		b.state[s.slot()] = s
	} else {
		b.changes = append(b.changes, e)
	}
	b.mu.Unlock()
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// Next returns the oldest change event, else a filled state slot. It blocks
// until there is one or ctx is done.
func (b *Bus) Next(ctx context.Context) (Event, error) {
	for {
		if e := b.take(); e != nil {
			return e, nil
		}
		select {
		case <-b.wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (b *Bus) take() Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.changes) > 0 {
		e := b.changes[0]
		b.changes[0] = nil
		b.changes = b.changes[1:]
		if len(b.changes) == 0 {
			b.changes = nil // let the backing array go
		}
		return e
	}
	for i, s := range b.state {
		if s != nil {
			b.state[i] = nil
			return s
		}
	}
	return nil
}
