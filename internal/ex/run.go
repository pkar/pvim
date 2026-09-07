package ex

import (
	"errors"
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// Dispatch: one command line in, one command run, one message out.

// ErrQuit is what a command that stops the editor returns when the Context has
// no Quit callback. A frontend that supplies one never sees it; the headless
// paths and the tests do, and it is an error rather than a bool for the same
// reason mode.ErrQuit is: it travels out through the return every caller
// already checks.
var ErrQuit = errors.New("ex: quit")

// Run parses and runs one ex command line.
//
// It is the entry point every colon line goes through: the command line, a
// ":normal" inside a ":global", a mapping's right-hand side, a user command's
// replacement, and the vimrc loader's ex statements. Bars have already been
// split by RunLine, which is the only caller that knows whether they were
// separators.
func (c *Context) Run(line string) error {
	cmd, err := Parse(line)
	if err != nil {
		return err
	}
	return c.RunCmd(cmd)
}

// RunCmd runs one already-parsed command. It is exported because internal/vimrc
// parses its own lines and wants the dispatch without the parse.
func (c *Context) RunCmd(cmd Cmd) error {
	switch cmd.Name {
	case cmdNop:
		return nil
	case cmdGoto:
		return c.gotoLine(cmd)
	}

	// A name starting with an uppercase letter is a user command, always. That
	// is vim's rule and it is what keeps ":JsonPretty" out of the built-in
	// table and ":W" an E492 rather than a mistyped ":w".
	if cmd.Typed != "" && cmd.Typed[0] >= 'A' && cmd.Typed[0] <= 'Z' {
		return c.runUser(cmd)
	}

	entry, ok := find(cmd.Name)
	if !ok {
		return withName(ErrNotAnEditorCommand, cmd.Typed)
	}
	if err := c.resolveFor(entry, &cmd); err != nil {
		return c.sourcedError(err, cmd.Line)
	}
	if entry.Handler == nil {
		return withName(ErrNotImplemented, cmd.Name)
	}
	return entry.Handler(c, cmd)
}

// resolveFor turns the command's Range into the LineRange its handler acts on.
//
// Four rules, in vim's order, and the order is the specification:
//
// - the range resolves against the buffer, the marks and the cursor;
// - a backwards one prompts, and a "y" swaps it rather than failing;
// - line 0 becomes line 1 for every command without vim's EX_ZEROR flag,
// which is why ":0d" deletes the first line and ":0put" puts above it;
// - a trailing count replaces the range with that many lines starting at its
// last line, which is why ":1,2>3" shifts lines 2 to 4.
func (c *Context) resolveFor(entry *Command, cmd *Cmd) error {
	if entry.Range == RangeNone || entry.Range == RangeCount {
		// RangeCount's "range" is a number the handler reads off Cmd.Range,
		// not lines in a buffer: ":tabn 3" is the third tab. Nothing is
		// resolved and nothing is checked.
		if cmd.Range.Given > 0 && entry.Range == RangeCount {
			if n, err := resolveAddr(cmd.Range.To, c, c.cursorLine()); err == nil {
				cmd.Count = n
			}
		}
		return nil
	}

	def := rangeCurrent
	if entry.Range == RangeFile {
		def = rangeFile
	}
	first, last, err := Resolve(cmd.Range, c, def)
	if errors.Is(err, ErrBackwardsRange) {
		ok, perr := c.confirmSwap()
		if perr != nil {
			return perr
		}
		if !ok {
			return errSilent
		}
		first, last = last, first
	} else if err != nil {
		return err
	}

	if !entry.Zero {
		if first == 0 {
			first = 1
		}
		if last == 0 {
			last = 1
		}
	}
	if cmd.Count > 0 && entry.Count {
		first = last
		last = first + cmd.Count - 1
		if err := c.checkLine(last); err != nil {
			return err
		}
	}
	cmd.Lines = LineRange{First: first, Last: last, Given: cmd.Range.Given}
	return nil
}

// sourcedError is vim's append_command: an error raised while parsing or
// resolving a command line that was not TYPED quotes the line after it.
//
// do_one_cmd keeps the errors it raises before a handler runs in an "errormsg"
// local and prints them at the end with append_command(*cmdlinep) when
// "sourcing" is set, which it is for every line a ":g" runs. So ":g/^a/+3d"
// that runs off the end of the buffer says "E16: Invalid range: +3d" where the
// same command typed at the colon prompt says a bare "E16: Invalid range".
//
// Only those errors. The ones a handler raises for itself go out through emsg
// and are never quoted, which is why ":g/^a/m99" says a bare E16 and
// ":g/^a/put x" a bare E353. Measured, all four.
func (c *Context) sourcedError(err error, line string) error {
	if err == nil || err == errSilent || c == nil || !c.sourced {
		return err
	}
	// The line as written, leading blanks and all: ":g/^a/ +3d" says
	// "E16: Invalid range: +3d" with two spaces in it.
	return withName(err, line)
}

// errSilent is a command that failed in a way that has already been reported,
// or that the user cancelled. Nothing prints it.
var errSilent = errors.New("")

// confirmSwap is vim's backwards-range prompt, measured:
// "Backwards range given, OK to swap (y/n)?", with "y" swapping and running
// and anything else abandoning.
func (c *Context) confirmSwap() (bool, error) {
	if c.Prompt == nil {
		return false, ErrBackwardsRange
	}
	answer, err := c.Prompt("Backwards range given, OK to swap (y/n)?", "yn")
	if err != nil {
		return false, err
	}
	return answer == 'y' || answer == 'Y', nil
}

// RunLine runs a command line that may hold several commands separated by "|".
//
// The vimrc has one of these and it is the nastiest line in the file:
//
//	au BufWritePost .vimrc so $MYVIMRC | if has('gui_running') && filereadable($MYGVIMRC) | so $MYGVIMRC | endif
//
// Splitting it needs to know which commands swallow a bar rather than being
// ended by one: ":normal", ":global", ":!" and the map family all take the
// rest of the line whatever is in it, and a splitter that does not know that
// turns ":g/x/s/a/b/ | echo" into two broken halves.
func (c *Context) RunLine(line string) error {
	for _, part := range SplitBars(line) {
		if err := c.Run(part); err != nil {
			return err
		}
	}
	return nil
}

// SplitBars breaks a command line at the bars that are separators.
//
// A bar is a separator unless it is escaped with a backslash, unless it is
// inside a part of the line the command reads for itself, or unless the
// command is one of the few that take the whole rest of the line. It is
// exported because internal/vimrc splits the same way before it decides which
// half is an ":if".
func SplitBars(line string) []string {
	var out []string
	rest := line
	for {
		n := cmdEnd(rest)
		if n >= len(rest) {
			return append(out, rest)
		}
		out = append(out, rest[:n])
		rest = rest[n+1:]
	}
}

// cmdEnd is the index of the bar that ends the first command in s, or len(s)
// when no bar in it is a separator.
//
// Vim does not have this function, because vim does not split a line up front:
// do_one_cmd parses the command and each command's own parser decides where it
// ends, setting eap->nextcmd when it finds a bar it does not want. This is that
// decision made in one place, and it has to reach the same answer, which takes
// four cases: the commands that take the whole rest of the line, ":s", the two
// repeat forms of ":s", and everything else, which ends at its first bar.
func cmdEnd(s string) int {
	p := &parser{s: s}
	var c Cmd
	_ = parseMods(p, &c)
	if _, err := parseRange(p); err != nil {
		// A range this cannot parse is a line whose command it cannot name
		// either, so it is cut at its first bar and the real parser gets to
		// report what is wrong with the half that mattered.
		return barAt(s, 0)
	}
	p.skipWhite()
	at := p.i
	name := scanName(p)
	if name == "" {
		return barAt(s, at)
	}
	if name == "!" {
		return len(s)
	}
	cmd, ok := Lookup(name)
	if ok && isMapCommand(cmd.Name) {
		// A bar in a right-hand side is part of the mapping: vim's map
		// commands have no EX_TRLBAR, so ":nnoremap gh:nohl<CR>|echo" maps
		// the whole of it and does not echo anything.
		return len(s)
	}
	if !ok {
		// A user command, whose definition this function cannot see. vim ends
		// one at a bar only when it was defined with -bar, and -bar is what
		// the vimrc's only user command has, so ending it there is the answer
		// that is right for this config and wrong for nobody in it.
		return barAt(s, p.i)
	}
	switch cmd.Name {
	// The commands without vim's EX_TRLBAR that this editor implements:
	// ":normal" replays the bar as a keystroke, ":global" and ":vglobal" hand
	// it to the command they run, and the map and command definitions store it
	// in the right-hand side.
	case "normal", "global", "vglobal", "autocmd", "command", "abbreviate",
		"echo", "echomsg", "redir":
		return len(s)

	// ":s" has no EX_TRLBAR either, and it is the one that cannot be answered
	// with a yes or a no: do_sub scans the delimiter, the pattern, the
	// replacement and the flags itself and only then calls check_nextcmd, so
	// ":s/a|b/X/" is one command and ":s/a/X/ | s/b/Y/" is two. Measured, all
	// four shapes; splitting at the first bar wrote "|b" into the file.
	case "substitute":
		if name != "substitute" && !strings.HasPrefix("substitute", name) {
			// ":sg" is ":s" with its flags written against the name and vim
			// consumes only the "s". Parse does the same and for the same
			// reason; the two have to agree or the halves this cuts are not
			// the halves that get parsed.
			p.i -= len(name) - 1
		}
		return barAt(s, subArgEnd(s, p.i, true))
	case "&", "~":
		// ":&" and ":~" are repeats by definition and never take a pattern, so
		// the "&" of ":&&" is a flag and not a delimiter.
		return barAt(s, subArgEnd(s, p.i, false))
	}
	return barAt(s, p.i)
}

// barAt is the index of the first unescaped bar in s at or after from, or
// len(s) when there is none.
func barAt(s string, from int) int {
	for i := from; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '|':
			return i
		}
	}
	return len(s)
}

