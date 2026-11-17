//go:build darwin && cratesint

// The Finder trash fallback against the real Finder. Run by hand:
//
//	CRATES_INT_FINDER=1 go test -tags cratesint -run Integration -v ./fsops/
//
// It sends one temp file to the Trash through Finder, as on macOS before 15.
// With CRATES_INT_EXPECT=denied, Automation for Finder must be off for this
// terminal, and the call must fail with ErrAutomationDenied (-1743).
package fsops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brennop/crates/model"
)

func TestIntegrationFinderTrash(t *testing.T) {
	if os.Getenv("CRATES_INT_FINDER") == "" {
		t.Skip("set CRATES_INT_FINDER=1; this sends a file to the Trash through Finder")
	}
	orig := hasTrashBin
	hasTrashBin = func() bool { return false }
	defer func() { hasTrashBin = orig }()

	p := filepath.Join(t.TempDir(), fmt.Sprintf("crates-finder-trash-%d.txt", time.Now().UnixNano()))
	if err := os.WriteFile(p, []byte("crates integration"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Trash(p)
	if os.Getenv("CRATES_INT_EXPECT") == "denied" {
		if !errors.As(err, new(model.ErrAutomationDenied)) {
			t.Fatalf("err = %v, want ErrAutomationDenied", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("file still there: %v", err)
	}
}
