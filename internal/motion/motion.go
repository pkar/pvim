// Package motion is where the cursor goes, and how much an operator takes on
// the way.
//
// A motion is not a destination. It is a destination plus a kind, and the kind
// is what the operator reads: dw and de leave the cursor in the same place and
// delete a different number of characters, because w is exclusive and e is
// inclusive. A kind that is one value wrong produces an editor that moves
// perfectly and deletes one character too many, forever, in a way no amount of
// staring at the cursor will show. So every kind in the table below is quoted
// from the |exclusive|, |inclusive| and |linewise| tags in vim 9.2's own
// motion.txt rather than remembered, and the ones the table cannot state --
// ; and, take the kind of the find they repeat, % is inclusive bare and
// linewise with a count -- say so and answer at run time in the Result.
//
// The kind is deliberately not decided by the motion alone. Three things above
// it can change it: v, V or CTRL-V typed between an operator and its motion
// (see Force), the two exclusive-becomes-inclusive and exclusive-becomes-
// linewise rules in motion.txt that only apply when an operator is waiting, and
// dd and yy, which are linewise whatever the motion says. Deciding all three
// at once is internal/operator's job, because that is the only place that knows
// the operator, the motion and the force together; the two motion.txt rules
// are written out in exclusive.go as AdjustExclusive for it to call, because
// they are a property of a motion's kind and nothing else and because the test
// that names them belongs beside the motions they change.
package motion

import (
	"strconv"

	"github.com/pkar/pvim/internal/text"
)

// Kind is what an operator does with the span a motion produced.
type Kind int

const (
	// KindCharExclusive is a charwise motion whose destination character is
	// not included: w, b, {, }, (, ), 0, ^, h, l, `x, / and ?.
	KindCharExclusive Kind = iota
	// KindCharInclusive is a charwise motion whose destination character is
	// included: e, E, ge, gE, f, t, $, g_ and a bare %.
	KindCharInclusive
	// KindLine is linewise: whole lines from the start's line to the
	// destination's, whatever columns either sat in. j, k, G, gg, H, M, L, +,
	// -, _, 'x and {count}%.
	KindLine
	// KindBlock is a rectangle. No motion produces it on its own: it arrives
	// from CTRL-V typed after an operator, and from blockwise visual mode. It
	// is a Kind rather than a fourth type somewhere else so that Force.Apply
	// has somewhere to land and every layer above speaks one vocabulary.
	KindBlock
)

// String names the kind the way motion.txt does.
func (k Kind) String() string {
	switch k {
	case KindCharExclusive:
		return "exclusive"
	case KindCharInclusive:
		return "inclusive"
	case KindLine:
		return "linewise"
	case KindBlock:
		return "blockwise"
	default:
		return "motion.Kind(" + strconv.Itoa(int(k)) + ")"
	}
}

// Force is the v, V or CTRL-V typed between an operator and its motion, which
// overrides the motion's own kind. See :help o_v.
type Force int

const (
	// ForceNone is the ordinary case: the motion's kind stands.
	ForceNone Force = iota
	// ForceChar is v. It makes a linewise motion charwise-exclusive, and it
	// swaps exclusive for inclusive and inclusive for exclusive on a charwise
	// one, which is what makes dvj and dv} useful.
	ForceChar
	// ForceLine is V: linewise, whatever the motion was.
	ForceLine
	// ForceBlock is CTRL-V: blockwise, whatever the motion was.
	ForceBlock
)

// Apply returns the kind a motion ends up with once the force has had its say.
func (f Force) Apply(k Kind) Kind {
	switch f {
	case ForceChar:
		switch k {
		case KindCharExclusive:
			return KindCharInclusive
		case KindCharInclusive:
			return KindCharExclusive
		default:
			return KindCharExclusive
		}
	case ForceLine:
		return KindLine
	case ForceBlock:
		return KindBlock
	default:
		return k
	}
}

