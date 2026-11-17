package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func setup(t *testing.T) (home string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"Master", "dl", "dl2"} {
		if err := os.Mkdir(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestParseValid(t *testing.T) {
	home := setup(t)
	cases := []struct {
		name  string
		data  string
		check func(t *testing.T, c Config)
	}{
		{
			name: "defaults",
			data: `master_dir = "~/Master"`,
			check: func(t *testing.T, c Config) {
				if c.MasterDir != filepath.Join(home, "Master") {
					t.Errorf("master = %q", c.MasterDir)
				}
				if c.DeleteMode != DeleteTrash || !c.Music.Enabled || c.Music.Autolaunch {
					t.Errorf("defaults not applied: %+v", c)
				}
				if !slices.Equal(c.RequiredTags, []string{"title", "artist", "album"}) {
					t.Errorf("required_tags = %v", c.RequiredTags)
				}
				if len(c.InboxDirs) != 0 {
					t.Errorf("inbox_dirs = %v", c.InboxDirs)
				}
			},
		},
		{
			name: "full",
			data: `
inbox_dirs    = ["~/dl", "` + home + `/x/../dl2", "~/not-yet"]
master_dir    = "` + home + `/Master/"
required_tags = ["title", "album_artist", "year"]
delete_mode   = "remove"

[music]
enabled    = false
autolaunch = true
`,
			check: func(t *testing.T, c Config) {
				want := []string{filepath.Join(home, "dl"), filepath.Join(home, "dl2"), filepath.Join(home, "not-yet")}
				if !slices.Equal(c.InboxDirs, want) {
					t.Errorf("inbox_dirs = %v, want %v", c.InboxDirs, want)
				}
				if c.MasterDir != filepath.Join(home, "Master") {
					t.Errorf("master = %q", c.MasterDir)
				}
				if c.DeleteMode != DeleteRemove || c.Music.Enabled || !c.Music.Autolaunch {
					t.Errorf("%+v", c)
				}
			},
		},
		{
			name: "empty required tags",
			data: "master_dir = \"~/Master\"\nrequired_tags = []",
			check: func(t *testing.T, c Config) {
				if len(c.RequiredTags) != 0 {
					t.Errorf("required_tags = %v", c.RequiredTags)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := parse(tc.data, home)
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, c)
		})
	}
}

func TestParseInvalid(t *testing.T) {
	home := setup(t)
	cases := []struct{ name, data, wantErr string }{
		{"no master", `inbox_dirs = ["~/dl"]`, "master_dir is required"},
		{"master missing", `master_dir = "~/nope"`, "no such file"},
		{"master is a file", `master_dir = "~/file"`, "not a directory"},
		{"inbox inside master", "master_dir = \"~/Master\"\ninbox_dirs = [\"~/Master/in\"]", "inside master_dir"},
		{"inbox is master", "master_dir = \"~/Master\"\ninbox_dirs = [\"~/Master\"]", "inside master_dir"},
		{"master inside inbox", "master_dir = \"~/Master\"\ninbox_dirs = [\"~\"]", "master_dir is inside inbox"},
		{"overlapping inboxes", "master_dir = \"~/Master\"\ninbox_dirs = [\"~/dl\", \"~/dl/sub\"]", "overlap"},
		{"bad delete mode", "master_dir = \"~/Master\"\ndelete_mode = \"shred\"", "delete_mode"},
		{"unknown tag", "master_dir = \"~/Master\"\nrequired_tags = [\"title\", \"bogus\"]", "unknown tag \"bogus\""},
		{"unknown key", "master_dir = \"~/Master\"\nfilename_rule = \"x\"", "unknown keys: filename_rule"},
		{"unknown music key", "master_dir = \"~/Master\"\n[music]\nautolauch = true", "unknown keys: music.autolauch"},
		{"wrong type", "master_dir = 3", "master_dir"},
		{"bad toml", "master_dir = ", "toml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parse(tc.data, home)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadAndPath(t *testing.T) {
	home := setup(t)
	f := filepath.Join(home, "config.toml")
	if err := os.WriteFile(f, []byte(`master_dir = "`+home+`/Master"`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPath, f)
	p, err := Path()
	if err != nil || p != f {
		t.Fatalf("Path() = %q, %v", p, err)
	}
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}

	t.Setenv(EnvPath, "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if p, _ := Path(); p != "/xdg/crates/config.toml" {
		t.Errorf("XDG path = %q", p)
	}
}
