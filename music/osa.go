//go:build darwin

package music

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brennop/crates/model"
	"github.com/brennop/crates/pathnorm"
)

// OSA is the Client that runs the embedded JXA scripts with osascript. Every
// call goes through one worker goroutine, one script at a time: Apple Events
// are slow and must not interleave. Arguments go in as argv and results come
// back as one JSON line, so nothing is ever interpolated into a script.
type OSA struct {
	opts Options

	reqs   chan *request
	ctx    context.Context // cancelled by Close; kills the script in flight
	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	latched  bool   // an add landed outside master: no more adds
	latchLoc string // where Music put that copy
}

var _ Service = (*OSA)(nil)

type request struct {
	ctx     context.Context
	script  string
	args    []string
	timeout time.Duration
	reply   chan reply
}

type reply struct {
	result json.RawMessage
	err    error
}

// New starts the client's worker.
func New(opts Options) Service {
	if opts.Bin == "" {
		opts.Bin = "osascript"
	}
	if opts.SnapshotTimeout == 0 {
		opts.SnapshotTimeout = SnapshotTimeout
	}
	if opts.CallTimeout == 0 {
		opts.CallTimeout = CallTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &OSA{
		opts:   opts,
		reqs:   make(chan *request),
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go c.work()
	return c
}

// Close stops the worker. A script in flight is killed and its caller gets
// ErrClosed; later calls get ErrClosed too.
func (c *OSA) Close() error {
	c.cancel()
	<-c.done
	return nil
}

func (c *OSA) work() {
	defer close(c.done)
	for {
		select {
		case <-c.ctx.Done():
			return
		case r := <-c.reqs:
			res, err := c.exec(r)
			r.reply <- reply{res, err}
		}
	}
}

// call runs one script on the worker and returns its result. With
// autolaunch on, a script that finds Music not running launches it and runs
// once more.
func (c *OSA) call(ctx context.Context, script string, args ...string) (json.RawMessage, error) {
	res, err := c.once(ctx, script, args)
	if c.opts.Autolaunch && errors.As(err, new(model.ErrMusicUnavailable)) {
		if _, lerr := c.once(ctx, scriptLaunch, nil); lerr != nil {
			return nil, lerr
		}
		res, err = c.once(ctx, script, args)
	}
	return res, err
}

func (c *OSA) once(ctx context.Context, script string, args []string) (json.RawMessage, error) {
	timeout := c.opts.CallTimeout
	if script == scriptSnapshot {
		timeout = c.opts.SnapshotTimeout
	}
	r := &request{ctx: ctx, script: script, args: args, timeout: timeout, reply: make(chan reply, 1)}
	select {
	case c.reqs <- r:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, ErrClosed
	}
	// The worker always answers, and it stops the script when ctx or Close
	// cancels, so this wait is bounded by the timeout.
	rep := <-r.reply
	return rep.result, rep.err
}

// exec runs one osascript process on the worker goroutine.
func (c *OSA) exec(r *request) (json.RawMessage, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	src, err := source(r.script)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(r.ctx, r.timeout)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()

	args := append([]string{"-l", "JavaScript", "-e", src}, r.args...)
	cmd := exec.CommandContext(ctx, c.opts.Bin, args...)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	switch {
	case c.ctx.Err() != nil:
		return nil, ErrClosed
	case r.ctx.Err() != nil:
		return nil, r.ctx.Err()
	case ctx.Err() != nil:
		return nil, &TimeoutError{Script: r.script, After: r.timeout.String()}
	}
	return parseOutput(r.script, stdout.Bytes(), stderr.Bytes(), runErr)
}

// envelope is the one shape every script prints (see scripts/lib.js).
type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Number  *int   `json:"number"`
		Message string `json:"message"`
	} `json:"error"`
}

// osascript prints "...: execution error: <message> (-1743)" on stderr for
// errors the prelude could not catch.
var stderrNumber = regexp.MustCompile(`\((-?\d+)\)\s*$`)

