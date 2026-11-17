package player

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/brennop/crates/events"
)

// observed are the properties folded into PlayerState. idle-active and path
// fill PlayerState.Idle and PlayerState.Path.
var observed = []string{"pause", "time-pos", "duration", "volume", "idle-active", "path"}

// maxSocketPath is the longest path a Unix socket address holds on macOS
// (sun_path is 104 bytes, NUL included).
const maxSocketPath = 103

// restartAttempts bounds how often a lost mpv is restarted in a row.
const restartAttempts = 3

// Config configures Start.
type Config struct {
	Sink events.Sink
	// Bin is the mpv binary; "" looks up "mpv" on PATH.
	Bin string
	// PidFile records the running mpv so the next start can kill an orphan
	// (see CleanOrphan). "" disables it.
	PidFile string
	// Args are extra mpv arguments, for tests (e.g. --ao=null).
	Args []string
}

// MPV is the Player backed by one mpv process. Its methods are safe from any
// goroutine.
type MPV struct {
	sink         events.Sink
	spawn        func() (*proc, net.Conn, error)
	cleanup      func(lastPID int)
	reqTimeout   time.Duration
	interval     time.Duration
	restartDelay time.Duration

	mu      sync.Mutex
	closed  bool
	gen     int // bumps per connection; events from an old one are dropped
	c       *conn
	p       *proc
	st      state
	cur     entry // the file whose end-file counts; zero while a load is in flight
	lastPID int

	tmu   sync.Mutex
	last  time.Time
	timer *time.Timer

	restarts sync.WaitGroup
}

type state struct {
	paused, idle bool
	pos, dur     time.Duration
	vol          float64
	path         string
}

type entry struct {
	id   int64
	path string
}

// Start kills an orphan mpv named by the pidfile, then starts mpv and
// connects to it.
func Start(cfg Config) (*MPV, error) {
	bin := cfg.Bin
	if bin == "" {
		var err error
		if bin, err = exec.LookPath("mpv"); err != nil {
			return nil, ErrNotInstalled
		}
	}
	if cfg.PidFile != "" {
		if _, err := CleanOrphan(cfg.PidFile); err != nil {
			cfg.Sink.Publish(events.Error{Op: "player: clean orphan mpv", Err: err})
		}
	}
	dir, err := os.MkdirTemp("", socketDirPrefix)
	if err != nil {
		return nil, err
	}
	sock := filepath.Join(dir, "mpv.sock")
	if len(sock) > maxSocketPath {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("player: socket path too long (%d bytes): %s", len(sock), sock)
	}
	m := newMPV(cfg.Sink, func() (*proc, net.Conn, error) {
		return startProcess(bin, sock, cfg.PidFile, cfg.Args)
	})
	m.cleanup = func(lastPID int) {
		os.RemoveAll(dir)
		if cfg.PidFile != "" {
			if pid, _, err := readPidfile(cfg.PidFile); err == nil && pid == lastPID {
				os.Remove(cfg.PidFile)
			}
		}
	}
	if err := m.boot(); err != nil {
		m.Close()
		return nil, err
	}
	return m, nil
}

func newMPV(sink events.Sink, spawn func() (*proc, net.Conn, error)) *MPV {
	return &MPV{
		sink:         sink,
		spawn:        spawn,
		reqTimeout:   RequestTimeout,
		interval:     StateInterval,
		restartDelay: 500 * time.Millisecond,
		st:           state{idle: true},
	}
}

func (m *MPV) boot() error {
	p, nc, err := m.spawn()
	if err != nil {
		return err
	}
	return m.attach(p, nc)
}

// attach makes p and nc the current process and connection and observes the
// properties. Playback state starts idle; nothing is resumed.
func (m *MPV) attach(p *proc, nc net.Conn) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		nc.Close()
		if p != nil {
			p.kill()
		}
		return ErrClosed
	}
	m.gen++
	g := m.gen
	c := newConn(nc, m.reqTimeout,
		func(msg message) { m.onEvent(g, msg) },
		func(err error) { m.onDown(g, err) })
	m.c, m.p = c, p
	m.st = state{idle: true}
	m.cur = entry{}
	if p != nil {
		m.lastPID = p.pid()
	}
	m.mu.Unlock()

	ctx := context.Background()
	for i, name := range observed {
		if _, err := c.request(ctx, nil, []any{"observe_property", i + 1, name}); err != nil {
			// Drop this half-made connection; a bumped gen makes its
			// onDown a no-op, so the caller decides whether to retry.
			m.mu.Lock()
			if m.gen == g {
				m.gen++
				m.c, m.p = nil, nil
			}
			m.mu.Unlock()
			c.close()
			if p != nil {
				p.kill()
			}
			return fmt.Errorf("player: observe %s: %w", name, err)
		}
	}
	return nil
}

