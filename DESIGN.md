# Music library manager: architecture outline

Nov 16, 2026 · @Brenno Cordeiro

## Review: problems found and fixes

The earlier design had fourteen problems, listed most severe first. The first five can lose files, library entries or playlist memberships; the rest cost reliability or speed.

| # | Problem in the earlier design | Fix in this outline |
| --- | --- | --- |
| 1 | `os.Rename` silently overwrites, and a flat master dir makes name clashes likely | No-clobber move; a collision returns an error the UI resolves |
| 2 | A track was identified by its path, which changes on promote | Stable `TrackID`; the path is an attribute |
| 3 | Music may copy a file on add and then track the copy | The copy setting must be off; every add reads the location back |
| 4 | The reconciler could mass-add the whole master dir if path matching is wrong | One normalization function; a safety valve on bulk adds |
| 5 | A delete that removes the Music entry and then fails on the file loses playlist memberships | Music goes first, so the common failure changes nothing; the rare case is reported in full |
| 6 | Music cannot look a track up by file path | Bulk snapshot of IDs and locations; `add` is idempotent as a fallback |
| 7 | The snapshot goes stale when playlists change in Music.app, and Sync Library may alter a playlist after a write | Refresh on focus; mutate by persistent ID; verify after every write |
| 8 | Paths and playlist names interpolated into scripts break on quotes | Arguments by `argv`, results as JSON |
| 9 | Nothing handled Music not running or a denied automation permission | `Unavailable` state; detect error -1743; `autolaunch` flag |
| 10 | fsnotify on macOS holds one descriptor per file in a watched directory | Polling sweeps |
| 11 | A queue of paths inside mpv goes stale when files move | App-owned queue of `TrackID` |
| 12 | The native reader and kid3 can disagree on frames, and ID3 tags rarely carry a duration | Native reader for list columns, kid3 for the editor; duration from the Xing header, corrected by mpv |
| 13 | `time-pos` events flood the UI, and a slow UI could block producers | Throttled state events; two delivery classes |
| 14 | Trash is not always available: `/usr/bin/trash` ships only from macOS 15 | `delete_mode` setting; Finder fallback |

Smaller fixes folded in: `--no-terminal` and a pidfile for mpv, a core that no longer imports Bubble Tea, and a library that is the sole owner of the index and hands out copies.

## Decisions and config

The master dir is one flat namespace, so name collisions on promote are the decision that matters most.

```toml
inbox_dirs    = ["~/Downloads/music", "~/Soulseek/complete"]
master_dir    = "~/Music/Master"          # flat: files sit directly in it
required_tags = ["title", "artist", "album"]
delete_mode   = "trash"                   # or "remove"

[music]
enabled    = true
autolaunch = false   # do not start Music.app just because this app started
```

