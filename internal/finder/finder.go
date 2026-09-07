package finder

import (
	"strings"

	"github.com/pkar/pvim/internal/key"
)

// Mode is which list the finder is searching.
//
// Three, in this order, because that is ctrlp's g:ctrlp_types default and the
// order <c-f> and <c-b> walk: `let s:types = ['fil', 'buf', 'mru']`
// (autoload/ctrlp.vim:209). ctrlp has a dozen more -- tags, quickfix, undo,
// bookmarks, changes, line -- and every one of them is unreachable from these
// defaults without a mapping the vimrc does not have.
type Mode int

// The three modes.
const (
	Files Mode = iota
	Buffers
	MRUFiles
)

// String is what the status line calls the mode, in ctrlp's own words
// (s:coretype_names, autoload/ctrlp.vim:217).
func (m Mode) String() string {
	switch m {
	case Buffers:
		return "buffers"
	case MRUFiles:
		return "mru files"
	default:
		return "files"
	}
}

// Item is one candidate.
type Item struct {
	// Display is what the match window shows and what the scorer matches
	// against: a path relative to the project root for files, a buffer's
	// name for buffers.
	Display string
	// Path is what CR opens. Absolute for files and MRU entries; the
	// buffer's name as the buffer list spells it in buffer mode.
	Path string
}

// Action is what a keystroke asked the editor to do. The finder itself opens
// nothing: it has no window, no buffer list and no idea what a tab is.
type Action int

// The actions. Everything that is not one of these is handled inside the
// finder and answered with None.
const (
	None Action = iota
	// Close is <esc>, <c-c> and <c-g>: put the match window away and go back
	// to where the cursor was.
	Close
	// Open, OpenTab, OpenVSplit and OpenSplit are ctrlp's
	// AcceptSelection("e"), ("t"), ("v") and ("h"), on <cr>, <c-t>, <c-v> and
	// <c-x> (s:prtmaps, autoload/ctrlp.vim:134-138).
	Open
	OpenTab
	OpenVSplit
	OpenSplit
	// Refresh is <F5>, ctrlp's PrtClearCache: walk the tree again.
	Refresh
)

// Result is what a key did.
type Result struct {
	Action Action
	// Item is the selection the action applies to, empty for Close and
	// Refresh and for an action taken with nothing matched.
	Item Item
}

// Finder is the match window's state: the query, the three lists and which of
// them is being searched.
//
// It owns no window and no buffer. cmd/pvim asks it for Lines and Prompt after
// every key and draws them; that is what makes the whole of this testable
// without an editor and what keeps the key table in one place.
type Finder struct {
	// Root is the project the file list was walked from, kept so that Lines
	// can shorten an MRU entry that lives under it the way ctrlp's
	// g:ctrlp_mruf_map_string does.
	Root string

	// Height is how many lines the match window may show, which cmd/pvim
	// works out as min(g:ctrlp_max_height, what the screen has). It is also
	// the number of matches kept: ctrlp caps the result list with s:mw_res
	// ('results:40' in this vimrc) and the window with s:winmaxh, and with
	// both at 40 nothing is ever off the top of the window.
	Height int

	// Current is the file the editor was on when the finder opened, which is
	// left out of every list. ctrlp does the same, in s:MatchIt: the item
	// equal to s:crfilerel is skipped unless g:ctrlp_match_current_file is
	// set, and it is not set by default (autoload/ctrlp.vim:670).
	Current string

	mode  Mode
	query string
	lists [3][]Item

	// matches is the ranked view of the current list, best first, already cut
	// to Height.
	matches []Item
	// sel indexes matches: 0 is the best match. Which line of the window that
	// is drawn on is Lines's business, not this field's.
	sel int
}

// New makes a finder with nothing in it.
func New(root string, height int) *Finder {
	return &Finder{Root: root, Height: height}
}

