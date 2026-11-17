# Music spike results (Phase 1)

Run on 2026-11-17 against the real library. macOS 15.6.1 (24G90), Music 1.5.6. kid3-cli, mpv and `/usr/bin/trash` all present.

Library at the time: 1375 file tracks, no URL, shared or catalog tracks, every track `cloudStatus = unknown`; 8 playlists (7 plain, plus the smart "Música" special playlist). The library has no Apple Music subscription and uses local files only.

## Scripts

`spike/music/`: JXA, one file per call, plus `lib.js`, a shared prelude that the `run` wrapper puts in front of each script. The Go client (7.1) should embed both and concatenate them the same way. Phase 7 moved them to `music/scripts/` and changed three: `playlist-add` and `playlist-remove` now return `{before, trackIDs}` and `playlist-add` skips current members; `delete` returns `{deleted, remaining}`.

- Invocation: `osascript -l JavaScript -e "<lib.js + script.js>" arg…`. Arguments arrive in `run(argv)`, including ones that start with `-`.
- Output is always one JSON line: `{"ok":true,"result":…}` or `{"ok":false,"error":{"number":N,"message":"…"}}`. Exceptions are caught in the prelude, so osascript exits 0 with a JSON error in most cases.
- Every script except `status`, `launch` and `echo` refuses to run when Music is not running and returns error number `-600`. `Application('com.apple.Music').running()` does not launch Music.

| Script | argv | Result |
| --- | --- | --- |
| `status` | none | `{running}` |
| `launch` | none | `{running}` |
| `snapshot` | none | `{tracks:[{id,location}], playlists:[{id,name,smart,folder,special,parent,trackIDs}], ms}` |
| `add` | path | track info `{id,location,name,artist,album,duration}` |
| `track` | persistent ID | track info |
| `refresh` | persistent ID | track info after refresh |
| `delete` | persistent ID | `{deleted}`, after confirming the entry is gone |
| `playlist` | playlist ID | `{id,name,smart,special,trackIDs}` |
| `playlist-add` | playlist ID, track IDs… | `{trackIDs}` (full membership after the write) |
| `playlist-remove` | playlist ID, track IDs… | `{trackIDs}` (full membership after the write) |
| `make-playlist` | name | `{id,name}`. Spike and integration tests only; the app never creates playlists |
| `echo` | anything | `{argv}`, to test quoting |

## Results

| Step | Result |
| --- | --- |
| 1.1 Prep | Library backed up (Music quit first) to `~/Music/crates-spike-backup-20261117-111113`. Copy setting found **off** (1.3 confirms it). Scratch master `~/Music/crates-spike-master` (outside the Media folder) holds 4 generated MP3s plus the fixture set in `set/`. Test playlist created by script and deleted afterwards. |
| 1.2 Snapshot | 1375 tracks, 8 playlists. Cold (right after launch): **2.8 s** total, 2.7 s of it reading the tracks. Warm: **0.6–0.9 s**. The 60 s timeout and refresh-on-focus are fine as designed. |
| 1.3 Add, copy off | Pass. The location returned is the source path. Adding the same file again returns the same persistent ID; the library count does not change. |
| 1.4 Add, copy on | **Not run** (owner's choice). The location check on every add (7.5) still covers this case. |
| 1.5 Delete | Pass. The entry is gone (including from its playlist) and the file stays on disk. Re-adding the same file gives a **new** persistent ID, so its old playlist memberships are lost for good. That confirms the `ErrPlaylistsLost` case. |
| 1.6 Refresh after tag edit | Pass. After `kid3-cli -c 'set title …'`, Music showed the new title and artist (with quotes and Unicode). Music had already shown the new values when the track was read before `refresh`, so it seems to re-read changed files on access. The app still calls `refresh` as designed. |
| 1.7 Replace under an entry | Pass. The entry was in the test playlist; a different MP3 was renamed over its path (new inode). The persistent ID and the playlist membership survived, and Music showed the new tags and the new duration (0.21 s → 5.22 s). Overwrite (6.5) works as designed: no delete and re-add. |
| 1.8 Playlist add/remove | Pass, by persistent IDs, with the full membership read back after each write. **Adding a track that is already in the playlist adds a second copy.** Remove deletes every occurrence. An unknown track ID returns -1728. |
| 1.9 Sync Library | **Not applicable.** No Apple Music subscription, no catalog tracks, local files only. No truncation risk; no delayed re-check needed. |
| 1.10 Failure modes, quoting | With Music quit, `status`, `snapshot` and `track` returned without launching it. The -1743 capture was **not run** (owner's choice); 7.3 must match on error number -1743 (see below). Quoting: `"`, `'`, Unicode and a leading `-` all survive `argv`; a playlist named `crates spike "test" it's ção` was created, written and read back intact. |

## Findings that change the plan

1. **Music returns locations in NFD.** For a file whose name is NFC on disk (`Café Été.mp3`), `location` came back decomposed. A raw string compare fails; an NFC compare matches. Every path from Music must go through `pathnorm.Normalize` before matching (the design already says so; this makes it required, not defensive).
2. **Playlist add duplicates.** `AddToPlaylist` (6.8, 7.6) must skip tracks already in the playlist, from the snapshot or the read-back, and its expected membership must be computed that way.
3. **Error messages are localized** (osascript printed `Não foi possível converter tipos. (-1700)`). Match errors by `number` only, never by message text: -600 not running, -1728 no such object, -1743 automation denied.
4. **Sync Library is off for this library** (no subscription). The forum truncation risk does not apply. Keep the cheap full-membership verify in 7.6; drop the delayed re-check.
5. **Folders and editability.** `folderPlaylists` is not reachable from the application object in JXA; detect folders by `specialKind == "folder"`. `Editable` = `!smart && specialKind == "none"`. The "Música" library playlist is smart with `specialKind = "Music"`.
6. **Parent IDs.** `parent` of a top-level playlist is the library source's ID (here `12E23BD0F9BFB94F`), not null. It is not needed by the design; the snapshot keeps it for a future folder view.
7. **`whose({location})` fails** with -1700, confirming that Music cannot look tracks up by path (Review item 6). The bulk snapshot is the only lookup.
8. One file track has a null location (its file is missing from Music's view). The snapshot must accept `location: null` and skip it for `path → MusicID`.

None of these contradicts the design's approach; DESIGN.md is updated for 2–5.
