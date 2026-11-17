package fsops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/brennop/crates/model"
)

// Mode says what Delete does with a file. The values match config's
// delete_mode; fsops cannot import config, so app converts between them.
type Mode string

const (
	ModeTrash  Mode = "trash"
	ModeRemove Mode = "remove"
)

const trashBin = "/usr/bin/trash"

// The hooks below are variables so tests can replace them. Nothing else changes
// them.
var (
	trashTimeout = 15 * time.Second
	hasTrashBin  = func() bool {
		fi, err := os.Stat(trashBin)
		return err == nil && !fi.IsDir()
	}
	run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
)

// finderScript asks Finder to delete the file named by argv[1]. The path is
// passed as an argument, never spliced into the script.
const finderScript = `on run argv
	tell application "Finder" to delete (POSIX file (item 1 of argv) as alias)
end run`

// errNumber matches the parenthesized error number in an osascript message,
// such as "(-1743)". Messages are localized, so the number is the only thing
// matched.
var errNumber = regexp.MustCompile(`\((-?\d+)\)`)

const errAutomationDenied = -1743

// Delete removes path according to mode.
func Delete(path string, mode Mode) error {
	switch mode {
	case ModeTrash:
		return Trash(path)
	case ModeRemove:
		return os.Remove(path)
	}
	return fmt.Errorf("fsops: unknown delete mode %q", mode)
}

// TrashMethod is how Trash reaches the Trash.
type TrashMethod string

const (
	TrashCommand TrashMethod = "command" // /usr/bin/trash
	TrashFinder  TrashMethod = "finder"  // Finder through osascript; needs the automation permission
)

// Method reports which way Trash will go on this machine.
func Method() TrashMethod {
	if hasTrashBin() {
		return TrashCommand
	}
	return TrashFinder
}

// Trash moves path to the Trash. It uses /usr/bin/trash when that exists
// (macOS 15 and later) and falls back to Finder through osascript otherwise.
// Files moved this way get no Put Back. The command has trashTimeout to finish.
func Trash(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), trashTimeout)
	defer cancel()

	var out []byte
	var err error
	if hasTrashBin() {
		out, err = run(ctx, trashBin, path)
	} else {
		out, err = run(ctx, "osascript", "-e", finderScript, path)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("trash %s: timed out after %s", path, trashTimeout)
	}
	if err == nil {
		return nil
	}
	if osaErrorNumber(out) == errAutomationDenied {
		return model.ErrAutomationDenied{}
	}
	return fmt.Errorf("trash %s: %w: %s", path, err, strings.TrimSpace(string(out)))
}

// osaErrorNumber returns the error number in osascript output, or 0 if none.
func osaErrorNumber(out []byte) int {
	m := errNumber.FindAllStringSubmatch(string(out), -1)
	if len(m) == 0 {
		return 0
	}
	n, err := strconv.Atoi(m[len(m)-1][1])
	if err != nil {
		return 0
	}
	return n
}
