package mode

import (
	"fmt"
	"strings"

	"github.com/pkar/pvim/internal/text"
)

// The identifier commands: K, the [i / ]i family, the [d / ]d family and the
// CTRL-W halves of both.
//
// All of them start the same way, in vim's find_ident_under_cursor: take the
// word the cursor is on, or the first word after it on the same line, and say
// "E349: No identifier under cursor" when the line holds neither. That one
// message was four of the twenty-two differences the third fuzz round found,
// because pvim beeped silently at every key that leads here.
//
// What comes after the word is vim's find_pattern_in_path, of which this is
// the part that reads the current buffer. 'include' is not honoured and no
// other file is opened: this editor has no include search, and a search that
// silently read half of one would be worse than one that reads none.
//
// The line range is where the four spellings differ, and it was measured key by
// key against vim 9.2.0321 rather than read out of the help:
//
//	[i [I [<Tab> and every CTRL-W form search from line 1
//	]i ]I ]<Tab> search from the line after the cursor
//
// with the end of the range always the end of the buffer. The one surprise is
// the clamp: a ] command on the LAST line of the buffer would start past the
// end, and vim starts on the last line instead, which is why "]<Tab>" there is
// always "E387: Match is on current line" and never "E389: Couldn't find
// pattern". Measured on a five-line file from each of its five lines, with the
// identifier on lines 1, 3 and 5 and then on line 5 alone: both give E387 from
// line 5 and E389 from line 4.

// identAction is what an identifier command does with the match it finds.
type identAction int

const (
	// identShow prints the matching line on the message line: "[i".
	identShow identAction = iota
	// identShowAll prints every matching line, numbered, under the file
	// name: "[I".
	identShowAll
	// identGoto moves the cursor to the match: "[<Tab>".
	identGoto
	// identSplit is identGoto in a new window: "CTRL-W i". The window is the
	// frontend's to make, so this reports the match and moves nothing.
	identSplit
)

// identCmd is one of the identifier commands taken apart.
type identCmd struct {
	action identAction
	// define searches 'define' rather than the identifier itself, which is
	// the whole difference between "[i" and "[d".
	define bool
	// fromCursor starts the search on the line after the cursor rather than
	// at line 1: the "]" half of every pair.
	fromCursor bool
}

// identCmds is every key sequence that starts with an identifier under the
// cursor, except K, which needs a shell and is answered by the caller.
//
// CTRL-W i and CTRL-W d are not here: they come in through IdentSplit, because
// the window they want is not the mode machine's to open.
var identCmds = map[string]identCmd{
	"[i":     {action: identShow},
	"]i":     {action: identShow, fromCursor: true},
	"[I":     {action: identShowAll},
	"]I":     {action: identShowAll, fromCursor: true},
	"[<Tab>": {action: identGoto},
	"]<Tab>": {action: identGoto, fromCursor: true},

	"[d":     {action: identShow, define: true},
	"]d":     {action: identShow, define: true, fromCursor: true},
	"[D":     {action: identShowAll, define: true},
	"]D":     {action: identShowAll, define: true, fromCursor: true},
	"[<C-D>": {action: identGoto, define: true},
	"]<C-D>": {action: identGoto, define: true, fromCursor: true},
}

// identUnderCursor is vim's find_ident_under_cursor with FIND_IDENT: the word
// the cursor is on, or the first one after it on the same line.
//
// It returns the word and the byte column it starts in, so that a jump can
// land where vim lands, which is the first byte of the match and not the
// first byte of the line.
func (e *Editor) identUnderCursor() (string, bool) {
	line := e.buf.Line(e.cur.Line)
	col := e.cur.Col
	if col > len(line) {
		col = len(line)
	}
	// vim scans forward on the line for the first keyword byte, so the cursor
	// sitting on a space or a bracket still finds the word after it. It does
	// not scan backwards past a non-keyword byte and it never leaves the line.
	for col < len(line) && !isKeywordByte(line[col], e.opt.IsKeyword) {
		col++
	}
	if col >= len(line) {
		return "", false
	}
	start := col
	for start > 0 && isKeywordByte(line[start-1], e.opt.IsKeyword) {
		start--
	}
	end := col
	for end < len(line) && isKeywordByte(line[end], e.opt.IsKeyword) {
		end++
	}
	return string(line[start:end]), true
}

