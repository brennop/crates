// Package library owns the track index, the SQLite cache and the polling
// sweeps. It is the only writer of the index; everything else gets copies of
// tracks and events.
//
// Scans and sweeps run on one watch goroutine, so they never overlap. Move,
// SetDuration, Get and Query may be called from any goroutine.
package library
