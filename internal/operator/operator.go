// Package operator applies d, c, y, <, >, =, gu, gU, g~, g?, gq, gw, J and zf
// to a span of a buffer, and puts a register back with p, P, gp, gP, ]p and [p.
//
// Two things make this a package and not a switch in the mode machine.
//
// The first is the span. An operator does not take a motion; it takes the text
// a motion resolved to, and the resolving is where vim keeps three rules that
// are easy to state and easy to forget: an inclusive motion covers the
// character it lands on and an exclusive one does not; an exclusive motion that
// ends in column 1 gives that column back and becomes inclusive; and if it also
// started at or before the first non-blank of its line, it becomes linewise
// instead. That last one is the whole of why d} on a paragraph leaves no blank
// line behind. SpanForMotion is where all three live, and it is the only
// function in the tree that needs to know them.
//
// The second is dot repeat. An operator has an identity, not just an effect:
// after dw, the . command replays a delete of a word, with a new count if one
// was typed, from wherever the cursor now is. Op is that identity, and it is on
// the Request so that a recorded operator is a value the mode machine can
// replay rather than a closure it has to keep alive.
//
// # Everything here was measured
//
// Every cursor rule, message string and register write in this package came out
// of running the case through /opt/homebrew/bin/vim 9.2 with
//
//	vim --clean -i NONE --not-a-term -s KEYS FILE
//
// under a pty, and dumping line('.'), col('.'), getreg() and getregtype()
// through writefile(). The comments name what was run wherever the answer
// disagrees with a plain reading of :help, which is more often than is
// comfortable: 'joinspaces' is ON under --clean, so J puts two spaces after a
// full stop by default; a blockwise yank pads a short line with spaces but a
// blockwise insert skips it; and 'shiftround' rounds through integer division
// of the old indent rather than by adding and then rounding.
package operator

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// Op is which operator, and it is the identity dot repeat replays.
type Op int

const (
	// OpNone is no operator: a motion typed on its own in normal mode.
	OpNone Op = iota
	// OpDelete is d, x, X and D, which are all d with the motion filled in.
	OpDelete
	// OpChange is c, s, S and C. It deletes and then leaves insert mode
	// running, which is what Result.Insert says.
	OpChange
	// OpYank is y.
	OpYank
	// OpShiftLeft and OpShiftRight are < and >, which are linewise whatever
	// the motion was and which honour 'shiftround'.
	OpShiftLeft
	OpShiftRight
	// OpIndent is =. With no 'equalprg' and no filetype indent, vim's internal
	// C indent is all there is, and this editor has 'autoindent' and the
	// vimrc's smartindent and nothing else, so = is close to a no-op and says
	// so rather than pretending to reformat. See Indent for what that costs.
	OpIndent
	// OpLower, OpUpper and OpToggle are gu, gU and g~.
	OpLower
	OpUpper
	OpToggle
	// OpRot13 is g?, which is in vim and costs four lines.
	OpRot13
	// OpFormat and OpFormatKeep are gq and gw: reflow to 'textwidth'. gw puts
	// the cursor back where it was and gq leaves it on the last reformatted
	// line, which is the only difference between them.
	OpFormat
	OpFormatKeep
	// OpFold is zf, which makes a manual fold over the span. The fold tree
	// belongs to internal/screen, so this calls Options.Folds when the caller
	// supplied one and records the range in Result either way.
	OpFold
	// OpJoin is J and gJ. It is in this list because it here and
	// because visual J takes a span, but it is not an operator in vim's sense:
	// normal-mode J takes a count and no motion, so the mode machine builds
	// the span itself. Spaces says which of the two it is.
	OpJoin
)