// identMatch is one line the identifier search found.
type identMatch struct {
	line int
	col  int
}

// identSearch walks the buffer for the identifier, in the range the command
// asks for, and returns every match in order.
//
// One match per LINE and not per occurrence: vim's find_pattern_in_path moves
// on to the next line once a line has matched, which is why "[I" over a line
// holding the word twice numbers it once.
func (e *Editor) identSearch(word string, c identCmd) []identMatch {
	first := 1
	if c.fromCursor {
		first = e.cur.Line + 1
		// The clamp: vim starts on the last line rather than past it, which
		// is what turns "]<Tab>" on the last line into E387.
		if first > e.buf.LineCount() {
			first = e.buf.LineCount()
		}
	}
	var out []identMatch
	for n := first; n <= e.buf.LineCount(); n++ {
		line := e.buf.Line(n)
		from := 0
		if c.define {
			end, ok := e.matchDefine(line)
			if !ok {
				continue
			}
			from = end
		}
		if col, ok := e.findWord(line, from, word); ok {
			out = append(out, identMatch{line: n, col: col})
		}
	}
	return out
}

// findWord is vim's "\<word\>" over one line, done by hand rather than through
// internal/regex because vim's \< is 'iskeyword'-aware and Go's \b is not. The
// translator says so in its own doc comment; this is the one place in the mode
// machine that would notice.
func (e *Editor) findWord(line []byte, from int, word string) (int, bool) {
	for i := from; i+len(word) <= len(line); i++ {
		if string(line[i:i+len(word)]) != word {
			continue
		}
		if i > 0 && isKeywordByte(line[i-1], e.opt.IsKeyword) {
			continue
		}
		if j := i + len(word); j < len(line) && isKeywordByte(line[j], e.opt.IsKeyword) {
			continue
		}
		return i, true
	}
	return 0, false
}

// matchDefine reports whether the line starts a macro definition under
// 'define', and where that part of it ends, because the identifier has to come
// after it.
//
// The default 'define' is "^\s*#\s*define", matched here directly rather than
// through internal/regex: it is the only value this editor ever sees, since
// nothing sets the option, and a translator call would turn a one-line rule
// into a compile per line of the buffer.
func (e *Editor) matchDefine(line []byte) (int, bool) {
	i := 0
	for i < len(line) && isSpaceByte(line[i]) {
		i++
	}
	if i >= len(line) || line[i] != '#' {
		return 0, false
	}
	i++
	for i < len(line) && isSpaceByte(line[i]) {
		i++
	}
	if !strings.HasPrefix(string(line[i:]), "define") {
		return 0, false
	}
	return i + len("define"), true
}

// identCommand runs one of the [i / [d family.
func (e *Editor) identCommand(s string, count int) error {
	c, ok := identCmds[s]
	if !ok {
		return e.beep()
	}
	found, err := e.runIdent(c, count)
	_ = found
	return err
}

// IdentSplit is CTRL-W i and CTRL-W d, which the frontend owns because they
// open a window.
//
// It answers the three messages vim answers before any window is made -- no
// identifier, match on the current line, nothing found -- and reports whether
// there is a match to split for. A true means the caller still has a window to
// open and this editor has not moved the cursor.
func (e *Editor) IdentSplit(define bool, count int) (bool, error) {
	return e.runIdent(identCmd{action: identSplit, define: define}, count)
}

// runIdent is the body all four actions share.
func (e *Editor) runIdent(c identCmd, count int) (bool, error) {
	if count < 1 {
		count = 1
	}
	word, ok := e.identUnderCursor()
	if !ok {
		e.Say("E349: No identifier under cursor")
		e.finish(false)
		return false, nil
	}

	matches := e.identSearch(word, c)
	if c.action == identShowAll {
		e.showAllIdent(matches)
		e.finish(false)
		return len(matches) > 0, nil
	}

	if len(matches) < count {
		// Two different codes for "nothing here", and vim picks by what was
		// being searched for rather than by which key was typed: a define
		// search that finds nothing is E388 whether it was "[d" or "[<C-D>".
		if c.define {
			e.Say("E388: Couldn't find definition")
		} else {
			e.Say("E389: Couldn't find pattern")
		}
		e.finish(false)
		return false, nil
	}
	m := matches[count-1]
	if m.line == e.cur.Line {
		e.Say("E387: Match is on current line")
		e.finish(false)
		return false, nil
	}

	switch c.action {
	case identShow:
		e.Say(msgOutTrans(e.buf.Line(m.line), e.opt.TabStop))
	case identGoto:
		// A jump, and one the help's list of them does not mention. vim's
		// find_pattern_in_path() calls setpcmark() under ACTION_GOTO, so
		// "[<Tab>" and "]<Tab>" both push the jumplist and set the ' mark.
		// Measured on 200 lines with the word on line 10: "100G10|[<Tab>"
		// leaves line 100 column 9 in :jumps and '' on the same place.
		e.setPCMark(e.cur)
		e.SetCursor(text.Pos{Line: m.line, Col: m.col})
	case identSplit:
		// The window is the caller's to open, and until it can be the cursor
		// stays where it is: a split that silently did not happen would look
		// exactly like a vim that also did nothing.
		e.finish(false)
		return true, nil
	}
	e.finish(false)
	return true, nil
}

