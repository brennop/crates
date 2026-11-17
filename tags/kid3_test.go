package tags

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/brennop/crates/internal/fixture"
	"github.com/brennop/crates/model"
)

func realKid3(t *testing.T) *Kid3 {
	t.Helper()
	if _, err := exec.LookPath("kid3-cli"); err != nil {
		t.Skip("kid3-cli not installed")
	}
	k, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { k.Close() })
	if !k.Available() {
		t.Fatal("kid3-cli on PATH but not available")
	}
	return k
}

func frameMap(frames []model.Frame) map[string]string {
	m := map[string]string{}
	for _, f := range frames {
		m[model.FrameChange{Name: f.Name, Index: f.Index}.Ref()] = f.Value
	}
	return m
}

// TestFullFixtures reads every fixture with kid3 and compares it with the
// native summary. Known disagreements are listed in docs/kid3-cli.md.
func TestFullFixtures(t *testing.T) {
	k := realKid3(t)
	set, err := fixture.WriteSet(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, f := range set {
		t.Run(name, func(t *testing.T) {
			frames, err := k.Full(ctx, f.Path)
			if err != nil {
				t.Fatalf("Full: %v", err)
			}
			if f.Tag == fixture.ID3v1 || f.Tag == fixture.Untagged {
				// Full reads tag 2 only; the native reader falls back to ID3v1.
				if len(frames) != 0 {
					t.Errorf("frames = %v, want none", frames)
				}
				return
			}
			got := frameMap(frames)
			s := f.Summary
			want := map[string]string{}
			for name, field := range FakeFieldFrames {
				if v, _ := s.Field(field); v != "" {
					want[name] = v
				}
			}
			if len(got) != len(want) {
				t.Errorf("frames %v, want %v", got, want)
			}
			for n, v := range want {
				if got[n] != v {
					t.Errorf("%s = %q, want %q", n, got[n], v)
				}
			}
		})
	}
}

// TestWriteRoundTrip writes quotes and Unicode into files whose paths hold
// them too, reads them back through kid3 and natively, and checks that both
// files of a multi-path write changed.
func TestWriteRoundTrip(t *testing.T) {
	k := realKid3(t)
	dir := filepath.Join(t.TempDir(), `dir "with" 'quotes' ☃`)
	ctx := context.Background()
	paths := map[fixture.TagKind]string{
		fixture.ID3v23: filepath.Join(dir, norm.NFD.String(`Café "One" it's.mp3`)),
		fixture.ID3v24: filepath.Join(dir, `日本語 & $HOME ; rm -rf.mp3`),
		fixture.ID3v1:  filepath.Join(dir, `-c dash.mp3`),
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var all []string
	for kind, p := range paths {
		if err := os.WriteFile(p, fixture.MP3(fixture.Options{Tag: kind, Summary: fixture.Full}), 0o644); err != nil {
			t.Fatal(err)
		}
		all = append(all, p)
	}
	slices.Sort(all)

	title := `It's "Ça" ☃ 日本語 \ back`
	changes := []model.FrameChange{
		{Name: "Title", Value: title},
		{Name: "Initial Key", Value: "8A"},
		{Name: "BPM", Value: "174"},
		{Name: "Comment", Value: ""}, // delete
		{Name: "Artist", Index: 1, Value: "Second Artist"},
		{Name: "My Custom", Value: `x"y`},
	}
	if err := k.Write(ctx, all, changes); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for kind, p := range paths {
		frames, err := k.Full(ctx, p)
		if err != nil {
			t.Fatalf("%v Full: %v", kind, err)
		}
		got := frameMap(frames)
		want := map[string]string{
			"Title": title, "Initial Key": "8A", "BPM": "174", "Comment": "",
			"Artist": "Artist", "Artist[1]": "Second Artist", "My Custom": `x"y`,
		}
		artist := "Artist"
		if kind == fixture.ID3v1 {
			// The write creates tag 2. It has no Artist yet, so setting
			// Artist[1] adds the first one.
			want["Artist"], want["Artist[1]"] = "Second Artist", ""
			artist = "Second Artist"
		}
		for ref, want := range want {
			if got[ref] != want {
				t.Errorf("%v: %s = %q, want %q", kind, ref, got[ref], want)
			}
		}
		s, _, err := Summary(p)
		if err != nil {
			t.Fatalf("%v Summary: %v", kind, err)
		}
		if s.Title != title || s.Key != "8A" || s.BPM != 174 || s.Comment != "" || s.Artist != artist {
			t.Errorf("%v: native summary %+v", kind, s)
		}
	}

	// Deleting the second artist renumbers nothing else.
	v24 := paths[fixture.ID3v24]
	if err := k.Write(ctx, []string{v24}, []model.FrameChange{{Name: "artist", Index: 1}}); err != nil {
		t.Fatal(err)
	}
	frames, _ := k.Full(ctx, v24)
	if m := frameMap(frames); m["Artist[1]"] != "" || m["Artist"] != "Artist" {
		t.Errorf("after delete: %v", m)
	}
}

func TestKid3MissingPath(t *testing.T) {
	k := realKid3(t)
	dir := t.TempDir()
	good := filepath.Join(dir, "good.mp3")
	os.WriteFile(good, fixture.MP3(fixture.Options{Tag: fixture.ID3v24, Summary: fixture.Full}), 0o644)
	missing := filepath.Join(dir, "missing.mp3")
	ctx := context.Background()

	if _, err := k.Full(ctx, missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Full missing: %v", err)
	}
	err := k.Write(ctx, []string{good, missing}, []model.FrameChange{{Name: "Title", Value: "changed"}})
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Write with a missing path: %v", err)
	}
	if s, _, _ := Summary(good); s.Title != fixture.Full.Title {
		t.Errorf("good file changed to %q", s.Title)
	}
}

func TestKid3SaveError(t *testing.T) {
	k := realKid3(t)
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	p := filepath.Join(t.TempDir(), "ro.mp3")
	os.WriteFile(p, fixture.MP3(fixture.Options{Tag: fixture.ID3v24, Summary: fixture.Full}), 0o444)
	err := k.Write(context.Background(), []string{p}, []model.FrameChange{{Name: "Title", Value: "x"}})
	var ke *Kid3Error
	if !errors.As(err, &ke) {
		t.Fatalf("err = %v, want *Kid3Error", err)
	}
}

func TestKid3Missing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	k, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if k.Available() {
		t.Error("Available with an empty PATH")
	}
	var want model.ErrKid3Missing
	if _, err := k.Full(context.Background(), "/x.mp3"); !errors.As(err, &want) {
		t.Errorf("Full: %v", err)
	}
	if err := k.Write(context.Background(), []string{"/x.mp3"}, []model.FrameChange{{Name: "Title"}}); !errors.As(err, &want) {
		t.Errorf("Write: %v", err)
	}
}

