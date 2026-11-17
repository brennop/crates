// Command crates is the music library manager.
package main

import (
	"fmt"
	"os"

	"github.com/brennop/crates/config"
)

func main() {
	path, err := config.Path()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := config.Load(path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "crates: not wired up yet (Phase 9)")
}
