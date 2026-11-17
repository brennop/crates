package events

import (
	"context"
	"testing"
	"time"
)

var _ Sink = (*Bus)(nil)

func TestPublishNeverBlocks(t *testing.T) {
	b := NewBus()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100_000; i++ {
			b.Publish(PlayerState{Position: time.Duration(i)})
			b.Publish(TrackRemoved{})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked with no reader")
	}
}

func TestStateCoalesces(t *testing.T) {
	b := NewBus()
	for i := 1; i <= 50; i++ {
		b.Publish(PlayerState{Position: time.Duration(i)})
		b.Publish(ScanProgress{Scanned: i})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	got := map[string]Event{}
	for i := 0; i < 2; i++ {
		e, err := b.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		switch e.(type) {
		case PlayerState:
			got["player"] = e
		case ScanProgress:
			got["scan"] = e
		}
	}
	if p := got["player"].(PlayerState); p.Position != 50 {
		t.Errorf("player position = %v, want latest (50)", p.Position)
	}
	if s := got["scan"].(ScanProgress); s.Scanned != 50 {
		t.Errorf("scanned = %d, want latest (50)", s.Scanned)
	}

	short, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if e, err := b.Next(short); err == nil {
		t.Fatalf("expected empty bus, got %#v", e)
	}
}

func TestChangeEventsBeforeState(t *testing.T) {
	b := NewBus()
	b.Publish(MusicStatus{})
	b.Publish(LibraryLoaded{Count: 1})
	e, _ := b.Next(context.Background())
	if _, ok := e.(LibraryLoaded); !ok {
		t.Fatalf("first = %T, want LibraryLoaded", e)
	}
	e, _ = b.Next(context.Background())
	if _, ok := e.(MusicStatus); !ok {
		t.Fatalf("second = %T, want MusicStatus", e)
	}
}

func TestChangeEventsAllArriveInOrder(t *testing.T) {
	const n = 10_000
	b := NewBus()
	go func() {
		for i := 0; i < n; i++ {
			b.Publish(LibraryLoaded{Count: i})
			if i%7 == 0 {
				b.Publish(PlayerState{}) // interleaved state must not disturb order
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	want := 0
	for want < n {
		e, err := b.Next(ctx)
		if err != nil {
			t.Fatalf("after %d events: %v", want, err)
		}
		l, ok := e.(LibraryLoaded)
		if !ok {
			continue
		}
		if l.Count != want {
			t.Fatalf("got %d, want %d", l.Count, want)
		}
		want++
	}
}

func TestNextWakesOnPublish(t *testing.T) {
	b := NewBus()
	got := make(chan Event, 1)
	go func() {
		e, _ := b.Next(context.Background())
		got <- e
	}()
	time.Sleep(10 * time.Millisecond)
	b.Publish(PlaylistsChanged{})
	select {
	case e := <-got:
		if _, ok := e.(PlaylistsChanged); !ok {
			t.Fatalf("got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("Next did not wake")
	}
}