// The three values Result.Curswant can take besides a display column.
//
// Curswant is the column j and k aim for, and it is why $jjj stays at the end
// of every line it passes and why moving through a short line and back out
// again returns to where you were. Most motions set it from where they landed,
// which is why that is the zero value.
const (
	// CurswantHere sets the wanted column from the destination. Almost every
	// motion does this, so it is the zero value and needs no thought.
	CurswantHere = 0
	// CurswantKeep leaves the wanted column alone: j, k, CTRL-F, CTRL-B,
	// CTRL-D, CTRL-U and the rest of the vertical family, which is the whole
	// mechanism behind $ sticking.
	CurswantKeep = -1
	// CurswantEOL is what $ sets: the end of whatever line the cursor lands
	// on, however long that line is. vim stores MAXCOL here.
	CurswantEOL = -2
	// CurswantUnset is Context.Curswant before any motion has run, and it is
	// vim's w_set_curswant being TRUE at startup: the first j or k works the
	// wanted column out from where the cursor actually is rather than aiming
	// at column zero. It is only ever a Context value; no motion returns it.
	//
	// It is a value and not the zero value because zero is a real display
	// column, and the difference shows the moment the first line starts with
	// a tab: on "\tindent" over "abcdefghijkl", vim's first j lands on byte 8
	// because the cursor on a tab sits at the tab's LAST cell, and a curswant
	// that defaulted to zero lands on byte 1.
	CurswantUnset = -3
)

// Result is what one motion produced.
type Result struct {
	// To is where the cursor goes. Meaningless when Ok is false.
	To text.Pos
	// Kind is how much an operator takes, before any Force or operator rule
	// has been applied. A motion whose kind depends on how it was invoked --
	// ; and, repeat a find and take its kind, % is inclusive bare and
	// linewise with a count -- reports the truth here and not the default the
	// table carries.
	Kind Kind
	// Ok is false when the motion failed: no such mark, no {char} on the line
	// for f, a count that runs off the end of the buffer. vim beeps and the
	// whole command, operator included, is abandoned. It is not the same as a
	// motion that succeeded and did not move.
	Ok bool
	// Jump is true for the motions that set the ' mark and push the jumplist:
	// ', `, G, /, ?, n, N, %, (, ), [[, ]], {, }, H, M and L. :help jumplist
	// has the list and this field is the whole of what the mode machine needs
	// to keep one.
	Jump bool
	// Curswant is the display column a later j or k should aim for, or one of
	// the three constants above. It is read whether or not Ok is true, which
	// is why a failing motion returns CurswantKeep and not the zero value.
	Curswant int
	// Count is how much of the requested count the motion managed. vim
	// abandons the command when a count cannot be met at all (3w at the last
	// word beeps) but honours a partial one for the linewise family (5j four
	// lines from the end moves four). A motion that met the count in full
	// reports it in full.
	Count int
	// Message is what the motion wants on the message line, which for a search
	// motion is the E486 nobody sees until the oracle diffs msgs.txt.
	Message string
	// Moved says a motion that FAILED still left the cursor somewhere new,
	// and To is where. It is the one place vim's C shows through: bckend_word
	// walks the cursor back a character at a time and only reports FAIL when
	// it runs out of buffer, by which time the cursor has already moved, and
	// nv_g_cmd's clearopbeep() beeps and abandons the operator without
	// putting it back. Measured on "[3": "l2ge" beeps and leaves the cursor
	// on the "[", where the same keys on "ab" succeed. Nothing else sets it,
	// because nothing else in vim mutates the cursor on a failed walk.
	Moved bool
	// NoAdjust says the operator must not apply the two :help exclusive
	// adjustments to this span, whatever its kind says. It is vim's
	// CA_NO_ADJ_OP_END and exactly one thing sets it: h, <BS> or <Left>
	// wrapping to the previous line with d or c waiting, where the motion has
	// already put its end after that line's last byte on purpose so that the
	// line separator goes with the delete. Adjusting it again would move the
	// end back over a character that is meant to stay, and d<BS> at the start
	// of a line would eat the last character of the line above instead of
	// joining the two.
	NoAdjust bool
}

