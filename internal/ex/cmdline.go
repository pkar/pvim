package ex

// Line is the command line being typed: ":", "/", "?", and the ":!" that
// follows them.
//
// It holds bytes and a byte position, not runes and a rune index, for the same
// reason internal/text does: vim addresses the command line by byte column,
// and every place the two models meet is a place a multi-byte character goes
// missing.
type Line struct {
	// Prefix is the character that opened it: ':', '/', '?' or '='.
	Prefix byte
	// Text is what has been typed.
	Text []byte
	// Pos is the cursor, a byte offset into Text.
	Pos int
	// HistIdx is where in the history the up and down arrows have walked to,
	// and -1 when the line is the one being typed rather than one recalled.
	HistIdx int
	// Saved is the line that was being typed before the history was walked,
	// which is what comes back when the arrows come back down past the end.
	Saved []byte

	// Hist is the history the arrows and CTRL-P walk. Nil is a line with no
	// history behind it, which is what a test and a ":normal" build.
	Hist *History

	// Src is where CTRL-R gets its text. Nil means CTRL-R inserts nothing,
	// which is the honest answer for a command line with no editor under it.
	Src Source

	// pending is the key CTRL-R is waiting for, zero when nothing is half
	// typed. One byte and not a state machine, because CTRL-R takes exactly
	// one more key.
	pending byte

	// comp is the wildmenu state, reset by every key that is not a Tab.
	comp completion
}

// completion is one run of wildmenu matches: what was typed, what matched, and
// which of them is showing.
type completion struct {
	matches []string
	// start is the byte offset the completed word begins at.
	start int
	// typed is the text that was there before the first Tab, which is what the
	// step past the last match puts back.
	typed string
	// idx is which match is showing, or len(matches) for the typed text.
	idx int
}

// HistKind is which of vim's separate histories a line belongs to.
type HistKind uint8

// The histories. Vim keeps five and this editor keeps the three that get used:
// a search history walked by the arrows after "/", a command history after
// ":", and an expression history that exists so that ":history" has something
// honest to say about it.
const (
	HistCommand HistKind = iota
	HistSearch
	HistExpr
)

// History is one kind's worth of recalled lines, oldest first.
//
// 'history' is 200 by default and the vimrc leaves it there. Writing these to
// ~/.cache/vim/history at exit, in pvim's own format rather than viminfo's, is
// left for later.
type History struct {
	Lines []string
	Max   int
}

// CompKind is what wildmenu should complete at the cursor.
type CompKind uint8

// The completion kinds. They are the ArgKind of the command being typed, one
// step further along: after ":e " the kind is files, after ":set " it is
// option names, after ":b " buffers, and at the start of the line it is
// command names.
const (
	CompNone CompKind = iota
	CompCommand
	CompFile
	CompDir
	CompBuffer
	CompOption
	CompHighlight
	CompTag
	CompColorscheme
)

// Completer produces the candidate list for one completion.
//
// It is an interface because the candidates come from four different places --
// the command table, the option table, the buffer list and the file system --
// and because a test for the command line should not have to touch a disk.
type Completer interface {
	// Complete returns the candidates for prefix, already sorted the way
	// wildmenu shows them.
	Complete(kind CompKind, prefix string) []string
}

// String returns the line as typed, without the prefix.
func (l *Line) String() string { return string(l.Text) }
