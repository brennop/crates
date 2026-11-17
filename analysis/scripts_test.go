package analysis

import (
	"context"
	"os"
	"sync"
	"testing"
)

// TestScripts runs the real audio jobs, in parallel, on the file named by
// CRATES_ANALYSIS_AUDIO. It needs uv and, on a first run, the network.
func TestScripts(t *testing.T) {
	path := os.Getenv("CRATES_ANALYSIS_AUDIO")
	if path == "" {
		t.Skip("set CRATES_ANALYSIS_AUDIO to an audio file to run the scripts")
	}
	a := New(Options{ScriptDir: t.TempDir()})
	var wg sync.WaitGroup
	for _, job := range []Job{BPM, SKey, EssentiaKey} {
		wg.Go(func() {
			r := a.Run(context.Background(), job, Track{Path: path})
			if r.Err != nil {
				t.Errorf("%s: %v", job, r.Err)
				return
			}
			t.Logf("%s: %+v", job, r)
			if job == BPM && r.BPM <= 0 || job != BPM && len(r.Keys) == 0 {
				t.Errorf("%s found nothing: %+v", job, r)
			}
		})
	}
	wg.Wait()
	if r := a.Run(context.Background(), BPM, Track{Path: path + ".missing"}); r.Err == nil {
		t.Error("a missing file gave no error")
	} else {
		t.Logf("missing file: %v", r.Err)
	}
}