// String names the operator with the keys that make it, which is what a
// showcmd, a message and a test failure all want.
func (o Op) String() string {
	switch o {
	case OpNone:
		return ""
	case OpDelete:
		return "d"
	case OpChange:
		return "c"
	case OpYank:
		return "y"
	case OpShiftLeft:
		return "<"
	case OpShiftRight:
		return ">"
	case OpIndent:
		return "="
	case OpLower:
		return "gu"
	case OpUpper:
		return "gU"
	case OpToggle:
		return "g~"
	case OpRot13:
		return "g?"
	case OpFormat:
		return "gq"
	case OpFormatKeep:
		return "gw"
	case OpFold:
		return "zf"
	case OpJoin:
		return "J"
	default:
		return "operator.Op(" + strconv.Itoa(int(o)) + ")"
	}
}

// Changes reports whether the operator writes to the buffer. y and zf do not,
// which decides whether an undo block is opened and whether the buffer is
// marked modified.
func (o Op) Changes() bool { return o != OpNone && o != OpYank && o != OpFold }

// Linewise reports whether the operator forces a linewise span whatever the
// motion said. < > and = are the three, and dd and yy get there a different
// way: the mode machine sees the operator's own key repeated and builds a
// linewise span itself.
func (o Op) Linewise() bool {
	switch o {
	case OpShiftLeft, OpShiftRight, OpIndent:
		return true
	default:
		return false
	}
}

// Block is the geometry of a blockwise span.
//
// It is display columns and not byte columns, because that is what a block is:
// CTRL-V down a file of tabs selects a rectangle on the screen, not a rectangle
// of bytes, and the byte column each line's edge lands on is computed per line
// through text.ByteColForDisplay. Left and Right are inclusive, which is the
// only way to name a one-column block.
type Block struct {
	// First and Last are buffer lines, 1-based and inclusive.
	First, Last int
	// Left and Right are display columns, 0-based and inclusive.
	Left, Right int
	// ToEOL is the $ block: every line runs to its own end and Right means
	// nothing. It is a flag rather than a very large Right so that a caller
	// cannot half-notice it.
	ToEOL bool
}

// Span is the text one operator acts on.
//
// Which fields are read depends on Type, and the doc on each says which:
// mixing them up is how a linewise delete ends up taking a column range.
type Span struct {
	// Type is charwise, linewise or blockwise.
	Type register.Type
	// Range is the text for a charwise span, half-open. For a linewise span
	// only Start.Line and End.Line are read and End.Line is INCLUSIVE, because
	// vim's linewise operations name the last line they touch and a half-open
	// line range cannot name a single line without a special case at the end
	// of the buffer. For a blockwise span it is ignored entirely.
	Range text.Range
	// Block is the geometry for a blockwise span and is ignored for the other
	// two.
	Block Block
	// EndsOnEmptyLine says a charwise span's end sits in column 0 of a line
	// that has no bytes in it, which the columns alone cannot tell from an end
	// that stops before the line it names.
	//
	// Two things read it. Lines() counts that line, because vim decrements
	// oap->line_count once in the exclusive adjustment and never derives it
	// from the columns afterwards: "ly2)" over a paragraph whose last line is
	// blank says "3 lines yanked". And applyChange reads it to tell C on a
	// blank line, which yanks an empty register, from c0, which leaves the
	// register alone -- vim's oap->empty is set for an exclusive region and
	// never for an inclusive one, and it is the flag op_delete's early return
	// is guarded by.
	EndsOnEmptyLine bool

	// EndAdjusted says the exclusive-motion rule moved the end of the span
	// back onto the previous line. vim keeps it as oap->end_adjusted and one
	// operator reads it: gq puts the cursor one line further down when it is
	// set, so "gq}" over a paragraph followed by a blank line leaves the
	// cursor on the blank line and a "." after it formats the next paragraph.
	EndAdjusted bool

	// FoldAdjusted says the closed folds this span touches have already been
	// folded into it, so Apply leaves it alone. SpanForMotion sets it because
	// it has to run the fold pass before its own exclusive-motion rules, and
	// SpanForCharsBefore sets it because the answer for X is that a backward
	// charwise operator does not see a fold at all. See fold.go, where both
	// measurements are.
	FoldAdjusted bool

	// OpStart is vim's oap->start with its COLUMN still on it, which the
	// linewise Range above has thrown away. do_pending_operator puts the
	// cursor there before it runs the operator, and gu, gU and g~ are the
	// ones that leave it: "ll gU j" comes back in column 3 and not column 1,
	// and "j$ gU k" comes back at the end of line 1 because that is where the
	// k landed. Zero when the span did not come from a motion, which is the
	// doubled form -- "gUU" -- where vim's nv_lineop has already moved the
	// cursor to the first non-blank and column zero is the right answer.
	OpStart text.Pos
}

