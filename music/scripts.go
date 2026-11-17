package music

import (
	"embed"
	"fmt"
)

// The JXA scripts, one per call, plus lib.js, the prelude put in front of
// each. scripts/run does the same concatenation for manual checks.
//
//go:embed scripts/*.js
var scriptFS embed.FS

// Script names, as files under scripts/ without the .js.
const (
	scriptStatus         = "status"
	scriptLaunch         = "launch"
	scriptSnapshot       = "snapshot"
	scriptAdd            = "add"
	scriptTrack          = "track"
	scriptRefresh        = "refresh"
	scriptDelete         = "delete"
	scriptPlaylist       = "playlist"
	scriptPlaylistAdd    = "playlist-add"
	scriptPlaylistRemove = "playlist-remove"
	scriptMakePlaylist   = "make-playlist"
	scriptEcho           = "echo"
)

// source returns the full source of a script: the prelude, then the script.
func source(name string) (string, error) {
	lib, err := scriptFS.ReadFile("scripts/lib.js")
	if err != nil {
		return "", err
	}
	body, err := scriptFS.ReadFile("scripts/" + name + ".js")
	if err != nil {
		return "", fmt.Errorf("music: no script %q", name)
	}
	return string(lib) + "\n" + string(body), nil
}
