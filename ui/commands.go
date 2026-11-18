package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/brennop/crates/analysis"
	"github.com/brennop/crates/app"
	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
)

// Each function below wraps the app method of the same name in a tea.Cmd.
// The command runs the method off the Update loop and returns its result
// message, named after the method. Functions take a ctx where the method
// does; pass one that the program cancels on quit, so Close does not wait
// on a slow external call.

// ---- Library ----

// QueryResult carries Gen back unchanged so the UI can drop results from
// queries it has since replaced.
type QueryResult struct {
	Gen uint64
	IDs []model.TrackID
	Err error
}

func Query(ctx context.Context, a *app.App, gen uint64, f app.Filter, s app.Sort) tea.Cmd {
	return func() tea.Msg {
		ids, err := a.Query(ctx, f, s)
		return QueryResult{Gen: gen, IDs: ids, Err: err}
	}
}

type TracksResult struct {
	Tracks []model.Track
}

func Tracks(a *app.App, ids []model.TrackID) tea.Cmd {
	return func() tea.Msg { return TracksResult{Tracks: a.Tracks(ids)} }
}

type RefreshResult struct{ Err error }

func Refresh(ctx context.Context, a *app.App) tea.Cmd {
	return func() tea.Msg { return RefreshResult{Err: a.Refresh(ctx)} }
}

type FocusGainedResult struct{ Err error }

func FocusGained(ctx context.Context, a *app.App) tea.Cmd {
	return func() tea.Msg { return FocusGainedResult{Err: a.FocusGained(ctx)} }
}

// ---- Files ----

type PromoteResult struct {
	Summary app.PromoteSummary
}

func Promote(ctx context.Context, a *app.App, ids []model.TrackID) tea.Cmd {
	return func() tea.Msg { return PromoteResult{Summary: a.Promote(ctx, ids)} }
}

type OverwriteResult struct {
	Inbox model.TrackID
	Err   error
}

func Overwrite(ctx context.Context, a *app.App, inbox model.TrackID) tea.Cmd {
	return func() tea.Msg { return OverwriteResult{Inbox: inbox, Err: a.Overwrite(ctx, inbox)} }
}

type DeleteResult struct {
	Summary app.DeleteSummary
}

func Delete(ctx context.Context, a *app.App, ids []model.TrackID) tea.Cmd {
	return func() tea.Msg { return DeleteResult{Summary: a.Delete(ctx, ids)} }
}

// ---- Tags ----

type FullTagsResult struct {
	ID     model.TrackID
	Frames []model.Frame
	Err    error
}

func FullTags(ctx context.Context, a *app.App, id model.TrackID) tea.Cmd {
	return func() tea.Msg {
		fs, err := a.FullTags(ctx, id)
		return FullTagsResult{ID: id, Frames: fs, Err: err}
	}
}

type WriteTagsResult struct {
	IDs []model.TrackID
	Err error
}

func WriteTags(ctx context.Context, a *app.App, ids []model.TrackID, changes []model.FrameChange) tea.Cmd {
	return func() tea.Msg { return WriteTagsResult{IDs: ids, Err: a.WriteTags(ctx, ids, changes)} }
}

// ---- Playlists ----

type PlaylistsResult struct {
	Playlists []model.Playlist
}

func Playlists(a *app.App) tea.Cmd {
	return func() tea.Msg { return PlaylistsResult{Playlists: a.Playlists()} }
}

type MembershipsResult struct {
	ID        model.TrackID
	Playlists []model.Playlist
	Err       error
}

func Memberships(a *app.App, id model.TrackID) tea.Cmd {
	return func() tea.Msg {
		ps, err := a.Memberships(id)
		return MembershipsResult{ID: id, Playlists: ps, Err: err}
	}
}

type AddToPlaylistResult struct {
	IDs        []model.TrackID
	PlaylistID string
	Err        error
}

func AddToPlaylist(ctx context.Context, a *app.App, ids []model.TrackID, playlistID string) tea.Cmd {
	return func() tea.Msg {
		return AddToPlaylistResult{IDs: ids, PlaylistID: playlistID, Err: a.AddToPlaylist(ctx, ids, playlistID)}
	}
}

type RemoveFromPlaylistResult struct {
	IDs        []model.TrackID
	PlaylistID string
	Err        error
}

func RemoveFromPlaylist(ctx context.Context, a *app.App, ids []model.TrackID, playlistID string) tea.Cmd {
	return func() tea.Msg {
		return RemoveFromPlaylistResult{IDs: ids, PlaylistID: playlistID, Err: a.RemoveFromPlaylist(ctx, ids, playlistID)}
	}
}

// ---- Playback ----

type PlayResult struct {
	ID  model.TrackID
	Err error
}

func Play(ctx context.Context, a *app.App, id model.TrackID) tea.Cmd {
	return func() tea.Msg { return PlayResult{ID: id, Err: a.Play(ctx, id)} }
}

type EnqueueResult struct {
	IDs []model.TrackID
}

func Enqueue(a *app.App, ids ...model.TrackID) tea.Cmd {
	return func() tea.Msg {
		a.Enqueue(ids...)
		return EnqueueResult{IDs: ids}
	}
}

type NextResult struct{ Err error }

func Next(ctx context.Context, a *app.App) tea.Cmd {
	return func() tea.Msg { return NextResult{Err: a.Next(ctx)} }
}

