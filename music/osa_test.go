//go:build darwin

package music

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/brennop/crates/model"
	"github.com/brennop/crates/pathnorm"
)

// stub is a fake osascript. It finds the script by the "// name:" comment
// that opens each script file, logs the call, and prints the next canned
// answer for that script: <name>.1, <name>.2, … in turn, then <name>.out.
// <name>.sleep delays it; <name>.err is printed on stderr. It also records
// a call that starts while another is running.
type stub struct {
	t   *testing.T
	dir string
	bin string
}

const stubScript = `#!/bin/sh
dir=%q
name=$(printf '%%s\n' "$4" | grep -m1 -oE '^// [a-z-]+:' | sed 's#^// ##; s#:$##')
shift 4
if [ -e "$dir/busy" ]; then echo overlap >> "$dir/overlap"; fi
touch "$dir/busy"
line="$name"
for a in "$@"; do line="$line	$a"; done
printf '%%s\n' "$line" >> "$dir/calls"
if [ -f "$dir/$name.sleep" ]; then sleep "$(cat "$dir/$name.sleep")"; fi
if [ -f "$dir/$name.err" ]; then cat "$dir/$name.err" >&2; fi
out="$dir/$name.out"
for i in 1 2 3 4 5 6 7 8 9; do
  if [ -f "$dir/$name.$i" ]; then out="$dir/$name.$i.used"; mv "$dir/$name.$i" "$out"; break; fi
done
if [ -f "$out" ]; then cat "$out"; fi
rm -f "$dir/busy"
`

func newStub(t *testing.T) *stub {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "osascript")
	if err := os.WriteFile(bin, fmt.Appendf(nil, stubScript, dir), 0o755); err != nil {
		t.Fatal(err)
	}
	return &stub{t: t, dir: dir, bin: bin}
}

// answer sets the standing answer for a script.
func (s *stub) answer(script, out string) { s.write(script+".out", out) }

// answers queues one-off answers, used before the standing one.
func (s *stub) answers(script string, outs ...string) {
	for i, o := range outs {
		s.write(fmt.Sprintf("%s.%d", script, i+1), o)
	}
}

