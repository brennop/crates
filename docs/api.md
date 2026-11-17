# UI API

The UI talks to `app` only through package `ui`: one function per `app` method that returns a `tea.Cmd`, a result message per command, and an event pump. Nothing here renders; the Bubble Tea model is yours.

Bubble Tea is `charm.land/bubbletea/v2` (pinned at v2.0.2, the last v2 release that builds with Go 1.24).

## Conventions

- **Calling.** `ui.Promote(ctx, a, ids)` returns a `tea.Cmd`. Bubble Tea runs it off the `Update` loop; it returns `ui.PromoteResult`. Return the command from `Update` and handle the result in a later `Update`.
- **Context.** Functions take a `ctx` wherever the `app` method does. Give them one context that you cancel when quitting, so calls into kid3-cli, mpv or Music stop instead of holding up `Close`.
- **Results.** Each result echoes the arguments you need to match it to the request (`ID`, `IDs`, `PlaylistID`, `Index`, `Gen`) and carries `Err` when the method can fail. Batch operations (`Promote`, `Delete`) return a summary, not an error.
- **Errors.** Typed errors live in `model` and are values: match them with `errors.As`. Sentinels in `app` (`ErrUnknownTrack`, `ErrNotInbox`, `ErrUnknownPlaylist`, `ErrQueueIndex`) and `music` (`ErrDisabled`, `ErrClosed`) match with `errors.Is`. `context.Canceled` can come back from any command that takes a `ctx`.

  ```go
  var c model.ErrCollision
  if errors.As(err, &c) { /* c.Name, c.Existing */ }
  ```

- **Background failures.** Work with no caller (reconciler adds, Music refresh after a tag write, the master sweep after a missed collision, auto-advance) reports through `events.Error{Op, Err}` on the bus.

## Events

```go
func (m model) Init() tea.Cmd { return ui.WaitForEvent(m.bus) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case events.TracksChanged:
		// ...
		return m, ui.WaitForEvent(m.bus) // re-arm
	// ... one case per event type, each re-arming
	}
}
```

`ui.WaitForEvent(bus)` blocks on `bus.Next` and returns the event value itself as the message. Keep exactly one outstanding: return it again after every event you handle.

Delivery has two classes:

| Class | Events | Delivery |
| --- | --- | --- |
| State | `PlayerState`, `ScanProgress`, `MusicStatus` | latest wins; one slot per kind, overwritten while you are behind |
| Change | `LibraryLoaded`, `TracksChanged`, `TrackRemoved`, `PlaylistsChanged`, `DeadEntry`, `ImportPending`, `PlayerDown`, `TrackEnded`, `LoadFailed`, `Error` | queued in order, never dropped |

Change events come before state events when both are waiting.

| Event | Meaning |
| --- | --- |
| `LibraryLoaded{Count}` | the cache is in the index; the list can be shown |
| `TracksChanged{Tracks}` | new or updated tracks, batched during scans; re-run your query |
| `TrackRemoved{IDs}` | these tracks left the index |
| `ScanProgress{Scanned, Total, Done}` | a library scan's progress |
| `PlaylistsChanged{}` | playlists or memberships changed; re-read `Playlists` / `Memberships` |
| `MusicStatus{State}` | Music's availability changed |
| `ImportPending{Count}` | the reconciler waits for `ConfirmImport` or `CancelImport` |
| `DeadEntry{MusicID, Path}` | a Music entry in master points at a missing file |
| `PlayerState{TrackID, Path, Paused, Idle, Position, Duration, Volume}` | playback state, throttled |
| `TrackEnded{TrackID, Path}` | the track played to its end; the app already advanced |
| `LoadFailed{TrackID, Path, Reason}` | mpv could not play it; playback stopped (or reloaded, after a tag write) |
| `PlayerDown{Err}` | mpv went away and is being restarted |
| `Error{Op, Err}` | a background failure |

## Commands

### Library

| Command | Result | Errors | Events after |
| --- | --- | --- | --- |
| `Query(ctx, a, gen, filter, sort)` | `QueryResult{Gen, IDs, Err}` | unknown sort field; `context.Canceled` | — |
| `Tracks(a, ids)` | `TracksResult{Tracks}` | — (unknown IDs are left out) | — |
| `Refresh(ctx, a)` | `RefreshResult{Err}` | sweep errors; Music errors (below) | `TracksChanged`, `TrackRemoved`, `PlaylistsChanged`, `MusicStatus`, then reconciler events |
| `FocusGained(ctx, a)` | `FocusGainedResult{Err}` | as `Refresh` | as `Refresh` |

`Filter{Text, Role}` and `Sort{Field, Desc}` are `app.Filter` / `app.Sort`. `Text` is a case-insensitive substring of any summary field; `Role` (a `*model.Role`) limits to master or inbox. `Field` is a summary field name or `path`, `size`, `mtime`, `duration`, `role`; `""` sorts by ID.