func (m *MPV) conn() (*conn, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.c == nil {
		return nil, 0, ErrClosed
	}
	return m.c, m.gen, nil
}

func (m *MPV) Load(ctx context.Context, path string, start time.Duration) error {
	c, g, err := m.conn()
	if err != nil {
		return err
	}
	// Until the reply names the new entry, no end-file counts: an eof of the
	// old file racing this load must not advance the queue.
	m.mu.Lock()
	m.cur = entry{}
	m.mu.Unlock()
	// Named arguments: the positional form of loadfile gained an index
	// argument in mpv 0.38, which shifted the options.
	cmd := map[string]any{"name": "loadfile", "url": path, "flags": "replace"}
	if start > 0 {
		cmd["options"] = "start=" + strconv.FormatFloat(start.Seconds(), 'f', 3, 64)
	}
	_, err = c.request(ctx, func(data json.RawMessage) {
		var r struct {
			ID int64 `json:"playlist_entry_id"`
		}
		json.Unmarshal(data, &r)
		m.mu.Lock()
		if m.gen == g {
			m.cur = entry{id: r.ID, path: path}
		}
		m.mu.Unlock()
	}, cmd)
	return err
}

func (m *MPV) Pause(ctx context.Context) error  { return m.set(ctx, "pause", true) }
func (m *MPV) Resume(ctx context.Context) error { return m.set(ctx, "pause", false) }

func (m *MPV) Stop(ctx context.Context) error {
	c, _, err := m.conn()
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.cur = entry{}
	m.mu.Unlock()
	_, err = c.request(ctx, nil, []any{"stop"})
	return err
}

func (m *MPV) Seek(ctx context.Context, pos time.Duration) error {
	c, _, err := m.conn()
	if err != nil {
		return err
	}
	_, err = c.request(ctx, nil, []any{"seek", pos.Seconds(), "absolute"})
	return err
}

func (m *MPV) SetVolume(ctx context.Context, v float64) error {
	return m.set(ctx, "volume", clampVolume(v))
}

func (m *MPV) set(ctx context.Context, prop string, v any) error {
	c, _, err := m.conn()
	if err != nil {
		return err
	}
	_, err = c.request(ctx, nil, []any{"set_property", prop, v})
	return err
}

// Close quits mpv, waits for it (killing that one process if it hangs) and
// removes the socket dir and the pidfile.
func (m *MPV) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	c, p := m.c, m.p
	m.c, m.p = nil, nil
	m.mu.Unlock()

	m.tmu.Lock()
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.tmu.Unlock()

	if c != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c.request(ctx, nil, []any{"quit"})
		cancel()
		c.close()
	}
	if p != nil {
		p.stop(2 * time.Second)
	}
	m.restarts.Wait()
	if m.cleanup != nil {
		m.mu.Lock()
		pid := m.lastPID
		m.mu.Unlock()
		m.cleanup(pid)
	}
	return nil
}

// ---- events from mpv ----

func (m *MPV) onEvent(g int, msg message) {
	m.mu.Lock()
	if g != m.gen || m.closed {
		m.mu.Unlock()
		return
	}
	switch msg.Event {
	case "property-change":
		m.fold(msg.Name, msg.Data)
		m.mu.Unlock()
		m.poke()
		return
	case "end-file":
		// Only the end of the file the app last loaded counts. stop,
		// redirect and quit come from the app's own load or stop (or from
		// mpv going away, handled by onDown) and are ignored.
		if msg.EntryID == 0 || msg.EntryID != m.cur.id {
			break
		}
		cur := m.cur
		var e events.Event
		switch msg.Reason {
		case "eof":
			e = events.TrackEnded{Path: cur.path}
		case "error":
			reason := msg.FileError
			if reason == "" {
				reason = "error"
			}
			e = events.LoadFailed{Path: cur.path, Reason: reason}
		}
		if e != nil {
			m.cur = entry{}
			m.mu.Unlock()
			m.sink.Publish(e)
			return
		}
	}
	m.mu.Unlock()
}