// stub returns a Kid3 that runs a shell script in place of kid3-cli. The
// script prints $STUB_OUT, records its arguments one per line in
// $STUB_ARGS, sleeps $STUB_SLEEP seconds and exits with $STUB_EXIT.
func stub(t *testing.T) (*Kid3, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "kid3-cli")
	script := `#!/bin/sh
for a in "$@"; do printf '%s\n' "$a"; done > "$STUB_ARGS"
printf '%s' "$KID3_CONFIG_FILE" > "$STUB_ARGS.cfg"
printf '%s' "$STUB_OUT"
sleep "${STUB_SLEEP:-0}"
exit "${STUB_EXIT:-0}"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	k, err := newKid3(bin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { k.Close() })
	args := filepath.Join(dir, "args")
	t.Setenv("STUB_ARGS", args)
	t.Setenv("STUB_SLEEP", "0")
	t.Setenv("STUB_EXIT", "0")
	return k, args
}

func TestRunStub(t *testing.T) {
	k, argsFile := stub(t)
	mp3 := filepath.Join(t.TempDir(), `a "b".mp3`)
	os.WriteFile(mp3, nil, 0o644)
	ctx := context.Background()
	ch := []model.FrameChange{{Name: "Title", Value: `"q"`}, {Name: "Artist", Index: 2, Value: "x"}}

	t.Run("ok", func(t *testing.T) {
		t.Setenv("STUB_OUT", `{"result":null}`+"\n"+`{"result":null}`+"\n")
		if err := k.Write(ctx, []string{mp3}, ch); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(argsFile)
		want := []string{
			"-c", `{"method":"set","params":["Title","\"q\"",2]}`,
			"-c", `{"method":"set","params":["Artist[2]","x",2]}`,
			mp3,
		}
		if got := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n"); !slices.Equal(got, want) {
			t.Errorf("args\n got %q\nwant %q", got, want)
		}
		cfg, _ := os.ReadFile(argsFile + ".cfg")
		if b, err := os.ReadFile(string(cfg)); err != nil || !strings.Contains(string(b), "Language=en") {
			t.Errorf("KID3_CONFIG_FILE %q: %q, %v", cfg, b, err)
		}
	})

	cases := []struct {
		name, out, exit, sleep string
		check                  func(error) bool
	}{
		{"too few answers", `{"result":null}`, "0", "0", func(e error) bool { return strings.Contains(e.Error(), "1 answers for 2") }},
		{"error object", `{"result":null}{"error":{"code":-1,"message":"Error while writing file"}}`, "1", "0",
			func(e error) bool {
				var ke *Kid3Error
				return errors.As(e, &ke) && ke.Message == "Error while writing file"
			}},
		{"pretty error after results", "{\"result\":null}\n{\"result\":null}\n{\n    \"error\": {\n        \"code\": -1,\n        \"message\": \"denied\"\n    }\n}\n", "1", "0",
			func(e error) bool { var ke *Kid3Error; return errors.As(e, &ke) && ke.Message == "denied" }},
		{"plain text", "x.mp3 does not exist", "0", "0", func(e error) bool { return strings.Contains(e.Error(), "unexpected output") }},
		{"bad exit", `{"result":null}{"result":null}`, "3", "0", func(e error) bool { return strings.Contains(e.Error(), "exit status 3") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("STUB_OUT", c.out)
			t.Setenv("STUB_EXIT", c.exit)
			err := k.Write(ctx, []string{mp3}, ch)
			if err == nil || !c.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}

	t.Run("timeout", func(t *testing.T) {
		t.Setenv("STUB_OUT", "")
		t.Setenv("STUB_SLEEP", "5")
		ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := k.Write(ctx, []string{mp3}, ch)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v", err)
		}
		if time.Since(start) > 3*time.Second {
			t.Errorf("took %v", time.Since(start))
		}
	})

	t.Run("no changes, no call", func(t *testing.T) {
		os.Remove(argsFile)
		if err := k.Write(ctx, []string{mp3}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
			t.Error("kid3-cli ran for an empty write")
		}
	})

	t.Run("bad change", func(t *testing.T) {
		if err := k.Write(ctx, []string{mp3}, []model.FrameChange{{Value: "x"}}); err == nil {
			t.Error("empty name accepted")
		}
	})
}

func TestFullIndexes(t *testing.T) {
	k, _ := stub(t)
	mp3 := filepath.Join(t.TempDir(), "a.mp3")
	os.WriteFile(mp3, nil, 0o644)
	t.Setenv("STUB_OUT", `{"result":{"taggedFile":{"fileName":"a.mp3","tag2":{"frames":[`+
		`{"name":"Artist","value":"A"},{"name":"Title","value":"T"},{"name":"artist","value":"B"},{"name":"Picture","value":""}]}}}}`)
	frames, err := k.Full(context.Background(), mp3)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Frame{
		{Name: "Artist", Value: "A"}, {Name: "Title", Value: "T"},
		{Name: "artist", Index: 1, Value: "B"}, {Name: "Picture", Value: ""},
	}
	if !slices.Equal(frames, want) {
		t.Errorf("frames %v, want %v", frames, want)
	}

	t.Setenv("STUB_OUT", `{"result":{"taggedFile":{"fileName":"a.mp3"}}}`)
	frames, err = k.Full(context.Background(), mp3)
	if err != nil || frames == nil || len(frames) != 0 {
		t.Errorf("no tag 2: %v, %v", frames, err)
	}

	// What kid3 answers for a path it did not open.
	t.Setenv("STUB_OUT", `{"result":{"taggedFile":{}}}`)
	if _, err := k.Full(context.Background(), mp3); err == nil {
		t.Error("empty taggedFile accepted")
	}
}
