# Music library manager: architecture outline

Nov 16, 2026 · @Brenno Cordeiro

## Review: problems found and fixes

The earlier design had fourteen problems, listed most severe first. The first five can lose files, library entries or playlist memberships; the rest cost reliability or speed.

| # | Problem in the earlier design | Fix in this outline |
| --- | --- | --- |
| 1 | `os.Rename` silently overwrites, and a flat master dir makes name clashes likely | No-clobber move; a collision returns an error, and the user picks skip or overwrite |
| 2 | A track was identified by its path, which changes on promote | Stable `TrackID`; the path is an attribute |
| 3 | Music may copy a file on add and then track the copy | The copy setting must be off; every add reads the location back |
| 4 | The reconciler could mass-add the whole master dir if path matching is wrong | One normalization function; a safety valve on bulk adds |
| 5 | A delete that removes the Music entry and then fails on the file loses playlist memberships | Music goes first, so the common failure changes nothing; the rare case is reported in full |
| 6 | Music cannot look a track up by file path | Bulk snapshot of IDs and locations; `add` is idempotent as a fallback |
| 7 | The snapshot goes stale when playlists change in Music.app, and Music may alter a playlist after a write | Refresh on focus; mutate by persistent ID; verify after every write |
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
- **Collisions.** Two inbox files can share a name. Promote never overwrites: it returns a collision error naming the existing master track, and the user picks skip (nothing happens) or overwrite (a separate call; see Operations > Overwrite).
- **Roles.** A track under an inbox dir is `Inbox`; under the master dir, `Master`. Playback and tag editing work on both. Playlists apply to `Master` only.
- **Invariant.** Every `Master` track is in the Music library. The reconciler enforces it; promote alone does not have to.
- **Music settings required.** In Music > Settings > Files, "Copy files to Media folder when adding to library" must be off, and the master dir must sit outside the Media folder. Otherwise Music tracks its own copy and may sort it into artist and album folders ([Apple: Files settings](https://support.apple.com/guide/music/mus0f5accea/mac)). The app checks this on every add (see Apple Music).

Tuning values are constants, not config keys:

| Constant | Value |
| --- | --- |
| Inbox sweep interval | 3 s |
| Scan worker pool | `runtime.NumCPU()` |
| Scan batch flush | every 250 ms or 500 tracks, whichever comes first |
| `PlayerState` throttle | 250 ms |
| mpv startup wait, request timeout | 5 s each |
| kid3-cli read, write timeout | 10 s, 30 s |
| osascript snapshot, other calls | 60 s, 15 s |
| Trash command timeout | 15 s |
| Safety-valve threshold | 100 adds in one reconcile pass |

## Package layout

&#91;embedded content: package layout · ui, app, four core packages, four externals\]

Calls go down and events come back up through one channel. No package under `app` imports another, except the leaf packages `events`, `model` and `pathnorm`, which any package may import. Nothing below `ui` imports Bubble Tea.

The shared types need leaf packages. `Track` cannot live in `library`: `TracksChanged` carries it and `library` publishes to the bus, which would cycle through `events`. `music` needs path normalization but may not import `library`.

| Package | Responsibility |
| --- | --- |
| `cmd` | Read config, build the pieces, start the program |
| `config` | TOML: roots, required tags, delete mode, Music options |
| `ui` | Commands, result messages and the event pump over `app`; the Bubble Tea model and views are the developer's |
| `app` | Facade for every operation that spans packages; play queue; reconciler |
| `library` | Index, cache, sweeps |
| `tags` | Native summary reader; kid3-cli full reader and writer |
| `player` | mpv process and IPC |
| `music` | Snapshot and mutations through `osascript`; `darwin` build tag with a no-op stub elsewhere |
| `fsops` | No-clobber move, cross-volume copy, trash |
| `events` | Event types and the bus shared by all packages |
| `model` | Shared types (`Track`, `TagSummary`, `Playlist`, …) and typed errors |
| `pathnorm` | Path normalization and case-sensitivity detection |

`tags`, `player` and `music` are interfaces, each with a fake for tests.

## Data model

A track has an internal ID that never changes; its path is only an attribute. Promote and rename change the path, so nothing outside the library may use a path as identity. The ID is stored in the cache, so it also survives restarts. These types live in `model`.

```go
type TrackID int64

type Role uint8 // Inbox | Master

type Track struct {
    ID       TrackID
    Path     string        // absolute, cleaned; changes on promote
    Role     Role          // derived from the root Path sits under
    Size     int64
    ModTime  time.Time
    Summary  TagSummary    // title, artist, album, album artist, track no., year, genre, comment, BPM, key
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
- **Summary vs full tags.** `TagSummary` is the handful of fields the list shows, sorts and filters on: title, artist, album, album artist, track no., year, genre, comment, BPM and key. The complete frame set is read on demand for the tag editor (see Tags).
- **BPM and key.** Stored as text, as the file has them: BPM from the `TBPM` frame, key from `TKEY` (free text such as `Am` or `8A`). `TagSummary.BPM` is a number when the frame parses as one, and `0` otherwise, and the editor shows the raw frame text. Neither field is a `dhowden/tag` field, so the native reader takes both from its raw frame map.

## Library: index, cache, scan, watch

The `library` package owns the only copy of the index and is its only writer. Everything else gets copies and events.

- **Index.** In memory: `map[TrackID]Track` plus `path -> TrackID`, behind one `RWMutex`. Queries return copies or ID slices. New IDs come from a counter that starts at the cache's highest ID plus one.
- **Cache.** One SQLite file through a pure-Go driver (no cgo). Each row is keyed by `TrackID`, has a unique path, and holds size, mtime, summary tags and duration. Promote updates the row's path. It is disposable: delete it or bump the schema version and the next start does a full scan.
- **Startup.** Load the cache, emit `LibraryLoaded` so the list shows at once, then scan in the background and correct it.
- **Scan.** `os.ReadDir` plus `stat` per root: non-recursive for the flat master dir, recursive for inboxes. Re-read tags only where size or mtime changed, with a worker pool. Skip dotfiles, temp files and anything that is not `.mp3`. Emit changes in batches.
- **Path normalization.** One function, in `pathnorm`, used everywhere: absolute, symlinks resolved, Unicode NFC. Compare case-insensitively on a case-insensitive volume. Paths coming back from Music go through the same function.
- **Watching = polling.** A `Watcher` interface with one implementation to start: a timed `ReadDir` + `stat` sweep. Inboxes sweep every 3 s; master sweeps on startup, on terminal focus and on a manual refresh key. fsnotify is avoided on macOS because its kqueue backend holds one open descriptor per file in a watched directory ([fsnotify source](https://fuchsia.googlesource.com/third_party/github.com/fsnotify/fsnotify/+/refs/tags/v1.6.0/backend_kqueue.go)).
- **Partial downloads.** Ignore `.part` and `.crdownload`, and index an inbox file only after its size is unchanged across two sweeps.
- **Idempotent updates.** When the app writes tags, both the write path and the next sweep see the change. `Upsert` compares old and new and emits nothing when they match.

## Tags: two tiers

List columns come from a fast native reader. The tag editor reads and writes through kid3-cli, so what the editor shows is exactly what kid3 will save.

- **Summary, in bulk.** A native Go ID3 reader (for example `dhowden/tag`, read-only) fills `TagSummary` during scans, including comment (`Comment()`), BPM and key (`TBPM`, `TKEY` from `Raw()`). No process per file.
- **Duration.** Returned by `Summary` along with the tags. Best effort at scan time from the MP3's Xing header, for example with `ComputeAudioDuration` in [derat/mpeg](https://pkg.go.dev/codeberg.org/derat/mpeg), which assumes constant bitrate when there is no Xing header. mpv corrects it the first time the track plays.
- **Full tags, one file.** Opening the editor makes one kid3-cli call. kid3-cli takes commands as JSON objects such as `{"method":"get","params":["artist",2]}` and answers in JSON, so there is no text scraping ([Kid3 handbook](https://kid3.sourceforge.io/kid3_en.html)).
- **Write.** One kid3-cli invocation per edit, one `-c` per changed frame; kid3 runs them in order and saves at the end. Check the exit status and the JSON `error` object, and wrap the call in a context timeout.
- **Same value on many files.** One call with all the paths; kid3 selects them and applies the command to the selection.
- **After a write.** Re-read the summary natively, `Upsert`, and if the track has a `MusicID`, ask Music to `refresh` it.
- **kid3-cli quirks** ([docs/kid3-cli.md](docs/kid3-cli.md)). Frame names follow the macOS UI language, so every call points `KID3_CONFIG_FILE` at a private config with English set. A missing path or an NFD-spelled path makes kid3 skip the commands and still exit 0, so paths are checked and passed as NFC, and the answers are counted.
- **Editing the playing track.** kid3 may rewrite the file under mpv. Allow it; if mpv then reports an error, reload the track at the saved position.

```go
type Store interface {
    Summary(path string) (TagSummary, time.Duration, error)            // native
    Full(ctx context.Context, path string) ([]Frame, error)            // kid3-cli
    Write(ctx context.Context, paths []string, ch []FrameChange) error // kid3-cli
}
```

## Player: one mpv process, app-owned queue

mpv runs once for the app's lifetime and is driven over its JSON IPC socket. The play queue lives in the app, not in mpv.

- **Process.** `mpv --idle --no-video --no-terminal --input-ipc-server=<socket>`. `--no-terminal` keeps mpv off the TTY the TUI owns. The socket sits in a per-run temp dir. A pidfile lets the next start kill an orphan left by a crash.
- **Protocol.** One line of JSON per message. One reader goroutine separates replies, matched by `request_id`, from events. Writes go through one mutex.
- **State.** `observe_property` on `pause`, `time-pos`, `duration` and `volume`, plus the `end-file` event. The player folds them into one `PlayerState` value.
- **Throttling.** `time-pos` changes many times a second. The player emits `PlayerState` at most every 250 ms, latest wins. Discrete events (track ended, load failed) go out at once and are never dropped.
- **Queue.** A `[]TrackID` in `app`. When a track ends, the app resolves the next ID to its current path and sends `loadfile`. A queue of paths inside mpv would go stale on promote, rename or delete.
- **Load failure.** If a track fails to load, the app reports it and stops playback; the queue stays on the failed track. The one exception is the reload after a tag edit on the playing track (see Tags).
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
- **Sync Library.** Off for this library: local files only, no Apple Music subscription. A developer report describes Sync Library cutting a playlist that holds catalog tracks short after a write ([Apple Developer Forums](https://developer.apple.com/forums/thread/844902)); that risk does not apply here. Playlist writes still re-read the whole playlist, which is cheap, and there is no delayed re-check.
- **Spike findings** ([docs/spike-music.md](docs/spike-music.md)). Music returns locations in NFD, so every path from Music goes through `Normalize` before matching. Adding a track that is already in a playlist adds a second copy, so `AddToPlaylist` skips current members. osascript error messages are localized; match on the error number only (-600 not running, -1728 no such object, -1743 automation denied). Folders are detected by `specialKind = folder`, and `Editable` means not smart and `specialKind = none`. A file track can have no location; the snapshot skips it.

## Operations

Every operation that touches more than one package lives in `app`, runs off the UI goroutine and reports by event.

### Promote (inbox to master)

1. Check `required_tags`. Fail with the list of missing ones.
2. Look the file's current name up among master tracks, case-insensitively. On a hit, return `ErrCollision` carrying the existing `TrackID`.
3. Move without clobbering. Claim the name first with `os.OpenFile(dst, O_CREATE|O_EXCL)`, which fails if the name exists, then `os.Rename(src, dst)` over that empty placeholder. A bare `os.Rename` would silently replace an existing file. Other volume: copy to a dot-prefixed temp file in master, `fsync`, then claim and rename the same way and remove the source.
4. Update path and role in the index and cache under the same `TrackID`. Emit `TracksChanged`.
5. Hand the track to the reconciler, which adds it to Music.

A batch promote runs these steps per track, in sequence. One failure does not stop the rest. The call returns a summary of promoted tracks, collisions and failures, which reaches the UI as the command's result message.

### Overwrite (after a collision)

Runs only when the user picks it after `ErrCollision`.

1. Check `required_tags`. Re-check the collision; if it is gone, run a plain promote.
2. If either track is playing, stop playback. Drop the inbox track from the queue.
3. Replace the master file without ever leaving its path empty. Hard-link the existing file, under its own name, into a dot-prefixed temp dir in master. Rename the inbox file over the master path (other volume: copy to a temp first, as in promote). Then trash or remove the linked copy per `delete_mode`. The master file's name is kept, even where the inbox name differs in case.
4. The master track keeps its `TrackID` and `MusicID` and takes the inbox file's size, mtime, summary and duration. The inbox `TrackID` is removed. Emit `TracksChanged` and `TrackRemoved`.
5. If the track has a `MusicID`, `music.Refresh`. The Music entry still points at the same path, so it keeps its playlist memberships (the spike confirms this).

If trashing the linked copy fails, the replacement stands and the old file stays in the temp dir; the error says where. The app never deletes that file on its own.

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

Add or remove by `MusicID` and playlist ID (adds skip tracks already in the playlist, since Music would add a second copy), verify, patch the snapshot, emit `PlaylistsChanged`. Offered only for tracks with a `MusicID` and playlists with `Editable = true`.

### Reconcile

Runs after each Music snapshot and each master sweep, and only with a snapshot newer than the sweep.

- **`MusicID`s come from the snapshot.** The cache does not store them; each pass attaches them by path, and clears one whose entry is gone from Music. Refresh and focus sweep master first and read the snapshot second, so the snapshot is the newer one.
- **Master track with no `MusicID`:** `add`, verify the location, store the ID.
- **Music entry inside master whose file is missing:** emit `DeadEntry`. Never auto-delete.
- **Safety valve:** if one pass wants to add more than 100 tracks, emit `ImportPending` with the count and wait for `ConfirmImport` or `CancelImport`. A path-matching bug would otherwise look like "nothing is in Music" and trigger a mass add. The first run on an existing master dir trips the valve by design, and the user confirms the import once.

## Feeding the UI

The core knows nothing about Bubble Tea. It exposes methods and one event channel, and `ui` adapts both. The Bubble Tea model, layout, keys and everything the user sees are written by the developer; this design stops at the API.

- **Requests.** `ui` exports one function per `app` method that returns a `tea.Cmd`. Its message is a result type named after the method (`PromoteResult`, `DeleteResult`, …) holding the return values and the error. `Update` never blocks and never touches files.
- **Errors.** Typed, in `model`, so the UI can match them with `errors.As`: `ErrCollision`, `ErrMissingTags`, `ErrPlaylistsLost`, `ErrBackupKept`, `ErrMusicUnavailable`, `ErrAutomationDenied`, `ErrCopySettingOn`, `ErrVerifyFailed`, `ErrNotEditable`, `ErrNotInMusic`, `ErrKid3Missing`.
- **Events.** Packages publish to `events.Bus` through a `Sink` interface. `ui.WaitForEvent` returns the next event as a message; the model's `Update` returns it again to re-arm it.
- **Two delivery classes.** State events (`PlayerState`, `ScanProgress`, `MusicStatus`) are latest-wins: one slot per kind, overwritten while the UI is behind. Change events (`TracksChanged`, `TrackRemoved`, `PlaylistsChanged`, `DeadEntry`, `ImportPending`, errors) are queued and never dropped. A slow UI cannot block mpv's reader or the scanner.
- **Batches.** `TracksChanged` carries a slice of `Track` values, coalesced during scans so the UI gets a few events a second, not one per file.
- **Bus.** Kept minimal: one mutex, a slice of change events, a map of state slots and a 1-buffered wake channel. `Publish` never blocks; `Next` returns the oldest change event, else a filled state slot. The UI is the only consumer, so `app` stands between the player and the bus: it acts on `TrackEnded`, `LoadFailed` and durations, then forwards them.
- **Windowed views.** `Query` returns IDs and `Tracks(ids)` returns values for just the rows on screen, so the UI never needs the whole library.
- **Queries.** `library.Query(filter, sort)` returns `[]TrackID` and runs in a goroutine through a `tea.Cmd`. The command takes a generation number and its result carries it back, so the UI can drop stale results. Debouncing input is up to the UI.
- **Focus.** The UI turns on focus reporting and sends `FocusGained`; `app` runs the master sweep and the Music snapshot refresh.
- **Version note.** Bubble Tea v2 is current, imported as `charm.land/bubbletea/v2`. `View` returns a `tea.View` struct and focus reporting is a field on it ([releases](https://github.com/charmbracelet/bubbletea/releases)). The command and message boundary used here is the same in v1 and v2.

## Build order

Prototype the Music scripts first: they are the only part that can invalidate the design.

1. **Music spike, scripts only.** On the real library: time the bulk snapshot; `add` and read back the location with the copy setting off and on; confirm deleting an entry leaves the file; confirm `refresh` picks up a tag edit; rename a different file over an added track's path and confirm its persistent ID and playlists survive (for overwrite); add and remove a playlist entry by persistent ID. Done; see [docs/spike-music.md](docs/spike-music.md).
2. **`fsops`.** No-clobber move, cross-volume copy, trash. Test names that differ only by case.
3. **`library`.** Index, cache, sweep, events. Test against a generated directory of several thousand files.
4. **`tags`.** Summary reader, kid3 JSON read and write.
5. **`player`.** mpv IPC and throttled state.
6. **`app`.** Promote, overwrite, delete, edit and the queue, against fakes for `music`, `player` and `tags`.
7. **`music` and the reconciler,** with the safety valve.
8. **`ui` API.** Commands, result messages and the event pump. The model and views are the developer's.

Open questions:

- [x] On a name collision, should promote always ask, or default to skipping the file?

Ask. The answers are skip or overwrite.

- [x] Is Sync Library on for this library? It decides how much of the playlist verification matters.

Off. The library has local files only and no Apple Music subscription (corrected after the spike).