`Query` returns IDs only. Ask for `Tracks` with just the IDs of the rows on screen. Debouncing typing and dropping stale results are yours: bump `gen` on every query and ignore a `QueryResult` whose `Gen` is not the latest.

### Files

| Command | Result | Errors | Events after |
| --- | --- | --- | --- |
| `Promote(ctx, a, ids)` | `PromoteResult{Summary}` | in `Summary.Failures`: `ErrMissingTags`, `ErrNotInbox`, `ErrUnknownTrack`, file errors | `TracksChanged` (moved tracks keep their ID); reconciler adds them to Music |
| `Overwrite(ctx, a, inboxID)` | `OverwriteResult{Inbox, Err}` | `ErrBackupKept` (done, old file kept), `ErrMissingTags`, `ErrNotInbox`, `ErrUnknownTrack`, file errors | `TracksChanged`, `TrackRemoved` (the inbox track); `Error` if Music's refresh fails |
| `Delete(ctx, a, ids)` | `DeleteResult{Summary}` | in `Summary.Failures`: `ErrPlaylistsLost`, Music errors, file errors, `ErrUnknownTrack` | `TrackRemoved`; `PlaylistsChanged` for tracks that were in Music |

`PromoteSummary` holds `Promoted`, `Collisions` (`{Inbox, Existing, Name}`) and `Failures` (`{ID, Err}`). `DeleteSummary` holds `Deleted` and `Failures`. One track failing never stops the rest.

### Tags

| Command | Result | Errors | Events after |
| --- | --- | --- | --- |
| `FullTags(ctx, a, id)` | `FullTagsResult{ID, Frames, Err}` | `ErrKid3Missing`, `ErrUnknownTrack`, kid3 errors | — |
| `WriteTags(ctx, a, ids, changes)` | `WriteTagsResult{IDs, Err}` | `ErrKid3Missing`, `ErrUnknownTrack`, kid3 errors (summaries are re-read even then) | `TracksChanged`; `Error` if Music's refresh fails |

Writing the playing track is safe: if mpv then fails to read it, the app reloads it at the same position instead of stopping.

### Playlists

| Command | Result | Errors | Events after |
| --- | --- | --- | --- |
| `Playlists(a)` | `PlaylistsResult{Playlists}` | — | — |
| `Memberships(a, id)` | `MembershipsResult{ID, Playlists, Err}` | `ErrUnknownTrack` | — |
| `AddToPlaylist(ctx, a, ids, playlistID)` | `AddToPlaylistResult{IDs, PlaylistID, Err}` | `ErrUnknownPlaylist`, `ErrNotEditable`, `ErrNotInMusic`, `ErrUnknownTrack`, `ErrVerifyFailed`, Music errors | `PlaylistsChanged` |
| `RemoveFromPlaylist(ctx, a, ids, playlistID)` | `RemoveFromPlaylistResult{IDs, PlaylistID, Err}` | as `AddToPlaylist` | `PlaylistsChanged` |

Both read the last snapshot; nothing is shown until the first `Refresh` / `FocusGained`. Tracks already in the playlist are skipped on add. After a failed write the app re-reads the snapshot, so `PlaylistsChanged` follows the error too.

### Playback

| Command | Result | Errors | Events after |
| --- | --- | --- | --- |
| `Play(ctx, a, id)` | `PlayResult{ID, Err}` | `ErrUnknownTrack`, player errors | `PlayerState` |
| `Enqueue(a, ids...)` | `EnqueueResult{IDs}` | — | — |
| `Next(ctx, a)` | `NextResult{Err}` | player errors | `PlayerState` |
| `Previous(ctx, a)` | `PreviousResult{Err}` | player errors | `PlayerState` |
| `RemoveFromQueue(ctx, a, i)` | `RemoveFromQueueResult{Index, Err}` | `ErrQueueIndex`, player errors | `PlayerState` if it stopped |
| `ClearQueue(ctx, a)` | `ClearQueueResult{Err}` | player errors | `PlayerState` |
| `Queue(a)` | `QueueResult{Queue}` | — | — |
| `TogglePause(ctx, a)` | `TogglePauseResult{Err}` | player errors | `PlayerState` |
| `Seek(ctx, a, d, relative)` | `SeekResult{Err}` | player errors | `PlayerState` |
| `SetVolume(ctx, a, v)` | `SetVolumeResult{Err}` | player errors | `PlayerState` |

`QueueState` is `{IDs, Pos, Playing}`; `Pos` is -1 and `Playing` 0 when nothing is current. The queue changes only through these commands and through the app itself (auto-advance on `TrackEnded`, deletes and overwrites dropping entries), so re-read `Queue` after those events. A track that fails to load is reported (`LoadFailed`, then `Error`) and playback stops.

### Music