// Lines returns the first and last line a span covers, inclusive, whatever its
// type. Every operator needs this and getting it from a charwise span means
// remembering that an end in column 0 does not count as touching that line.
func (s Span) Lines() (first, last int) {
	switch s.Type {
	case register.TypeBlock:
		return s.Block.First, s.Block.Last
	case register.TypeLine:
		return s.Range.Start.Line, s.Range.End.Line
	default:
		first, last = s.Range.Start.Line, s.Range.End.Line
		// A half-open charwise end sitting in column 0 stops before the line
		// it names, so that line is not covered. dw at the end of a line
		// reaches (n+1, 0) and touches one line, not two, and the "N fewer
		// lines" message counts on the difference. The exception is an end the
		// span builder already moved onto a line with no bytes in it, where
		// column 0 is the whole of the line and not a position in front of it.
		if last > first && s.Range.End.Col == 0 && !s.EndsOnEmptyLine {
			last--
		}
		return first, last
	}
}

// Empty reports whether the span covers no text at all, which is vim's
// oap->empty: the operator beeps and nothing happens, and no register is
// written. d0 in column 0 is the everyday case.
func (s Span) Empty() bool {
	if s.Type != register.TypeChar {
		return false
	}
	return !s.Range.Start.Before(s.Range.End)
}

// Options are the editor settings an operator's result depends on.
type Options struct {
	// ShiftWidth, TabStop, SoftTabStop and ExpandTab decide what < and > move
	// by and what they leave behind. The vimrc sets sw=4 ts=2 sts=2, which is
	// exactly the combination that catches an implementation that assumes one
	// of them equals another. SoftTabStop is carried for completeness and is
	// deliberately not read: vim's set_indent builds an indent out of
	// 'tabstop' and 'expandtab' alone, measured with ts=2 sw=4 noet, where >>
	// on an unindented line leaves two tabs and not one tab and two spaces.
	ShiftWidth  int
	TabStop     int
	SoftTabStop int
	ExpandTab   bool
	// ShiftRound is 'shiftround': < and > round to a multiple of
	// 'shiftwidth' instead of adding and subtracting one. The vimrc has it on.
	ShiftRound bool
	// AutoIndent and SmartIndent are what c and o put on the new line, and
	// what gq gives a paragraph's second and later lines. With both off, cc
	// on an indented line leaves the cursor in column 0 and gq strips the
	// indent off every line but the first.
	AutoIndent  bool
	SmartIndent bool
	// JoinSpaces is 'joinspaces': J puts two spaces after a line ending in
	// '.', '!' or '?' rather than one.
	//
	// Not off by default, whatever :help says. --clean vim 9.2 on this machine
	// answers "joinspaces" to :set js?, so J on "The end." followed by "next"
	// gives two spaces, and DefaultOptions has it on to match.
	JoinSpaces bool
	// JoinSpacesOnlyPeriod is the 'j' flag of 'cpoptions': two spaces after a
	// full stop but only one after '!' and '?'. Off under --clean, where cpo
	// is "aABceFsz".
	JoinSpacesOnlyPeriod bool
	// CinWords is 'cinwords': the keywords that start a block in a language
	// with no braces, which under 'smartindent' get a shiftwidth after them.
	// The vimrc sets it for python and nothing else sets it at all.
	CinWords []string
	// TextWidth is what gq reflows to. Zero means 79 here; see Format for the
	// rule vim actually uses and why this package cannot apply it.
	TextWidth int
	// Folds is the window's fold tree, or nil for a caller with no window.
	//
	// It is not a setting and it is here because this is what reaches both
	// halves of the job: SpanForMotion takes an Options and nothing else, and
	// the fold pass has to run there rather than in Apply, before the
	// exclusive-motion rules read the start's column. fold.go carries the
	// measurement that forces the order.
	Folds Folds
	// Report is 'report': the number of changed lines above which the message
	// line says "N fewer lines". Two, by default, and the message is diffed by
	// the oracle, so an operator that changes lines and says nothing fails a
	// case whose buffer is perfect.
	//
	// Zero is a real value and the one people set, not a missing field: with
	// :set report=0 a single "dd" prints "1 line less". A caller that fills
	// no Options at all gets a report of 0 along with a shiftwidth of 0 and a
	// tabstop of 0 and is broken whatever this field does, so DefaultOptions
	// is the only thing that supplies the 2.
	Report int
}