func (s *stub) write(name, data string) {
	s.t.Helper()
	if err := os.WriteFile(filepath.Join(s.dir, name), []byte(data), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

// calls returns each call as script name followed by its argv.
func (s *stub) calls() [][]string {
	b, err := os.ReadFile(filepath.Join(s.dir, "calls"))
	if err != nil {
		return nil
	}
	var out [][]string
	for l := range strings.SplitSeq(strings.TrimSuffix(string(b), "\n"), "\n") {
		out = append(out, strings.Split(l, "\t"))
	}
	return out
}

func (s *stub) names() []string {
	var out []string
	for _, c := range s.calls() {
		out = append(out, c[0])
	}
	return out
}

func (s *stub) client(opts Options) *OSA {
	s.t.Helper()
	opts.Bin = s.bin
	c := New(opts).(*OSA)
	s.t.Cleanup(func() { c.Close() })
	return c
}

func ok(result string) string { return `{"ok":true,"result":` + result + "}\n" }

func fail(n int) string {
	return fmt.Sprintf(`{"ok":false,"error":{"number":%d,"message":"localized text"}}`+"\n", n)
}

var ctx = context.Background()

func TestScriptsEmbedded(t *testing.T) {
	for _, name := range []string{scriptStatus, scriptLaunch, scriptSnapshot, scriptAdd, scriptTrack, scriptRefresh,
		scriptDelete, scriptPlaylist, scriptPlaylistAdd, scriptPlaylistRemove, scriptMakePlaylist, scriptEcho} {
		src, err := source(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(src, "function run(argv)") || !strings.Contains(src, "// "+name+":") {
			t.Errorf("%s: prelude or header missing", name)
		}
	}
	if _, err := source("nope"); err == nil {
		t.Error("unknown script accepted")
	}
}

// TestRealOsascriptArgv runs the echo script through the real osascript.
// It does not touch Music.
func TestRealOsascriptArgv(t *testing.T) {
	if _, err := exec.LookPath("osascript"); err != nil {
		t.Skip("no osascript")
	}
	c := New(Options{}).(*OSA)
	defer c.Close()
	args := []string{`a "quoted" name`, "it's", "-leading-dash", "Café Été", "`$(touch /tmp/x)`", ""}
	var res struct {
		Argv []string `json:"argv"`
	}
	if err := c.decodeOnce(ctx, &res, scriptEcho, args...); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Argv, args) {
		t.Errorf("argv = %q, want %q", res.Argv, args)
	}
}

func TestErrorsByNumber(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		stderr string
		check  func(error) bool
	}{
		{"not running", fail(-600), "", func(err error) bool { return errors.As(err, new(model.ErrMusicUnavailable)) }},
		{"denied", fail(-1743), "", func(err error) bool { return errors.As(err, new(model.ErrAutomationDenied)) }},
		{"no object", fail(-1728), "", func(err error) bool { return errors.Is(err, ErrNotFound) }},
		{"denied on stderr", "", "execution error: Não autorizado. (-1743)\n", func(err error) bool {
			return errors.As(err, new(model.ErrAutomationDenied))
		}},
		{"other", fail(-1700), "", func(err error) bool {
			var se *ScriptError
			return errors.As(err, &se) && se.Number == -1700 && se.Script == scriptTrack && !errors.Is(err, ErrNotFound)
		}},
		{"garbage", "not json\n", "", func(err error) bool {
			var se *ScriptError
			return errors.As(err, &se) && se.Number == 0
		}},
		{"null number", `{"ok":false,"error":{"number":null,"message":"boom"}}`, "", func(err error) bool {
			var se *ScriptError
			return errors.As(err, &se) && se.Number == 0 && se.Message == "boom"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub(t)
			s.answer(scriptTrack, tc.stdout)
			if tc.stderr != "" {
				s.write(scriptTrack+".err", tc.stderr)
			}
			_, err := s.client(Options{}).call(ctx, scriptTrack, "X")
			if err == nil || !tc.check(err) {
				t.Errorf("err = %#v (%v)", err, err)
			}
		})
	}
}

func TestAutolaunch(t *testing.T) {
	snap := ok(`{"tracks":[],"playlists":[]}`)

	s := newStub(t)
	s.answers(scriptSnapshot, fail(-600))
	s.answer(scriptSnapshot, snap)
	if _, err := s.client(Options{}).Snapshot(ctx); !errors.As(err, new(model.ErrMusicUnavailable)) {
		t.Errorf("autolaunch off: err = %v", err)
	}
	if got := s.names(); !slices.Equal(got, []string{scriptSnapshot}) {
		t.Errorf("autolaunch off: calls = %v; Music must not be launched", got)
	}

	s = newStub(t)
	s.answers(scriptSnapshot, fail(-600))
	s.answer(scriptSnapshot, snap)
	s.answer(scriptLaunch, ok(`{"running":true}`))
	if _, err := s.client(Options{Autolaunch: true}).Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s.names(); !slices.Equal(got, []string{scriptSnapshot, scriptLaunch, scriptSnapshot}) {
		t.Errorf("autolaunch on: calls = %v", got)
	}
}

func TestStatusAndLaunch(t *testing.T) {
	s := newStub(t)
	s.answers(scriptStatus, ok(`{"running":false}`), fail(-1743))
	s.answer(scriptStatus, ok(`{"running":true}`))
	s.answer(scriptLaunch, ok(`{"running":true}`))
	c := s.client(Options{Autolaunch: true})
	if st, err := c.Status(ctx); err != nil || st != model.MusicUnavailable {
		t.Errorf("not running: %v, %v", st, err)
	}
	if st, err := c.Status(ctx); st != model.MusicDenied || !errors.As(err, new(model.ErrAutomationDenied)) {
		t.Errorf("denied: %v, %v", st, err)
	}
	if st, err := c.Status(ctx); err != nil || st != model.MusicAvailable {
		t.Errorf("running: %v, %v", st, err)
	}
	if err := c.Launch(ctx); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.Join(s.names(), " "), scriptLaunch); n != 1 {
		t.Errorf("Status launched Music: calls = %v", s.names())
	}
}