// Options are the editor settings a motion's answer depends on.
//
// They are passed in rather than read from a global for the same reason
// internal/regex takes its own: a motion is a pure function of the buffer, the
// position and these, and a package that can reach a global option is a package
// that cannot be table-tested.
type Options struct {
	// TabStop is 'tabstop', which every display-column motion needs: |, g0,
	// g$, and the column j and k aim for.
	TabStop int
	// IsKeyword is 'iskeyword', the option that decides where a word ends and
	// therefore what w, b, e and ge do. Its vim syntax ("@,48-57,_,192-255")
	// is parsed here and nowhere else.
	IsKeyword string
	// WhichWrap is 'whichwrap': which of h, l, <Left>, <Right>, <BS>, <Space>
	// and ~ may cross a line boundary. Empty means none of them, which is the
	// default and what the vimrc leaves it at.
	WhichWrap string
	// StartOfLine is 'startofline': whether G, gg, H, M, L, CTRL-F and friends
	// move to the first non-blank or keep the column.
	StartOfLine bool
	// ScrollOff is 'scrolloff', which is what stops H and L landing on the
	// very first and last line of a scrolled window.
	ScrollOff int
	// Paragraphs and Sections are the nroff macro lists { } and [[ ]] consult
	// on top of their blank-line and column-1-brace rules.
	Paragraphs string
	Sections   string
	// MatchPairs is 'matchpairs', which is the whole of what % knows about
	// besides comments and preprocessor lines.
	MatchPairs string
	// Wrap is 'wrap'. It is what gj, gk, g0, g^ and g$ mean by a display line;
	// with it off they are j, k, 0, ^ and $.
	Wrap bool
	// Width is the window's text width in cells, which gj and gk need to know
	// where a display line breaks. Zero means unwrapped, so they behave as j
	// and k.
	Width int
}

// DefaultOptions is vim's own defaults for the settings above, before any
// vimrc has had a say. It exists so a test can say what it is changing.
func DefaultOptions() Options {
	return Options{
		TabStop:     8,
		IsKeyword:   "@,48-57,_,192-255",
		WhichWrap:   "b,s",
		StartOfLine: true,
		MatchPairs:  "(:),{:},[:]",
		Paragraphs:  "IPLPPPQPP TPHPLIPpLpItpplpipbp",
		Sections:    "SHNHH HUnhsh",
		Wrap:        true,
	}
}

// Find is the last f, F, t or T, which is the entire state; and, need.
type Find struct {
	// Cmd is 'f', 'F', 't' or 'T', and zero when nothing has been found yet,
	// in which case; and, fail rather than doing nothing.
	Cmd byte
	// Char is the target character.
	Char rune
}

// Window is what H, M and L need: which buffer lines the window is showing.
// It is filled in by the frontend, and left zero by a headless run, where the
// three motions have no meaning and report Ok false.
type Window struct {
	// Top and Bottom are the first and last buffer line displayed, 1-based and
	// inclusive.
	Top, Bottom int
	// AtTop and AtBottom say whether the window is scrolled hard against the
	// start or the end of the buffer, which is what suspends 'scrolloff' for
	// H and L.
	AtTop, AtBottom bool
	// Height is how many rows the window has, which is not the same as the
	// number of buffer lines on screen: a ten-line file in a thirty-nine row
	// window shows ten and is thirty-nine tall. vim's cursor_correct caps
	// 'scrolloff' at half the WINDOW, so the two answer H differently on
	// every file shorter than the screen. Zero falls back to the lines shown,
	// which is what a caller that has not been taught about rows deserves and
	// what internal/motion's own tests use.
	Height int
	// LeftCol is the display column drawn in the leftmost cell, which is zero
	// unless 'wrap' is off and a long line has scrolled the window sideways.
	// g0 and g$ are the only motions that read it, and they are the reason it
	// is here: with 'nowrap' a display line is what the window is showing, so
	// g0 is this column and g$ is this column plus the width.
	LeftCol int
}

// Context is the editor state a motion reads and, for a few of them, writes.
//
// It is an explicit struct rather than a set of globals because f, ; and, are
// exactly the feature that a global breaks quietly: a macro that runs a find
// leaves the last find behind it, and a test that cannot construct that state
// cannot catch it.
type Context struct {
	// Find is the last f, F, t or T. A successful f, F, t or T replaces it;
	// and, read it and leave it alone.
	Find Find
	// Curswant is the display column j and k are aiming for, carried between
	// motions. See the Curswant constants.
	Curswant int
	// Window is where H, M and L look. Zero in a headless run.
	Window Window
}