// DefaultOptions is vim's own defaults for the settings above, as
// /opt/homebrew/bin/vim --clean reports them: sw=8 ts=8 sts=0 noet noai
// joinspaces tw=0 report=2 cpo=aABceFsz.
func DefaultOptions() Options {
	return Options{ShiftWidth: 8, TabStop: 8, TextWidth: 0, Report: 2, JoinSpaces: true}
}

// shiftWidth is 'shiftwidth' with vim's zero rule applied: a shiftwidth of 0
// means "use 'tabstop'", which is what the option's documentation promises and
// what >> on a zero 'shiftwidth' was measured doing.
func (o Options) shiftWidth() int {
	sw := o.ShiftWidth
	if sw <= 0 {
		sw = o.tabStop()
	}
	if sw < 1 {
		sw = 1
	}
	return sw
}

// tabStop is 'tabstop', never zero, because every display-column calculation
// divides by it.
func (o Options) tabStop() int {
	if o.TabStop < 1 {
		return 8
	}
	return o.TabStop
}

// Registers is the part of internal/register an operator touches.
//
// It is an interface and not *register.File so that a test of this package can
// watch what an operator wrote without standing up the whole register file,
// and so that a caller that genuinely has nowhere to put the text -- a:normal
// running with the black hole already resolved, a fixture -- can pass nil. Any
// *register.File satisfies it.
type Registers interface {
	// Yank records a yank: the unnamed register always, and "0 as well unless
	// a register was named.
	Yank(name byte, v register.Value) error
	// Delete records a delete or a change: the unnamed register, "- when the
	// text is within one line, and the "1 shift when it is not or when
	// useRegOne says the motion demands it whatever its size.
	Delete(name byte, v register.Value, useRegOne bool) error
}