// showAllIdent is "[I": the file name, then one numbered line per match.
//
// The format is vim's, measured: "%3d: " for the match number, "%4d" for the
// line number, a space, and the line with tabs expanded and control characters
// shown as "^X".
func (e *Editor) showAllIdent(matches []identMatch) {
	if len(matches) == 0 {
		e.Say("E389: Couldn't find pattern")
		return
	}
	name := e.name
	if name == "" {
		name = "[No Name]"
	}
	e.Say(name)
	for i, m := range matches {
		e.Say(fmt.Sprintf("%3d: %4d %s", i+1, m.line, msgOutTrans(e.buf.Line(m.line), e.opt.TabStop)))
	}
}

// msgOutTrans is vim's msg_outtrans over one line of the buffer: a Tab
// advances to the next 'tabstop' boundary as spaces and every other control
// character shows as "^X".
//
// The tab stop is 'tabstop' and not a fixed eight, measured under both option
// profiles: the same line shows six spaces after "ab" under `vim --clean`
// (ts=8) and two under the vimrc profile (ts=2).
func msgOutTrans(line []byte, tabstop int) string {
	if tabstop < 1 {
		tabstop = 8
	}
	var b strings.Builder
	col := 0
	for _, c := range line {
		switch {
		case c == '\t':
			n := tabstop - col%tabstop
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case c < 0x20:
			b.WriteByte('^')
			b.WriteByte(c + '@')
			col += 2
		case c == 0x7f:
			b.WriteString("^?")
			col += 2
		default:
			b.WriteByte(c)
			col++
		}
	}
	return b.String()
}

// KeywordError is K over an identifier: the word was found and 'keywordprg'
// has to be run over it, which needs a shell the mode machine has no business
// owning. The frontend catches this, runs the command line in Cmd, and the
// message vim prints for it comes out of the ex layer's ":!".
type KeywordError struct {
	// Cmd is the ex command line to run, without the leading colon:
	// "!man 'foo'" for the default 'keywordprg'.
	Cmd string
}

func (e *KeywordError) Error() string { return "mode: 'keywordprg' needs a shell: " + e.Cmd }

// keyword is K: run 'keywordprg' over the identifier under the cursor.
//
// Measured against vim 9.2.0321: K over "foo" leaves ":!man 'foo'\r" on the
// message line and runs the command, and K with no identifier on the line is
// E349 and nothing else. The single quotes are vim's shell escaping and they
// are what makes a word with a shell metacharacter in it safe.
func (e *Editor) keyword(count int) error {
	word, ok := e.identUnderCursor()
	if !ok {
		e.Say("E349: No identifier under cursor")
		return e.finishQuiet()
	}
	prg := e.opt.KeywordPrg
	if prg == "" {
		prg = "man"
	}
	arg := shellQuote(word)
	if count > 1 && prg == "man" {
		// vim's nv_ident special-cases man, because a count in front of K is
		// a manual section and "man foo 2" is not the same command as
		// "man 2 foo".
		arg = fmt.Sprintf("%d %s", count, arg)
	}
	e.finish(false)
	return &KeywordError{Cmd: "!" + prg + " " + arg}
}

// finishQuiet ends a command that said something and changed nothing.
func (e *Editor) finishQuiet() error {
	e.finish(false)
	return nil
}

// shellQuote wraps a word the way vim's vim_strsave_shellescape does for a
// POSIX shell: single quotes, with an embedded quote closed, escaped and
// reopened.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