// SetItems fills one of the three lists. The finder re-ranks if that list is
// the one on screen.
func (f *Finder) SetItems(m Mode, items []Item) {
	if m < Files || m > MRUFiles {
		return
	}
	f.lists[m] = items
	if m == f.mode {
		f.rank()
	}
}

// Mode is which list is being searched, and Query what has been typed.
func (f *Finder) Mode() Mode    { return f.mode }
func (f *Finder) Query() string { return f.query }

// SetMode moves to a list directly, which is what opening the finder in a
// particular mode does.
func (f *Finder) SetMode(m Mode) {
	if m < Files || m > MRUFiles {
		return
	}
	f.mode = m
	f.rank()
}

// Selected is the item under the cursor, and false when nothing matched.
func (f *Finder) Selected() (Item, bool) {
	if f.sel < 0 || f.sel >= len(f.matches) {
		return Item{}, false
	}
	return f.matches[f.sel], true
}

// Matches is the ranked list, best first.
func (f *Finder) Matches() []Item { return f.matches }

// Prompt is the line ctrlp echoes under the match window.
//
// ">>> " is s:BuildPrompt's base with neither the regex nor the by-filename
// toggle on (autoload/ctrlp.vim:815), and the trailing underscore is what
// ctrlp draws where the cursor is when there is nothing after it. pvim has a
// real caret it could put there instead; the underscore is kept because it is
// what the message line can draw and because it is what this prompt has looked
// like for fifteen years.
func (f *Finder) Prompt() string { return ">>> " + f.query + "_" }

// Lines is the match window's contents, top line first.
//
// Best match LAST, which is ctrlp's default and looks upside down written
// down: s:mw_order is 'btt' (autoload/ctrlp.vim:294), s:Render reverses the
// lines and puts the cursor on the last one with `norm! G` (:743, :769). It is
// the right way up on the screen -- the prompt is drawn under the window, so
// the best match is the line nearest the thing you are typing into -- and it
// is why <c-k> walks away from the best match and <c-j> walks back toward it.
//
// The selected line is marked with ">" in the first column, which is ctrlp's
// s:lineflag, and every other line gets two spaces so the text lines up.
func (f *Finder) Lines() [][]byte {
	out := make([][]byte, 0, len(f.matches))
	for i := len(f.matches) - 1; i >= 0; i-- {
		mark := "  "
		if i == f.sel {
			mark = "> "
		}
		out = append(out, []byte(mark+f.matches[i].Display))
	}
	if len(out) == 0 {
		// vim will not show a window with no lines in it, and an empty match
		// window that says nothing looks like a hang. ctrlp puts the string
		// in the window too (s:Render's "NO ENTRIES").
		out = append(out, []byte(" == NO ENTRIES =="))
	}
	return out
}

// Key feeds one keystroke to the finder.
//
// The table is ctrlp's s:prtmaps (autoload/ctrlp.vim:121-157) with three
// differences, all of them deliberate:
//
// - <c-n> and <c-p> move the selection here. In ctrlp they are
// PrtHistory(-1) and PrtHistory(1), which walk the prompt history, and
// there is no prompt history in pvim to walk. A file finder wants all four
// of <c-j>, <c-k>, <c-n> and <c-p> to move and this does.
// - <c-h> is a backspace here always. ctrlp makes it PrtCurLeft in a GUI
// and PrtBS in a terminal (autoload/ctrlp.vim:159), and there is no
// cursor to move left because this prompt has no left: the query is only
// ever appended to and trimmed from the end.
// - <c-d> (by filename), <c-r> (regex mode), <c-z>/<c-o> (multi-open),
// <tab> (expand directory), <c-y> (create file) and <c-\> (insert from a
// register) do nothing. Each is a feature and not a key, and none of them is
// built here.
func (f *Finder) Key(k key.Key) Result {
	switch {
	case k.Special == key.KeyEsc, k == key.Ctrl('c'), k == key.Ctrl('g'):
		return Result{Action: Close}
	case k.Special == key.KeyCR:
		return f.accept(Open)
	case k == key.Ctrl('t'):
		return f.accept(OpenTab)
	case k == key.Ctrl('v'):
		return f.accept(OpenVSplit)
	case k == key.Ctrl('x'):
		return f.accept(OpenSplit)
	case k == key.Ctrl('j'), k.Special == key.KeyDown:
		f.move(-1)
	case k == key.Ctrl('k'), k.Special == key.KeyUp:
		f.move(1)
	case k == key.Ctrl('n'):
		f.move(-1)
	case k == key.Ctrl('p'):
		f.move(1)
	case k == key.Ctrl('f'):
		f.cycle(1)
	case k == key.Ctrl('b'):
		f.cycle(-1)
	case k.Special == key.KeyBS, k == key.Ctrl('h'), k == key.Ctrl(']'):
		f.backspace()
	case k == key.Ctrl('w'):
		f.deleteWord()
	case k == key.Ctrl('u'):
		f.setQuery("")
	case k.Special == key.KeyF5:
		return Result{Action: Refresh}
	case k.IsRune() && k.Mod == 0 && k.Rune >= ' ' && k.Rune != 0x7f:
		f.setQuery(f.query + string(k.Rune))
	}
	return Result{}
}