type PreviousResult struct{ Err error }

func Previous(ctx context.Context, a *app.App) tea.Cmd {
	return func() tea.Msg { return PreviousResult{Err: a.Previous(ctx)} }
}

type RemoveFromQueueResult struct {
	Index int
	Err   error
}

func RemoveFromQueue(ctx context.Context, a *app.App, i int) tea.Cmd {
	return func() tea.Msg { return RemoveFromQueueResult{Index: i, Err: a.RemoveFromQueue(ctx, i)} }
}

type ClearQueueResult struct{ Err error }

func ClearQueue(ctx context.Context, a *app.App) tea.Cmd {
	return func() tea.Msg { return ClearQueueResult{Err: a.ClearQueue(ctx)} }
}

type QueueResult struct {
	Queue app.QueueState
}

func Queue(a *app.App) tea.Cmd {
	return func() tea.Msg { return QueueResult{Queue: a.Queue()} }
}

type TogglePauseResult struct{ Err error }

func TogglePause(ctx context.Context, a *app.App) tea.Cmd {
	return func() tea.Msg { return TogglePauseResult{Err: a.TogglePause(ctx)} }
}

type SeekResult struct{ Err error }

func Seek(ctx context.Context, a *app.App, d time.Duration, relative bool) tea.Cmd {
	return func() tea.Msg { return SeekResult{Err: a.Seek(ctx, d, relative)} }
}

type SetVolumeResult struct{ Err error }

func SetVolume(ctx context.Context, a *app.App, v float64) tea.Cmd {
	return func() tea.Msg { return SetVolumeResult{Err: a.SetVolume(ctx, v)} }
}

// ---- Music ----

type MusicStatusResult struct {
	State model.MusicState
}

func MusicStatus(a *app.App) tea.Cmd {
	return func() tea.Msg { return MusicStatusResult{State: a.MusicStatus()} }
}

type LaunchMusicResult struct{ Err error }

func LaunchMusic(ctx context.Context, a *app.App) tea.Cmd {
	return func() tea.Msg { return LaunchMusicResult{Err: a.LaunchMusic(ctx)} }
}

// PendingImportResult carries the count of the import waiting for an
// answer, or 0 when none is.
type PendingImportResult struct {
	Count int
}

func PendingImport(a *app.App) tea.Cmd {
	return func() tea.Msg { return PendingImportResult{Count: a.PendingImport()} }
}

type ConfirmImportResult struct{}

func ConfirmImport(a *app.App) tea.Cmd {
	return func() tea.Msg {
		a.ConfirmImport()
		return ConfirmImportResult{}
	}
}

type CancelImportResult struct{}

func CancelImport(a *app.App) tea.Cmd {
	return func() tea.Msg {
		a.CancelImport()
		return CancelImportResult{}
	}
}

type DeadEntriesResult struct {
	Entries []events.DeadEntry
}

func DeadEntries(a *app.App) tea.Cmd {
	return func() tea.Msg { return DeadEntriesResult{Entries: a.DeadEntries()} }
}

// ---- Analysis ----

// AnalyzeResult carries one job's result for track ID.
type AnalyzeResult struct {
	ID     model.TrackID
	Result analysis.Result
}

// Analyze runs one analysis job; batch one per job to run them in parallel.
func Analyze(ctx context.Context, a *app.App, id model.TrackID, job analysis.Job) tea.Cmd {
	return func() tea.Msg { return AnalyzeResult{ID: id, Result: a.Analyze(ctx, id, job)} }
}

// SpekResult says how a Spek window ended: nil when the user closed it.
type SpekResult struct{ Err error }

// Spek opens track id in Spek; the result comes when its window closes.
func Spek(ctx context.Context, a *app.App, id model.TrackID) tea.Cmd {
	return func() tea.Msg { return SpekResult{Err: a.Spek(ctx, id)} }
}

// ---- Other ----

type CapabilitiesResult struct {
	Capabilities app.Capabilities
}

func Capabilities(a *app.App) tea.Cmd {
	return func() tea.Msg { return CapabilitiesResult{Capabilities: a.Capabilities()} }
}

type CloseResult struct{ Err error }

// Close shuts the app down: reconciler, mpv, Music, kid3 and the cache.
// Cancel the ctx given to the other commands first, so calls in flight end
// instead of holding Close up. A second Close does nothing.
func Close(a *app.App) tea.Cmd {
	return func() tea.Msg { return CloseResult{Err: a.Close()} }
}

// ---- Events ----

// WaitForEvent blocks on the bus and returns the next event itself as the
// message. Update returns WaitForEvent(bus) again after handling each event
// to keep the pump running; exactly one should be outstanding.
func WaitForEvent(bus *events.Bus) tea.Cmd {
	return func() tea.Msg {
		e, err := bus.Next(context.Background())
		if err != nil {
			return nil
		}
		return e
	}
}

// SaveBeatportTokenResult answers SaveBeatportToken.
type SaveBeatportTokenResult struct{ Err error }

// SaveBeatportToken saves a Beatport token for the Beatport job.
func SaveBeatportToken(a *app.App, token string) tea.Cmd {
	return func() tea.Msg { return SaveBeatportTokenResult{Err: a.SaveBeatportToken(token)} }
}
