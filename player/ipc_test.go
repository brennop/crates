package player

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/brennop/crates/events"
)

// ---- a recording sink ----

type sink struct {
	mu  sync.Mutex
	evs []stamped
	ch  chan struct{}
}

type stamped struct {
	at time.Time
	e  events.Event
}

func newSink() *sink { return &sink{ch: make(chan struct{}, 1)} }

func (s *sink) Publish(e events.Event) {
	s.mu.Lock()
	s.evs = append(s.evs, stamped{time.Now(), e})
	s.mu.Unlock()
	select {
	case s.ch <- struct{}{}:
	default:
	}
}

func (s *sink) all() []stamped {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]stamped(nil), s.evs...)
}

// wait returns the first event (from index from on) that ok accepts.
func (s *sink) wait(t *testing.T, d time.Duration, ok func(events.Event) bool) events.Event {
	t.Helper()
	deadline := time.After(d)
	for {
		for _, x := range s.all() {
			if ok(x.e) {
				return x.e
			}
		}
		select {
		case <-s.ch:
		case <-deadline:
			t.Fatalf("event not seen within %v; got %v", d, s.all())
			return nil
		}
	}
}

func count[T events.Event](s *sink) int {
	n := 0
	for _, x := range s.all() {
		if _, ok := x.e.(T); ok {
			n++
		}
	}
	return n
}

// ---- a fake mpv socket server ----

type req struct {
	Command   json.RawMessage `json:"command"`
	RequestID int64           `json:"request_id"`
}

// name is the command name, positional or named.
func (r req) name() string {
	var pos []any
	if json.Unmarshal(r.Command, &pos) == nil && len(pos) > 0 {
		s, _ := pos[0].(string)
		return s
	}
	var named map[string]any
	json.Unmarshal(r.Command, &named)
	s, _ := named["name"].(string)
	return s
}

type server struct {
	ln    net.Listener
	sock  string
	conns chan *sconn
	// auto: answer every request with success (loadfile with a fresh entry
	// id) instead of handing it to the test.
	auto bool
}

type sconn struct {
	nc    net.Conn
	wmu   sync.Mutex
	reqs  chan req
	entry int64
}

func newServer(t *testing.T, auto bool) *server {
	t.Helper()
	dir, err := os.MkdirTemp("", "crates-mpvtest-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &server{ln: ln, sock: sock, conns: make(chan *sconn, 8), auto: auto}
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			c := &sconn{nc: nc, reqs: make(chan req, 64)}
			go c.serve(s.auto)
			s.conns <- c
		}
	}()
	return s
}

func (c *sconn) serve(auto bool) {
	defer close(c.reqs)
	sc := bufio.NewScanner(c.nc)
	for sc.Scan() {
		var r req
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		c.reqs <- r
		if auto {
			if r.name() == "loadfile" {
				c.entry++
				c.reply(r.RequestID, fmt.Sprintf(`{"playlist_entry_id":%d}`, c.entry), "success")
			} else {
				c.reply(r.RequestID, "", "success")
			}
		}
	}
}

func (c *sconn) send(line string) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.nc.Write([]byte(line + "\n"))
}

func (c *sconn) reply(id int64, data, errStr string) {
	if data == "" {
		c.send(fmt.Sprintf(`{"request_id":%d,"error":%q}`, id, errStr))
	} else {
		c.send(fmt.Sprintf(`{"data":%s,"request_id":%d,"error":%q}`, data, id, errStr))
	}
}

func (c *sconn) next(t *testing.T) req {
	t.Helper()
	select {
	case r, ok := <-c.reqs:
		if !ok {
			t.Fatal("connection closed")
		}
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("no request")
	}
	return req{}
}

// answerObserve answers the observe_property requests attach sends.
func (c *sconn) answerObserve(t *testing.T) {
	t.Helper()
	for range observed {
		r := c.next(t)
		if r.name() != "observe_property" {
			t.Fatalf("got %s, want observe_property", r.name())
		}
		c.reply(r.RequestID, "", "success")
	}
}