// Request is one operator application.
type Request struct {
	// Buf is the buffer, which the operator mutates directly. Opening the undo
	// block is the caller's job: one d is one undo step, but one dot repeat
	// inside a macro is one step per repeat, and only the mode machine knows
	// which it is looking at.
	Buf *text.Buffer
	// Regs is the register file. Even y and d with no "x prefix write to it,
	// which is why it is not optional; a nil Regs means the text goes nowhere
	// and is for tests and for a caller that has already sent it to "_.
	Regs Registers
	// Op is which operator.
	Op Op
	// Span is what it acts on.
	Span Span
	// At is where the cursor was when the operator was typed. It is not
	// Span.Range.Start: a backward motion puts the span start ahead of the
	// cursor, and y, gw and J all put the cursor somewhere that depends on
	// where it came from.
	At text.Pos
	// Register is the "x prefix, or zero when none was typed. Uppercase means
	// append, and internal/register is where that rule lives.
	Register byte
	// Count is the count typed, already multiplied out: 2d3w arrives as 6.
	// Most operators have consumed their count in the motion by the time they
	// get here; J, < and > and the visual-mode operators have not.
	Count int
	// Arg is the character an operator needed after itself: the replacement
	// character for visual r, the fold level for zf. Zero otherwise.
	Arg byte
	// Visual is true when the operator came from a visual-mode selection
	// rather than from an operator and a motion. Two rules read it: a charwise
	// delete from an indent to the end of a line becomes linewise in normal
	// mode and does not in visual mode, and gq's cursor rule differs the same
	// way.
	Visual bool
	// TrimTrailing is "zy" rather than "y": vim 9's yank without trailing
	// white space. It changes nothing except a blockwise yank, where each
	// line of the block loses the spaces and tabs at its end. Measured
	// against vim 9.2.0321: "zy$", "zyy" and "zyw" put exactly what "y$",
	// "yy" and "yw" put, trailing spaces included, and a blockwise "zy" over
	// "ab " puts "ab".
	TrimTrailing bool
	// Spaces is what a join puts between the lines it joins: true for J, which
	// inserts one space (two after a sentence end under 'joinspaces') and
	// strips leading white space from the next line, false for gJ, which
	// joins the bytes as they are.
	Spaces bool
	// NumberedRegister forces a delete into "1 however short it was. It is
	// vim's oap->use_reg_one and it is set by the motion, not by the operator:
	// %, (, ), `, /, ?, n, N, { and } all send their text to "1 even when the
	// delete is half a word. register.MotionForcesNumbered answers it from the
	// motion's key.
	NumberedRegister bool
	// Opt is the settings above.
	Opt Options
}

// Result is what the operator left behind.
type Result struct {
	// Cursor is where vim leaves the cursor, which is a per-operator rule and
	// not "the start of the span": dd on the last line moves up, p of a
	// linewise register lands on the first non-blank of the first new line,
	// and >> keeps the column under 'startofline' being off. Every one of
	// those was run through vim before it was written down.
	Cursor text.Pos
	// Insert is true when the operator ends with insert mode running: c, s, S,
	// C, and the visual-block I and A. The mode machine switches; the operator
	// does not.
	Insert bool
	// Message is what the message line should say, empty when 'report' says
	// nothing is worth saying. It is diffed by the oracle, so "3 fewer lines"
	// with the wrong number fails a case whose buffer is right. More than one
	// line is separated by a newline, which = needs: it prints its progress
	// line and then its result.
	Message string
	// Keep says the message survives the redraw that follows the command, so
	// that the redraw prints it a second time: vim's set_keep_msg, which
	// msgmore() and the shift report call and which the yank report, the case
	// report and "--No lines in buffer--" do not. It is not decoration. The
	// redirect cmd/oracle diffs holds the message twice when this is set and
	// once when it is not, measured on "3dd" against "3yy".
	Keep bool
	// FoldFirst and FoldLast are the line range zf asked for, inclusive, and
	// zero for every other operator. The fold tree itself lives in
	// internal/screen; an operator that owned it could not be tested without a
	// window. A caller that has a window passes it as Options.Folds and zf
	// makes the fold there as well, which is the only way one ever exists:
	// these two numbers on their own are a report, and for four months nothing
	// read them, so zf was a no-op with a doc comment.
	FoldFirst, FoldLast int
}

// ErrNoRange is an operator handed a span with nothing in it. vim beeps and
// abandons the command, which is what a caller does with this.
var ErrNoRange = errors.New("operator: empty range")

// ErrEmptyRegister is a put of a register nothing has been yanked into. The
// caller turns it into vim's "E353: Nothing in register x", because the name is
// the caller's and this package never sees it.
var ErrEmptyRegister = errors.New("operator: nothing in register")