func (f *Finder) accept(a Action) Result {
	it, ok := f.Selected()
	if !ok {
		return Result{Action: Close}
	}
	return Result{Action: a, Item: it}
}

// move walks the selection. Positive is away from the best match, which on
// screen is upward; see Lines.
//
// It stops at each end rather than wrapping, because ctrlp's PrtSelectMove is
// a literal `norm! j` or `norm! k` in the match window and neither of those
// wraps.
func (f *Finder) move(by int) {
	f.sel += by
	if f.sel < 0 {
		f.sel = 0
	}
	if f.sel >= len(f.matches) {
		f.sel = len(f.matches) - 1
	}
	if f.sel < 0 {
		f.sel = 0
	}
}

// cycle is <c-f> and <c-b>, ToggleType(1) and ToggleType(-1), which wrap
// through the three modes (s:walker in autoload/ctrlp.vim).
func (f *Finder) cycle(by int) {
	m := int(f.mode) + by
	switch {
	case m < 0:
		m = int(MRUFiles)
	case m > int(MRUFiles):
		m = int(Files)
	}
	f.mode = Mode(m)
	f.rank()
}

func (f *Finder) setQuery(q string) {
	f.query = q
	f.rank()
}

func (f *Finder) backspace() {
	if f.query == "" {
		return
	}
	// One rune and not one byte: a query is typed and a multi-byte character
	// deleted a third at a time leaves the query holding half a rune, which
	// the scorer would then never match.
	r := []rune(f.query)
	f.setQuery(string(r[:len(r)-1]))
}

// deleteWord is PrtDeleteWord: the trailing run of non-separators, and the
// separators before it.
func (f *Finder) deleteWord() {
	q := strings.TrimRight(f.query, " /")
	if i := strings.LastIndexAny(q, " /"); i >= 0 {
		f.setQuery(q[:i+1])
		return
	}
	f.setQuery("")
}

// rank re-runs the scorer over the current list.
//
// The selection goes back to the best match on every keystroke, which is
// ctrlp: s:Render moves the cursor to the end of the redrawn window every time
// it draws.
func (f *Finder) rank() {
	list := f.lists[f.mode]
	names := make([]string, 0, len(list))
	kept := make([]Item, 0, len(list))
	for _, it := range list {
		if f.Current != "" && it.Path == f.Current {
			continue
		}
		names = append(names, it.Display)
		kept = append(kept, it)
	}
	limit := f.Height
	if limit <= 0 {
		limit = 1
	}
	ranked := Rank(names, f.query)
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	f.matches = make([]Item, len(ranked))
	for i, m := range ranked {
		f.matches[i] = kept[m.Index]
	}
	f.sel = 0
}
