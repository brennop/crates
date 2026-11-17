package tags

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/brennop/crates/model"
)

// Timeouts for one kid3-cli call.
const (
	ReadTimeout  = 10 * time.Second
	WriteTimeout = 30 * time.Second
)

// tag2 is kid3's number for the ID3v2 tag.
const tag2 = 2

// kid3Config makes kid3-cli answer with English frame names. Without it the
// names follow the macOS UI language, and Frame.Name would change with it.
// See docs/kid3-cli.md.
const kid3Config = "[MainWindow]\nLanguage=en\n"

// Kid3Error is an error object kid3-cli answered with.
type Kid3Error struct {
	Code    int
	Message string
}

func (e *Kid3Error) Error() string {
	return fmt.Sprintf("kid3-cli: %s (code %d)", e.Message, e.Code)
}

// Kid3 is the Store: Summary is native, Full and Write go through kid3-cli.
// Its methods are safe from any goroutine.
type Kid3 struct {
	bin string // "" when kid3-cli was not found
	dir string // holds the private kid3 config
	cfg string
}

// New finds kid3-cli on PATH. When it is missing, New still succeeds:
// Summary works, Full and Write return model.ErrKid3Missing, and Available
// reports false. Close removes the private config New writes.
func New() (*Kid3, error) {
	bin, err := exec.LookPath("kid3-cli")
	if err != nil {
		return &Kid3{}, nil
	}
	return newKid3(bin)
}

func newKid3(bin string) (*Kid3, error) {
	dir, err := os.MkdirTemp("", "crates-kid3-")
	if err != nil {
		return nil, err
	}
	cfg := filepath.Join(dir, "kid3.ini")
	if err := os.WriteFile(cfg, []byte(kid3Config), 0o600); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &Kid3{bin: bin, dir: dir, cfg: cfg}, nil
}

// Available reports whether kid3-cli was found, for app.Capabilities.
func (k *Kid3) Available() bool { return k.bin != "" }

// Close removes the private kid3 config.
func (k *Kid3) Close() error {
	if k.dir == "" {
		return nil
	}
	return os.RemoveAll(k.dir)
}

// Summary reads natively; see the package function.
func (k *Kid3) Summary(path string) (model.TagSummary, time.Duration, error) {
	return Summary(path)
}

// Full lists every frame of the file's ID3v2 tag in one kid3-cli call. A
// file with no ID3v2 tag has no frames.
func (k *Kid3) Full(ctx context.Context, path string) ([]model.Frame, error) {
	res, err := k.run(ctx, ReadTimeout, []command{{"get", []any{"all", tag2}}}, []string{path})
	if err != nil {
		return nil, err
	}
	var ans struct {
		TaggedFile struct {
			FileName string `json:"fileName"`
			Tag2     *struct {
				Frames []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"frames"`
			} `json:"tag2"`
		} `json:"taggedFile"`
	}
	if err := json.Unmarshal(res[0], &ans); err != nil {
		return nil, fmt.Errorf("kid3-cli: unexpected answer to get all: %w", err)
	}
	if ans.TaggedFile.FileName == "" {
		// The answer kid3 gives for a path it silently failed to open.
		return nil, fmt.Errorf("kid3-cli did not open %s", path)
	}
	if ans.TaggedFile.Tag2 == nil {
		return []model.Frame{}, nil
	}
	frames := make([]model.Frame, 0, len(ans.TaggedFile.Tag2.Frames))
	seen := map[string]int{}
	for _, f := range ans.TaggedFile.Tag2.Frames {
		// kid3 matches names without regard to case, so index the same way.
		key := strings.ToLower(f.Name)
		frames = append(frames, model.Frame{Name: f.Name, Index: seen[key], Value: f.Value})
		seen[key]++
	}
	return frames, nil
}

// Write applies changes to every file in paths in one kid3-cli call, one
// command per change, in order; kid3 saves once at the end.
func (k *Kid3) Write(ctx context.Context, paths []string, changes []model.FrameChange) error {
	if len(paths) == 0 || len(changes) == 0 {
		return nil
	}
	cmds := make([]command, len(changes))
	for i, c := range changes {
		if c.Name == "" || c.Index < 0 {
			return fmt.Errorf("tags: bad frame change %+v", c)
		}
		cmds[i] = command{"set", []any{c.Ref(), c.Value, tag2}}
	}
	_, err := k.run(ctx, WriteTimeout, cmds, paths)
	return err
}

type command struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
}

// run calls kid3-cli once with one -c per command and the paths as
// arguments, never through a shell, and returns each command's result.
//
// kid3-cli skips every command and exits 0 when a path does not exist, so
// the paths are checked first and the answers counted. It also ignores a
// path spelled in NFD, answering as if the file had no tags, so paths go in
// as NFC; APFS and HFS+ find the file either way, and the stat catches a
// volume that does not. A failed save comes as an extra error object after
// the results. See docs/kid3-cli.md.
func (k *Kid3) run(ctx context.Context, timeout time.Duration, cmds []command, paths []string) ([]json.RawMessage, error) {
	if k.bin == "" {
		return nil, model.ErrKid3Missing{}
	}
	args := make([]string, 0, 2*len(cmds)+len(paths))
	for _, c := range cmds {
		b, err := json.Marshal(c)
		if err != nil {
			return nil, err
		}
		args = append(args, "-c", string(b))
	}
	for _, p := range paths {
		abs, err := filepath.Abs(p) // an absolute path cannot pass for a flag
		if err != nil {
			return nil, err
		}
		abs = norm.NFC.String(abs)
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("tags: %s is not a regular file", abs)
		}
		args = append(args, abs)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, k.bin, args...)
	cmd.Env = append(os.Environ(), "KID3_CONFIG_FILE="+k.cfg)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("kid3-cli: %w", ctx.Err())
	}

	results, err := parseAnswers(stdout.Bytes())
	if err != nil {
		return nil, err
	}
	if runErr != nil {
		return nil, fmt.Errorf("kid3-cli: %w: %s", runErr, strings.TrimSpace(stderr.String()))
	}
	if len(results) != len(cmds) {
		out := strings.TrimSpace(stdout.String() + " " + stderr.String())
		return nil, fmt.Errorf("kid3-cli: %d answers for %d commands: %s", len(results), len(cmds), out)
	}
	return results, nil
}

// parseAnswers reads the JSON objects kid3-cli prints, one per command plus
// any error. It returns the first error object as a *Kid3Error.
func parseAnswers(out []byte) ([]json.RawMessage, error) {
	var results []json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var a struct {
			Result *json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		err := dec.Decode(&a)
		if errors.Is(err, io.EOF) {
			return results, nil
		}
		if err != nil {
			return nil, fmt.Errorf("kid3-cli: unexpected output: %q", strings.TrimSpace(string(out)))
		}
		switch {
		case a.Error != nil:
			return nil, &Kid3Error{Code: a.Error.Code, Message: a.Error.Message}
		case a.Result != nil:
			results = append(results, *a.Result)
		default:
			// "result":null decodes to a nil pointer; it is still an answer.
			results = append(results, json.RawMessage("null"))
		}
	}
}
