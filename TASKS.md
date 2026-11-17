# Tasks

Breakdown of [DESIGN.md](DESIGN.md). Phases 1–8 follow the design's build order. Phase 0 (foundation) and Phase 9 (wiring) cover pieces the build order leaves implicit. Each phase names the design sections it implements.

**Scope.** These tasks build the backend and the API the UI calls: `app` methods, events, and the `tea.Cmd`s and result messages in `ui`. The Bubble Tea model, layout, keys and everything the user sees are left to the developer.

## Decisions

Settled on 2026-11-17 and folded into DESIGN.md.

- **Collisions.** The UI asks. The answers are skip (no call) or overwrite (`app.Overwrite`, task 6.5). Promote itself still never overwrites.
- **TrackID across restarts.** Stored in the cache: `id` primary key, `path` unique, so promote is an `UPDATE` of the path.
- **Shared leaf packages.** `model` holds the shared types and typed errors; `pathnorm` holds `Normalize` and case-sensitivity detection. Any package may import both. `Track` cannot live in `library`: `TracksChanged` carries it and `library` publishes to the bus, which would cycle through `events`. `music` needs `Normalize` but may not import `library`.
- **No filename rule.** Dropped from config; the app never builds filenames.
- **Tuning values are constants,** listed below.
- **Duration comes with the summary:** `Summary(path) (TagSummary, time.Duration, error)`.
- **The bus is the simplest thing that works** (task 0.3). Batch promote's summary is its return value, not a separate event.
- **UI.** Left to the developer; see Scope.
- **Shutdown** stops mpv, removes the socket dir and pidfile, cancels in-flight external calls, and flushes and closes the cache (task 9.2).
- **A track that fails to load** is reported and playback stops.
- **Sync Library is on,** so playlist verify-after-write is required and spike 1.9 must run.

| Constant | Value | Task |
| --- | --- | --- |
| Inbox sweep interval | 3 s | 3.6 |
| Scan worker pool | `runtime.NumCPU()` | 3.4 |
| Scan batch flush | every 250 ms or 500 tracks, whichever comes first | 3.4 |
| `PlayerState` throttle | 250 ms (about 4 a second) | 5.6 |
| mpv startup wait, request timeout | 5 s each | 5.1, 5.3 |
| kid3-cli read, write timeout | 10 s, 30 s | 4.2 |
| osascript snapshot, other calls | 60 s, 15 s (revisit after spike 1.2) | 7.2 |
| Trash command timeout | 15 s | 2.5 |
| Safety-valve threshold | 100 adds in one reconcile pass | 7.8 |

## Phase 0: Foundation

Can run alongside the Music spike. Design: Package layout, Data model, Decisions and config, Feeding the UI.

- [x] **0.1 Module and layout.** `go mod init`; create `cmd`, `config`, `ui`, `app`, `library`, `tags`, `player`, `music`, `fsops`, `events`, `model`, `pathnorm`. A test enforces the import rules: no package under `app` imports another except `events`, `model` and `pathnorm`, and nothing below `ui` imports Bubble Tea. *Test:* `go build ./...`; the import test fails on a deliberate bad import.
- [x] **0.2 Shared types and errors.** In `model`: `TrackID`, `Role` (`Inbox` | `Master`), `Track`, `TagSummary` and `Playlist` as in Data model, plus `Frame` and `FrameChange` once 4.3 settles them. `Track` is passed by value everywhere. Typed errors the UI can match with `errors.As`: `ErrCollision` (existing `TrackID`, zero if not indexed yet), `ErrMissingTags` (the missing names), `ErrPlaylistsLost` (Music entry deleted, then the file delete failed), `ErrBackupKept` (overwrite done, but the replaced file could not be trashed), `ErrMusicUnavailable`, `ErrAutomationDenied`, `ErrCopySettingOn`, `ErrVerifyFailed`, `ErrNotEditable`, `ErrNotInMusic`, `ErrKid3Missing`.
- [x] **0.3 Event bus.** Event types in `events`. State events: `PlayerState`, `ScanProgress`, `MusicStatus`. Change events: `LibraryLoaded`, `TracksChanged` (slice of `Track`), `TrackRemoved`, `PlaylistsChanged`, `DeadEntry`, `ImportPending` (safety valve), `PlayerDown`, `TrackEnded`, `LoadFailed`, `Error`. Implementation: one mutex, a slice for change events, a map of state slots by kind, and a 1-buffered wake channel. `Publish` never blocks. `Next(ctx)` returns the oldest change event, else any filled state slot. There is one consumer, the UI. Producers publish through an `events.Sink` interface (`Publish(Event)`); `app` wraps the bus as the player's sink, so it can act on `TrackEnded`, `LoadFailed` and durations before forwarding them. *Test:* a publisher with no reader never blocks; state events coalesce to the latest; 10,000 change events all arrive, in order.
- [x] **0.4 Config.** Load TOML: `inbox_dirs`, `master_dir`, `required_tags`, `delete_mode`, `[music] enabled, autolaunch`. Expand `~` and normalize the dirs. Validate: master exists; no inbox inside master and master not inside an inbox; `delete_mode` is `trash` or `remove`; every `required_tags` name is a `TagSummary` field. Defaults for missing keys. Pick the config file location. *Test:* table tests for valid and invalid files.
- [x] **0.5 Test fixtures.** Small valid MP3s (a repeated silent CBR frame) with and without a Xing header; ID3v2.3, ID3v2.4, ID3v1-only and untagged; names with quotes, NFD Unicode and case-only differences. Plus a generator for a directory of several thousand such files. Used by Phases 2 to 5.

