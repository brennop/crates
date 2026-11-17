package model

import (
	"slices"
	"testing"
)

func TestParseCamelot(t *testing.T) {
	for key, want := range map[string]string{
		"8A": "8A", "8B": "8B", "08a": "8A", "12B": "12B", "1A": "1A",
		"1m": "8A", "1d": "8B", "6m": "1A", // Open Key
		"Am": "8A", "C": "8B", "Cmaj": "8B", "A minor": "8A",
		"Abm": "1A", "G#m": "1A", "B": "1B", "F#": "2B", "Gb": "2B", "Dbm": "12A", "C#m": "12A",
		"E": "12B", "Em": "9A", "G": "9B", "Bb": "6B", "Gm": "6A", "B♭": "6B", "Fm": "4A",
	} {
		if got, ok := ParseCamelot(key); !ok || got.String() != want {
			t.Errorf("ParseCamelot(%q) = %v, %v; want %s", key, got, ok, want)
		}
	}
	for _, key := range []string{"", "13A", "0B", "8C", "H", "Am7", "x"} {
		if c, ok := ParseCamelot(key); ok {
			t.Errorf("ParseCamelot(%q) = %v, want not ok", key, c)
		}
	}
}

func TestCompareKeys(t *testing.T) {
	keys := []string{"zz", "C", "", "12A", "Am", "2B", "1A", "8A", "Abm", "foo"}
	slices.SortStableFunc(keys, CompareKeys)
	// Am and 8A are the same key, so they keep their input order.
	want := []string{"1A", "Abm", "2B", "Am", "8A", "C", "12A", "", "foo", "zz"}
	if !slices.Equal(keys, want) {
		t.Errorf("sorted %q, want %q", keys, want)
	}
}
