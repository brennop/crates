package ui

import (
	"charm.land/bubbles/v2/help"
	binding "charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// Each view matches keys against its bindings, and draws its hints from the
// same bindings, so a hint cannot drift from the key it names. The table's
// own KeyMap covers movement (see moveKey).

func newBinding(help, desc string, keys ...string) binding.Binding {
	return binding.NewBinding(binding.WithKeys(keys...), binding.WithHelp(help, desc))
}

// libraryKeys are the Library's keys outside the prompts and dialogs.
var libraryKeys = struct {
	ForceQuit, Quit, Pause, Search, Next, Prev, Sort binding.Binding
	EditTitle, EditArtist, EditComment, EditKey      binding.Binding
	Playlists, Delete, DeleteNow, Analyze, Inbox     binding.Binding
	Promote                                          binding.Binding

	// z and g are prefixes: zz centers, gg goes to the top, ge edits the
	// genre. A lone g must not go to the top (see New).
	Center, G, Top, EditGenre binding.Binding

	// Edit is the hint for the five edit keys.
	Edit binding.Binding
}{
	ForceQuit: newBinding("ctrl+c", "quit", "ctrl+c"),
	Quit:      newBinding("q", "quit", "q"),
	Pause:     newBinding("space", "pause", "space"),
	Search:    newBinding("/", "search", "/"),
	Next:      newBinding("n", "next match", "n"),
	Prev:      newBinding("N", "previous match", "N"),
	Sort:      newBinding("s", "sort", "s"),

	EditTitle:   newBinding("t", "edit title", "t"),
	EditArtist:  newBinding("a", "edit artist", "a"),
	EditComment: newBinding("c", "edit comment", "c"),
	EditKey:     newBinding("e", "edit key", "e"),

	Playlists: newBinding("p", "playlists", "p"),
	Delete:    newBinding("x", "delete", "x"),
	DeleteNow: newBinding("X", "delete without asking", "X"),
	Analyze:   newBinding("m", "analysis", "m"),
	Inbox:     newBinding("i", "inbox", "i"),
	Promote:   newBinding("P", "promote", "P"),

	Center:    newBinding("zz", "center", "z"),
	G:         newBinding("g", "prefix", "g"),
	Top:       newBinding("gg", "top", "g"),
	EditGenre: newBinding("ge", "edit genre", "e"),

	Edit: newBinding("c/a/t/e/ge", "edit", "c", "a", "t", "e"),
}

// libraryHints are the keys the idle status line lists, with promote in
// the inbox view.
func libraryHints(inbox bool) []binding.Binding {
	k := libraryKeys
	if inbox {
		return []binding.Binding{k.Pause, k.Search, k.Edit, k.Sort, k.Inbox, k.Promote, k.Playlists, k.Analyze, k.Delete, k.Quit}
	}
	return []binding.Binding{k.Pause, k.Search, k.Edit, k.Sort, k.Inbox, k.Playlists, k.Analyze, k.Delete, k.Quit}
}

// promptKeys end the / search and tag edit prompts; the textinput handles
// the rest. Erase on an empty line closes the prompt, as in vim.
var promptKeys = struct{ Accept, Cancel, Erase binding.Binding }{
	Accept: newBinding("enter", "accept", "enter"),
	Cancel: newBinding("esc", "cancel", "esc"),
	Erase:  newBinding("backspace", "erase", "backspace"),
}

// confirmKeys answer the delete confirm.
var confirmKeys = struct{ Yes, No binding.Binding }{
	Yes: newBinding("y", "delete", "y", "enter"),
	No:  newBinding("n", "cancel", "n", "esc", "q", "x"),
}

// dialogKeys are the playlist dialog's own keys; the list brings movement
// and filtering.
var dialogKeys = struct{ Toggle, Close binding.Binding }{
	Toggle: newBinding("space", "toggle", "space", "enter"),
	Close:  newBinding("esc", "close", "esc", "p", "q"),
}

// hints renders bindings as one "key desc · key desc" line, dropping the
// ones that do not fit in width (0 for no limit). It is unstyled; the
// caller styles the line.
func hints(width int, bs ...binding.Binding) string {
	h := help.New()
	h.ShortSeparator = " · "
	plain := lipgloss.NewStyle()
	h.Styles = help.Styles{ShortKey: plain, ShortDesc: plain, ShortSeparator: plain, Ellipsis: plain}
	h.SetWidth(width)
	return h.ShortHelpView(bs)
}
