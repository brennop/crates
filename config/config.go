// Package config loads and validates the TOML config file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/brennop/crates/model"
	"github.com/brennop/crates/pathnorm"
)

// DeleteMode says what delete does with a file.
type DeleteMode string

const (
	DeleteTrash  DeleteMode = "trash"
	DeleteRemove DeleteMode = "remove"
)

type Config struct {
	InboxDirs    []string   `toml:"inbox_dirs"`    // normalized after Load
	MasterDir    string     `toml:"master_dir"`    // normalized after Load
	RequiredTags []string   `toml:"required_tags"` // TagSummary field names
	DeleteMode   DeleteMode `toml:"delete_mode"`
	Music        Music      `toml:"music"`
}

type Music struct {
	Enabled    bool `toml:"enabled"`
	Autolaunch bool `toml:"autolaunch"`
}

// Defaults returns the values used for keys the file leaves out.
func Defaults() Config {
	return Config{
		RequiredTags: []string{model.FieldTitle, model.FieldArtist, model.FieldAlbum},
		DeleteMode:   DeleteTrash,
		Music:        Music{Enabled: true, Autolaunch: false},
	}
}

// EnvPath names an environment variable that overrides the config location.
const EnvPath = "CRATES_CONFIG"

// Path returns the config file location: $CRATES_CONFIG, else
// $XDG_CONFIG_HOME/crates/config.toml, else ~/.config/crates/config.toml.
func Path() (string, error) {
	if p := os.Getenv(EnvPath); p != "" {
		return p, nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "crates", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "crates", "config.toml"), nil
}

// TagsPath returns the tag vocabulary shown while editing a comment:
// TAGS.md beside the config file.
func TagsPath() (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "TAGS.md"), nil
}

// Load reads, validates and normalizes the config file at path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	c, err := parse(string(data), home)
	if err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return c, nil
}

func parse(data, home string) (Config, error) {
	c := Defaults()
	md, err := toml.Decode(data, &c)
	if err != nil {
		return Config{}, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return Config{}, fmt.Errorf("unknown keys: %s", strings.Join(keys, ", "))
	}
	if err := c.normalize(home); err != nil {
		return Config{}, err
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c *Config) normalize(home string) error {
	if c.MasterDir == "" {
		return errors.New("master_dir is required")
	}
	var err error
	if c.MasterDir, err = normDir(c.MasterDir, home); err != nil {
		return fmt.Errorf("master_dir: %w", err)
	}
	for i, d := range c.InboxDirs {
		if d == "" {
			return fmt.Errorf("inbox_dirs[%d] is empty", i)
		}
		if c.InboxDirs[i], err = normDir(d, home); err != nil {
			return fmt.Errorf("inbox_dirs[%d]: %w", i, err)
		}
	}
	return nil
}

func normDir(p, home string) (string, error) {
	if p == "~" {
		p = home
	} else if strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[2:])
	}
	return pathnorm.Normalize(p)
}

func (c *Config) validate() error {
	fi, err := os.Stat(c.MasterDir)
	if err != nil {
		return fmt.Errorf("master_dir: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("master_dir %s is not a directory", c.MasterDir)
	}
	for i, in := range c.InboxDirs {
		if pathnorm.Within(in, c.MasterDir) {
			return fmt.Errorf("inbox %s is inside master_dir", in)
		}
		if pathnorm.Within(c.MasterDir, in) {
			return fmt.Errorf("master_dir is inside inbox %s", in)
		}
		for _, other := range c.InboxDirs[:i] {
			if pathnorm.Within(in, other) || pathnorm.Within(other, in) {
				return fmt.Errorf("inboxes %s and %s overlap", other, in)
			}
		}
	}
	switch c.DeleteMode {
	case DeleteTrash, DeleteRemove:
	default:
		return fmt.Errorf("delete_mode %q: want %q or %q", c.DeleteMode, DeleteTrash, DeleteRemove)
	}
	for _, t := range c.RequiredTags {
		if !model.IsSummaryField(t) {
			return fmt.Errorf("required_tags: unknown tag %q (known: %s)", t, strings.Join(model.SummaryFields, ", "))
		}
	}
	c.RequiredTags = slices.Compact(c.RequiredTags)
	return nil
}