func (s *server) accept(t *testing.T) *sconn {
	t.Helper()
	select {
	case c := <-s.conns:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("no connection")
	}
	return nil
}

// start makes an MPV connected to the server, with no process behind it.
func (s *server) start(t *testing.T, sk events.Sink) (*MPV, *sconn) {
	t.Helper()
	m := newMPV(sk, func() (*proc, net.Conn, error) {
		nc, err := net.Dial("unix", s.sock)
		return nil, nc, err
	})
	m.restartDelay = 10 * time.Millisecond
	errc := make(chan error, 1)
	go func() { errc <- m.boot() }()
	c := s.accept(t)
	if !s.auto {
		c.answerObserve(t)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if s.auto {
		for range observed {
			c.next(t)
		}
	}
	t.Cleanup(func() { m.Close() })
	return m, c
}

// ---- tests ----

func TestRepliesInterleavedWithEvents(t *testing.T) {
	sk := newSink()
	srv := newServer(t, false)
	m, c := srv.start(t, sk)

	pauseErr := make(chan error, 1)
	seekErr := make(chan error, 1)
	go func() { pauseErr <- m.Pause(context.Background()) }()
	r1 := c.next(t)
	go func() { seekErr <- m.Seek(context.Background(), 3*time.Second) }()
	r2 := c.next(t)
	if r1.name() != "set_property" || r2.name() != "seek" {
		t.Fatalf("requests %s, %s", r1.name(), r2.name())
	}
	var seekArgs []any
	json.Unmarshal(r2.Command, &seekArgs)
	if seekArgs[1] != 3.0 || seekArgs[2] != "absolute" {
		t.Errorf("seek args %v", seekArgs)
	}

	// Answer out of order, with events in between.
	c.send(`{"event":"property-change","id":1,"name":"pause","data":true}`)
	c.reply(r2.RequestID, "", "error running command")
	c.send(`{"event":"audio-reconfig"}`)
	c.send(`{"event":"property-change","id":2,"name":"time-pos","data":1.5}`)
	c.reply(r1.RequestID, "", "success")

	if err := <-pauseErr; err != nil {
		t.Errorf("pause: %v", err)
	}
	var ce *CommandError
	if err := <-seekErr; !errors.As(err, &ce) || ce.Command != "seek" || ce.Msg != "error running command" {
		t.Errorf("seek: %v", err)
	}
	sk.wait(t, time.Second, func(e events.Event) bool {
		s, ok := e.(events.PlayerState)
		return ok && s.Paused && s.Position == 1500*time.Millisecond
	})
}

func TestLoadCommand(t *testing.T) {
	srv := newServer(t, true)
	m, c := srv.start(t, newSink())
	if err := m.Load(context.Background(), `/m/it's "a" ñ.mp3`, 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	r := c.next(t)
	var cmd map[string]any
	if err := json.Unmarshal(r.Command, &cmd); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"name": "loadfile", "url": `/m/it's "a" ñ.mp3`, "flags": "replace", "options": "start=1.500"}
	if fmt.Sprint(cmd) != fmt.Sprint(want) {
		t.Errorf("loadfile = %v, want %v", cmd, want)
	}
	m.Load(context.Background(), "/m/b.mp3", 0)
	cmd = nil
	json.Unmarshal(c.next(t).Command, &cmd)
	if _, ok := cmd["options"]; ok {
		t.Errorf("start 0 sent options: %v", cmd)
	}
	m.SetVolume(context.Background(), 140)
	var args []any
	json.Unmarshal(c.next(t).Command, &args)
	if args[2] != 100.0 {
		t.Errorf("volume not clamped: %v", args)
	}
}

func TestEndFileReasons(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		reason string
		want   events.Event // nil: ignored
	}{
		{"eof", events.TrackEnded{Path: "/m/a.mp3"}},
		{"error", events.LoadFailed{Path: "/m/a.mp3", Reason: "unrecognized file format"}},
		{"stop", nil},
		{"redirect", nil},
		{"quit", nil},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			sk := newSink()
			srv := newServer(t, true)
			m, c := srv.start(t, sk)
			if err := m.Load(ctx, "/m/a.mp3", 0); err != nil {
				t.Fatal(err)
			}
			extra := ""
			if tc.reason == "error" {
				extra = `,"file_error":"unrecognized file format"`
			}
			sent := time.Now()
			c.send(fmt.Sprintf(`{"event":"end-file","reason":%q,"playlist_entry_id":1%s}`, tc.reason, extra))
			// A marker event behind it, so "nothing published" is decided.
			c.send(`{"event":"property-change","id":4,"name":"volume","data":42}`)
			sk.wait(t, time.Second, func(e events.Event) bool {
				s, ok := e.(events.PlayerState)
				return ok && s.Volume == 42
			})
			var got events.Event
			for _, x := range sk.all() {
				switch x.e.(type) {
				case events.TrackEnded, events.LoadFailed:
					got = x.e
					if d := x.at.Sub(sent); d > 100*time.Millisecond {
						t.Errorf("%T took %v; must not be throttled", got, d)
					}
				}
			}
			if got != tc.want {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestStaleEndFileIgnored(t *testing.T) {
	ctx := context.Background()
	sk := newSink()
	srv := newServer(t, true)
	m, c := srv.start(t, sk)
	m.Load(ctx, "/m/a.mp3", 0) // entry 1
	m.Load(ctx, "/m/b.mp3", 0) // entry 2
	c.send(`{"event":"end-file","reason":"eof","playlist_entry_id":1}`)
	c.send(`{"event":"end-file","reason":"eof","playlist_entry_id":2}`)
	e := sk.wait(t, time.Second, func(e events.Event) bool { _, ok := e.(events.TrackEnded); return ok })
	if e.(events.TrackEnded).Path != "/m/b.mp3" {
		t.Errorf("got %#v", e)
	}
	// After Stop, the stopped entry's end counts for nothing.
	m.Load(ctx, "/m/c.mp3", 0) // entry 3
	m.Stop(ctx)
	c.send(`{"event":"end-file","reason":"eof","playlist_entry_id":3}`)
	c.send(`{"event":"property-change","id":4,"name":"volume","data":7}`)
	sk.wait(t, time.Second, func(e events.Event) bool { s, ok := e.(events.PlayerState); return ok && s.Volume == 7 })
	if n := count[events.TrackEnded](sk); n != 1 {
		t.Errorf("%d TrackEnded, want 1", n)
	}
}

func TestLoadFailureBeforeReplyIsSeen(t *testing.T) {
	// mpv answers loadfile, then fails the file at once. The reply's entry id
	// must be recorded before the end-file is handled.
	sk := newSink()
	srv := newServer(t, false)
	m, c := srv.start(t, sk)
	done := make(chan error, 1)
	go func() { done <- m.Load(context.Background(), "/m/bad.mp3", 0) }()
	r := c.next(t)
	c.reply(r.RequestID, `{"playlist_entry_id":9}`, "success")
	c.send(`{"event":"end-file","reason":"error","playlist_entry_id":9,"file_error":"no audio"}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	e := sk.wait(t, time.Second, func(e events.Event) bool { _, ok := e.(events.LoadFailed); return ok })
	if e != (events.LoadFailed{Path: "/m/bad.mp3", Reason: "no audio"}) {
		t.Errorf("got %#v", e)
	}
}

func TestThrottle(t *testing.T) {
	sk := newSink()
	srv := newServer(t, true)
	_, c := srv.start(t, sk)

	start := time.Now()
	for i := 0; time.Since(start) < 900*time.Millisecond; i++ {
		c.send(fmt.Sprintf(`{"event":"property-change","id":2,"name":"time-pos","data":%d}`, i))
		time.Sleep(2 * time.Millisecond)
	}
	c.send(`{"event":"property-change","id":2,"name":"time-pos","data":100000}`)
	sk.wait(t, time.Second, func(e events.Event) bool {
		s, ok := e.(events.PlayerState)
		return ok && s.Position == 100000*time.Second
	})

	var at []time.Time
	for _, x := range sk.all() {
		if _, ok := x.e.(events.PlayerState); ok {
			at = append(at, x.at)
		}
	}
	// About 900 ms of changes: at most 5 states (one at once, then one per
	// 250 ms), and never two closer than the interval allows.
	if len(at) > 6 || len(at) < 3 {
		t.Errorf("%d PlayerState events for ~400 changes", len(at))
	}
	for i := 1; i < len(at); i++ {
		if d := at[i].Sub(at[i-1]); d < StateInterval-20*time.Millisecond {
			t.Errorf("states %d and %d only %v apart", i-1, i, d)
		}
	}
}

func TestRequestTimeout(t *testing.T) {
	srv := newServer(t, false)
	m, _ := srv.start(t, newSink())
	m.mu.Lock()
	m.c.timeout = 50 * time.Millisecond
	m.mu.Unlock()
	if err := m.Pause(context.Background()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Resume(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestCrashRecovery(t *testing.T) {
	ctx := context.Background()
	sk := newSink()
	srv := newServer(t, true)
	m, c := srv.start(t, sk)
	m.Load(ctx, "/m/a.mp3", 0)
	c.next(t)
	c.send(`{"event":"property-change","id":1,"name":"pause","data":false}`)
	c.send(`{"event":"property-change","id":5,"name":"idle-active","data":false}`)

	c.nc.Close() // mpv went away
	down := sk.wait(t, time.Second, func(e events.Event) bool { _, ok := e.(events.PlayerDown); return ok })
	if down.(events.PlayerDown).Err == nil {
		t.Error("PlayerDown without an error")
	}

	c2 := srv.accept(t)
	var got []string
	for range observed {
		got = append(got, c2.next(t).name())
	}
	for _, n := range got {
		if n != "observe_property" {
			t.Fatalf("after restart got %v", got)
		}
	}
	// Nothing else is sent: playback is not resumed.
	select {
	case r := <-c2.reqs:
		t.Fatalf("unexpected %s after restart", r.name())
	case <-time.After(100 * time.Millisecond):
	}
	sk.wait(t, time.Second, func(e events.Event) bool { s, ok := e.(events.PlayerState); return ok && s.Idle })
	// The new connection works.
	if err := m.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	// An eof for the pre-crash entry counts for nothing.
	c2.send(`{"event":"end-file","reason":"eof","playlist_entry_id":1}`)
	time.Sleep(50 * time.Millisecond)
	if n := count[events.TrackEnded](sk); n != 0 {
		t.Errorf("%d TrackEnded after restart", n)
	}
}

func TestClose(t *testing.T) {
	srv := newServer(t, true)
	sk := newSink()
	m, c := srv.start(t, sk)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if r := c.next(t); r.name() != "quit" {
		t.Errorf("Close sent %s, want quit", r.name())
	}
	if err := m.Pause(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("after Close: %v", err)
	}
	if n := count[events.PlayerDown](sk); n != 0 {
		t.Error("Close reported PlayerDown")
	}
	m.Close() // twice is fine
}

func TestFake(t *testing.T) {
	sk := newSink()
	f := NewFake(sk)
	ctx := context.Background()
	f.Load(ctx, "/a.mp3", time.Second)
	f.SetVolume(ctx, -5)
	boom := errors.New("boom")
	f.Fail("pause", boom)
	if err := f.Pause(ctx); err != boom {
		t.Errorf("pause = %v", err)
	}
	f.Emit(events.TrackEnded{Path: "/a.mp3"})
	want := []Call{{Op: "load", Path: "/a.mp3", Pos: time.Second}, {Op: "volume"}, {Op: "pause"}}
	if fmt.Sprint(f.Calls()) != fmt.Sprint(want) {
		t.Errorf("calls %v", f.Calls())
	}
	if count[events.TrackEnded](sk) != 1 {
		t.Error("Emit did not publish")
	}
}
