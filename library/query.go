package library

import (
	"cmp"
	"fmt"
	"sort"
	"strings"

	"github.com/brennop/crates/model"
)

// Filter selects tracks. The zero value selects every track.
type Filter struct {
	// Text is a case-insensitive substring to find in any summary field.
	// "" matches all.
	Text string
	// Role restricts to one root. nil matches both.
	Role *model.Role
}

// Sort orders the result by one column. Field is a summary field name
// (model.SummaryFields), or one of "path", "size", "mtime", "duration",
// "role". "" sorts by ID. Ties fall back to ID ascending.
type Sort struct {
	Field string
	Desc  bool
}

// Query returns the IDs of the matching tracks in the requested order. It is
// safe from any goroutine.
func (l *Library) Query(f Filter, s Sort) ([]model.TrackID, error) {
	cmpFn, err := comparator(s.Field)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(f.Text)
	x := l.idx
	x.mu.RLock()
	defer x.mu.RUnlock()

	matched := make([]model.Track, 0, len(x.tracks))
	for _, t := range x.tracks {
		if f.Role != nil && t.Role != *f.Role {
			continue
		}
		if needle != "" && !matchesText(t, needle) {
			continue
		}
		matched = append(matched, t)
	}
	sort.Slice(matched, func(i, j int) bool {
		c := cmpFn(&matched[i], &matched[j])
		if s.Desc {
			c = -c
		}
		if c != 0 {
			return c < 0
		}
		return matched[i].ID < matched[j].ID
	})
	ids := make([]model.TrackID, len(matched))
	for i, t := range matched {
		ids[i] = t.ID
	}
	return ids, nil
}

func matchesText(t model.Track, needle string) bool {
	for _, name := range model.SummaryFields {
		v, _ := t.Summary.Field(name)
		if strings.Contains(strings.ToLower(v), needle) {
			return true
		}
	}
	return false
}

func comparator(field string) (func(a, b *model.Track) int, error) {
	switch field {
	case "":
		return func(a, b *model.Track) int { return 0 }, nil
	case "path":
		return func(a, b *model.Track) int { return strings.Compare(a.Path, b.Path) }, nil
	case "size":
		return func(a, b *model.Track) int { return cmp.Compare(a.Size, b.Size) }, nil
	case "mtime":
		return func(a, b *model.Track) int { return a.ModTime.Compare(b.ModTime) }, nil
	case "duration":
		return func(a, b *model.Track) int { return cmp.Compare(a.Duration, b.Duration) }, nil
	case "role":
		return func(a, b *model.Track) int { return cmp.Compare(a.Role, b.Role) }, nil
	case model.FieldTrackNumber:
		return func(a, b *model.Track) int { return cmp.Compare(a.Summary.TrackNumber, b.Summary.TrackNumber) }, nil
	case model.FieldYear:
		return func(a, b *model.Track) int { return cmp.Compare(a.Summary.Year, b.Summary.Year) }, nil
	case model.FieldBPM:
		return func(a, b *model.Track) int { return cmp.Compare(a.Summary.BPM, b.Summary.BPM) }, nil
	}
	if !model.IsSummaryField(field) {
		return nil, fmt.Errorf("library: cannot sort by %q", field)
	}
	return func(a, b *model.Track) int {
		va, _ := a.Summary.Field(field)
		vb, _ := b.Summary.Field(field)
		return strings.Compare(strings.ToLower(va), strings.ToLower(vb))
	}, nil
}
