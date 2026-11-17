package fsops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brennop/crates/model"
)

// fakeRun replaces the command runner for one test and records each call as
// the command name followed by its arguments.
func fakeRun(t *testing.T, has bool, out []byte, err error) *[][]string {
	t.Helper()
	var calls [][]string
	realRun, realHas := run, hasTrashBin
	hasTrashBin = func() bool { return has }
	run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return out, err
	}
	t.Cleanup(func() { run, hasTrashBin = realRun, realHas })
	return &calls
}

func TestDeleteRemove(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.mp3")
	writeFile(t, p, "x")

	if err := Delete(p, ModeRemove); err != nil {
		t.Fatal(err)
	}
	mustMissing(t, p)
	if err := Delete(p, ModeRemove); err == nil {
		t.Error("removing a missing file should fail")
	}
}

func TestDeleteUnknownMode(t *testing.T) {
	if err := Delete("/nonexistent", Mode("bogus")); err == nil {
		t.Error("unknown mode should fail")
	}
}

func TestTrashUsesBinaryWhenPresent(t *testing.T) {
	calls := fakeRun(t, true, nil, nil)

	if err := Trash("/Music/a.mp3"); err != nil {
		t.Fatal(err)
	}
	want := []string{trashBin, "/Music/a.mp3"}
	if len(*calls) != 1 || !equal((*calls)[0], want) {
		t.Errorf("calls = %q, want %q", *calls, want)
	}
}

func TestTrashFallsBackToFinder(t *testing.T) {
	calls := fakeRun(t, false, nil, nil)

	if err := Trash("/Music/a b.mp3"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %q", *calls)
	}
	c := (*calls)[0]
	if c[0] != "osascript" || c[1] != "-e" || !strings.Contains(c[2], `tell application "Finder"`) {
		t.Errorf("osascript call = %q", c)
	}
	// The path is the last argument, passed as argv and never spliced into the script.
	if c[len(c)-1] != "/Music/a b.mp3" || strings.Contains(c[2], "a b.mp3") {
		t.Errorf("path not passed as argv: %q", c)
	}
}

func TestTrashAutomationDenied(t *testing.T) {
	out := []byte("execution error: Not authorized to send Apple events to Finder. (-1743)\n")
	fakeRun(t, false, out, errors.New("exit status 1"))

	err := Trash("/Music/a.mp3")
	var d model.ErrAutomationDenied
	if !errors.As(err, &d) {
		t.Fatalf("Trash error = %v, want ErrAutomationDenied", err)
	}
}

func TestTrashFailureReportsOutput(t *testing.T) {
	fakeRun(t, true, []byte("trash: no such file\n"), errors.New("exit status 1"))

	err := Trash("/Music/a.mp3")
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("Trash error = %v, want the command's output", err)
	}
	var d model.ErrAutomationDenied
	if errors.As(err, &d) {
		t.Error("a non-automation failure must not report automation denied")
	}
}

func TestTrashTimeout(t *testing.T) {
	realRun, realHas, realTimeout := run, hasTrashBin, trashTimeout
	trashTimeout = 50 * time.Millisecond
	run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	hasTrashBin = func() bool { return true }
	t.Cleanup(func() {
		run, hasTrashBin, trashTimeout = realRun, realHas, realTimeout
	})

	start := time.Now()
	err := Trash("/Music/a.mp3")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Trash error = %v, want a timeout", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("Trash took %v after a 50ms timeout", time.Since(start))
	}
}

func TestOsaErrorNumber(t *testing.T) {
	cases := map[string]int{
		"execution error: nope. (-1743)":                 -1743,
		"execution error: Nicht erlaubt. (-1743)\n":      -1743,
		"execution error: something (-128) then (-1743)": -1743,
		"no number here": 0,
		"":               0,
	}
	for in, want := range cases {
		if got := osaErrorNumber([]byte(in)); got != want {
			t.Errorf("osaErrorNumber(%q) = %d, want %d", in, got, want)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