// Request is one motion, asked for.
//
// A struct rather than six parameters because this is the signature seven
// packages call and one that grows a seventh argument would break
// every one of them. A field added here breaks nothing.
type Request struct {
	// Buf is the buffer. A motion never changes it.
	Buf *text.Buffer
	// Ctx is the carried state. A motion may write Ctx.Find; nothing else.
	Ctx *Context
	// From is where the cursor is now.
	From text.Pos
	// Count is the count typed, already multiplied out: 2d3w arrives here as
	// 6. Zero means the user typed none, which is not the same as one for the
	// handful of motions that care (% with no count is the matchpair jump,
	// with one it is a percentage of the file).
	Count int
	// Arg is the character the motion needed after its own key: the target of
	// f, F, t and T, and the name of a mark for ' and `. Zero for every other
	// motion.
	Arg byte
	// Pending is true when an operator is waiting on this motion. It is not
	// cosmetic: cw behaves as ce, a few motions clamp differently at the end
	// of the buffer, and the two exclusive adjustments in motion.txt apply
	// only here.
	Pending bool
	// Op is which operator is waiting, as the character that started it: 'd',
	// 'c', 'y', '<', '>', '=', '!', and 'g' for the two-character ones, whose
	// second character no motion has ever needed. Zero when none is.
	//
	// Three motions read it and vim would be wrong without them. cw is ce
	// unless the cursor is on a blank, which is a rule about c and not about
	// operators in general, so a yw and a dw on the same character take
	// different text. And h, <BS> and <Left> wrapping to the previous line
	// leave the cursor after that line's last byte for d and c, so that the
	// line separator goes with the delete, and on the last character for
	// everything else.
	Op byte
	// Opt is the settings above.
	Opt Options
}

// Count1 is the count with vim's default of one filled in, which is what every
// motion but % and | actually wants.
func (r Request) Count1() int {
	if r.Count < 1 {
		return 1
	}
	return r.Count
}

// Func is one motion.
type Func func(r Request) Result

// Motion is one entry in the table: what the keys are, what kind of span the
// motion makes, and the function that runs it.
type Motion struct {
	// Keys is the key sequence in vim's own notation, which is what
	// key.Format prints and therefore how the mode machine looks a motion up
	// without this package ever importing internal/key.
	Keys string
	// Kind is the kind from motion.txt. It is the table's answer and the
	// Result's Kind is the truth: the few motions whose kind depends on how
	// they were called say so in their doc comment and override it.
	Kind Kind
	// Jump says the motion sets the ' mark and pushes the jumplist.
	Jump bool
	// NeedsArg says a character has to be read before the motion can run: the
	// target of f, F, t and T, the name of a mark for ' and `. The mode
	// machine waits for it, and an Escape there abandons the command.
	NeedsArg bool
	// Do runs it.
	Do Func
}

// fail is what a motion returns when it cannot happen at all: no {char} on the
// line for f, no such mark, a count of 200 for %. Vim beeps, the operator is
// abandoned and the cursor does not move.
//
// Curswant is CurswantKeep and not the zero value on purpose. Result.Curswant
// is read whether or not the motion succeeded, because vim sets the wanted
// column before it finds out: 2$ on the last line of the buffer fails and
// still leaves j and k aiming at the end of the line. Every other failure has
// to say "leave it alone" out loud for that to work.
func fail() Result { return Result{Curswant: CurswantKeep} }

// failMsg is fail with something for the message line, which is the E20 a mark
// that was never set has to print.
func failMsg(msg string) Result { return Result{Curswant: CurswantKeep, Message: msg} }

// notImplemented is the body of the two motions this package cannot answer: n
// and N repeat the last search, the last search pattern lives in
// internal/search, and internal/motion may not import it -- the arrow between
// the two packages runs the other way and internal/deps_test.go enforces it.
// The mode machine owns both, the way it owns / and ?, and the table keeps the
// rows so that their kind and their jump flag are stated in one place with
// every other motion's.
//
// It fails rather than returning From unchanged, because a motion that quietly
// does not move is a case the oracle reports as a cursor one column out and
// nobody traces back to a missing function.
func notImplemented(r Request) Result {
	return fail()
}