// parseOutput turns a script's output into its result or a typed error.
func parseOutput(script string, stdout, stderr []byte, runErr error) (json.RawMessage, error) {
	line := bytes.TrimSpace(stdout)
	var env envelope
	if len(line) > 0 && json.Unmarshal(line, &env) == nil && (env.OK || env.Error != nil) {
		if env.OK {
			return env.Result, nil
		}
		n := 0
		if env.Error.Number != nil {
			n = *env.Error.Number
		}
		return nil, scriptError(script, n, env.Error.Message)
	}
	msg := strings.TrimSpace(string(stderr))
	n := 0
	if m := stderrNumber.FindStringSubmatch(msg); m != nil {
		n, _ = strconv.Atoi(m[1])
	}
	if msg == "" {
		msg = "no result"
		if runErr != nil {
			msg = runErr.Error()
		}
	}
	return nil, scriptError(script, n, msg)
}

func scriptError(script string, n int, msg string) error {
	switch n {
	case errNotRunning:
		return model.ErrMusicUnavailable{}
	case errDenied:
		return model.ErrAutomationDenied{}
	}
	return &ScriptError{Script: script, Number: n, Message: msg}
}

// decode runs a script and unmarshals its result into v.
func (c *OSA) decode(ctx context.Context, v any, script string, args ...string) error {
	res, err := c.call(ctx, script, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(res, v); err != nil {
		return fmt.Errorf("music %s: bad result: %w", script, err)
	}
	return nil
}

// Status reports Available, Unavailable (not running), Denied, or
// CopySettingOn once an add has found the setting on. It never launches
// Music.
func (c *OSA) Status(ctx context.Context) (model.MusicState, error) {
	var res struct {
		Running bool `json:"running"`
	}
	if err := c.decodeOnce(ctx, &res, scriptStatus); err != nil {
		if errors.As(err, new(model.ErrAutomationDenied)) {
			return model.MusicDenied, err
		}
		return model.MusicUnavailable, err
	}
	c.mu.Lock()
	latched := c.latched
	c.mu.Unlock()
	switch {
	case latched:
		return model.MusicCopySettingOn, nil
	case !res.Running:
		return model.MusicUnavailable, nil
	}
	return model.MusicAvailable, nil
}

// decodeOnce is decode without autolaunch.
func (c *OSA) decodeOnce(ctx context.Context, v any, script string, args ...string) error {
	res, err := c.once(ctx, script, args)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(res, v); err != nil {
		return fmt.Errorf("music %s: bad result: %w", script, err)
	}
	return nil
}

// Launch starts Music if it is not running.
func (c *OSA) Launch(ctx context.Context) error {
	var res struct {
		Running bool `json:"running"`
	}
	if err := c.decodeOnce(ctx, &res, scriptLaunch); err != nil {
		return err
	}
	if !res.Running {
		return model.ErrMusicUnavailable{}
	}
	return nil
}

type snapshotResult struct {
	Tracks []struct {
		ID       string  `json:"id"`
		Location *string `json:"location"`
	} `json:"tracks"`
	Playlists []struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Smart    bool     `json:"smart"`
		Special  string   `json:"special"`
		TrackIDs []string `json:"trackIDs"`
	} `json:"playlists"`
}

// Snapshot reads every file track's location and every user playlist. Taken
// is when the read started.
func (c *OSA) Snapshot(ctx context.Context) (Snapshot, error) {
	taken := time.Now()
	var res snapshotResult
	if err := c.decode(ctx, &res, scriptSnapshot); err != nil {
		return Snapshot{}, err
	}
	return buildSnapshot(taken, res), nil
}

