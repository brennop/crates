package music

import (
	"fmt"
	"slices"

	"github.com/brennop/crates/model"
)

// expectAdd is the membership an add should leave: before, then each new
// track that was not a member yet, once.
func expectAdd(before, add []string) []string {
	want := slices.Clone(before)
	for _, id := range add {
		if !slices.Contains(want, id) {
			want = append(want, id)
		}
	}
	return want
}

// expectRemove is before without any occurrence of the removed tracks.
func expectRemove(before, remove []string) []string {
	return slices.DeleteFunc(slices.Clone(before), func(id string) bool { return slices.Contains(remove, id) })
}

// verifyMembers compares the membership read back after a write with the
// expected one, as multisets: a missing, extra or doubled track fails,
// whatever caused it. Order is not compared, since Music owns it.
func verifyMembers(playlistID string, got, want []string) error {
	count := map[string]int{}
	for _, id := range want {
		count[id]++
	}
	for _, id := range got {
		count[id]--
	}
	var missing, extra int
	for _, n := range count {
		if n > 0 {
			missing += n
		} else {
			extra -= n
		}
	}
	if missing == 0 && extra == 0 {
		return nil
	}
	return model.ErrVerifyFailed{What: fmt.Sprintf("playlist %s has %d tracks, expected %d (%d missing, %d unexpected)",
		playlistID, len(got), len(want), missing, extra)}
}
