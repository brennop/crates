package model

import (
	"cmp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Camelot is a key's place on the Camelot wheel: Number 1–12, A for minor
// and B for major. Harmonically adjacent keys are one Number apart, and a
// key and its relative major/minor share a Number.
type Camelot struct {
	Number int
	Minor  bool
}

// String writes the key in Camelot notation, e.g. "8A".
func (c Camelot) String() string {
	letter := "B"
	if c.Minor {
		letter = "A"
	}
	return strconv.Itoa(c.Number) + letter
}

// ParseCamelot reads a key written in Camelot ("8A"), Open Key ("1m", "1d")
// or musical notation ("Am", "C#m", "Bb", "F# minor").
func ParseCamelot(key string) (Camelot, bool) {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return Camelot{}, false
	}

	// Camelot or Open Key: a number 1–12 and a letter.
	if d := strings.IndexFunc(k, func(r rune) bool { return r < '0' || r > '9' }); d > 0 {
		n, err := strconv.Atoi(k[:d])
		if err != nil || n < 1 || n > 12 {
			return Camelot{}, false
		}
		switch k[d:] {
		case "a":
			return Camelot{n, true}, true
		case "b":
			return Camelot{n, false}, true
		case "m", "d": // Open Key 1 is C / Am, Camelot 8
			return Camelot{(n-1+7)%12 + 1, k[d:] == "m"}, true
		}
		return Camelot{}, false
	}

	pc, ok := map[byte]int{'c': 0, 'd': 2, 'e': 4, 'f': 5, 'g': 7, 'a': 9, 'b': 11}[k[0]]
	if !ok {
		return Camelot{}, false
	}
	rest := k[1:]
	if r, size := utf8.DecodeRuneInString(rest); r == '#' || r == '♯' {
		pc, rest = pc+1, rest[size:]
	} else if r == 'b' || r == '♭' {
		pc, rest = pc+11, rest[size:]
	}
	minor := false
	switch strings.TrimSpace(rest) {
	case "", "maj", "major":
	case "m", "min", "minor":
		minor = true
	default:
		return Camelot{}, false
	}
	// Each step around the wheel is a fifth (7 semitones). C major and A
	// minor are both 8.
	offset := 7
	if minor {
		offset = 4
	}
	return Camelot{(pc%12*7+offset)%12 + 1, minor}, true
}

// CompareKeys orders key texts around the Camelot wheel: by number, then A
// before B. Keys it cannot read come after every readable one, in
// case-insensitive text order.
func CompareKeys(a, b string) int {
	ca, oka := ParseCamelot(a)
	cb, okb := ParseCamelot(b)
	switch {
	case oka && okb:
		if c := cmp.Compare(ca.Number, cb.Number); c != 0 {
			return c
		}
		return -compareBool(ca.Minor, cb.Minor) // minor (A) first
	case oka:
		return -1
	case okb:
		return 1
	}
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}