// fold applies one property change. A null value (property unavailable)
// resets the field. Called with m.mu held.
func (m *MPV) fold(name string, data json.RawMessage) {
	var f float64
	var b bool
	var s string
	json.Unmarshal(data, &f)
	json.Unmarshal(data, &b)
	json.Unmarshal(data, &s)
	secs := time.Duration(f * float64(time.Second))
	switch name {
	case "pause":
		m.st.paused = b
	case "time-pos":
		m.st.pos = secs
	case "duration":
		m.st.dur = secs
	case "volume":
		m.st.vol = f
	case "idle-active":
		m.st.idle = b
	case "path":
		m.st.path = s
	}
}

// poke publishes PlayerState now if the last one went out at least interval
// ago, else once the interval is up. The value is read when it goes out, so
// the latest wins.
func (m *MPV) poke() {
	m.tmu.Lock()
	if m.timer != nil {
		m.tmu.Unlock()
		return
	}
	wait := m.interval - time.Since(m.last)
	if wait <= 0 {
		m.last = time.Now()
		m.tmu.Unlock()
		m.emitState()
		return
	}
	m.timer = time.AfterFunc(wait, func() {
		m.tmu.Lock()
		if m.timer == nil { // stopped by Close
			m.tmu.Unlock()
			return
		}
		m.timer = nil
		m.last = time.Now()
		m.tmu.Unlock()
		m.emitState()
	})
	m.tmu.Unlock()
}

func (m *MPV) emitState() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	st := m.st
	m.mu.Unlock()
	m.sink.Publish(events.PlayerState{
		Path:     st.path,
		Paused:   st.paused,
		Idle:     st.idle,
		Position: st.pos,
		Duration: st.dur,
		Volume:   st.vol,
	})
}

// onDown handles a socket that closed while the player was open: report it,
// reset the state to idle and restart mpv without resuming playback.
func (m *MPV) onDown(g int, err error) {
	m.mu.Lock()
	if g != m.gen || m.closed {
		m.mu.Unlock()
		return
	}
	p := m.p
	m.c, m.p = nil, nil
	m.st = state{idle: true}
	m.cur = entry{}
	m.restarts.Add(1)
	m.mu.Unlock()

	m.sink.Publish(events.PlayerDown{Err: fmt.Errorf("player: lost mpv: %w", err)})
	m.poke()
	go m.restart(p)
}

func (m *MPV) restart(old *proc) {
	defer m.restarts.Done()
	if old != nil {
		old.kill()
	}
	var err error
	for i := 1; i <= restartAttempts; i++ {
		m.mu.Lock()
		closed := m.closed
		m.mu.Unlock()
		if closed {
			return
		}
		if err = m.boot(); err == nil || err == ErrClosed {
			return
		}
		time.Sleep(m.restartDelay * time.Duration(i))
	}
	m.sink.Publish(events.Error{Op: "player: restart mpv", Err: err})
}

// ---- the mpv process ----

const socketDirPrefix = "crates-mpv-"

type proc struct {
	cmd    *exec.Cmd
	exited chan struct{}
}

func (p *proc) pid() int { return p.cmd.Process.Pid }

// kill kills this one process and waits for it.
func (p *proc) kill() {
	p.cmd.Process.Kill()
	<-p.exited
}

// stop waits up to d for the process to exit on its own, then kills it.
func (p *proc) stop(d time.Duration) {
	select {
	case <-p.exited:
	case <-time.After(d):
		p.kill()
	}
}

func startProcess(bin, sock, pidfile string, extra []string) (*proc, net.Conn, error) {
	os.Remove(sock)
	args := append([]string{"--idle", "--no-video", "--no-terminal", "--input-ipc-server=" + sock}, extra...)
	cmd := exec.Command(bin, args...)
	// Its own process group: a Ctrl-C in the terminal must not reach mpv.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("player: start mpv: %w", err)
	}
	p := &proc{cmd: cmd, exited: make(chan struct{})}
	go func() {
		cmd.Wait()
		close(p.exited)
	}()
	if pidfile != "" {
		if err := writePidfile(pidfile, p.pid(), sock); err != nil {
			p.kill()
			return nil, nil, err
		}
	}
	deadline := time.Now().Add(StartTimeout)
	for {
		nc, err := net.Dial("unix", sock)
		if err == nil {
			return p, nc, nil
		}
		select {
		case <-p.exited:
			removePidfile(pidfile)
			return nil, nil, fmt.Errorf("player: mpv exited during startup: %v", cmd.ProcessState)
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			p.kill()
			removePidfile(pidfile)
			return nil, nil, fmt.Errorf("player: mpv socket did not accept within %v: %w", StartTimeout, err)
		}
	}
}
