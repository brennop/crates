package tags

import (
	"context"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brennop/crates/model"
)

// Fake is an in-memory Store for tests. Each path holds a list of frames;
// Summary derives a TagSummary from them through the kid3 names listed in
// FakeFieldFrames. Failures are scripted per call kind and path.
type Fake struct {
	mu        sync.Mutex
	files     map[string]*fakeFile
	fail      map[failKey][]error
	missing   bool // behave as if kid3-cli were not installed
	writes    []FakeWrite
	fullCalls int
	closed    bool
}

type fakeFile struct {
	frames   []model.Frame
	duration time.Duration
}

type failKey struct {
	op   string
	path string
}

// FakeWrite records one Write call.
type FakeWrite struct {
	Paths   []string
	Changes []model.FrameChange
}

// Operation names for Fail.
const (
	OpSummary = "summary"
	OpFull    = "full"
	OpWrite   = "write"
)

// FakeFieldFrames maps the frame names the fake reads its summary from to
// TagSummary fields; they are the names kid3-cli uses.
var FakeFieldFrames = map[string]string{
	"Title": model.FieldTitle, "Artist": model.FieldArtist, "Album": model.FieldAlbum,
	"Album Artist": model.FieldAlbumArtist, "Track Number": model.FieldTrackNumber,
	"Date": model.FieldYear, "Genre": model.FieldGenre, "Comment": model.FieldComment,
	"BPM": model.FieldBPM, "Initial Key": model.FieldKey,
}

// Close marks the fake closed; see Closed.
func (f *Fake) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// Closed reports whether Close ran.
func (f *Fake) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// NewFake returns an empty fake.
func NewFake() *Fake {
	return &Fake{files: map[string]*fakeFile{}, fail: map[failKey][]error{}}
}

// Put adds or replaces a file with the given frames and duration.
func (f *Fake) Put(path string, d time.Duration, frames ...model.Frame) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = &fakeFile{frames: append([]model.Frame(nil), frames...), duration: d}
}

// PutSummary adds or replaces a file whose frames hold s.
func (f *Fake) PutSummary(path string, d time.Duration, s model.TagSummary) {
	var frames []model.Frame
	for name, field := range FakeFieldFrames {
		if v, _ := s.Field(field); v != "" {
			frames = append(frames, model.Frame{Name: name, Value: v})
		}
	}
	f.Put(path, d, frames...)
}

// Remove drops a file, as if it were deleted from disk.
func (f *Fake) Remove(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, path)
}

// Fail makes the next calls of op on path return errs, one per call, in
// order. An empty path matches any path. Write fails if any of its paths
// matches.
func (f *Fake) Fail(op, path string, errs ...error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := failKey{op, path}
	f.fail[k] = append(f.fail[k], errs...)
}

// SetMissing makes Full and Write return model.ErrKid3Missing, as when
// kid3-cli is not installed.
func (f *Fake) SetMissing(missing bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.missing = missing
}

// Available reports false after SetMissing(true).
func (f *Fake) Available() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.missing
}

// Writes returns the Write calls that succeeded, in order.
func (f *Fake) Writes() []FakeWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeWrite(nil), f.writes...)
}

// FullCalls returns how many times Full was called.
func (f *Fake) FullCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fullCalls
}

// takeFail pops a scripted error for op on any of paths. Callers hold mu.
func (f *Fake) takeFail(op string, paths ...string) error {
	keys := append(append(make([]string, 0, len(paths)+1), paths...), "")
	for _, p := range keys {
		k := failKey{op, p}
		if errs := f.fail[k]; len(errs) > 0 {
			f.fail[k] = errs[1:]
			return errs[0]
		}
	}
	return nil
}

func (f *Fake) file(path string) (*fakeFile, error) {
	ff, ok := f.files[path]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	return ff, nil
}

func (f *Fake) Summary(path string) (model.TagSummary, time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFail(OpSummary, path); err != nil {
		return model.TagSummary{}, 0, err
	}
	ff, err := f.file(path)
	if err != nil {
		return model.TagSummary{}, 0, err
	}
	var s model.TagSummary
	for _, fr := range ff.frames {
		if fr.Index != 0 {
			continue
		}
		setField(&s, FakeFieldFrames[fr.Name], fr.Value)
	}
	return s, ff.duration, nil
}

func (f *Fake) Full(ctx context.Context, path string) ([]model.Frame, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fullCalls++
	if f.missing {
		return nil, model.ErrKid3Missing{}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.takeFail(OpFull, path); err != nil {
		return nil, err
	}
	ff, err := f.file(path)
	if err != nil {
		return nil, err
	}
	return append([]model.Frame{}, ff.frames...), nil
}

// Write applies the changes to every path, or to none: a missing path or a
// scripted failure leaves all files as they were.
func (f *Fake) Write(ctx context.Context, paths []string, ch []model.FrameChange) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.missing {
		return model.ErrKid3Missing{}
	}
	if len(paths) == 0 || len(ch) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.takeFail(OpWrite, paths...); err != nil {
		return err
	}
	for _, p := range paths {
		if _, err := f.file(p); err != nil {
			return err
		}
	}
	for _, c := range ch {
		if c.Name == "" || c.Index < 0 {
			return fmt.Errorf("tags: bad frame change %+v", c)
		}
	}
	for _, p := range paths {
		ff := f.files[p]
		for _, c := range ch {
			ff.frames = applyChange(ff.frames, c)
		}
	}
	f.writes = append(f.writes, FakeWrite{
		Paths:   append([]string(nil), paths...),
		Changes: append([]model.FrameChange(nil), ch...),
	})
	return nil
}

// applyChange mirrors kid3: set the Index-th frame named Name (any case),
// append it if missing, delete it on an empty value. Indexes are renumbered.
func applyChange(frames []model.Frame, c model.FrameChange) []model.Frame {
	pos, n := -1, 0
	for i, fr := range frames {
		if strings.EqualFold(fr.Name, c.Name) {
			if n == c.Index {
				pos = i
				break
			}
			n++
		}
	}
	switch {
	case pos >= 0 && c.Value == "":
		frames = append(frames[:pos:pos], frames[pos+1:]...)
	case pos >= 0:
		frames[pos].Value = c.Value
	case c.Value != "":
		frames = append(frames, model.Frame{Name: c.Name, Value: c.Value})
	}
	seen := map[string]int{}
	for i := range frames {
		k := strings.ToLower(frames[i].Name)
		frames[i].Index = seen[k]
		seen[k]++
	}
	return frames
}

func setField(s *model.TagSummary, field, v string) {
	switch field {
	case model.FieldTitle:
		s.Title = v
	case model.FieldArtist:
		s.Artist = v
	case model.FieldAlbum:
		s.Album = v
	case model.FieldAlbumArtist:
		s.AlbumArtist = v
	case model.FieldTrackNumber:
		s.TrackNumber = leadingInt(v) // "3/12" is track 3
	case model.FieldYear:
		s.Year = leadingInt(v) // "2001-05-02" is 2001
	case model.FieldGenre:
		s.Genre = v
	case model.FieldComment:
		s.Comment = v
	case model.FieldBPM:
		s.BPM = parseBPM(v)
	case model.FieldKey:
		s.Key = v
	}
}

func leadingInt(v string) int {
	end := 0
	for end < len(v) && v[end] >= '0' && v[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(v[:end])
	return n
}
