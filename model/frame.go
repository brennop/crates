package model

import "strconv"

// Frame is one ID3v2 frame as kid3-cli lists it (docs/kid3-cli.md). Name is
// kid3's English display name ("Title", "Track Number", "Initial Key"), or the
// description for a TXXX frame. Index tells repeated frames of the same Name
// apart, in tag order: 0 for the first, 1 for the second. Binary frames such
// as pictures carry their description, not their data, in Value.
type Frame struct {
	Name  string
	Index int
	Value string
}

// FrameChange sets one frame. Name and Index address the frame as in Frame;
// a Name with no such frame creates it (an unknown Name becomes a TXXX
// frame). An empty Value deletes the frame.
type FrameChange struct {
	Name  string
	Index int
	Value string
}

// Ref is the name kid3-cli addresses the frame by: Name, or Name[Index] for
// a repeated frame.
func (c FrameChange) Ref() string {
	if c.Index == 0 {
		return c.Name
	}
	return c.Name + "[" + strconv.Itoa(c.Index) + "]"
}
