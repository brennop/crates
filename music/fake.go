package music

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/brennop/crates/model"
)

// Fake is an in-memory Music for app tests. It holds tracks by persistent ID
// with their locations, and playlists with their members. Failures are
// scripted per operation (Fail), or set as standing conditions: not running
// (SetRunning), automation denied (SetDenied), the copy setting on
// (SetCopySetting), and a playlist truncated by the next write (TruncateNext).
type Fake struct {
	mu        sync.Mutex
	running   bool
	launched  int
	denied    bool
	copyOn    bool
	latched   bool // an add found the copy setting on; adds refuse from then on
	next      int
	tracks    map[string]string // MusicID -> location
	playlists []model.Playlist
	members   map[string][]string
	truncate  map[string]bool // playlist ID -> cut the next write short
	fail      map[string][]error
	calls     []FakeCall
	now       func() time.Time
	closed    bool
}

// FakeCall records one Client call.
type FakeCall struct {
	Op         string // one of the Op* names
	Path       string // add
	PlaylistID string
	MusicIDs   []string
}

// Operation names for Fail and FakeCall.
const (
	OpStatus   = "status"
	OpLaunch   = "launch"
	OpSnapshot = "snapshot"
	OpAdd      = "add"
	OpPlAdd    = "playlist-add"
	OpPlRemove = "playlist-remove"
	OpDelete   = "delete"
	OpRefresh  = "refresh"
)

// MediaDir is where the fake puts an added file's copy when the copy
// setting is on.
const MediaDir = "/Music/Media.localized/Music"

var _ Service = (*Fake)(nil)

// Close marks the fake closed; see Closed. Calls still work, so a test can
// inspect the fake afterwards.
func (f *Fake) Close() error {
	f.set(func() { f.closed = true })
	return nil
}

// Closed reports whether Close ran.
func (f *Fake) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// NewFake returns a running Music with an empty library.
func NewFake() *Fake {
	return &Fake{
		running:  true,
		tracks:   map[string]string{},
		members:  map[string][]string{},
		truncate: map[string]bool{},
		fail:     map[string][]error{},
		now:      time.Now,
	}
}

// AddPlaylist creates a playlist with the given members.
func (f *Fake) AddPlaylist(p model.Playlist, musicIDs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playlists = append(f.playlists, p)
	f.members[p.ID] = slices.Clone(musicIDs)
}

// Members returns a playlist's current membership.
func (f *Fake) Members(playlistID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.members[playlistID])
}

// Track returns the location of an entry and whether it exists.
func (f *Fake) Track(musicID string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	loc, ok := f.tracks[musicID]
	return loc, ok
}

// Launches returns how many times Launch ran.
func (f *Fake) Launches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.launched
}

// SetRunning quits or starts Music. While it is not running, every call but
// Status and Launch returns ErrMusicUnavailable.
func (f *Fake) SetRunning(b bool) { f.set(func() { f.running = b }) }

// SetDenied makes every call return ErrAutomationDenied, as after -1743.
func (f *Fake) SetDenied(b bool) { f.set(func() { f.denied = b }) }

// SetCopySetting turns Music's "copy files to Media folder" setting on or
// off. With it on, Add copies the file into MediaDir and returns
// ErrCopySettingOn.
func (f *Fake) SetCopySetting(b bool) { f.set(func() { f.copyOn = b }) }

// TruncateNext makes the next write to the playlist drop its first member
// as well, as Sync Library was reported to do. The write then returns
// ErrVerifyFailed.
func (f *Fake) TruncateNext(playlistID string) { f.set(func() { f.truncate[playlistID] = true }) }

// Fail makes the next calls of op return errs, one per call, in order. A
// failing call changes nothing.
func (f *Fake) Fail(op string, errs ...error) {
	f.set(func() { f.fail[op] = append(f.fail[op], errs...) })
}

// Calls returns the calls so far, oldest first.
func (f *Fake) Calls() []FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// CallsOf returns the calls of one operation.
func (f *Fake) CallsOf(op string) []FakeCall {
	var out []FakeCall
	for _, c := range f.Calls() {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

func (f *Fake) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// begin records a call and returns the error it must fail with, if any.
// Callers hold mu.
func (f *Fake) begin(ctx context.Context, c FakeCall) error {
	c.MusicIDs = slices.Clone(c.MusicIDs)
	f.calls = append(f.calls, c)
	if err := ctx.Err(); err != nil {
		return err
	}
	if errs := f.fail[c.Op]; len(errs) > 0 {
		f.fail[c.Op] = errs[1:]
		return errs[0]
	}
	if f.denied {
		return model.ErrAutomationDenied{}
	}
	if !f.running && c.Op != OpStatus && c.Op != OpLaunch {
		return model.ErrMusicUnavailable{}
	}
	return nil
}

func (f *Fake) Status(ctx context.Context) (model.MusicState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, FakeCall{Op: OpStatus}); err != nil {
		if _, ok := err.(model.ErrAutomationDenied); ok {
			return model.MusicDenied, err
		}
		return model.MusicUnavailable, err
	}
	switch {
	case f.latched:
		return model.MusicCopySettingOn, nil
	case !f.running:
		return model.MusicUnavailable, nil
	}
	return model.MusicAvailable, nil
}

func (f *Fake) Launch(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, FakeCall{Op: OpLaunch}); err != nil {
		return err
	}
	f.running = true
	f.launched++
	return nil
}