// Apply runs one operator over its span.
//
// It mutates the buffer, writes the registers the operator's rules say to
// write, and returns where the cursor goes. It does not open an undo block, it
// does not switch mode and it does not touch the jumplist: all three belong to
// the caller, which is the only thing that knows whether this Apply is one
// command, one step of a dot repeat, or the hundredth iteration of a macro.
func Apply(r Request) (Result, error) {
	if r.Buf == nil {
		return Result{}, errors.New("operator: no buffer")
	}
	if r.Count < 1 {
		r.Count = 1
	}
	if r.Opt.TabStop < 1 {
		r.Opt.TabStop = 8
	}
	// vim's do_pending_operator widens the operator over the closed folds it
	// touches before it runs it, and so does every path into this one that no
	// motion resolved: dd, x, D and a visual selection. Normal mode's J is the
	// one thing here that never reaches do_pending_operator; see foldsWiden.
	if foldsWiden(r.Op, r.Visual) {
		r.Span = foldExtendSpan(r.Buf, r.Span, r.Opt.Folds, r.Visual, r.Opt.tabStop())
	}

	switch r.Op {
	case OpDelete:
		return applyDelete(r)
	case OpChange:
		return applyChange(r)
	case OpYank:
		return applyYank(r)
	case OpShiftLeft, OpShiftRight:
		return applyShift(r)
	case OpIndent:
		return applyIndent(r)
	case OpLower, OpUpper, OpToggle, OpRot13:
		return applyCase(r)
	case OpJoin:
		return applyJoin(r)
	case OpFormat, OpFormatKeep:
		return applyFormat(r)
	case OpFold:
		return applyFold(r)
	default:
		return Result{}, fmt.Errorf("operator: %v is not an operator that acts on a span", r.Op)
	}
}

// applyFold makes the manual fold zf asked for. It changes no text, which is
// why it is short: internal/screen keeps the fold tree and this is the one
// place that knows which lines the user pointed at.
//
// The fold goes into Options.Folds when the caller has a window, and the range
// comes back in the Result either way. A caller that passes no Folds gets the
// range and no fold, which is what every test in this package and every headless
// run does.
//
// The cursor stays where it was, clamped onto the span's first line: zfj from
// column 3 leaves the cursor in column 3, and G zfk moves it up to the line the
// fold starts on. Measured, because "the start of the fold" would have been the
// obvious guess and it is only half right.
//
// A blockwise zf is the exception and lands on the block's own top-left
// corner, which is not the cursor's column when the block was made rightwards:
// on three lines of " abcdef", CTRL-V j l zf from column 6 leaves the cursor
// in column 6 and not the 7 the cursor was in. Same corner op_yank uses, so it
// is worked out the same way.
func applyFold(r Request) (Result, error) {
	first, last := r.Span.Lines()
	at := r.At
	switch {
	case r.Span.Type == register.TypeBlock:
		blk := r.Span.Block
		col, _ := blockInsertCol(r.Buf.Line(blk.First), blk, r.Opt.tabStop())
		at = text.Pos{Line: blk.First, Col: col}
	case at.Line != first:
		at = text.Pos{Line: first, Col: at.Col}
	}
	if r.Opt.Folds != nil {
		r.Opt.Folds.Create(first, last)
	}
	return Result{Cursor: clampNormal(r.Buf, at), FoldFirst: first, FoldLast: last}, nil
}

// lineMessage builds vim's msgmore(): the "N more lines" and "N fewer lines"
// that the oracle diffs. n is the signed change in the buffer's line count and
// an absolute value at or below 'report' says nothing at all.
func lineMessage(n, report int) string {
	if n > report {
		return plural(n, "more line", "more lines")
	}
	if -n > report {
		// Not "1 fewer line". vim's msgmore spells the singular "%ld line
		// less" and the plural "%ld fewer lines", which reads like a typo and
		// is what :set report=0 | dd prints.
		if -n == 1 {
			return "1 line less"
		}
		return plural(-n, "fewer line", "fewer lines")
	}
	return ""
}

// plural picks vim's singular or plural wording for a count. vim runs every one
// of these through NGETTEXT with both forms spelled out, so "1 fewer line" is a
// string it can print and an implementation that always says "lines" differs on
// a case nobody thinks to write.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
