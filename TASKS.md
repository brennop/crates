# Tasks

Breakdown of [DESIGN.md](DESIGN.md). Phases 1–8 follow the design's build order. Phase 0 (foundation) and Phase 9 (wiring) cover pieces the build order leaves implicit. Each phase names the design sections it implements.

Already settled in DESIGN.md:

- **Name collision on promote:** always ask.
- **Sync Library is on:** playlist verify-after-write is required, and spike 1.8 must run.

## Decisions to settle

Gaps and conflicts found while breaking the design down. Each one blocks the tasks it names.

- [ ] **D1. Collision choices.** Promote asks, but the design never lists the answers. The app never renames and never overwrites, so the in-app options are likely: skip, delete the inbox copy, or jump to the existing master track. Replacing the master file would drop its playlist memberships. *Blocks 6.3, 6.4, 8.6.*
- [ ] **D2. TrackID across restarts.** IDs are stable within a run. Decide whether the cache stores them (recommended: `id` primary key, `path` unique, so promote is an `UPDATE` of the path) or every start assigns fresh ones. *Blocks 3.3.*
- [ ] **D3. Leaf packages for shared types and `Normalize`.** `TracksChanged` carries `Track` values and `library` publishes to the bus, so `Track` cannot live in `library` without an import cycle through `events`. `TagSummary` is produced by `tags` and stored by `library`, which may not import each other. `music` must normalize the paths it reads back but may not import `library`. Recommended: `model` (`TrackID`, `Role`, `Track`, `TagSummary`, `Frame`, `FrameChange`, `Playlist`) and `pathnorm` (`Normalize`, case-sensitivity detection), both importable by every package. *Blocks 0.2, 3.1, 7.4.*
- [ ] **D4. "Filename rule" in the `config` table.** The Decisions section says the app never builds filenames, and the TOML sample has no such key. Recommended: drop it. *Blocks 0.4.*
- [ ] **D5. Unspecified numbers.** Safety-valve threshold, inbox sweep interval, kid3-cli and osascript timeouts: constants or config keys? *Blocks 0.4, 3.6, 7.2, 7.8.*
- [ ] **D6. Duration in `tags.Store`.** The interface has `Summary` only, but scans also compute duration. Either return it with the summary (one file open) or add `Duration(path)`. *Blocks 4.1.*
- [ ] **D7. Bus delivery order.** When a state slot and queued change events are both pending, which comes first? Suggested: change events in FIFO order, then the latest state of each kind. *Blocks 0.3.*
- [ ] **D8. UI layout and keymap.** The design specifies the UI's plumbing, not its screens or keys. *Blocks Phase 8.*
- [ ] **D9. Shutdown.** Not in the design: stop mpv, remove the socket dir and pidfile, cancel in-flight osascript and kid3-cli calls, flush and close the cache. *Blocks 9.2.*
- [ ] **D10. Load failure in the queue.** When mpv cannot load a track: report and advance (suggested), or report and stop? *Blocks 6.8.*

## Phase 0: Foundation

Can run alongside the Music spike. Design: Package layout, Data model, Decisions and config, Feeding the UI.

