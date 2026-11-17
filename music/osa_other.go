//go:build !darwin

package music

// New returns the Disabled client: Music exists only on macOS.
func New(Options) Service { return Disabled{} }
