package library

import (
	"fmt"
	"sync"
	"time"

	"github.com/brennop/crates/model"
	"github.com/brennop/crates/pathnorm"
)

// index is the in-memory track table. Its methods take the lock and hand out
// copies. The roots are fixed after Open, so keyFor needs no lock.
type index struct {
	mu     sync.RWMutex
	roots  []*root // master first, then the inboxes
	tracks map[model.TrackID]model.Track
	keys   map[string]model.TrackID // comparison key -> ID
	next   model.TrackID
}

func newIndex(roots []*root) *index {
	return &index{
		roots:  roots,
		tracks: make(map[model.TrackID]model.Track),
		keys:   make(map[string]model.TrackID),
		next:   1,
	}
}

// rootFor returns the root path sits under. The master wins if roots overlap.
func (x *index) rootFor(path string) (*root, bool) {
	for _, r := range x.roots {
		if pathnorm.WithinKey(path, r.dir, r.caseSensitive) {
			return r, true
		}
	}
	return nil, false
}

// keyFor returns the comparison key of a path under some root.
func (x *index) keyFor(path string) (string, *root, error) {
	r, ok := x.rootFor(path)
	if !ok {
		return "", nil, fmt.Errorf("%s is outside every root", path)
	}
	return pathnorm.Key(path, r.caseSensitive), r, nil
}

// upsert stores t and reports whether anything changed. A zero ID takes the
// ID of the track already under t's key, or a new one. The role comes from
// the root; MusicID is kept from the stored track, since the scan never sets it.
func (x *index) upsert(t model.Track) (model.Track, bool, error) {
	key, r, err := x.keyFor(t.Path)
	if err != nil {
		return model.Track{}, false, err
	}
	t.Role = r.role

	x.mu.Lock()
	defer x.mu.Unlock()
	if t.ID == 0 {
		if id, ok := x.keys[key]; ok {
			t.ID = id
		} else {
			t.ID = x.next
		}
	}
	if other, ok := x.keys[key]; ok && other != t.ID {
		return model.Track{}, false, fmt.Errorf("%s is already track %d", t.Path, other)
	}
	old, exists := x.tracks[t.ID]
	if exists {
		t.MusicID = old.MusicID
		if sameTrack(old, t) {
			return old, false, nil
		}
		oldKey, _, _ := x.keyFor(old.Path)
		delete(x.keys, oldKey)
	}
	x.tracks[t.ID] = t
	x.keys[key] = t.ID
	if t.ID >= x.next {
		x.next = t.ID + 1
	}
	return t, true, nil
}

// move gives track id a new path. Any track already at that path is
// displaced and removed: the file it indexed is now this one. It returns the
// moved track and the displaced ID, or 0.
func (x *index) move(id model.TrackID, path string) (model.Track, model.TrackID, error) {
	key, r, err := x.keyFor(path)
	if err != nil {
		return model.Track{}, 0, err
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	old, ok := x.tracks[id]
	if !ok {
		return model.Track{}, 0, ErrUnknownTrack
	}
	oldKey, _, _ := x.keyFor(old.Path)
	var displaced model.TrackID
	if other, ok := x.keys[key]; ok && other != id {
		displaced = other
		delete(x.tracks, other)
	}
	delete(x.keys, oldKey)
	t := old
	t.Path = path
	t.Role = r.role
	x.tracks[id] = t
	x.keys[key] = id
	return t, displaced, nil
}

// remove drops the given tracks and returns the IDs that were present.
func (x *index) remove(ids []model.TrackID) []model.TrackID {
	x.mu.Lock()
	defer x.mu.Unlock()
	var gone []model.TrackID
	for _, id := range ids {
		t, ok := x.tracks[id]
		if !ok {
			continue
		}
		key, _, _ := x.keyFor(t.Path)
		if x.keys[key] == id {
			delete(x.keys, key)
		}
		delete(x.tracks, id)
		gone = append(gone, id)
	}
	return gone
}

// setDuration fills in a duration the track does not have yet. It reports
// whether it stored one.
func (x *index) setDuration(id model.TrackID, d time.Duration) (model.Track, bool, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	t, ok := x.tracks[id]
	if !ok {
		return model.Track{}, false, ErrUnknownTrack
	}
	if t.Duration != 0 || d == 0 {
		return t, false, nil
	}
	t.Duration = d
	x.tracks[id] = t
	return t, true, nil
}

func (x *index) byKey(key string) (model.Track, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	id, ok := x.keys[key]
	if !ok {
		return model.Track{}, false
	}
	return x.tracks[id], true
}

// get returns copies of the tracks with the given IDs, in the order given.
// Unknown IDs are skipped.
func (x *index) get(ids []model.TrackID) []model.Track {
	x.mu.RLock()
	defer x.mu.RUnlock()
	out := make([]model.Track, 0, len(ids))
	for _, id := range ids {
		if t, ok := x.tracks[id]; ok {
			out = append(out, t)
		}
	}
	return out
}

// under returns copies of the tracks that sit under r.
func (x *index) under(r *root) []model.Track {
	x.mu.RLock()
	defer x.mu.RUnlock()
	var out []model.Track
	for _, t := range x.tracks {
		if got, ok := x.rootFor(t.Path); ok && got == r {
			out = append(out, t)
		}
	}
	return out
}

func (x *index) count() int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return len(x.tracks)
}

func sameTrack(a, b model.Track) bool {
	return a.ID == b.ID &&
		a.Path == b.Path &&
		a.Role == b.Role &&
		a.Size == b.Size &&
		a.ModTime.Equal(b.ModTime) &&
		a.Summary == b.Summary &&
		a.Duration == b.Duration &&
		a.MusicID == b.MusicID
}
