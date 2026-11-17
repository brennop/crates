# crates

A terminal music library manager for macOS. Downloads land in one or more inbox folders. You check their tags, play them, and promote the keepers into one flat master folder. Every master track is kept in the Apple Music library, where you edit playlists from the terminal.

The design is in [DESIGN.md](DESIGN.md), and the API the UI is written against is in [docs/api.md](docs/api.md).

## Requirements

- **macOS.** The Music integration runs through `osascript`. Deleting to the Trash uses `/usr/bin/trash` (macOS 15 and later) and falls back to Finder on older systems.
- **Go 1.24** to build.
- **mpv** for playback (required): `brew install mpv`.
- **kid3-cli** for the tag editor (optional): `brew install kid3`. Without it, everything except tag editing works. The track list reads tags natively either way.

Build and check the setup:

```sh
go build -o crates .
./crates -check
```

`-check` reads the config, looks for mpv and kid3-cli, and reports how deleting to the Trash will work, then exits. Without it, crates starts the app. `-headless` runs it without the UI and logs every event to stderr.

## Using it

The Library view is a table of the master tracks, sorted by artist: title, artist, genre, key, BPM, length and comments. The selected track plays: move with `↑`/`↓` (or `j`/`k`, `pgup`/`pgdown`, `g`/`G`), and the row under the cursor starts once you stop on it. Key and BPM are tinted by value: keys by their place on the Camelot wheel (a key and its relative major/minor share a color), BPM around the color wheel once every 90 BPM. `space` pauses, `q` quits.

## Apple Music settings

The master folder must live in the Music library as plain file references, never as copies. Before the first run:

1. In **Music > Settings > Files**, turn **off** "Copy files to Media folder when adding to library".
2. Keep `master_dir` **outside** Music's Media folder (by default under `~/Music/Music`).

If the copy setting is on, Music adds its own copy, and the app notices on the first add. It reports `ErrCopySettingOn`, stops adding to Music until restart, and leaves the copy for you to remove from Music and from disk.

On the first run against a master folder that is not in Music yet, the app asks before adding more than 100 tracks at once, so a path-matching problem can never cause a mass import. Confirm it once.

iCloud Music Library (Sync Library) is assumed off.

## Automation permission

macOS asks once whether your terminal app (Terminal, iTerm, Ghostty, …) may control **Music**, and on systems without `/usr/bin/trash` also **Finder**. Allow it. If you denied it, every Music call fails with error -1743, and the app reports `ErrAutomationDenied`. Turn it back on in **System Settings > Privacy & Security > Automation**, under your terminal app.

The permission belongs to the terminal app, not to the crates binary, so a new terminal app asks again.

## Config

The config file is `$CRATES_CONFIG` if set, else `$XDG_CONFIG_HOME/crates/config.toml`, else `~/.config/crates/config.toml`.

```toml
inbox_dirs    = ["~/Downloads/music", "~/Soulseek/complete"]
master_dir    = "~/Music/Master"
required_tags = ["title", "artist", "album"]
delete_mode   = "trash"

[music]
enabled    = true
autolaunch = false
```

| Key | Default | Meaning |
| --- | --- | --- |
| `master_dir` | (required) | The flat master folder. Files sit directly in it, and subfolders are ignored. It must exist. |
| `inbox_dirs` | `[]` | Folders scanned recursively for new downloads. They need not exist yet. They may not overlap each other or the master. |
| `required_tags` | `["title", "artist", "album"]` | Tags a track needs before promote. Names: `title`, `artist`, `album`, `album_artist`, `track`, `year`, `genre`, `comment`, `bpm`, `key`. |
| `delete_mode` | `"trash"` | `"trash"` moves deleted files to the Trash; `"remove"` deletes them for good. |
| `music.enabled` | `true` | Keep master in sync with Apple Music and offer playlist editing. Off, crates never talks to Music. |
| `music.autolaunch` | `false` | Start Music when a call finds it not running. Off, crates waits and offers to launch it. |

`~` expands to your home directory. Unknown keys are errors.

## Good to know

- **No Put Back.** Files trashed by `/usr/bin/trash` (macOS 15 and later) land in the Trash without Put Back. To restore one, drag it out by hand.
- **Promote never overwrites.** When a name is already taken in master, you choose skip or overwrite. Overwrite keeps the master track's Music entry and playlists.
- **Deleting a master track** removes its Music entry first. If the file then cannot be deleted, the reconciler adds it back to Music, but its playlist memberships are gone, and the error says so.
- **Partial downloads** (`.part`, `.crdownload`, or a file still growing) stay hidden until their size holds across two inbox sweeps, about 3 to 6 seconds.
- **Empty files in master.** An interrupted move can leave a zero-byte file under the real name. crates does not index it and reports it once. Delete it by hand if it is not yours.
- **State.** The library cache and mpv's pidfile live in `~/Library/Caches/crates/`. The cache can be deleted at any time; the next start rescans.

## Tests

```sh
go test -race ./...
```

Integration tests run by hand and are described at the top of each file:

- `go test -tags cratesint -run Integration -v .` runs the app with real mpv and kid3-cli. The Music parts need `CRATES_MUSIC_MASTER` and `CRATES_MUSIC_PLAYLIST`, and some need `CRATES_INT_EXPECT`.
- `go test -tags musicint -run Integration -v ./music/` runs the Music client alone.
- `CRATES_INT_FINDER=1 go test -tags cratesint -run Integration -v ./fsops/` runs the Finder trash fallback.