**Phase 0 notes.** Module `github.com/brennop/crates`; the binary is `cmd/crates`. The import test (`imports_test.go`) ranks packages cmd > ui > app > core > leaves; a new top-level package counts as core, the strictest rule. `required_tags` names are `title`, `artist`, `album`, `album_artist`, `track`, `year`, `genre`; `TagSummary.Missing` serves 6.3. `Frame` and `FrameChange` wait for 4.3. Config lives at `$CRATES_CONFIG`, else `$XDG_CONFIG_HOME/crates/config.toml`, else `~/.config/crates/config.toml`. Unknown keys are errors, and overlapping inboxes are rejected. `master_dir` has no default; inbox dirs need not exist yet. `pathnorm.Normalize` (3.1 minus case sensitivity) was written early for config. Fixtures live in `internal/fixture` and are checked against ffprobe; `go run ./cmd/genfixtures -dir D -n 5000` writes a scale-test library.

## Phase 1: Music spike (scripts only)

Prototype the Music scripts before any Go: they are the only part that can invalidate the design. Write them as JXA that takes `argv` and prints JSON, so they carry over to 7.1. Design: Apple Music, Build order 1.

- [ ] **1.1 Prep.** Back up the Music library. In Music > Settings > Files, turn "Copy files to Media folder when adding to library" off. Make a scratch master dir outside the Media folder with a few test MP3s, and a throwaway test playlist. Note the macOS and Music versions.
- [ ] **1.2 Bulk snapshot.** One script returns the persistent ID and location of every file track, then each user playlist's persistent ID, name, smart and folder flags, and track persistent IDs. Time it on the real library, with Music freshly launched and warm. *Record:* track and playlist counts, run time. If it takes more than a few seconds, revisit how often 7.4 refreshes and the 60 s timeout.
- [ ] **1.3 Add, copy setting off.** `add` a file from the scratch master; read back the persistent ID and location; the location matches the source path after normalization. `add` the same file again: same persistent ID, no duplicate.
- [ ] **1.4 Add, copy setting on.** Turn the setting on, add a new file, confirm the location is inside the Media folder. Turn it off again and clean up the copy.
- [ ] **1.5 Delete keeps the file.** Delete a track by persistent ID; the file is still on disk.
- [ ] **1.6 Refresh picks up a tag edit.** Change a tag with kid3-cli, `refresh` the track by persistent ID, and confirm Music shows the new value.
- [ ] **1.7 Replace a file under an entry.** For overwrite (6.5): add a scratch file and put it in the test playlist, then rename a different MP3 over the same path and `refresh` the entry. The persistent ID and the playlist membership must survive, and Music must show the new tags and duration. If they don't, overwrite has to delete and re-add the entry, which loses memberships; revisit 6.5.
- [ ] **1.8 Playlist add and remove by persistent ID.** Add a track to the test playlist by IDs, then remove it, reading the playlist back after each.
- [ ] **1.9 Sync Library playlist test.** Repeat 1.8 on a playlist that also holds catalog (Apple Music) tracks. Re-read the full membership right after the write and again a minute later; nothing else may be cut ([forum report](https://developer.apple.com/forums/thread/844902)). *Record:* whether truncation happens and whether a delayed re-check is needed (feeds 7.6).
- [ ] **1.10 Failure modes and quoting.** With Music quit, a "running" check must not launch it. Turn the terminal's Music switch off in Privacy & Security > Automation, capture osascript's exact -1743 output, then turn it back on. Paths and playlist names with `"`, `'` and Unicode survive the trip through `argv`.
- [ ] **1.11 Write-up and gate.** Record the results in `docs/spike-music.md`. If any result contradicts the design (for example, `add` duplicates or delete removes the file), update DESIGN.md before Phase 2.

## Phase 2: `fsops`

Design: Operations > Promote step 3, Delete step 3; Review items 1 and 14.

- [ ] **2.1 No-clobber move, same volume.** Claim `dst` with `os.OpenFile(dst, O_CREATE|O_EXCL|O_WRONLY)`, close it, then `os.Rename(src, dst)` over the placeholder. `EEXIST` returns `ErrCollision`. If the rename fails, remove the placeholder. *Test:* an existing `dst` is never touched; names that differ only by case collide on a case-insensitive volume; no placeholder survives a failed rename.
- [ ] **2.2 Cross-volume move.** On `EXDEV`, copy to a dot-prefixed temp file in master, `fsync` it, claim the name as in 2.1, rename the temp over the placeholder, then remove the source. Keep mode and mtime. On any failure remove the temp and leave the source. *Test:* injected `EXDEV` in unit tests; a real second volume (a small disk image attached with `hdiutil`) in an integration test.
- [ ] **2.3 Replace, for overwrite.** `Replace(src, dst, mode)` swaps in the new file so that `dst` is never missing and the old file is handled per `delete_mode`. Hard-link `dst`, under its own name, into a dot-prefixed temp dir in master, so it reaches the Trash with its real name. Rename `src` over `dst` (cross-volume: copy to a temp as in 2.2 first). Then trash or remove the linked copy and the temp dir. If that last step fails, the swap stands, the old file stays in the temp dir, and the call returns `ErrBackupKept` with its path. *Test:* new content at `dst`; old content in the Trash or gone, per mode; a failure before the rename leaves both files untouched.
- [ ] **2.4 Stale temp cleanup.** Temps are dotfiles, so scans skip them. At startup, remove copy temps left by a crash; list kept 2.3 backups so `app` can report them, and never delete those automatically.
- [ ] **2.5 Delete a file.** `delete_mode = "trash"`: use `/usr/bin/trash` when the binary exists (macOS 15+), otherwise Finder through osascript with the path as `argv`. Detect by the binary, not the OS version. The Finder fallback needs its own automation permission and can fail with -1743. `"remove"`: `os.Remove`. Check the exit status; 15 s timeout. *Test:* unit tests for remove; a manual check of both trash paths.

## Phase 3: `library`

Design: Library, Data model; Review items 2, 4, 10.

- [ ] **3.1 `Normalize`.** In `pathnorm`: absolute, cleaned, symlinks resolved, Unicode NFC. It must work for paths that no longer exist (dead Music entries): resolve the deepest existing ancestor and append the rest. Detect case sensitivity per root (for example `pathconf(_PC_CASE_SENSITIVE)` or a case-swapped probe); the comparison key is case-folded on case-insensitive volumes. *Test:* relative paths, `..`, a symlinked root, NFD input, a missing file, case variants.
- [ ] **3.2 Index.** `map[TrackID]Track` plus key → `TrackID`, behind one `RWMutex`, with a monotonic ID counter. `Get(ids)` returns copies. `Upsert` emits nothing when old and new match. `Move(id, newPath)` keeps the ID and re-derives the role from the root. `Remove`. The library is the only writer. *Test:* `-race` with concurrent readers and a writer; a no-op `Upsert` emits no event.
- [ ] **3.3 Cache.** SQLite through a pure-Go driver (for example `modernc.org/sqlite`), no cgo. Columns: `id` primary key, `path` unique, size, mtime, summary fields, duration. The ID counter starts at `max(id) + 1`. A schema version table; a version mismatch or unreadable file means delete and full scan. Each batch is written in one transaction. *Test:* round trip; IDs survive a restart; a version bump or a deleted file triggers a full scan.
- [ ] **3.4 Scan.** Per root, `os.ReadDir` plus `stat`: non-recursive for master, recursive for inboxes. Skip dotfiles, `.part`, `.crdownload` and anything that is not `.mp3` (case-insensitive). Re-read summary and duration only where size or mtime changed, with a worker pool of `runtime.NumCPU()`. Files gone from disk emit `TrackRemoved`. Flush `TracksChanged` every 250 ms or 500 tracks, and publish `ScanProgress`. `library` defines its own summary-reader interface, since it may not import `tags`.
- [ ] **3.5 Startup.** Load the cache into the index, emit `LibraryLoaded`, then scan in the background and correct the index. *Test:* on the generated library, `LibraryLoaded` arrives before the scan finishes.
- [ ] **3.6 Polling watcher.** A `Watcher` interface with one sweep implementation. Inboxes sweep every 3 s. Master sweeps on startup and on demand through `SweepMaster(ctx)`, which returns when the sweep is done so the reconciler can follow. No fsnotify. Overlapping sweep requests coalesce into one.
- [ ] **3.7 Partial downloads.** Index an inbox file only after its size is unchanged across two sweeps; keep a pending set between sweeps.
- [ ] **3.8 Query.** `Query(filter, sort) []TrackID`: text filter over summary fields, role filter, sort by any column. Safe from any goroutine. *Test:* benchmark on several thousand tracks.
- [ ] **3.9 Duration setter.** `SetDuration(id, d)` updates index and cache, only when the stored duration is 0.
- [ ] **3.10 Scale test.** On a generated dir of several thousand files, measure cold scan, warm start from cache, a no-change sweep, and memory.

## Phase 4: `tags`

Design: Tags: two tiers; Review item 12.

- [ ] **4.1 Native summary reader.** `Summary(path) (TagSummary, time.Duration, error)`. `dhowden/tag` fills title, artist, album, album artist, track number, year and genre. Duration comes from the Xing header via `derat/mpeg` `ComputeAudioDuration`, which assumes CBR when there is none. An unreadable tag returns an error for that file and never stops a scan.
- [ ] **4.2 kid3-cli runner.** Find `kid3-cli` at startup. When it is missing, `Full` and `Write` return `ErrKid3Missing` and `app.Capabilities()` says so. Paths go in as arguments, never through a shell. Timeouts: 10 s to read, 30 s to write. Check the exit status and the JSON `error` object.
- [ ] **4.3 Confirm the full-read command.** From the [Kid3 handbook](https://kid3.sourceforge.io/kid3_en.html), find the JSON command that lists every frame of tag 2 and the shape of its answer. Record it in `docs/` and define `Frame` and `FrameChange` from it.
- [ ] **4.4 `Full(ctx, path)`.** One kid3-cli call returning `[]Frame`.
- [ ] **4.5 `Write(ctx, paths, changes)`.** One invocation per edit, one `-c` per changed frame; for the same value on many files, all paths go in that one call.
- [ ] **4.6 Fake `Store`.** In memory, with scriptable failures, for Phase 6.
- [ ] **4.7 Fixture tests.** Summary on every 0.5 fixture; duration with and without a Xing header; kid3 write-then-read round trip with quotes and Unicode in values and paths. Note where the native reader and kid3 disagree.

## Phase 5: `player`

Design: Player; Review items 11, 13; smaller fixes (`--no-terminal`, pidfile).

- [ ] **5.1 Process.** Check that mpv is on `PATH`. Start `mpv --idle --no-video --no-terminal --input-ipc-server=<socket>` with the socket in a per-run temp dir; wait up to 5 s for the socket to accept.
- [ ] **5.2 Pidfile and orphans.** Write a pidfile. At startup, if one exists, kill that PID only if it is alive and `ps -p <pid> -o command=` shows mpv with this app's `--input-ipc-server`; otherwise just delete the stale pidfile. Never kill by name or pattern.
- [ ] **5.3 IPC client.** One JSON message per line. One reader goroutine routes replies to waiting callers by `request_id` and everything else to the event handler. Writes go through one mutex. Each request times out after 5 s.
- [ ] **5.4 Commands.** Load a path (replacing the current file, with an optional start position), pause and resume, stop, seek, set volume.
- [ ] **5.5 Observe and fold.** `observe_property` on `pause`, `time-pos`, `duration` and `volume`, folded into one `PlayerState`. Map `end-file` by reason: `eof` → `TrackEnded`; `error` → `LoadFailed`; `stop`, `redirect` and `quit` caused by the app's own load or stop are ignored, or the queue would skip a track on every manual change. Events go to the player's `Sink`.
- [ ] **5.6 Throttle.** `PlayerState` goes out at most every 250 ms, latest wins. `TrackEnded` and `LoadFailed` go out at once.
- [ ] **5.7 Crash recovery.** If the socket closes unexpectedly: emit `PlayerDown`, restart mpv, re-observe, and do not resume playback.
- [ ] **5.8 Interface and fake.** `player.Player` and an in-memory fake that can emit any event on demand.
- [ ] **5.9 Tests.** Against a fake socket server: replies interleaved with events, throttling, every `end-file` reason. With real mpv (skipped when not installed): play a fixture, then kill mpv mid-play and check the restart.

## Phase 6: `app`, against fakes

Design: Operations, Player > Queue; Review items 1, 5, 11. Uses the fakes for `music`, `player` and `tags`.

- [ ] **6.1 `music.Client` interface and fake.** Define the interface now: snapshot, `Add`, `AddToPlaylist`, `RemoveFromPlaylist`, `DeleteTrack`, `Refresh`, status and launch. The fake can fail on demand: unavailable, -1743, a location mismatch on add, a truncated playlist after a write. The real client is Phase 7.
- [ ] **6.2 Facade.** `app` is built from the library, `tags.Store`, `player.Player`, `music.Client`, `fsops`, the bus and config. Methods are safe from any goroutine, return values or a `model` typed error, and never wait on the UI.
- [ ] **6.3 Promote one track.** (1) Check `required_tags`; fail with `ErrMissingTags`. (2) Look the file's name up among master tracks by case-insensitive key; a hit returns `ErrCollision` with the existing `TrackID`. (3) Move with `fsops`; if `fsops` finds a collision the index missed (master not swept yet), return `ErrCollision` without an ID and trigger a master sweep. (4) Update path and role under the same `TrackID` in index and cache; emit `TracksChanged`. (5) Hand the track to the reconciler. *Test:* a collision never overwrites; the track keeps its ID and its place in the queue.
- [ ] **6.4 `Promote(ids)`.** Runs 6.3 per track, in sequence; a failure does not stop the rest. Returns a summary: promoted IDs, collisions (inbox ID and existing ID), and failures with their errors.
- [ ] **6.5 `Overwrite(inboxID)`.** Check `required_tags`. Re-check the collision; if it is gone, do a plain promote. If either track is playing, stop playback; drop the inbox track from the queue. `fsops.Replace` onto the existing master track's path, which keeps the master file's name (it may differ from the inbox name in case). The master track keeps its `TrackID` and `MusicID` and takes the inbox track's size, mtime, summary and duration; the inbox `TrackID` is removed. Emit `TracksChanged` and `TrackRemoved`. If the track has a `MusicID`, `music.Refresh`; per spike 1.7 its playlists survive. *Test:* memberships untouched in the fake; no stale ID left in the queue; `ErrBackupKept` passes through.
- [ ] **6.6 `Delete(ids)`.** Per track: (1) drop it from the queue, stopping playback first if it is playing; (2) for a `Master` track with a `MusicID`, delete the Music entry, and on failure stop with nothing changed; (3) trash or remove the file per `delete_mode`; (4) remove it from index and cache and emit `TrackRemoved`. If (3) fails after (2), return `ErrPlaylistsLost`: the reconciler will re-add the file, without its memberships. *Test:* each failure point leaves exactly the state the design describes.
- [ ] **6.7 Tags.** `FullTags(ctx, id)` returns `[]Frame`. `WriteTags(ctx, ids, changes)`: `tags.Write`, re-read each summary, `Upsert`, then `music.Refresh` for tracks with a `MusicID`.
- [ ] **6.8 Playlists.** `Playlists()`, `Memberships(id)`, `AddToPlaylist(ids, playlistID)`, `RemoveFromPlaylist(ids, playlistID)`. Only tracks with a `MusicID` (else `ErrNotInMusic`) and playlists with `Editable = true` (else `ErrNotEditable`). Verify, patch the snapshot, emit `PlaylistsChanged`. A failed verify returns `ErrVerifyFailed` and refreshes the snapshot.
- [ ] **6.9 Queue.** A `[]TrackID` with a current position: `Play(id)`, `Enqueue(ids)`, `Next()`, `Previous()`, `RemoveFromQueue(i)`, `ClearQueue()`, `Queue()`; transport `TogglePause()`, `Seek(d, relative)`, `SetVolume(v)`. Loading resolves an ID to its current path at that moment. *Test:* the queue survives a promote (path changed, same ID), an overwrite and a delete.
- [ ] **6.10 Player events.** `app` is the player's `Sink`. `TrackEnded`: load the next ID, skipping IDs no longer in the index; at the end of the queue, stop. `LoadFailed`: publish `Error`, stop playback, keep the queue position on the failed track. Exception: right after `WriteTags` on the playing track, the first failure reloads it at the saved position instead. A `PlayerState` with a duration for a track whose `Duration` is 0 calls `library.SetDuration`. Forward every event to the bus.
- [ ] **6.11 Focus and refresh.** `FocusGained()` and `Refresh()` run `SweepMaster` and the Music snapshot refresh.

## Phase 7: `music` and the reconciler

Design: Apple Music, Operations > Reconcile; Review items 3, 4, 6, 7, 8, 9.

- [ ] **7.1 Scripts and runner.** Move the spike scripts into `music/scripts/` and embed them with `go:embed`. Run `osascript -l JavaScript -e <embedded source> <args…>`, so arguments arrive as `argv` and nothing is interpolated; parse JSON from stdout.
- [ ] **7.2 One worker.** Every call goes through a single goroutine, one at a time. Timeouts: 60 s for the snapshot, 15 s for everything else.
- [ ] **7.3 Status and errors.** Error -1743 becomes `ErrAutomationDenied`, whose message names Privacy & Security > Automation. With `autolaunch = false` and Music not running, calls return `ErrMusicUnavailable` without launching it. Publish a `MusicStatus` state event: `Available`, `Unavailable`, `Denied`, `CopySettingOn` or `Disabled`. `app.LaunchMusic()` starts Music on request.
- [ ] **7.4 Snapshot.** Build `path → MusicID` (paths through `Normalize`), `MusicID → []PlaylistID`, and the playlist list with `Editable`. Refresh on startup, on `FocusGained` and `Refresh`, and after any failed mutation; the app's successful mutations patch it in place. Stamp each snapshot with its time for the reconciler.
- [ ] **7.5 Add.** `add`, read back the persistent ID and location, normalize, compare with the master path. On a mismatch the copy setting is on: set `MusicStatus` to `CopySettingOn`, stop all further adds, return `ErrCopySettingOn`, and leave cleanup to the user. Retries are safe because `add` is idempotent.
- [ ] **7.6 Mutations with verify.** `AddToPlaylist` and `RemoveFromPlaylist` re-read the whole playlist and compare it with the expected membership, not just the changed track, so a Sync Library truncation is caught. `DeleteTrack` removes only the library entry and confirms it is gone. `Refresh`. Any mismatch returns `ErrVerifyFailed`. Add a delayed re-check if spike 1.9 showed one is needed.
- [ ] **7.7 Stub and disabled mode.** The real client builds under the `darwin` tag, with a no-op stub elsewhere. `music.enabled = false` uses the no-op client, sets `MusicStatus` to `Disabled` and turns the reconciler off.
- [ ] **7.8 Reconciler (in `app`).** Runs after each snapshot and each master sweep, only when the snapshot is newer than the sweep. Attach `MusicID`s from `path → MusicID` to master tracks. A master track with no `MusicID` is added and its ID stored; adds wait while Music is unavailable. A Music entry inside master whose file is missing emits `DeadEntry` and is never deleted. Safety valve: if one pass would add more than 100 tracks, emit `ImportPending` with the count and wait for `ConfirmImport()` or `CancelImport()`. The first run on an existing master dir trips it by design. `DeadEntries()` returns the current list.
- [ ] **7.9 Tests.** Reconciler against the fake client: the valve trips and resumes on confirm; dead entries; adds held while unavailable; a stale snapshot is ignored; the copy-setting latch stops adds. Real-script integration tests behind a build tag, run by hand against the test playlist.

## Phase 8: UI API

Design: Feeding the UI. Everything here is plumbing for the developer's Bubble Tea model; nothing renders.

- [ ] **8.1 Freeze the `app` surface.** Collect the exported methods from Phases 6 and 7, fill any gaps, and add `Tracks(ids) []Track` (copies for the rows the UI shows), `Query(ctx, filter, sort)` and `Capabilities()` (kid3-cli present, trash method, `delete_mode`, Music enabled). The full set:
  - Library: `Query`, `Tracks`, `Refresh`, `FocusGained`
  - Files: `Promote`, `Overwrite`, `Delete`
  - Tags: `FullTags`, `WriteTags`
  - Playlists: `Playlists`, `Memberships`, `AddToPlaylist`, `RemoveFromPlaylist`
  - Playback: `Play`, `Enqueue`, `Next`, `Previous`, `RemoveFromQueue`, `ClearQueue`, `Queue`, `TogglePause`, `Seek`, `SetVolume`
  - Music: `MusicStatus`, `LaunchMusic`, `ConfirmImport`, `CancelImport`, `DeadEntries`
  - Other: `Capabilities`, `Close`
- [ ] **8.2 Commands.** In `ui`, one exported function per `app` method that returns a `tea.Cmd`. Its message is a result type named after the method (`PromoteResult`, `DeleteResult`, …) holding the return values and the error. `Query` takes a generation number and `QueryResult` carries it back; debouncing input and dropping stale generations are up to the UI.
- [ ] **8.3 Event pump.** `ui.WaitForEvent(bus) tea.Cmd` blocks on `bus.Next` and returns the event value itself as the message. The UI's `Update` returns it again after each event to re-arm it.
- [ ] **8.4 Tests.** Against fakes, each command returns its result message, and the pump delivers events in bus order.
- [ ] **8.5 API reference.** `docs/api.md`: each command, its result message, the errors it can return, and the events that follow it. Cover the flows the UI drives: promote with collisions (skip means no call, overwrite means `Overwrite`); the safety valve (`ImportPending`, then `ConfirmImport` or `CancelImport`); `MusicStatus` and `LaunchMusic`; the `ErrPlaylistsLost` and `ErrBackupKept` cases; focus (turn on focus reporting in the `tea.View`, then send `FocusGained`).

## Phase 9: Wiring and release

- [ ] **9.1 `cmd`.** Read config and run startup checks: config valid, mpv present, kid3-cli optional, trash method. Exit with the error if a required check fails. Build the pieces, wire the bus and sinks, inject the summary reader into `library`, start `app` (cache load, sweeps, snapshot). Then pass `app` and the bus to the developer's UI constructor and run the program.
- [ ] **9.2 Shutdown.** `app.Close()` stops mpv, removes the socket dir and pidfile, cancels in-flight osascript and kid3-cli calls, and flushes and closes the cache.
- [ ] **9.3 Integration test on the real library.** Headless, behind a build tag, driving `app` with real mpv, kid3-cli and Music on the test playlist; some steps need manual toggles. Cold and warm start. A download landing in an inbox stays hidden until its size settles. Promote one track, then a batch that includes a collision, then overwrite it. Edit tags on the playing track. Add to and remove from a playlist that holds catalog tracks. Delete a master track in both delete modes. Quit Music mid-run. Deny automation permission. Kill mpv mid-play. Kill the app and restart; the orphan mpv is cleaned up.
- [ ] **9.4 README.** Setup requirements: Music settings (copy off, master outside the Media folder), the automation permission, dependencies (mpv, kid3-cli), a config reference, and the note that files trashed by `/usr/bin/trash` have no Put Back.