| Command | Result | Errors | Events after |
| --- | --- | --- | --- |
| `MusicStatus(a)` | `MusicStatusResult{State}` | — | — |
| `LaunchMusic(ctx, a)` | `LaunchMusicResult{Err}` | `music.ErrDisabled`, `ErrAutomationDenied`, `ErrMusicUnavailable` | `MusicStatus`, `PlaylistsChanged`, reconciler events |
| `ConfirmImport(a)` | `ConfirmImportResult{}` | — | `TracksChanged` as tracks gain Music IDs; `Error` per failed add |
| `CancelImport(a)` | `CancelImportResult{}` | — | — |
| `DeadEntries(a)` | `DeadEntriesResult{Entries}` | — | — |

Music errors anywhere above are `ErrMusicUnavailable` (not running, autolaunch off), `ErrAutomationDenied` (the Automation permission), `ErrCopySettingOn` (Music copied a file into its Media folder) and `music.ErrDisabled` (`[music] enabled = false`).

### Other

| Command | Result | Errors | Events after |
| --- | --- | --- | --- |
| `Capabilities(a)` | `CapabilitiesResult{Capabilities}` | — | — |
| `Close(a)` | `CloseResult{Err}` | player close errors | none; stop the pump |

`Capabilities` is `{Kid3, TrashMethod, DeleteMode, MusicEnabled}`, fixed for the run. Hide tag editing without `Kid3`. `TrashMethod` is `command` (`/usr/bin/trash`) or `finder` (Finder through osascript, which needs the Automation permission for Finder). `DeleteMode` is `trash` or `remove`. Without `MusicEnabled`, hide playlists and Music status: `MusicStatus` stays `disabled`.

## Flows

### Promote with collisions

1. Send `Promote(ctx, a, ids)`.
2. `PromoteResult.Summary.Collisions` lists inbox tracks whose name is taken in master. `Existing` is the master track's ID, or 0 if master holds the file but the index has not seen it yet (a sweep follows by itself).
3. Ask the user about each collision:
   - **Skip:** send nothing. The inbox track stays where it is.
   - **Overwrite:** send `Overwrite(ctx, a, c.Inbox)`. The master track keeps its ID, name, Music entry and playlists, and takes the inbox file's content and tags; the inbox track is removed. If the collision is gone by then, it is a plain promote.
4. An `OverwriteResult` with `ErrBackupKept` is a success: the master file is replaced, but the old one could not be trashed or removed and stays at `ErrBackupKept.Path`. Tell the user; the app never deletes it.

Promote itself never overwrites.

### Safety valve

When a reconcile pass would add more than 100 tracks to Music, it adds none and publishes `ImportPending{Count}`.

- **Confirm:** `ConfirmImport(a)`. The pass runs and adds them; `TracksChanged` follows as tracks gain Music IDs, `Error{Op: "add to Music"}` for any that fail.
- **Cancel:** `CancelImport(a)`. Nothing is added. The next pass on a fresh snapshot (focus, `Refresh`, `LaunchMusic`) asks again.

Either answer does nothing when no import is pending, so a stale prompt is harmless.

### Music status and launch

`MusicStatus` arrives as an event whenever the state changes; `MusicStatus(a)` reads it on demand (`unavailable` before the first Music call).

| State | Meaning | What the UI offers |
| --- | --- | --- |
| `available` | Music answers | — |
| `unavailable` | Music is not running | `LaunchMusic` |
| `denied` | the Automation permission is off | point to System Settings > Privacy & Security > Automation, then `Refresh` |
| `copy setting on` | Music copied an added file | point to Music > Settings > Files; holds until restart, adds stay off |
| `disabled` | `[music] enabled = false` | hide Music features |

`LaunchMusic` starts Music, then reads a snapshot, which lets the reconciler resume held adds.

### ErrPlaylistsLost

A delete of a master track in Music removes the Music entry first, then the file. If the file then fails to delete, the track's `DeleteResult` failure is `ErrPlaylistsLost{Path, Err}`: the file is still in master, the reconciler will re-add it to Music, and its playlist memberships are gone. Tell the user which file and why the delete failed (`Err`, often `ErrAutomationDenied` from the Finder trash).

If the Music delete itself fails, nothing is lost: the failure is that Music error and the file is untouched.

### ErrBackupKept

See step 4 of the promote flow. It comes from `Overwrite` only.

### Focus

Turn on focus reporting in your view, and send `FocusGained` when the terminal regains focus:

```go
func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.ReportFocus = true
	return v
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.FocusMsg:
		return m, ui.FocusGained(m.ctx, m.app)
	}
	// ...
}
```

`FocusGained` sweeps master (picking up files changed outside the app) and refreshes the Music snapshot, which reruns the reconciler. Expect `TracksChanged` / `TrackRemoved`, `PlaylistsChanged`, maybe `ImportPending` and `DeadEntry`.

### Shutdown

Cancel the context given to the commands, stop re-arming `WaitForEvent`, then run `Close(a)` and quit on `CloseResult`.