func TestTimeoutAndClose(t *testing.T) {
	s := newStub(t)
	s.write(scriptTrack+".sleep", "5")
	s.answer(scriptTrack, ok(`{}`))
	c := s.client(Options{CallTimeout: 200 * time.Millisecond})
	start := time.Now()
	_, err := c.call(ctx, scriptTrack, "X")
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Errorf("err = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("timeout took %v", d)
	}

	c = s.client(Options{})
	cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, err := c.call(cctx, scriptTrack, "X"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("caller deadline: err = %v", err)
	}

	errc := make(chan error, 1)
	go func() {
		_, err := c.call(ctx, scriptTrack, "X")
		errc <- err
	}()
	time.Sleep(200 * time.Millisecond)
	start = time.Now()
	c.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("in flight at Close: err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not stop the script in flight")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Close took %v", d)
	}
	if _, err := c.call(ctx, scriptTrack, "X"); !errors.Is(err, ErrClosed) {
		t.Errorf("after Close: err = %v", err)
	}
}

func TestOneScriptAtATime(t *testing.T) {
	s := newStub(t)
	s.write(scriptTrack+".sleep", "0.05")
	s.answer(scriptTrack, ok(`{"id":"X"}`))
	c := s.client(Options{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.call(ctx, scriptTrack, "X"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(s.calls()) != 8 {
		t.Errorf("calls = %d", len(s.calls()))
	}
	if _, err := os.Stat(filepath.Join(s.dir, "overlap")); err == nil {
		t.Error("two scripts ran at once")
	}
}

func TestSnapshotBuild(t *testing.T) {
	root, err := pathnorm.Normalize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nfc := filepath.Join(root, "Café Été.mp3")
	if err := os.WriteFile(nfc, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	nfd := norm.NFD.String(nfc)
	s := newStub(t)
	s.answer(scriptSnapshot, ok(fmt.Sprintf(`{"tracks":[{"id":"A","location":%q},{"id":"B","location":null},{"id":"C","location":%q}],
	 "playlists":[
	  {"id":"P1","name":"Plain","smart":false,"folder":false,"special":"none","parent":"L","trackIDs":["A","C","A"]},
	  {"id":"P2","name":"Smart","smart":true,"folder":false,"special":"none","parent":"L","trackIDs":["A"]},
	  {"id":"P3","name":"Folder","smart":false,"folder":true,"special":"folder","parent":"L","trackIDs":[]},
	  {"id":"P4","name":"Música","smart":true,"folder":false,"special":"Music","parent":"L","trackIDs":["A","C"]}],
	 "ms":{"tracks":1,"playlists":1}}`, nfd, root+"/gone.mp3")))
	before := time.Now()
	snap, err := s.client(Options{}).Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Taken.Before(before) || snap.Taken.After(time.Now()) {
		t.Errorf("taken = %v", snap.Taken)
	}
	if snap.Tracks[nfc] != "A" || snap.Tracks[root+"/gone.mp3"] != "C" || len(snap.Tracks) != 2 {
		t.Errorf("tracks = %q", snap.Tracks)
	}
	want := []model.Playlist{
		{ID: "P1", Name: "Plain", Editable: true},
		{ID: "P2", Name: "Smart"},
		{ID: "P3", Name: "Folder"},
		{ID: "P4", Name: "Música"},
	}
	if !slices.Equal(snap.Playlists, want) {
		t.Errorf("playlists = %+v", snap.Playlists)
	}
	if !slices.Equal(snap.Members["P1"], []string{"A", "C", "A"}) || len(snap.Members["P3"]) != 0 {
		t.Errorf("members = %q", snap.Members)
	}
}

func TestAdd(t *testing.T) {
	root, err := pathnorm.Normalize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "Ação.mp3")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	info := func(id, loc string) string { return ok(fmt.Sprintf(`{"id":%q,"location":%q}`, id, loc)) }

	s := newStub(t)
	s.answers(scriptAdd, info("A", norm.NFD.String(path)), `{"ok":true,"result":{"id":"B","location":null}}`)
	s.answer(scriptAdd, info("C", "/Users/x/Music/Media.localized/Music/Unknown/Ação.mp3"))
	s.answer(scriptStatus, ok(`{"running":true}`))
	c := s.client(Options{})

	if id, err := c.Add(ctx, path); err != nil || id != "A" {
		t.Errorf("NFD location: %q, %v", id, err)
	}
	if _, err := c.Add(ctx, path); !errors.As(err, new(model.ErrVerifyFailed)) {
		t.Errorf("null location: %v", err)
	}
	var cs model.ErrCopySettingOn
	if _, err := c.Add(ctx, path); !errors.As(err, &cs) || cs.Path != path || !strings.Contains(cs.Location, "Media.localized") {
		t.Errorf("copy setting: %v", err)
	}
	n := len(s.calls())
	if _, err := c.Add(ctx, path); !errors.As(err, &cs) {
		t.Errorf("after latch: %v", err)
	}
	if len(s.calls()) != n {
		t.Error("an add ran after the copy setting was found on")
	}
	if st, err := c.Status(ctx); err != nil || st != model.MusicCopySettingOn {
		t.Errorf("status after latch: %v, %v", st, err)
	}
	if got := s.calls()[0]; !slices.Equal(got, []string{scriptAdd, path}) {
		t.Errorf("add argv = %q", got)
	}
}

func TestAddIgnoresCaseOnCaseInsensitiveVolume(t *testing.T) {
	root, err := pathnorm.Normalize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cs, err := pathnorm.CaseSensitive(root); err != nil || cs {
		t.Skip("temp dir is case-sensitive")
	}
	path := filepath.Join(root, "Song.mp3")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s := newStub(t)
	s.answer(scriptAdd, ok(fmt.Sprintf(`{"id":"A","location":%q}`, filepath.Join(root, "SONG.mp3"))))
	if id, err := s.client(Options{}).Add(ctx, path); err != nil || id != "A" {
		t.Errorf("%q, %v", id, err)
	}
}

func TestPlaylistWrites(t *testing.T) {
	m := func(before, after string) string {
		return ok(fmt.Sprintf(`{"before":%s,"trackIDs":%s}`, before, after))
	}
	s := newStub(t)
	s.answers(scriptPlaylistAdd,
		m(`["A"]`, `["A","B"]`),     // fine: A already there, B added
		m(`["A","Z"]`, `["Z","B"]`), // A vanished during the write
		m(`["A"]`, `["A","B","B"]`), // B added twice
	)
	s.answers(scriptPlaylistRemove,
		m(`["A","B","A"]`, `["B"]`),
		m(`["A","B"]`, `["A","B"]`),
	)
	c := s.client(Options{})

	got, err := c.AddToPlaylist(ctx, "P", []string{"A", "B"})
	if err != nil || !slices.Equal(got, []string{"A", "B"}) {
		t.Errorf("add: %q, %v", got, err)
	}
	if got := s.calls()[0]; !slices.Equal(got, []string{scriptPlaylistAdd, "P", "A", "B"}) {
		t.Errorf("argv = %q", got)
	}
	if _, err := c.AddToPlaylist(ctx, "P", []string{"B"}); !errors.As(err, new(model.ErrVerifyFailed)) {
		t.Errorf("truncated: %v", err)
	}
	got, err = c.AddToPlaylist(ctx, "P", []string{"B"})
	if !errors.As(err, new(model.ErrVerifyFailed)) || len(got) != 3 {
		t.Errorf("doubled: %q, %v", got, err)
	}
	if got, err := c.RemoveFromPlaylist(ctx, "P", []string{"A"}); err != nil || !slices.Equal(got, []string{"B"}) {
		t.Errorf("remove: %q, %v", got, err)
	}
	if _, err := c.RemoveFromPlaylist(ctx, "P", []string{"A"}); !errors.As(err, new(model.ErrVerifyFailed)) {
		t.Errorf("remove did nothing: %v", err)
	}
}

func TestDeleteAndRefresh(t *testing.T) {
	s := newStub(t)
	s.answers(scriptDelete, ok(`{"deleted":"A","remaining":0}`), ok(`{"deleted":"A","remaining":1}`))
	s.answers(scriptRefresh, ok(`{"id":"A","location":"/x"}`), fail(-1728))
	c := s.client(Options{})
	if err := c.DeleteTrack(ctx, "A"); err != nil {
		t.Error(err)
	}
	if err := c.DeleteTrack(ctx, "A"); !errors.As(err, new(model.ErrVerifyFailed)) {
		t.Errorf("still there: %v", err)
	}
	if err := c.Refresh(ctx, "A"); err != nil {
		t.Error(err)
	}
	if err := c.Refresh(ctx, "A"); !errors.Is(err, ErrNotFound) {
		t.Errorf("gone: %v", err)
	}
}
