// Command genfixtures writes test MP3s: the standard fixture set, or a
// generated library of many files for scale tests.
//
//	go run ./cmd/genfixtures -dir /tmp/lib -n 5000
//	go run ./cmd/genfixtures -dir /tmp/set -set
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/brennop/crates/internal/fixture"
)

func main() {
	dir := flag.String("dir", "", "output directory (required)")
	n := flag.Int("n", 5000, "number of files to generate")
	seed := flag.Int64("seed", 1, "random seed")
	set := flag.Bool("set", false, "write the standard fixture set instead")
	flag.Parse()
	if *dir == "" {
		flag.Usage()
		os.Exit(2)
	}
	var count int
	var err error
	if *set {
		var files map[string]fixture.File
		files, err = fixture.WriteSet(*dir)
		count = len(files)
	} else {
		var files []fixture.File
		files, err = fixture.GenerateLibrary(*dir, *n, *seed)
		count = len(files)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d files to %s\n", count, *dir)
}