- **Filenames.** Promote keeps the file's existing name. The app never builds or changes filenames.
- **Collisions.** Two inbox files can share a name. Promote never overwrites: it returns a collision error naming the existing master track, and the UI decides what to do.
- **Roles.** A track under an inbox dir is `Inbox`; under the master dir, `Master`. Playback and tag editing work on both. Playlists apply to `Master` only.
- **Invariant.** Every `Master` track is in the Music library. The reconciler enforces it; promote alone does not have to.
- **Music settings required.** In Music > Settings > Files, "Copy files to Media folder when adding to library" must be off, and the master dir must sit outside the Media folder. Otherwise Music tracks its own copy and may sort it into artist and album folders ([Apple: Files settings](https://support.apple.com/guide/music/mus0f5accea/mac)). The app checks this on every add (see Apple Music).

## Package layout

&#91;embedded content: package layout · ui, app, four core packages, four externals\]

Calls go down and events come back up through one channel. No package under `app` imports another, and nothing below `ui` imports Bubble Tea.

| Package | Responsibility |
| --- | --- |
| `cmd` | Read config, build the pieces, start the program |
| `config` | TOML: roots, filename rule, delete mode, Music options |
| `ui` | Bubble Tea model; turns events into messages; view state only |
| `app` | Facade for every operation that spans packages; play queue; reconciler |
| `library` | Index, cache, sweeps, path normalization |
| `tags` | Native summary reader; kid3-cli full reader and writer |
| `player` | mpv process and IPC |
| `music` | Snapshot and mutations through `osascript`; `darwin` build tag with a no-op stub elsewhere |
| `fsops` | No-clobber move, cross-volume copy, trash |
| `events` | Event types and the bus shared by all packages |

`tags`, `player` and `music` are interfaces, each with a fake for tests.

## Data model

A track has an internal ID that never changes; its path is only an attribute. Promote and rename change the path, so nothing outside the library may use a path as identity.

```go
type TrackID int64

type Role uint8 // Inbox | Master

type Track struct {
    ID       TrackID
    Path     string        // absolute, cleaned; changes on promote
    Role     Role          // derived from the root Path sits under
    Size     int64
    ModTime  time.Time
    Summary  TagSummary    // title, artist, album, album artist, track no., year, genre
    Duration time.Duration // 0 = not known yet
    MusicID  string        // Music persistent ID; "" = not in the library
}

type Playlist struct {
    ID       string // Music persistent ID, never the name
    Name     string
    Editable bool   // false for smart playlists and folders
}
```

- **Values, not pointers.** The library hands out copies of `Track`. The UI never holds a pointer into the index.
- **Playlist membership** lives in the `music` package as `MusicID -> []PlaylistID`, not on `Track`. It changes outside the app and is refreshed as a whole.
- **Summary vs full tags.** `TagSummary` is the handful of fields the list shows. The complete frame set is read on demand for the tag editor (see Tags).

## Library: index, cache, scan, watch

The `library` package owns the only copy of the index and is its only writer. Everything else gets copies and events.

- **Index.** In memory: `map[TrackID]Track` plus `path -> TrackID`, behind one `RWMutex`. Queries return copies or ID slices.
- **Cache.** One SQLite file through a pure-Go driver (no cgo), keyed by path, holding size, mtime, summary tags and duration. It is disposable: delete it or bump the schema version and the next start does a full scan.
- **Startup.** Load the cache, emit `LibraryLoaded` so the list shows at once, then scan in the background and correct it.
- **Scan.** `os.ReadDir` plus `stat` per root: non-recursive for the flat master dir, recursive for inboxes. Re-read tags only where size or mtime changed, with a worker pool. Skip dotfiles, temp files and anything that is not `.mp3`. Emit changes in batches.
- **Path normalization.** One function used everywhere: absolute, symlinks resolved, Unicode NFC. Compare case-insensitively on a case-insensitive volume. Paths coming back from Music go through the same function.
- **Watching = polling.** A `Watcher` interface with one implementation to start: a timed `ReadDir` + `stat` sweep. Inboxes sweep every few seconds; master sweeps on startup, on terminal focus and on a manual refresh key. fsnotify is avoided on macOS because its kqueue backend holds one open descriptor per file in a watched directory ([fsnotify source](https://fuchsia.googlesource.com/third_party/github.com/fsnotify/fsnotify/+/refs/tags/v1.6.0/backend_kqueue.go)).
- **Partial downloads.** Ignore `.part` and `.crdownload`, and index an inbox file only after its size is unchanged across two sweeps.
- **Idempotent updates.** When the app writes tags, both the write path and the next sweep see the change. `Upsert` compares old and new and emits nothing when they match.

## Tags: two tiers

List columns come from a fast native reader. The tag editor reads and writes through kid3-cli, so what the editor shows is exactly what kid3 will save.

- **Summary, in bulk.** A native Go ID3 reader (for example `dhowden/tag`, read-only) fills `TagSummary` during scans. No process per file.
- **Duration.** Best effort at scan time from the MP3's Xing header, for example with `ComputeAudioDuration` in [derat/mpeg](https://pkg.go.dev/codeberg.org/derat/mpeg), which assumes constant bitrate when there is no Xing header. mpv corrects it the first time the track plays.
- **Full tags, one file.** Opening the editor makes one kid3-cli call. kid3-cli takes commands as JSON objects such as `{"method":"get","params":["artist",2]}` and answers in JSON, so there is no text scraping ([Kid3 handbook](https://kid3.sourceforge.io/kid3_en.html)).
- **Write.** One kid3-cli invocation per edit, one `-c` per changed frame; kid3 runs them in order and saves at the end. Check the exit status and the JSON `error` object, and wrap the call in a context timeout.
- **Same value on many files.** One call with all the paths; kid3 selects them and applies the command to the selection.
- **After a write.** Re-read the summary natively, `Upsert`, and if the track has a `MusicID`, ask Music to `refresh` it.
- **Editing the playing track.** kid3 may rewrite the file under mpv. Allow it; if mpv then reports an error, reload the track at the saved position.

```go
type Store interface {
    Summary(path string) (TagSummary, error)                           // native
    Full(ctx context.Context, path string) ([]Frame, error)            // kid3-cli
    Write(ctx context.Context, paths []string, ch []FrameChange) error // kid3-cli
}
```

## Player: one mpv process, app-owned queue

mpv runs once for the app's lifetime and is driven over its JSON IPC socket. The play queue lives in the app, not in mpv.

- **Process.** `mpv --idle --no-video --no-terminal --input-ipc-server=<socket>`. `--no-terminal` keeps mpv off the TTY the TUI owns. The socket sits in a per-run temp dir. A pidfile lets the next start kill an orphan left by a crash.
- **Protocol.** One line of JSON per message. One reader goroutine separates replies, matched by `request_id`, from events. Writes go through one mutex.
- **State.** `observe_property` on `pause`, `time-pos`, `duration` and `volume`, plus the `end-file` event. The player folds them into one `PlayerState` value.
- **Throttling.** `time-pos` changes many times a second. The player emits `PlayerState` at most about 4 times a second, latest wins. Discrete events (track ended, load failed) go out at once and are never dropped.
- **Queue.** A `[]TrackID` in `app`. When a track ends, the app resolves the next ID to its current path and sends `loadfile`. A queue of paths inside mpv would go stale on promote, rename or delete.
- **Duration backfill.** When mpv reports `duration` for a track whose `Duration` is 0, the app stores it in the index and cache.
- **Failure.** If the socket closes unexpectedly, emit `PlayerDown`, restart mpv and re-observe. Do not auto-resume playback.

Protocol reference: [mpv JSON IPC](https://sources.debian.org/src/mpv/0.41.0-2/DOCS/man/ipc.rst).

## Apple Music

Music is reached only through `osascript`, one script at a time, and every track is addressed by persistent ID after the first lookup.

- **Client.** A `music.Client` interface. Scripts are JXA files embedded with `go:embed`. Arguments go in as `argv` and results come back as JSON on stdout. Nothing is interpolated into script source, so quotes in paths and playlist names cannot break a script.
- **One worker.** All calls run through a single goroutine with a context timeout. Apple Events are slow and must not interleave.
- **Snapshot.** Music cannot filter tracks by file location in a `whose` clause ([Doug's AppleScripts](https://dougscripts.com/itunes/2010/12/get-a-track-reference-from-a-file-path/)). So the app reads the persistent IDs and locations of all file tracks in bulk, then each user playlist's track IDs. It builds `path -> MusicID`, `MusicID -> []PlaylistID` and the playlist list.
- **Snapshot refresh.** On startup, on terminal focus, on a manual key and after any failed mutation. The app's own successful mutations patch the snapshot in place.
- **Add.** `add` the file, then read back its persistent ID and location. If the location is not the master path, the copy setting is on: stop adding, report it, and leave cleanup to the user. `add` on a file already in the library returns the existing track and does not duplicate it (same source), so retries are safe.
- **Mutations.** `AddToPlaylist`, `RemoveFromPlaylist`, `DeleteTrack` (removes the library entry only; the file is the app's job) and `Refresh`. Each one re-reads what it changed and compares.
- **Playlists.** Addressed by persistent ID, never by name. Smart playlists and folders are listed with `Editable = false`.
- **Music not running.** With `autolaunch = false` the client reports `Unavailable`. Adds wait in the reconciler. Playlist edits and master deletes fail with a clear error and an offer to launch Music.
- **Permission.** macOS attaches the automation permission to the terminal app that runs the binary. After a denial every script fails with error -1743; the app detects that code and points the user to Privacy & Security > Automation ([Scripting OS X](https://scriptingosx.com/2020/09/avoiding-applescript-security-and-privacy-requests/)).
- **Sync Library risk.** One unanswered developer report describes a playlist with catalog tracks being cut short after a just-added local file was put into it ([Apple Developer Forums](https://developer.apple.com/forums/thread/844902)). The verify-after-write rule above is the mitigation; test this on your own library.

## Operations

Every operation that touches more than one package lives in `app`, runs off the UI goroutine and reports by event.

### Promote (inbox to master)

1. Check `required_tags`. Fail with the list of missing ones.
2. Look the file's current name up among master tracks, case-insensitively. On a hit, return `ErrCollision` carrying the existing `TrackID`.
3. Move without clobbering. Claim the name first with `os.OpenFile(dst, O_CREATE|O_EXCL)`, which fails if the name exists, then `os.Rename(src, dst)` over that empty placeholder. A bare `os.Rename` would silently replace an existing file. Other volume: copy to a dot-prefixed temp file in master, `fsync`, then claim and rename the same way and remove the source.
4. Update path and role in the index and cache under the same `TrackID`. Emit `TracksChanged`.
5. Hand the track to the reconciler, which adds it to Music.

A batch promote runs these steps per track, in sequence. One failure does not stop the rest; a summary event closes the batch.

### Delete

1. Drop the track from the queue. If it is playing, stop playback first.
2. If it is a `Master` track with a `MusicID`, delete the Music entry. On failure stop here: nothing has changed.
3. Trash or remove the file per `delete_mode`. Trash uses `/usr/bin/trash`, which ships from macOS 15, and falls back to Finder scripting on older systems. Files trashed by the command get no Put Back ([Michael Tsai](https://mjtsai.com/blog/2025/08/26/the-trash-command)).
4. Remove the track from the index and cache. Emit `TrackRemoved`.

If step 3 fails after step 2 succeeded, the file is still in master, so the reconciler re-adds it to Music. That keeps the invariant, but its playlist memberships are gone. The error message must say so.

### Edit tags

1. `tags.Write` through kid3-cli.
2. Re-read the summary and `Upsert`.
3. If the track has a `MusicID`, `music.Refresh`.

### Playlists

Add or remove by `MusicID` and playlist ID, verify, patch the snapshot, emit `PlaylistsChanged`. Offered only for tracks with a `MusicID` and playlists with `Editable = true`.

### Reconcile

Runs after each Music snapshot and each master sweep, and only with a snapshot newer than the sweep.

- **Master track with no `MusicID`:** `add`, verify the location, store the ID.
- **Music entry inside master whose file is missing:** emit `DeadEntry`. Never auto-delete.
- **Safety valve:** if one pass wants to add more than a set number of tracks, pause and ask. A path-matching bug would otherwise look like "nothing is in Music" and trigger a mass add. The first run on an existing master dir trips the valve by design, and the user confirms the import once.

## Feeding the UI

The core knows nothing about Bubble Tea. It exposes methods and one event channel, and `ui` adapts both.

- **Requests.** `ui` wraps each `app` method in a `tea.Cmd` that returns a result message. `Update` never blocks and never touches files.
- **Events.** Packages publish to `events.Bus`. `ui` runs one `tea.Cmd` that waits on the bus, returns the event as a message and re-arms itself.
- **Two delivery classes.** State events (`PlayerState`, `ScanProgress`) are latest-wins: one slot per kind, overwritten while the UI is behind. Change events (`TracksChanged`, `TrackRemoved`, `PlaylistsChanged`, `DeadEntry`, errors) are queued and never dropped. A slow UI cannot block mpv's reader or the scanner.
- **Batches.** `TracksChanged` carries a slice of `Track` values, coalesced during scans so the UI gets a few events a second, not one per file.
- **View state.** `ui` holds a `[]TrackID` for the current filter and sort, a cursor, and the `Track` values for the visible rows only. It renders only those rows.
- **Queries.** `library.Query(filter, sort)` returns `[]TrackID` and runs in a goroutine through a `tea.Cmd`. Each request carries a generation number and `ui` drops any result older than the latest. Typing is debounced.
- **Focus.** Bubble Tea can report terminal focus. `ui` forwards focus-gained to `app`, which runs the master sweep and the Music snapshot refresh.
- **Version note.** Bubble Tea v2 is current, imported as `charm.land/bubbletea/v2`. `View` returns a `tea.View` struct and focus reporting is a field on it ([releases](https://github.com/charmbracelet/bubbletea/releases)). The command and message boundary used here is the same in v1 and v2.

## Build order

Prototype the Music scripts first: they are the only part that can invalidate the design.

1. **Music spike, scripts only.** On the real library: time the bulk snapshot; `add` and read back the location with the copy setting off and on; confirm deleting an entry leaves the file; confirm `refresh` picks up a tag edit; add and remove a playlist entry by persistent ID; repeat the playlist test with Sync Library on.
2. **`fsops`.** No-clobber move, cross-volume copy, trash. Test names that differ only by case.
3. **`library`.** Index, cache, sweep, events. Test against a generated directory of several thousand files.
4. **`tags`.** Summary reader, kid3 JSON read and write.
5. **`player`.** mpv IPC and throttled state.
6. **`app`.** Promote, delete, edit and the queue, against fakes for `music`, `player` and `tags`.
7. **`music` and the reconciler,** with the safety valve.
8. **`ui`.**

Open questions:

- [x] On a name collision, should promote always ask, or default to skipping the file?

Ask

- [x] Is Sync Library on for this library? It decides how much of the playlist verification matters.

Sync Library is On\