// subArgEnd is where a ":s" argument stops being the command's own text.
//
// from is the index of the first byte after the command name. The return is
// the index of the first byte a bar could be a separator at, which is after
// the flags: vim's do_sub reads the delimiter, then the pattern up to the next
// unescaped delimiter, then the replacement up to the one after that, then the
// flag letters and the count, and only then looks for what comes next.
//
// The repeat forms -- ":s", ":sg", ":&", ":~" -- have no delimiter and no
// pattern, so their argument is the flags alone, which is why ":s|echo" is a
// repeat followed by an echo and ":s/a|b/X/" is not. pattern says whether this
// spelling may carry one at all.
func subArgEnd(s string, from int, pattern bool) int {
	i := from
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if pattern && i < len(s) && !isAlnum(s[i]) && s[i] != '"' && s[i] != '|' && s[i] != '\\' {
		delim := s[i]
		i++
		i = skipToDelim(s, i, delim)
		i = skipToDelim(s, i, delim)
	}
	// The flags, the count and the white space between them. None of them can
	// be a bar, which is the whole point: the first bar after here is vim's
	// check_nextcmd and the first bar before here belonged to the pattern or
	// the replacement.
	for i < len(s) && (isSubFlag(s[i]) || isDigit(s[i]) || s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// skipToDelim advances past the next unescaped delim, or to the end of the
// line when there is none, which is what ":s/b" with nothing after it does.
func skipToDelim(s string, i int, delim byte) int {
	for ; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == delim {
			return i + 1
		}
	}
	return len(s)
}

// isSubFlag is the flag letters ":s" takes, from vim's do_sub. "&" is one of
// them, which is why ":s/a/b/&g" reuses the flags of the last substitute.
func isSubFlag(c byte) bool {
	switch c {
	case '&', 'c', 'e', 'g', 'i', 'I', 'n', 'p', '#', 'l', 'r':
		return true
	}
	return false
}

// isAlnum is ASCII only, on purpose: vim's ASCII_ISALNUM is what decides
// whether a byte can be a ":s" delimiter, and a multi-byte character can be.
func isAlnum(c byte) bool { return isAlpha(c) || isDigit(c) }

// gotoLine is the bare ":5": move to that line, first non-blank.
func (c *Context) gotoLine(cmd Cmd) error {
	first, last, err := Resolve(cmd.Range, c, rangeCurrent)
	if errors.Is(err, ErrBackwardsRange) {
		// A bare range never runs anything, so a backwards one just uses the
		// second address, which is where vim leaves the cursor.
		err = nil
	} else if err != nil {
		return err
	}
	_ = first
	if last < 1 {
		last = 1
	}
	c.moveTo(last)
	return nil
}

// moveTo puts the cursor on the first non-blank of a line, which is where
// every ex command that moves it leaves it.
func (c *Context) moveTo(line int) {
	b := c.buffer()
	if line < 1 {
		line = 1
	}
	if n := b.LineCount(); line > n {
		line = n
	}
	c.Ed.SetCursor(text.Pos{Line: line, Col: firstNonBlank(b.Line(line))})
	c.Sync()
}

// firstNonBlank is the byte column of the first character that is not a space
// or a tab, or the last column of a line that is all blanks.
func firstNonBlank(line []byte) int {
	for i, ch := range line {
		if ch != ' ' && ch != '\t' {
			return i
		}
	}
	if len(line) == 0 {
		return 0
	}
	return len(line) - 1
}

// The small accessors every handler uses. They exist so that a Context with a
// half-filled set of pointers -- which is what a test builds -- cannot panic
// its way through a command.

func (c *Context) buffer() *text.Buffer {
	if c == nil || c.Ed == nil {
		return text.New()
	}
	return c.Ed.Buffer()
}

func (c *Context) cursorLine() int {
	if c == nil || c.Ed == nil {
		return 1
	}
	return c.Ed.Cursor().Line
}

func (c *Context) setCursorLine(n int) {
	if c == nil || c.Ed == nil {
		return
	}
	c.Ed.SetCursor(text.Pos{Line: n})
}

// setCursorLineRaw is vim's bare "curwin->w_cursor.lnum = n" with nothing
// after it: no beginline, no check_cursor_col, and a column that is now past
// the end of the line left where it is.
//
// The only thing that touches such a column is mb_check_adjust_col, whose rule
// is the whole of this and was measured on ":2,2j" from every column of a
// six-character line onto a two-character one: a column greater than the line's
// length becomes zero, and anything up to and including it, the NUL at the end
// included, is kept exactly. Columns 1 to 3 come out as 1 to 3 and column 4
// onwards comes out as 1.
func (c *Context) setCursorLineRaw(n int) {
	if c == nil || c.Ed == nil {
		return
	}
	b := c.buffer()
	if n < 1 {
		n = 1
	}
	if last := b.LineCount(); last > 0 && n > last {
		n = last
	}
	col := c.Ed.Cursor().Col
	if col > len(b.Line(n)) {
		col = 0
	}
	c.Ed.SetCursor(text.Pos{Line: n, Col: col})
	c.Sync()
}

// moveToKeepCol is vim's check_cursor() after an assignment to
// curwin->w_cursor.lnum: the line changes, the column stays where it was and is
// clamped to the new line.
//
// It is a second helper and not a flag on setCursorLine because the column
// policy is per call site. A ";" in a range is this one -- get_address does
// "curwin->w_cursor.lnum = ea.line2" and nothing to the column, so "3G3|" then
// ":1;+1y" ends in column 3 -- and ":j", ":<" and the substitute report are
// beginline(BL_SOL) sites that want the first non-blank instead.
func (c *Context) moveToKeepCol(n int) {
	if c == nil || c.Ed == nil {
		return
	}
	b := c.buffer()
	if n < 1 {
		n = 1
	}
	if last := b.LineCount(); last > 0 && n > last {
		n = last
	}
	// The last character of the line and not the position after it: the
	// buffer's own clamp allows the end of the line because insert mode lives
	// there, and check_cursor_col in normal mode does not.
	col := c.Ed.Cursor().Col
	if max := len(b.Line(n)) - 1; col > max {
		col = max
	}
	if col < 0 {
		col = 0
	}
	c.Ed.SetCursor(text.Pos{Line: n, Col: col})
	c.Sync()
}

// say puts a line on the message area.
func (c *Context) say(s string) {
	if c == nil {
		return
	}
	if c.Msg != nil {
		c.Msg(s)
		return
	}
	if c.Ed != nil {
		c.Ed.Say(s)
	}
}

// sayRaw writes into the message stream with no framing: no leading newline
// and no trailing one. ":!" is the only command that needs it, because the
// exchange vim prints for a shell command is not a sequence of lines. See
// Context.bangCommand.
func (c *Context) sayRaw(s string) {
	if c == nil {
		return
	}
	if c.MsgRaw != nil {
		c.MsgRaw(s)
		return
	}
	if c.Ed != nil {
		c.Ed.SayRaw(s)
	}
}

// sayKeep is say for the messages vim marks worth keeping across the redraw
// that follows the command: its set_keep_msg, which msgmore() and the
// substitute report call.
//
// It matters because ":redir" catches the redisplay as well as the message,
// so ":2d 3" leaves "3 fewer lines" in the log TWICE and ":2,4y" leaves
// "3 lines yanked" once. Which messages are kept was measured through
// cmd/oracle rather than read out of vim's source: the line-count reports of
// :d, :pu, :co, :t and :r are, the substitute and match counts are, and
// "N lines yanked", "N lines moved", "N lines filtered" and
// "--No lines in buffer--" are not.
func (c *Context) sayKeep(s string) {
	if c == nil {
		return
	}
	if c.MsgKeep != nil {
		c.MsgKeep(s)
		return
	}
	c.say(s)
}

// sayLineCount is vim's msgmore(): the "N more lines" and "N fewer lines" a
// command that changed the line count reports, said only when the count is
// over 'report' and kept across the redraw.
func (c *Context) sayLineCount(delta int) {
	n := delta
	if n < 0 {
		n = -n
	}
	if !c.reportOver(n) {
		return
	}
	word := " more "
	if delta < 0 {
		word = " fewer "
	}
	c.sayKeep(strconv.Itoa(n) + word + plural(n, "line", "lines"))
}

// opts returns the option state, filling in vim's defaults for a caller that
// built a Context without one.
//
// It writes the fallback back onto the Context rather than handing out a fresh
// copy each time, because ":set" writes through this and a copy would take the
// change with it when it went out of scope.
func (c *Context) opts() *options.Options {
	if c == nil {
		d := options.Defaults()
		return &d
	}
	if c.Opt == nil {
		d := options.Defaults()
		c.Opt = &d
	}
	return c.Opt
}

// tab returns the current tab page, or nil. Every window command goes through
// it rather than through c.Tabs.Current() directly, because window.Tabs has no
// nil-receiver guard and a Context built by the vimrc loader has no tabs at
// all.
func (c *Context) tab() *window.TabPage {
	if c == nil || c.Tabs == nil {
		return nil
	}
	return c.Tabs.Current()
}

// report is 'report': how many lines a command has to touch before it says so.
// Vim's rule is strictly more than the option, which is why ":1,2d" is silent
// with the default of 2 and ":1,3d" says "3 fewer lines".
func (c *Context) reportOver(n int) bool { return n > c.opts().G.Report }

// bufs returns the buffer list, making an empty one on the fly so that a
// Context built for a test that never touches a file still works.
func (c *Context) bufs() *BufList {
	if c.Bufs == nil {
		c.Bufs = NewBufList()
	}
	return c.Bufs
}

// current returns the Buf the editor is showing, adding one for the editor's
// buffer if the list has never been told about it.
func (c *Context) current() *Buf {
	l := c.bufs()
	if l.Cur != nil {
		return l.Cur
	}
	b := l.Add("", c.buffer())
	l.Cur = b
	return b
}

// lines returns the buffer's lines in a range as a slice of copies, which is
// what every command that yanks, copies or filters wants.
func (c *Context) lines(first, last int) [][]byte {
	b := c.buffer()
	out := make([][]byte, 0, last-first+1)
	for n := first; n <= last; n++ {
		line := b.Line(n)
		cp := make([]byte, len(line))
		copy(cp, line)
		out = append(out, cp)
	}
	return out
}

// plural renders vim's "1 line" and "3 lines", which every count message needs
// and which is the difference between matching vim's message and nearly
// matching it.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// trimArgs strips the white space around a command's arguments, which almost
// every handler wants and one or two -- the filter, which passes its argument
// to a shell -- deliberately do not.
func trimArgs(s string) string { return strings.TrimSpace(s) }