// buildSnapshot normalizes locations (Music returns them in NFD) and skips
// tracks with none.
func buildSnapshot(taken time.Time, res snapshotResult) Snapshot {
	s := Snapshot{
		Taken:   taken,
		Tracks:  make(map[string]string, len(res.Tracks)),
		Members: make(map[string][]string, len(res.Playlists)),
	}
	for _, t := range res.Tracks {
		if t.Location == nil || *t.Location == "" {
			continue
		}
		p, err := pathnorm.Normalize(*t.Location)
		if err != nil {
			continue
		}
		s.Tracks[p] = t.ID
	}
	for _, p := range res.Playlists {
		s.Playlists = append(s.Playlists, playlistOf(p.ID, p.Name, p.Smart, p.Special))
		s.Members[p.ID] = slices.Clone(p.TrackIDs)
	}
	return s
}

type trackInfo struct {
	ID       string  `json:"id"`
	Location *string `json:"location"`
}

// Add adds the file and reads back where Music put it. A location other
// than path means the copy setting is on: the client latches and refuses
// every later add.
func (c *OSA) Add(ctx context.Context, path string) (string, error) {
	c.mu.Lock()
	latched, loc := c.latched, c.latchLoc
	c.mu.Unlock()
	if latched {
		return "", model.ErrCopySettingOn{Path: path, Location: loc}
	}
	var t trackInfo
	if err := c.decode(ctx, &t, scriptAdd, path); err != nil {
		return "", err
	}
	if t.ID == "" || t.Location == nil {
		return "", model.ErrVerifyFailed{What: "added " + path + " but Music reports no location for it"}
	}
	got, err := pathnorm.Normalize(*t.Location)
	if err != nil {
		got = *t.Location
	}
	if !samePath(got, path) {
		c.mu.Lock()
		c.latched, c.latchLoc = true, got
		c.mu.Unlock()
		return "", model.ErrCopySettingOn{Path: path, Location: got}
	}
	return t.ID, nil
}

// samePath compares two normalized paths, ignoring case where the volume
// does.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	cs, err := pathnorm.CaseSensitive(filepath.Dir(b))
	if err != nil {
		return false
	}
	return pathnorm.Key(a, cs) == pathnorm.Key(b, cs)
}

type membership struct {
	Before []string `json:"before"`
	After  []string `json:"trackIDs"`
}

// AddToPlaylist adds the tracks that are not members yet and compares the
// whole membership read back with the one read just before plus the new
// tracks.
func (c *OSA) AddToPlaylist(ctx context.Context, playlistID string, musicIDs []string) ([]string, error) {
	var m membership
	if err := c.decode(ctx, &m, scriptPlaylistAdd, append([]string{playlistID}, musicIDs...)...); err != nil {
		return nil, err
	}
	return m.After, verifyMembers(playlistID, m.After, expectAdd(m.Before, musicIDs))
}

// RemoveFromPlaylist removes every occurrence of the tracks and compares
// the membership read back with the one read before minus the tracks.
func (c *OSA) RemoveFromPlaylist(ctx context.Context, playlistID string, musicIDs []string) ([]string, error) {
	var m membership
	if err := c.decode(ctx, &m, scriptPlaylistRemove, append([]string{playlistID}, musicIDs...)...); err != nil {
		return nil, err
	}
	return m.After, verifyMembers(playlistID, m.After, expectRemove(m.Before, musicIDs))
}

// DeleteTrack removes the library entry and confirms no entry with that ID
// is left.
func (c *OSA) DeleteTrack(ctx context.Context, musicID string) error {
	var res struct {
		Remaining int `json:"remaining"`
	}
	if err := c.decode(ctx, &res, scriptDelete, musicID); err != nil {
		return err
	}
	if res.Remaining != 0 {
		return model.ErrVerifyFailed{What: fmt.Sprintf("track %s is still in the library after delete", musicID)}
	}
	return nil
}

// Refresh makes Music re-read the file behind an entry.
func (c *OSA) Refresh(ctx context.Context, musicID string) error {
	var t trackInfo
	if err := c.decode(ctx, &t, scriptRefresh, musicID); err != nil {
		return err
	}
	if t.ID != musicID {
		return model.ErrVerifyFailed{What: fmt.Sprintf("refresh of %s answered for %q", musicID, t.ID)}
	}
	return nil
}
