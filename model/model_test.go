package model

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

func TestMissing(t *testing.T) {
	s := TagSummary{Title: "t", Artist: "a", Year: 1999}
	got := s.Missing([]string{FieldTitle, FieldAlbum, FieldYear, FieldTrackNumber, "bogus"})
	want := []string{FieldAlbum, FieldTrackNumber, "bogus"}
	if !slices.Equal(got, want) {
		t.Fatalf("Missing = %v, want %v", got, want)
	}
}

func TestSummaryFieldsKnown(t *testing.T) {
	for _, f := range SummaryFields {
		if !IsSummaryField(f) {
			t.Errorf("%q not recognized", f)
		}
	}
	if IsSummaryField("comment") {
		t.Error("comment should not be a summary field")
	}
}

func TestErrorsAs(t *testing.T) {
	cause := errors.New("disk full")
	cases := []struct {
		err   error
		match func(error) bool
	}{
		{ErrCollision{Name: "a.mp3", Existing: 7}, func(e error) bool { var x ErrCollision; return errors.As(e, &x) && x.Existing == 7 }},
		{ErrMissingTags{Missing: []string{"title"}}, func(e error) bool { var x ErrMissingTags; return errors.As(e, &x) && x.Missing[0] == "title" }},
		{ErrPlaylistsLost{Path: "p", Err: cause}, func(e error) bool { var x ErrPlaylistsLost; return errors.As(e, &x) && errors.Is(e, cause) }},
		{ErrBackupKept{Path: "p", Err: cause}, func(e error) bool { var x ErrBackupKept; return errors.As(e, &x) && errors.Is(e, cause) }},
		{ErrMusicUnavailable{}, func(e error) bool { var x ErrMusicUnavailable; return errors.As(e, &x) }},
		{ErrAutomationDenied{}, func(e error) bool { var x ErrAutomationDenied; return errors.As(e, &x) }},
		{ErrCopySettingOn{Path: "a", Location: "b"}, func(e error) bool { var x ErrCopySettingOn; return errors.As(e, &x) && x.Location == "b" }},
		{ErrVerifyFailed{What: "w"}, func(e error) bool { var x ErrVerifyFailed; return errors.As(e, &x) }},
		{ErrNotEditable{PlaylistID: "P"}, func(e error) bool { var x ErrNotEditable; return errors.As(e, &x) }},
		{ErrNotInMusic{ID: 3}, func(e error) bool { var x ErrNotInMusic; return errors.As(e, &x) && x.ID == 3 }},
		{ErrKid3Missing{}, func(e error) bool { var x ErrKid3Missing; return errors.As(e, &x) }},
	}
	for _, c := range cases {
		wrapped := fmt.Errorf("op: %w", c.err)
		if !c.match(wrapped) {
			t.Errorf("%T not matched through a wrap", c.err)
		}
		if c.err.Error() == "" {
			t.Errorf("%T has an empty message", c.err)
		}
	}
}
