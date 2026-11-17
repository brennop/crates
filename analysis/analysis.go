// Package analysis guesses a track's tempo and key from its audio, and
// looks the track up on Beatport. Each Job runs on its own, so a caller can
// run them in parallel and show each result as it arrives.
//
// The audio jobs are Python scripts, embedded here and run with
// `uv run --script`, which installs their pinned dependencies (Essentia,
// Deezer's S-KEY and PyTorch) into a cached environment on first use. That
// first run downloads a few hundred MB and takes minutes; later runs take
// seconds.
package analysis

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Job is one analysis of a track.
type Job int

const (
	BPM         Job = iota // tempo, Essentia's RhythmExtractor2013
	SKey                   // key, Deezer's S-KEY model
	EssentiaKey            // key, Essentia's KeyExtractor with its EDM profiles
	Beatport               // BPM and key from Beatport's catalog
)

// Jobs lists every job, in the order a panel shows them.
var Jobs = []Job{BPM, SKey, EssentiaKey, Beatport}

func (j Job) String() string {
	switch j {
	case BPM:
		return "BPM · Essentia"
	case SKey:
		return "Key · S-KEY"
	case EssentiaKey:
		return "Key · Essentia"
	case Beatport:
		return "Beatport"
	}
	return fmt.Sprintf("Job(%d)", int(j))
}

// Timeouts. A script's first run installs its environment, so it gets a
// long one.
const (
	ScriptTimeout   = 15 * time.Minute
	BeatportTimeout = 15 * time.Second
)

// KeyGuess is one key estimate. Label names the method or profile when a
// job makes several guesses.
type KeyGuess struct {
	Label    string
	Key      string  // as the method writes it, e.g. "A minor"
	Strength float64 // the method's own confidence; 0 when it gives none
}

// Result is what one job found. Fields a job does not fill stay zero.
type Result struct {
	Job        Job
	BPM        float64
	Confidence float64 // Essentia's beat confidence, 0–5.32
	Keys       []KeyGuess
	Match      string // Beatport: "artists – title (mix)" of the matched track
	Err        error
}

// Track is what a job needs to know about a track.
type Track struct {
	Path, Artist, Title string
}

// Options configure an Analyzer.
type Options struct {
	// BeatportToken is an OAuth access token for Beatport's v4 API. It is
	// used as is and never refreshed.
	BeatportToken string
	// BeatportTokenFile holds a Beatport token response (access_token,
	// refresh_token, expires_in), used when BeatportToken is "". The job
	// refreshes the access token when it expires and writes the new pair
	// back. With neither, the Beatport job returns ErrNoBeatportToken.
	BeatportTokenFile string
	// ScriptDir is where the embedded scripts are written. "" means the
	// user cache dir.
	ScriptDir string
}

// ErrNoBeatportToken: the Beatport job has no token to call the API with.
var ErrNoBeatportToken = errors.New("no Beatport token: set BEATPORT_TOKEN or save a token response to beatport.json beside the config")

// ErrNoUV: uv is not on PATH, so the audio jobs cannot run.
var ErrNoUV = errors.New("uv not found on PATH: install it (brew install uv)")

// Analyzer runs jobs. Its methods are safe from any goroutine.
type Analyzer struct {
	opts   Options
	client *http.Client
	api    string     // Beatport API base, swappable in tests
	tokMu  sync.Mutex // one token refresh at a time: a refresh token works once

	once   sync.Once
	dir    string
	dirErr error
}

//go:embed scripts/*.py
var scripts embed.FS

// New returns an Analyzer. It does not look for uv or write the scripts
// until a job needs them.
func New(o Options) *Analyzer {
	return &Analyzer{
		opts:   o,
		client: &http.Client{Timeout: BeatportTimeout},
		api:    "https://api.beatport.com/v4",
	}
}

// Run runs job on t. Cancelling ctx kills a script in flight.
func (a *Analyzer) Run(ctx context.Context, job Job, t Track) Result {
	r := Result{Job: job}
	switch job {
	case BPM:
		var out struct{ BPM, Confidence float64 }
		r.Err = a.script(ctx, &out, "essentia_analyze.py", "bpm", t.Path)
		r.BPM, r.Confidence = out.BPM, out.Confidence
	case SKey:
		var out struct{ Key string }
		r.Err = a.script(ctx, &out, "skey_analyze.py", t.Path)
		if r.Err == nil {
			r.Keys = []KeyGuess{{Key: out.Key}}
		}
	case EssentiaKey:
		var out struct {
			Keys []struct {
				Profile, Key string
				Strength     float64
			}
		}
		r.Err = a.script(ctx, &out, "essentia_analyze.py", "key", t.Path)
		for _, k := range out.Keys {
			r.Keys = append(r.Keys, KeyGuess{Label: k.Profile, Key: k.Key, Strength: k.Strength})
		}
	case Beatport:
		r = a.beatport(ctx, t)
		r.Job = job
	default:
		r.Err = fmt.Errorf("analysis: unknown job %d", job)
	}
	return r
}

// script runs one embedded script with args and decodes its JSON output
// into out. On failure the error carries the last line of its stderr.
func (a *Analyzer) script(ctx context.Context, out any, name string, args ...string) error {
	uv, err := exec.LookPath("uv")
	if err != nil {
		return ErrNoUV
	}
	dir, err := a.scriptDir()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, ScriptTimeout)
	defer cancel()
	stdout, stderr, err := runGroup(ctx, uv, append([]string{"run", "--quiet", "--script", filepath.Join(dir, name)}, args...)...)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, ErrTooMuchMemory) {
			return err
		}
		if msg := lastLine(string(stderr)); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	if err := json.Unmarshal(stdout, out); err != nil {
		return fmt.Errorf("%s: bad output: %w", name, err)
	}
	return nil
}

// scriptDir writes the embedded scripts out once, rewriting any that
// differ, and returns their directory.
func (a *Analyzer) scriptDir() (string, error) {
	a.once.Do(func() {
		dir := a.opts.ScriptDir
		if dir == "" {
			cache, err := os.UserCacheDir()
			if err != nil {
				a.dirErr = err
				return
			}
			dir = filepath.Join(cache, "crates", "analysis")
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			a.dirErr = err
			return
		}
		entries, _ := scripts.ReadDir("scripts")
		for _, e := range entries {
			data, _ := scripts.ReadFile("scripts/" + e.Name())
			p := filepath.Join(dir, e.Name())
			if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, data) {
				continue
			}
			if err := os.WriteFile(p, data, 0o644); err != nil {
				a.dirErr = err
				return
			}
		}
		a.dir = dir
	})
	return a.dir, a.dirErr
}

// lastLine is the last non-blank line of s, where Python puts the
// exception.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
