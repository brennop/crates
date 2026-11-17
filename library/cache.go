package library

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/brennop/crates/model"
)

// schemaVersion is stored in schema_version. Bump it when the table changes:
// the next start then deletes the cache and rebuilds it with a full scan.
const schemaVersion = 2

var createStatements = []string{
	`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
	`CREATE TABLE tracks (
		id           INTEGER PRIMARY KEY,
		path         TEXT    NOT NULL UNIQUE,
		size         INTEGER NOT NULL,
		mtime_ns     INTEGER NOT NULL,
		title        TEXT    NOT NULL,
		artist       TEXT    NOT NULL,
		album        TEXT    NOT NULL,
		album_artist TEXT    NOT NULL,
		track_no     INTEGER NOT NULL,
		year         INTEGER NOT NULL,
		genre        TEXT    NOT NULL,
		comment      TEXT    NOT NULL,
		bpm          REAL    NOT NULL,
		key          TEXT    NOT NULL,
		duration_ns  INTEGER NOT NULL
	)`,
}

const trackColumns = `id, path, size, mtime_ns, title, artist, album, album_artist, track_no, year, genre, comment, bpm, key, duration_ns`

const upsertSQL = `INSERT INTO tracks (` + trackColumns + `)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		path = excluded.path, size = excluded.size, mtime_ns = excluded.mtime_ns,
		title = excluded.title, artist = excluded.artist, album = excluded.album,
		album_artist = excluded.album_artist, track_no = excluded.track_no,
		year = excluded.year, genre = excluded.genre, comment = excluded.comment,
		bpm = excluded.bpm, key = excluded.key, duration_ns = excluded.duration_ns`

// cache is the SQLite copy of the index. It is disposable: losing it costs a
// full scan. Each write is one transaction, so a batch lands whole or not at
// all.
type cache struct {
	path string
	db   *sql.DB
}

// openCache opens the cache at path and returns the tracks it holds. A cache
// of another schema version, or one that cannot be read, is deleted and
// replaced with an empty one.
func openCache(path string) (*cache, []model.Track, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, err
	}
	c, tracks, err := openAndRead(path)
	if err == nil {
		return c, tracks, nil
	}
	if err := removeCacheFiles(path); err != nil {
		return nil, nil, fmt.Errorf("cache: %w", err)
	}
	c, err = createCache(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cache: %w", err)
	}
	return c, nil, nil
}

func openAndRead(path string) (*cache, []model.Track, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, nil, err
	}
	c := &cache{path: path, db: db}
	tracks, err := c.readAll()
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return c, tracks, nil
}

func (c *cache) readAll() ([]model.Track, error) {
	var version int
	if err := c.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		return nil, err
	}
	if version != schemaVersion {
		return nil, fmt.Errorf("cache schema %d, want %d", version, schemaVersion)
	}
	rows, err := c.db.Query(`SELECT ` + trackColumns + ` FROM tracks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Track
	for rows.Next() {
		var (
			t              model.Track
			id, mtime, dur int64
		)
		if err := rows.Scan(&id, &t.Path, &t.Size, &mtime,
			&t.Summary.Title, &t.Summary.Artist, &t.Summary.Album, &t.Summary.AlbumArtist,
			&t.Summary.TrackNumber, &t.Summary.Year, &t.Summary.Genre,
			&t.Summary.Comment, &t.Summary.BPM, &t.Summary.Key, &dur); err != nil {
			return nil, err
		}
		t.ID = model.TrackID(id)
		t.ModTime = time.Unix(0, mtime)
		t.Duration = time.Duration(dur)
		out = append(out, t)
	}
	return out, rows.Err()
}

func createCache(path string) (*cache, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	c := &cache{path: path, db: db}
	for _, stmt := range createStatements {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, schemaVersion); err != nil {
		db.Close()
		return nil, err
	}
	return c, nil
}

// openDB opens the file on one connection, so the pragmas apply to all of
// its use. A file that is not a database fails here or on the first read.
func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{`PRAGMA journal_mode = WAL`, `PRAGMA synchronous = NORMAL`} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

// write applies one batch: removals first, then upserts. A row already at an
// upserted track's path under another ID is stale (the index has already
// dropped that track), so it goes too.
func (c *cache) write(upserts []model.Track, removed []model.TrackID) error {
	if len(upserts) == 0 && len(removed) == 0 {
		return nil
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range removed {
		if _, err := tx.Exec(`DELETE FROM tracks WHERE id = ?`, int64(id)); err != nil {
			return err
		}
	}
	for _, t := range upserts {
		if _, err := tx.Exec(`DELETE FROM tracks WHERE path = ? AND id <> ?`, t.Path, int64(t.ID)); err != nil {
			return err
		}
		if _, err := tx.Exec(upsertSQL,
			int64(t.ID), t.Path, t.Size, t.ModTime.UnixNano(),
			t.Summary.Title, t.Summary.Artist, t.Summary.Album, t.Summary.AlbumArtist,
			t.Summary.TrackNumber, t.Summary.Year, t.Summary.Genre,
			t.Summary.Comment, t.Summary.BPM, t.Summary.Key, int64(t.Duration)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *cache) close() error {
	return c.db.Close()
}

// removeCacheFiles deletes the database and its SQLite side files. Missing
// files are not an error.
func removeCacheFiles(path string) error {
	var errs []error
	for _, p := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
