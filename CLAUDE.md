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
| Truncating, measuring, layout | lipgloss (`Width`, `JoinVertical`, `Compositor`) and `ansi.Truncate` |

Read the component's source in the module cache (`go env GOMODCACHE`) to see what it can do; check its options before deciding it can't.

Write your own only when the component cannot do the job, and say why in a doc comment where you write it. `ui/scroll.go` is the example: `table` cannot keep a scrolloff margin or center the cursor, so the Library does its own scrolling. Don't replace the existing exceptions either: the scrolling in `scroll.go`, the self-padded cells in `row` (so tints fill the cell), and the `z`/`g` prefixes (bubbles has no key sequences).

`TestKeysMatchBindings` fails when a key press is matched by its `String()`. Add a binding instead, so the hint lines stay true.

## Checks

`go vet ./... && go test ./...`. `imports_test.go` enforces the package layering: nothing below `ui` imports Charm packages.
