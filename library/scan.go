package library

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/pathnorm"
)

// Tuning constants, from TASKS.md.
const (
	inboxInterval = 3 * time.Second
	batchInterval = 250 * time.Millisecond
	batchMax      = 500
)

// file is a track file found on disk.
type file struct {
	path  string
	key   string
	size  int64
	mtime time.Time
}

// isTrackName keeps .mp3 files that are not dotfiles. Partial downloads
// (.part, .crdownload) fail the extension test.
func isTrackName(name string) bool {
	return !strings.HasPrefix(name, ".") && strings.EqualFold(filepath.Ext(name), ".mp3")
}

// listRoot returns the track files under r. complete is false when some part
// of the root could not be read, so absent files must not be treated as
// deleted.
func listRoot(r *root) ([]file, bool, error) {
	var files []file
	complete := true
	if !r.recursive {
		entries, err := os.ReadDir(r.dir)
		if err != nil {
			return nil, false, err
		}
		for _, e := range entries {
			if !isTrackName(e.Name()) {
				continue
			}
			f, ok, err := statFile(r, pathnorm.Child(r.dir, e.Name()))
			if err != nil {
				complete = false
			} else if ok {
				files = append(files, f)
			}
		}
		return files, complete, nil
	}
	err := filepath.WalkDir(r.dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			complete = false
			return nil
		}
		if d.IsDir() {
			if p != r.dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !isTrackName(d.Name()) {
			return nil
		}
		f, ok, err := statFile(r, pathnorm.Child(filepath.Dir(p), d.Name()))
		if err != nil {
			complete = false
		} else if ok {
			files = append(files, f)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return files, complete, nil
}

// statFile stats path. ok is false for a directory or an entry that vanished
// after it was listed.
func statFile(r *root, path string) (file, bool, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return file{}, false, nil
	}
	if err != nil {
		return file{}, false, err
	}
	if fi.IsDir() {
		return file{}, false, nil
	}
	return file{
		path:  path,
		key:   pathnorm.Key(path, r.caseSensitive),
		size:  fi.Size(),
		mtime: fi.ModTime(),
	}, true, nil
}

// settled reports whether an inbox file has kept its size since the previous
// sweep. The first sighting only records the size. A settled file is
// forgotten here, and the caller indexes it.
func settled(r *root, f file) bool {
	prev, seen := r.pending[f.key]
	if seen && prev == f.size {
		delete(r.pending, f.key)
		return true
	}
	r.pending[f.key] = f.size
	return false
}

// scanRoot brings the index in line with the files under r. Only the watch
// goroutine calls it. A missing root returns an error wrapping
// fs.ErrNotExist, and nothing is removed.
func (l *Library) scanRoot(ctx context.Context, r *root) error {
	if _, err := os.Stat(r.dir); err != nil {
		return err
	}
	files, complete, err := listRoot(r)
	if err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(files))
	var moved []model.Track // known tracks whose path changed; no read needed
	var jobs []file         // new or changed files that need a summary read
	for _, f := range files {
		seen[f.key] = struct{}{}
		if t, ok := l.idx.byKey(f.key); ok && t.Size == f.size && t.ModTime.Equal(f.mtime) {
			delete(r.pending, f.key)
			if t.Path != f.path {
				t.Path = f.path
				moved = append(moved, t)
			}
			continue
		}
		if r.role == model.Inbox && !settled(r, f) {
			continue
		}
		jobs = append(jobs, f)
	}
	if r.role == model.Inbox {
		for k := range r.pending {
			if _, ok := seen[k]; !ok {
				delete(r.pending, k)
			}
		}
	}

	total := len(files)
	resolved := total - len(jobs)
	l.commit(moved)
	l.progress(resolved, total, false)

	results := l.readAll(ctx, jobs)
	tick := time.NewTicker(batchInterval)
	defer tick.Stop()
	var batch []model.Track
	open := true
	for open {
		select {
		case t, ok := <-results:
			if !ok {
				open = false
				break
			}
			batch = append(batch, t)
			resolved++
			if len(batch) >= batchMax {
				l.commit(batch)
				batch = batch[:0]
				l.progress(resolved, total, false)
			}
		case <-tick.C:
			if len(batch) > 0 {
				l.commit(batch)
				batch = batch[:0]
				l.progress(resolved, total, false)
			}
		}
	}
	l.commit(batch)

	if err := ctx.Err(); err != nil {
		return err
	}
	if complete {
		var gone []model.TrackID
		for _, t := range l.idx.under(r) {
			if _, ok := seen[pathnorm.Key(t.Path, r.caseSensitive)]; !ok {
				gone = append(gone, t.ID)
			}
		}
		if removed := l.idx.remove(gone); len(removed) > 0 {
			if err := l.cache.write(nil, removed); err != nil {
				l.report("cache", err)
			}
			l.sink.Publish(events.TrackRemoved{IDs: removed})
		}
	}
	l.progress(total, total, true)
	return nil
}

// readAll reads the summaries of jobs with a pool of workers and streams the
// results. The channel closes when every job is done, or early on cancel.
func (l *Library) readAll(ctx context.Context, jobs []file) <-chan model.Track {
	feed := make(chan file)
	out := make(chan model.Track)
	go func() {
		defer close(feed)
		for _, f := range jobs {
			select {
			case feed <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < l.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range feed {
				out <- l.readOne(f)
			}
		}()
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

// readOne builds a track from a file. A file whose tags cannot be read is
// still indexed, with an empty summary, and the error is reported.
func (l *Library) readOne(f file) model.Track {
	t := model.Track{Path: f.path, Size: f.size, ModTime: f.mtime}
	s, d, err := l.reader.Summary(f.path)
	if err != nil {
		l.report("read tags "+f.path, err)
		return t
	}
	t.Summary = s
	t.Duration = d
	return t
}

// commit stores tracks in the index and the cache and publishes the ones
// that changed.
func (l *Library) commit(ts []model.Track) {
	if len(ts) == 0 {
		return
	}
	var changed []model.Track
	for _, t := range ts {
		stored, ok, err := l.idx.upsert(t)
		if err != nil {
			l.report("index "+t.Path, err)
			continue
		}
		if ok {
			changed = append(changed, stored)
		}
	}
	if len(changed) == 0 {
		return
	}
	if err := l.cache.write(changed, nil); err != nil {
		l.report("cache", err)
	}
	l.sink.Publish(events.TracksChanged{Tracks: changed})
}

func (l *Library) progress(scanned, total int, done bool) {
	l.sink.Publish(events.ScanProgress{Scanned: scanned, Total: total, Done: done})
}

func (l *Library) report(op string, err error) {
	l.sink.Publish(events.Error{Op: op, Err: err})
}