- [ ] **0.1 Module and layout.** `go mod init`; create `cmd`, `config`, `ui`, `app`, `library`, `tags`, `player`, `music`, `fsops`, `events` and the D3 leaf packages. Add a test that enforces the import rules: no package under `app` imports another (the leaf packages and `events` excepted), and nothing below `ui` imports Bubble Tea. *Test:* `go build ./...`; the import test fails on a deliberate bad import.
- [ ] **0.2 Shared types.** `TrackID`, `Role` (`Inbox` | `Master`), `Track`, `TagSummary`, `Playlist` as in Data model, in the D3 leaf package. `Track` is passed by value everywhere.
- [ ] **0.3 Event bus.** `events.Bus` with the event types: `LibraryLoaded`, `ScanProgress`, `TracksChanged` (slice of `Track`), `TrackRemoved`, `PlayerState`, `PlayerDown`, `TrackEnded`, `LoadFailed`, `PlaylistsChanged`, `DeadEntry`, Music status, promote batch summary, safety-valve prompt, errors. Two delivery classes: state events are latest-wins, one slot per kind; change events go into an unbounded FIFO and are never dropped. `Publish` never blocks. *Test:* a publisher with no reader never blocks; state events coalesce to the latest; 10,000 change events all arrive, in order.
- [ ] **0.4 Config.** Load TOML: `inbox_dirs`, `master_dir`, `required_tags`, `delete_mode`, `[music] enabled, autolaunch`, plus any D5 keys. Expand `~` and normalize dirs. Validate: master exists; no inbox inside master or master inside an inbox; `delete_mode` is `trash` or `remove`; every `required_tags` name is a `TagSummary` field. Apply defaults for missing keys. Pick the config file location. *Test:* table tests for valid and invalid files.
- [ ] **0.5 Test fixtures.** Small valid MP3s (a repeated silent CBR frame) with and without a Xing header; ID3v2.3, ID3v2.4, ID3v1-only and untagged; names with quotes, NFD Unicode and case-only differences. Plus a generator for a directory of several thousand files. Used by phases 2 to 5.

## Phase 1: Music spike (scripts only)

Prototype the Music scripts before any Go: they are the only part that can invalidate the design. Write them as JXA that takes `argv` and prints JSON, so they carry over to 7.1. Design: Apple Music, Build order 1.

