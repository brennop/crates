# crates

Design: [DESIGN.md](DESIGN.md). Tasks: [TASKS.md](TASKS.md). The API the UI is written against: [docs/api.md](docs/api.md).

## UI (`ui/`): use Charm components before writing your own

The UI is Bubble Tea v2 with bubbles v2 and lipgloss v2 (`charm.land/...`). Before you build any UI piece, check whether bubbles already has it, and use that:

| Need | Use |
|---|---|
| A line of text input (prompt, filter, edit field) | `bubbles/textinput` (see `newPromptInput` in `ui/prompt.go`) |
| Multi-line text | `bubbles/textarea` |
| A selectable, scrollable or filterable list | `bubbles/list` with a custom `ItemDelegate` (see `ui/playlists.go`) |
| Rows in columns | `bubbles/table` |
| Scrolling a block of text | `bubbles/viewport` |
| Keys | a `key.Binding` in `ui/keys.go`, matched with `key.Matches` |
| Key hints and help | `bubbles/help`, through `hints` in `ui/keys.go` |
| Loading or progress | `bubbles/spinner`, `bubbles/progress` |
| A box over the table | `dialogBox` and `overlay` in `ui/dialog.go` |
| Mouse clicks | `bubblezone` zones: `zones.Mark` in `View`, `zones.Get(id).Pos` in `Update` (see `rowClick` in `ui/library.go`) |
| Truncating, measuring, layout | lipgloss (`Width`, `JoinVertical`, `Compositor`) and `ansi.Truncate` |

Read the component's source in the module cache (`go env GOMODCACHE`) to see what it can do; check its options before deciding it can't.

Write your own only when the component cannot do the job, and say why in a doc comment where you write it. `ui/scroll.go` is the example: `table` cannot keep a scrolloff margin or center the cursor, so the Library does its own scrolling. Don't replace the existing exceptions either: the scrolling in `scroll.go`, the self-padded cells in `row` (so tints fill the cell), and the `z`/`g` prefixes (bubbles has no key sequences).

`TestKeysMatchBindings` fails when a key press is matched by its `String()`. Add a binding instead, so the hint lines stay true.

## Checks

`go vet ./... && go test ./...`. `imports_test.go` enforces the package layering: nothing below `ui` imports Charm packages.

After any change to `analysis/`, also run the real scripts on a full-length track (not a short clip) and keep the memory budget test passing:

```
CRATES_ANALYSIS_AUDIO="<a 6+ minute mp3 from master>" go test -count=1 -run TestScripts -v ./analysis
```

It fails if the jobs together pass 2 GB, if any job passes its 1.2 GB cap, or if cancelling leaves a process behind.

## Heavy external processes

The owner's Mac has 8 GB of RAM. An analysis job that used memory without limit, plus a cancel that left its Python child running, once froze it and forced a hard restart. Anything that starts ML models, audio decoding or other heavy subprocesses:

- Measure peak memory and CPU on realistic input (a full-length track, the whole library), with every job that runs at once running together. A short clip proves nothing: memory can grow with input length.
- Run it through `analysis.runGroup` (or the same pattern): its own process group, killed as a group on cancel and on quit, a memory cap, lower priority. `exec.CommandContext` kills only the direct child; `uv`, shells and wrappers leave their children running.
- Test that cancelling mid-run leaves no process behind, with the real program.
- Limit threads (torch, BLAS) so playback and the rest of the machine keep working.
