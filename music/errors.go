package music

import (
	"errors"
	"fmt"
)

// osascript and Apple Event error numbers the client tells apart. Messages
// are localized, so errors are matched by number only.
const (
	errNotRunning = -600
	errNoObject   = -1728
	errDenied     = -1743
)

// ErrNotFound matches a ScriptError for error -1728: no track or playlist
// with that persistent ID.
var ErrNotFound = errors.New("music: no such object")

// ErrDisabled: Music integration is off in the config.
var ErrDisabled = errors.New("music: disabled in config")

// ErrClosed: the client was closed.
var ErrClosed = errors.New("music: client closed")

// ScriptError is a script failure that has no model typed error.
type ScriptError struct {
	Script  string
	Number  int // 0 when the script gave none
	Message string
}

func (e *ScriptError) Error() string {
	if e.Number == 0 {
		return fmt.Sprintf("music %s: %s", e.Script, e.Message)
	}
	return fmt.Sprintf("music %s: %s (%d)", e.Script, e.Message, e.Number)
}

// Is makes errors.Is(err, ErrNotFound) true for error -1728.
func (e *ScriptError) Is(target error) bool {
	return target == ErrNotFound && e.Number == errNoObject
}

// TimeoutError: a script ran past its timeout and was killed.
type TimeoutError struct {
	Script string
	After  string
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("music %s: no answer after %s", e.Script, e.After)
}
