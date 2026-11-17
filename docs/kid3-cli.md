# kid3-cli: the commands `tags` uses

Checked against kid3-cli 3.9.6 (Homebrew) on macOS 15, 2026-11-17. Source: the [Kid3 handbook](https://kid3.sourceforge.io/kid3_en.html), section "kid3-cli", plus the probes below.

## Calling convention

```
kid3-cli -c '<json command>' [-c '<json command>' ...] <path> [<path> ...]
```

- Each `-c` is a JSON object `{"method": ..., "params": [...]}`. kid3 prints one JSON answer per command, `{"result": ...}` or `{"error": {"code": N, "message": "..."}}`, one per line.
- Several paths select all those files. A `set` applies to every selected file, even when they are in different directories, and kid3 saves the changed files once, after the last command.
- A failed save prints an extra, pretty-printed `{"error": ...}` object after the per-command answers and exits 1. Example message: `Error while writing file:\n<path>: Permission denied`.
- An unknown command prints an `error` object and exits 1.

## Full read (task 4.3)

```
{"method":"get","params":["all",2]}
```

`2` is tag 2 (ID3v2). The answer:

```json
{"result":{"taggedFile":{
  "fileName":"a.mp3","fileNameChanged":false,
  "format":"MPEG 1 Layer 3 128 kbps 44100 Hz 1 Channels",
  "tag2":{"format":"ID3v2.4.0","frames":[
    {"changed":false,"name":"Title","value":"Song"},
    {"changed":false,"name":"Artist","value":"Artist"},
    {"changed":false,"name":"Artist","value":"Second"},
    {"changed":false,"name":"Date","value":"2001"},
    {"changed":false,"name":"Track Number","value":"3"},
    {"changed":false,"name":"Initial Key","value":"Am"},
    {"changed":false,"name":"MyCustom","value":"xyz"}]}}}}
```

- A file with no ID3v2 tag (ID3v1-only or untagged) has no `tag2` key.
- `name` is kid3's display name (`Title`, `Track Number`, `Initial Key`, `Album Artist`). A TXXX frame shows its description. kid3 names both TYER (v2.3) and TDRC (v2.4) `Date`.
- Repeated frames appear once per frame, in tag order, under the same name.

So `model.Frame` is `{Name, Index, Value}`, where `Index` counts earlier frames with the same name, compared without regard to case. `changed` is always false on a fresh read and is dropped.

## Write (task 4.5)

```
{"method":"set","params":["<name>","<value>",2]}
```

- `<name>` is matched without regard to case and accepts the display names that `get all` returns, so a `Frame` read back can be written as is.
- `name[i]` addresses the i-th frame of that name (`artist[1]` is the second Artist). If the frame does not exist it is added. On a file with no Artist frame, `artist[1]` therefore creates the first one.
- An unknown name creates a TXXX frame with that description.
- An empty value deletes the frame. Later frames of the same name move up an index.
- Each `set` answers `{"result":null}`.

So `model.FrameChange` is `{Name, Index, Value}`, sent as `Ref()` (`Name` or `Name[Index]`), and an empty `Value` deletes.

## Traps, and what `tags` does about them

1. **Frame names follow the macOS UI language,** not `LANG` or `LC_ALL`. On a Portuguese system `Title` comes back as `Título`. kid3 reads `MainWindow/Language` from its config, and the `KID3_CONFIG_FILE` environment variable points it at another config file. `tags.New` writes a private INI with `[MainWindow] Language=en` to a temp dir and sets `KID3_CONFIG_FILE` for every call. That also means kid3-cli ignores the user's own kid3 settings (`~/Library/Preferences/com.kid3.Kid3.plist`) and runs on kid3's defaults. kid3 does not write to that file.
2. **A missing path is not an error.** kid3 prints `<path> does not exist` as plain text, runs none of the commands, and exits 0. With several paths, one missing path still prints that line and runs nothing. `tags` stats every path first and requires exactly one answer per command.
3. **An NFD file name is silently ignored.** For a path spelled in NFD (as Go's `norm.NFD`, Finder copies, or some downloads produce), `get all` answers `{"result":{"taggedFile":{}}}`, `get title` answers `null`, and `set` answers `null`, saves nothing and exits 0. The same file passed in NFC works, because APFS and HFS+ look names up regardless of normalization. `tags` converts every path to NFC and stats it, so a normalization-sensitive volume errors instead of doing nothing. `Full` also treats a `taggedFile` with no `fileName` as an error. This looked random at first: zsh globs return NFC names, so the same file worked from the shell and failed from Go.
4. **Paths beginning with `-`.** `tags` passes absolute paths only, so a file name can never be read as a flag.

## Where the native reader and kid3 disagree (task 4.7)

- **ID3v1-only files.** The native reader (`dhowden/tag`) falls back to ID3v1 and fills title, artist, album, year, comment, track and genre. `Full` reads tag 2 only and shows no frames. The first kid3 write creates an ID3v2 tag next to the ID3v1 one; the native reader then prefers ID3v2, so fields that were only in ID3v1 vanish from the summary unless the editor writes them. Phase 6 may want `Full` to fall back to tag 1 for display.
- **Year.** kid3 shows the raw `Date` text (`2001-05-02` is possible). The summary keeps only the year as a number.
- **Track.** kid3 shows the raw `Track Number` text (`3/12`). The summary keeps the leading number.
- **BPM.** kid3 shows the raw text. The summary parses it as a number, accepts a comma as the decimal point, and treats anything else as unset.
- No disagreement on any text field of the ID3v2.3, ID3v2.4, UTF-16, quoted or NFD fixtures.

## Duration

Native only, through `derat/mpeg` `ComputeAudioDuration`, with the ID3v2 header size (plus footer) and a trailing ID3v1 tag skipped. With a Xing or Info header it is exact (frames × 1152 / sample rate). Without one it is size ÷ header bitrate. The fixtures have no padding bits, so their real rate is 127.7 kbit/s, not 128, and the estimate runs 0.3% short. When no MPEG-1 Layer III frame is found in the first 8 KiB after the tag, duration is 0, and mpv fills it on first play.