- [ ] **1.1 Prep.** Back up the Music library. In Music > Settings > Files, turn "Copy files to Media folder when adding to library" off. Make a scratch master dir outside the Media folder with a few test MP3s and a throwaway test playlist. Note the macOS and Music versions.
- [ ] **1.2 Bulk snapshot.** One script returns the persistent ID and location of every file track, then each user playlist's persistent ID, name, smart/folder flags and track persistent IDs. Time it on the real library, with Music freshly launched and warm. *Record:* track and playlist counts, run time. If it takes more than a few seconds, revisit how often 7.4 refreshes.
- [ ] **1.3 Add, copy setting off.** `add` a file from the scratch master; read back the persistent ID and location; the location matches the source path after normalization. `add` the same file again: same persistent ID, no duplicate.
- [ ] **1.4 Add, copy setting on.** Turn the setting on, add a new file, confirm the location is inside the Media folder. Turn it off again and clean up the copy.
- [ ] **1.5 Delete keeps the file.** Delete a track by persistent ID; the file is still on disk.
- [ ] **1.6 Refresh picks up a tag edit.** Change a tag with kid3-cli, `refresh` the track by persistent ID, and confirm Music shows the new value.
- [ ] **1.7 Playlist add and remove by persistent ID.** Add a track to the test playlist by IDs, then remove it, reading the playlist back after each.
- [ ] **1.8 Sync Library playlist test.** Repeat 1.7 on a playlist that also holds catalog (Apple Music) tracks. Re-read the full membership right after the write and again a minute later; nothing else may be cut ([forum report](https://developer.apple.com/forums/thread/844902)). *Record:* whether truncation happens and whether a delayed re-check is needed (feeds 7.6).
- [ ] **1.9 Failure modes and quoting.** With Music quit, a "running" check must not launch it. Turn the terminal's Music switch off in Privacy & Security > Automation and capture osascript's exact -1743 output, then turn it back on. Paths and playlist names with `"`, `'` and Unicode survive the trip through `argv`.
- [ ] **1.10 Write-up and gate.** Record the results in `docs/spike-music.md`. If any result contradicts the design (for example, `add` duplicates or delete removes the file), update DESIGN.md before Phase 2.

## Phase 2: `fsops`

Design: Operations > Promote step 3, Delete step 3; Review items 1 and 14.

- [ ] **2.1 No-clobber move, same volume.** Claim `dst` with `os.OpenFile(dst, O_CREATE|O_EXCL|O_WRONLY)`, close it, then `os.Rename(src, dst)` over the placeholder. `EEXIST` returns a typed collision error. If the rename fails, remove the placeholder. *Test:* an existing `dst` is never touched; names that differ only by case collide on a case-insensitive volume; no placeholder survives a failed rename.
- [ ] **2.2 Cross-volume move.** On `EXDEV`, copy to a dot-prefixed temp file in master, `fsync` it, claim the name as in 2.1, rename the temp over the placeholder, then remove the source. Keep mode and mtime. On any failure remove the temp and leave the source. *Test:* injected `EXDEV` in unit tests; a real second volume (a small disk image attached with `hdiutil`) in an integration test.
- [ ] **2.3 Stale temp cleanup.** Temps left by a crash are dotfiles, so scans skip them; remove the app's own temps from master at startup.
- [ ] **2.4 Delete a file.** `delete_mode = "trash"`: use `/usr/bin/trash` when the binary exists (macOS 15+), otherwise Finder through osascript with the path as `argv`. Detect by the binary, not the OS version. The Finder fallback needs its own automation permission and can fail with -1743. `"remove"`: `os.Remove`. Check exit status and use a timeout. *Test:* unit tests for remove; manual check of both trash paths.

## Phase 3: `library`

Design: Library, Data model; Review items 2, 4, 10.

- [ ] **3.1 `Normalize`.** In the D3 leaf package: absolute, cleaned, symlinks resolved, Unicode NFC. It must work for paths that no longer exist (dead Music entries): resolve the deepest existing ancestor and append the rest. Detect case sensitivity per root (for example `pathconf(_PC_CASE_SENSITIVE)` or a case-swapped probe); the comparison key is case-folded on case-insensitive volumes. *Test:* relative paths, `..`, a symlinked root, NFD input, a missing file, case variants.
- [ ] **3.2 Index.** `map[TrackID]Track` plus key → `TrackID`, behind one `RWMutex`, with a monotonic ID counter. `Get(ids)` returns copies. `Upsert` emits nothing when old and new match. `Move(id, newPath)` keeps the ID and re-derives the role from the root. `Remove`. The library is the only writer. *Test:* `-race` with concurrent readers and a writer; a no-op `Upsert` emits no event.
- [ ] **3.3 Cache.** SQLite through a pure-Go driver (for example `modernc.org/sqlite`), no cgo. Rows hold path, size, mtime, summary fields, duration and, per D2, the ID. A schema version table; a version mismatch or unreadable file means delete and full scan. Write each batch in one transaction. *Test:* round trip; bumping the version or deleting the file triggers a full scan.
- [ ] **3.4 Scan.** Per root, `os.ReadDir` plus `stat`: non-recursive for master, recursive for inboxes. Skip dotfiles, `.part`, `.crdownload` and anything that is not `.mp3` (case-insensitive). Re-read summary and duration only where size or mtime changed, with a worker pool. Files gone from disk emit `TrackRemoved`. Emit `TracksChanged` in batches, plus `ScanProgress`. `library` defines its own summary-reader interface, since it may not import `tags`.
- [ ] **3.5 Startup.** Load the cache into the index, emit `LibraryLoaded`, then scan in the background and correct the index. *Test:* on the generated library, `LibraryLoaded` arrives before the scan finishes.
- [ ] **3.6 Polling watcher.** A `Watcher` interface with one sweep implementation. Inboxes sweep every few seconds (D5). Master sweeps on startup and on `Refresh()` (focus or manual key). No fsnotify. Overlapping sweep requests coalesce into one.
- [ ] **3.7 Partial downloads.** Index an inbox file only after its size is unchanged across two sweeps; keep a pending set between sweeps.
- [ ] **3.8 Query.** `Query(filter, sort) []TrackID`: text filter over summary fields, role filter, sort by any column. Safe to call from any goroutine. *Test:* benchmark on several thousand tracks.
- [ ] **3.9 Duration setter.** `SetDuration(id, d)` updates index and cache, only when the stored duration is 0.
- [ ] **3.10 Scale test.** On a generated dir of several thousand files, measure cold scan, warm start from cache, a no-change sweep, and memory.

## Phase 4: `tags`

Design: Tags: two tiers; Review item 12.

- [ ] **4.1 Native summary reader.** `dhowden/tag` fills `TagSummary`: title, artist, album, album artist, track number, year, genre. Duration from the Xing header with `derat/mpeg` `ComputeAudioDuration`, which assumes CBR when there is none (shape per D6). An unreadable tag returns an error for that file and never stops a scan.
- [ ] **4.2 kid3-cli runner.** Find `kid3-cli` at startup; when it is missing, the editor is disabled with a message. Paths go in as arguments, never through a shell. Context timeout on every call; check the exit status and the JSON `error` object.
- [ ] **4.3 Confirm the full-read command.** From the [Kid3 handbook](https://kid3.sourceforge.io/kid3_en.html), find the JSON command that lists every frame of tag 2 and the shape of its answer. Record it in `docs/` and define `Frame` and `FrameChange` from it.
- [ ] **4.4 `Full(ctx, path)`.** One kid3-cli call returning `[]Frame`.
- [ ] **4.5 `Write(ctx, paths, changes)`.** One invocation per edit, one `-c` per changed frame; for the same value on many files, all paths go in that one call.
- [ ] **4.6 Fake `Store`.** In-memory, with scriptable failures, for Phase 6.
- [ ] **4.7 Fixture tests.** Summary on every 0.5 fixture; duration with and without a Xing header; kid3 write-then-read round trip with quotes and Unicode in values and paths. Note where the native reader and kid3 disagree.

## Phase 5: `player`

Design: Player; Review items 11, 13; smaller fixes (`--no-terminal`, pidfile).

- [ ] **5.1 Process.** Check mpv is on `PATH`. Start `mpv --idle --no-video --no-terminal --input-ipc-server=<socket>` with the socket in a per-run temp dir; wait until the socket accepts.
- [ ] **5.2 Pidfile and orphans.** Write a pidfile. At startup, if one exists, kill that PID only if it is alive and `ps -p <pid> -o command=` shows mpv with this app's `--input-ipc-server`; otherwise just delete the stale pidfile. Never kill by name or pattern.
- [ ] **5.3 IPC client.** One JSON message per line. One reader goroutine routes replies to waiting callers by `request_id` and everything else to the event handler. Writes go through one mutex. Each request has a timeout.
- [ ] **5.4 Commands.** Load a path (replacing the current file, with an optional start position), pause and resume, stop, seek, set volume.
- [ ] **5.5 Observe and fold.** `observe_property` on `pause`, `time-pos`, `duration`, `volume`, folded into one `PlayerState`. Map `end-file` by reason: `eof` → `TrackEnded`; `error` → `LoadFailed`; `stop`, `redirect` and `quit` caused by the app's own load or stop are ignored, or the queue would skip a track on every manual change.
- [ ] **5.6 Throttle.** `PlayerState` goes out at most about 4 times a second, latest wins. `TrackEnded` and `LoadFailed` go out at once.
- [ ] **5.7 Crash recovery.** If the socket closes unexpectedly: emit `PlayerDown`, restart mpv, re-observe, and do not resume playback.
- [ ] **5.8 Interface and fake.** `player.Player` and an in-memory fake that can emit any event on demand.
- [ ] **5.9 Tests.** Against a fake socket server: replies interleaved with events, throttling, every `end-file` reason. With real mpv (skipped when not installed): play a fixture, then kill mpv mid-play and check the restart.

## Phase 6: `app`, against fakes

Design: Operations, Player > Queue; Review items 1, 5, 11. Fakes for `music`, `player` and `tags`.

- [ ] **6.1 `music.Client` interface and fake.** Define the interface now: snapshot, `Add`, `AddToPlaylist`, `RemoveFromPlaylist`, `DeleteTrack`, `Refresh`, status and launch. The fake scripts failures: `Unavailable`, -1743, a location mismatch on add, a truncated playlist after a write. The real client is Phase 7.
- [ ] **6.2 Facade.** `app` is built from the library, `tags.Store`, `player.Player`, `music.Client`, `fsops`, the bus and config. Methods are safe from any goroutine; long ones report progress by event.
- [ ] **6.3 Promote.** (1) Check `required_tags`; fail with the missing list. (2) Look the file's name up among master tracks by case-insensitive key; a hit returns `ErrCollision` with the existing `TrackID`. (3) Move with `fsops`; if `fsops` finds a collision the index missed (master not swept yet), return `ErrCollision` without an ID and trigger a master sweep. (4) Update path and role under the same `TrackID` in index and cache; emit `TracksChanged`. (5) Hand the track to the reconciler. *Test:* a collision never overwrites; the track keeps its ID and its place in the queue.
- [ ] **6.4 Batch promote.** Runs 6.3 per track, in sequence; a failure does not stop the rest. A summary event closes the batch: promoted, collided (for the D1 prompt), failed with reasons.
- [ ] **6.5 Delete.** (1) Drop from the queue; stop playback first if it is playing. (2) `Master` with a `MusicID`: delete the Music entry; on failure stop, nothing changed. (3) Trash or remove per `delete_mode`. (4) Remove from index and cache; emit `TrackRemoved`. If (3) fails after (2), the error must say the playlist memberships are gone and the reconciler will re-add the track. *Test:* each failure point leaves exactly the state the design describes.
- [ ] **6.6 Edit tags.** `tags.Write`, re-read the summary, `Upsert`, then `music.Refresh` if the track has a `MusicID`. Works for many files at once. If the track is playing and mpv then reports an error, reload it at the saved position.
- [ ] **6.7 Playlists.** Add or remove by `MusicID` and playlist ID; offered only for tracks with a `MusicID` and playlists with `Editable = true`. Verify, patch the snapshot, emit `PlaylistsChanged`. A failed verify reports an error and refreshes the snapshot.
- [ ] **6.8 Queue.** A `[]TrackID`: play now, enqueue, next, previous, remove, clear. On `TrackEnded`, resolve the next ID to its current path and load it; skip IDs no longer in the index. `LoadFailed` per D10. *Test:* the queue survives a promote (path changed, same ID) and a delete.
- [ ] **6.9 Duration backfill.** When `PlayerState` reports a duration for a track whose `Duration` is 0, call `library.SetDuration`.
- [ ] **6.10 Focus and refresh.** `FocusGained()` and `Refresh()` run the master sweep and the Music snapshot refresh.

## Phase 7: `music` and the reconciler

Design: Apple Music, Operations > Reconcile; Review items 3, 4, 6, 7, 8, 9.

- [ ] **7.1 Scripts and runner.** Move the spike scripts into `music/scripts/` and embed them with `go:embed`. Run `osascript -l JavaScript -e <embedded source> <args…>`, so arguments arrive as `argv` and nothing is interpolated; parse JSON from stdout.
- [ ] **7.2 One worker.** Every call goes through a single goroutine, one at a time, each with a context timeout (D5).
- [ ] **7.3 Status and errors.** Error -1743 becomes a permission error that points to Privacy & Security > Automation. With `autolaunch = false` and Music not running, report `Unavailable` without launching it. `Launch()` backs the UI's offer. Status is published as a state event.
- [ ] **7.4 Snapshot.** Build `path → MusicID` (paths through `Normalize`), `MusicID → []PlaylistID`, and the playlist list with `Editable`. Refresh on startup, on focus, on the manual key and after any failed mutation; the app's successful mutations patch it in place. Stamp each snapshot with its time for the reconciler.
- [ ] **7.5 Add.** `add`, read back the persistent ID and location, normalize, compare with the master path. On a mismatch the copy setting is on: latch that state, stop all further adds, report it, and leave cleanup to the user. Retries are safe because `add` is idempotent.
- [ ] **7.6 Mutations with verify.** `AddToPlaylist` and `RemoveFromPlaylist` re-read the whole playlist and compare it with the expected membership, not just the changed track, so a Sync Library truncation is caught. `DeleteTrack` confirms the entry is gone and removes only the library entry. `Refresh`. Any mismatch is an error and triggers a snapshot refresh. Add a delayed re-check if 1.8 showed one is needed.
- [ ] **7.7 Stub and disabled mode.** The real client builds under the `darwin` tag, with a no-op stub elsewhere. `music.enabled = false` uses the no-op client and turns the reconciler off.
- [ ] **7.8 Reconciler (in `app`).** Runs after each snapshot and each master sweep, only when the snapshot is newer than the sweep. Attach `MusicID`s from `path → MusicID` to master tracks. A master track with no `MusicID` is added and its ID stored; adds wait while Music is `Unavailable`. A Music entry inside master whose file is missing emits `DeadEntry`, never a delete. Safety valve: if one pass would add more than the D5 threshold, pause and emit a prompt; continue only on confirmation. The first run on an existing master dir trips it by design.
- [ ] **7.9 Tests.** Reconciler against the fake client: the valve trips and resumes on confirm; dead entries; adds held while unavailable; a stale snapshot is ignored; the copy-setting latch stops adds. Real-script integration tests behind a build tag, run by hand against the test playlist.

## Phase 8: `ui`

Bubble Tea v2 (`charm.land/bubbletea/v2`). Design: Feeding the UI. Layout and keys per D8.

- [ ] **8.1 Plumbing.** Each `app` method is wrapped in a `tea.Cmd` that returns a result message; `Update` never blocks and never touches files. One `tea.Cmd` waits on the bus, returns the event as a message and re-arms itself.
- [ ] **8.2 Track list.** View state is a `[]TrackID` for the current filter and sort, a cursor, and `Track` values for the visible rows only; render only those rows. Columns from `TagSummary`, plus duration, role and an in-Music marker. Apply `TracksChanged` and `TrackRemoved` to the visible rows.
- [ ] **8.3 Filter and sort.** Debounced typing. `library.Query` runs in a `tea.Cmd` carrying a generation number; drop any result older than the latest.
- [ ] **8.4 Player bar and queue.** Show `PlayerState` (paused, position, duration, volume), the playing track and the queue. Keys for play, pause, next, seek, volume, enqueue.
- [ ] **8.5 Focus and refresh.** Turn on focus reporting on the `tea.View` and forward focus-gained to `app.FocusGained`. A manual refresh key.
- [ ] **8.6 Promote flow.** Single and multi-select. Show missing required tags, the collision prompt (D1) and the batch summary.
- [ ] **8.7 Delete flow.** Confirm, stating the `delete_mode` (and that trashed files have no Put Back). Show the partial-failure message from 6.5 verbatim.
- [ ] **8.8 Tag editor.** Open with `Full`, edit frames, save with `Write`. Same-value edit across a selection. Disabled with a message when kid3-cli is missing.
- [ ] **8.9 Playlist picker.** Editable playlists only, marked with the selected tracks' memberships; offered only for tracks with a `MusicID`.
- [ ] **8.10 Music status and prompts.** An `Unavailable` banner with a launch-Music action; the -1743 guidance; the copy-setting warning; the safety-valve confirmation with the number of tracks to add; the `DeadEntry` list.
- [ ] **8.11 Errors.** Change-event errors stay on screen until dismissed; none are dropped.

## Phase 9: Wiring and release

- [ ] **9.1 `cmd`.** Read config, build the pieces, wire the bus, inject the summary reader into `library`, start Bubble Tea. Startup checks with clear messages: config valid, mpv present, kid3-cli present (or editor off), trash method available.
- [ ] **9.2 Shutdown.** Per D9: stop mpv, remove the socket dir and pidfile, cancel in-flight external calls, flush and close the cache.
- [ ] **9.3 End-to-end on the real library.** Cold and warm start. A download landing in an inbox stays hidden until its size settles. Promote one track and a batch that includes a collision. Edit tags on the playing track. Add to and remove from a playlist that holds catalog tracks (Sync Library on). Delete a master track in both delete modes. Quit Music mid-session. Deny automation permission. Kill mpv mid-play. Kill the app and restart; the orphan mpv is cleaned up.
- [ ] **9.4 README.** Required Music settings (copy off, master outside the Media folder), the automation permission, dependencies (mpv, kid3-cli), a config reference, and the trash caveat.