func (f *Fake) Snapshot(ctx context.Context) (Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, FakeCall{Op: OpSnapshot}); err != nil {
		return Snapshot{}, err
	}
	s := Snapshot{
		Taken:     f.now(),
		Tracks:    make(map[string]string, len(f.tracks)),
		Playlists: slices.Clone(f.playlists),
		Members:   make(map[string][]string, len(f.members)),
	}
	for id, loc := range f.tracks {
		s.Tracks[loc] = id
	}
	for pid, ids := range f.members {
		s.Members[pid] = slices.Clone(ids)
	}
	return s, nil
}

func (f *Fake) Add(ctx context.Context, path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, FakeCall{Op: OpAdd, Path: path}); err != nil {
		return "", err
	}
	if f.latched {
		return "", model.ErrCopySettingOn{Path: path}
	}
	for id, loc := range f.tracks {
		if loc == path {
			return id, nil
		}
	}
	f.next++
	id := fmt.Sprintf("F%015X", f.next)
	loc := path
	if f.copyOn {
		loc = filepath.Join(MediaDir, filepath.Base(path))
	}
	f.tracks[id] = loc
	if loc != path {
		f.latched = true
		return "", model.ErrCopySettingOn{Path: path, Location: loc}
	}
	return id, nil
}

// playlist checks that a playlist exists and can be edited. Callers hold mu.
func (f *Fake) playlist(id string) error {
	for _, p := range f.playlists {
		if p.ID == id {
			if !p.Editable {
				return model.ErrNotEditable{PlaylistID: id}
			}
			return nil
		}
	}
	return fmt.Errorf("music: no playlist %s (-1728)", id)
}

// known checks that every ID is a library entry. Callers hold mu.
func (f *Fake) known(ids []string) error {
	for _, id := range ids {
		if _, ok := f.tracks[id]; !ok {
			return fmt.Errorf("music: no track %s (-1728)", id)
		}
	}
	return nil
}

func (f *Fake) AddToPlaylist(ctx context.Context, playlistID string, musicIDs []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, FakeCall{Op: OpPlAdd, PlaylistID: playlistID, MusicIDs: musicIDs}); err != nil {
		return nil, err
	}
	if err := f.playlist(playlistID); err != nil {
		return nil, err
	}
	if err := f.known(musicIDs); err != nil {
		return nil, err
	}
	want := slices.Clone(f.members[playlistID])
	for _, id := range musicIDs {
		if !slices.Contains(want, id) {
			want = append(want, id)
		}
	}
	return f.write(playlistID, want)
}

func (f *Fake) RemoveFromPlaylist(ctx context.Context, playlistID string, musicIDs []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, FakeCall{Op: OpPlRemove, PlaylistID: playlistID, MusicIDs: musicIDs}); err != nil {
		return nil, err
	}
	if err := f.playlist(playlistID); err != nil {
		return nil, err
	}
	want := slices.DeleteFunc(slices.Clone(f.members[playlistID]), func(id string) bool {
		return slices.Contains(musicIDs, id)
	})
	return f.write(playlistID, want)
}

// write stores want as the playlist's membership, cut short if a truncation
// is scripted, and verifies it the way the real client does. Callers hold mu.
func (f *Fake) write(playlistID string, want []string) ([]string, error) {
	got := slices.Clone(want)
	if f.truncate[playlistID] {
		delete(f.truncate, playlistID)
		if len(got) > 0 {
			got = got[1:]
		}
	}
	f.members[playlistID] = got
	if !slices.Equal(got, want) {
		return slices.Clone(got), model.ErrVerifyFailed{What: fmt.Sprintf("playlist %s has %d tracks, expected %d", playlistID, len(got), len(want))}
	}
	return slices.Clone(got), nil
}

func (f *Fake) DeleteTrack(ctx context.Context, musicID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, FakeCall{Op: OpDelete, MusicIDs: []string{musicID}}); err != nil {
		return err
	}
	if err := f.known([]string{musicID}); err != nil {
		return err
	}
	delete(f.tracks, musicID)
	for pid, ids := range f.members {
		f.members[pid] = slices.DeleteFunc(ids, func(id string) bool { return id == musicID })
	}
	return nil
}

func (f *Fake) Refresh(ctx context.Context, musicID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, FakeCall{Op: OpRefresh, MusicIDs: []string{musicID}}); err != nil {
		return err
	}
	return f.known([]string{musicID})
}
