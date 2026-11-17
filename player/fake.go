package player

import (
	"context"
	"sync"
	"time"

	"github.com/brennop/crates/events"
)

// Call is one method call recorded by Fake.
type Call struct {
	Op     string // "load", "pause", "resume", "stop", "seek", "volume", "close"
	Path   string // load
	Pos    time.Duration
	Volume float64
}

// Fake is an in-memory Player for app tests. It records calls, returns the
// errors scripted with Fail, and publishes whatever Emit is given.
type Fake struct {
	sink events.Sink

	mu    sync.Mutex
	calls []Call
	fail  map[string]error
}

var (
	_ Player = (*Fake)(nil)
	_ Player = (*MPV)(nil)
)

func NewFake(sink events.Sink) *Fake {
	return &Fake{sink: sink, fail: map[string]error{}}
}

// Emit publishes e to the sink as the player would.
func (f *Fake) Emit(e events.Event) { f.sink.Publish(e) }

// Fail makes every later call of op return err; nil clears it.
func (f *Fake) Fail(op string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.fail, op)
	} else {
		f.fail[op] = err
	}
}

// Calls returns the calls so far, oldest first.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

func (f *Fake) record(c Call) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return f.fail[c.Op]
}

func (f *Fake) Load(_ context.Context, path string, start time.Duration) error {
	return f.record(Call{Op: "load", Path: path, Pos: start})
}
func (f *Fake) Pause(context.Context) error  { return f.record(Call{Op: "pause"}) }
func (f *Fake) Resume(context.Context) error { return f.record(Call{Op: "resume"}) }
func (f *Fake) Stop(context.Context) error   { return f.record(Call{Op: "stop"}) }
func (f *Fake) Seek(_ context.Context, pos time.Duration) error {
	return f.record(Call{Op: "seek", Pos: pos})
}
func (f *Fake) SetVolume(_ context.Context, v float64) error {
	return f.record(Call{Op: "volume", Volume: clampVolume(v)})
}
func (f *Fake) Close() error { return f.record(Call{Op: "close"}) }
