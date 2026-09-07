package ex

import (
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// The commands that change or read the text of a range: ":d", ":y", ":pu",
// ":co", ":t", ":m", ":j", ":>", ":<", ":normal", ":k", ":p" and its two
// spellings.
//
// Where a command is exactly a normal-mode one with a range in front of it,
// this file drives the mode machine rather than reimplementing it. ":j" is J
// with a count and ":>" is >> with a count, and both carry rules -- what a
// join puts between two lines, what 'shiftround' does to an indent of three --
// that are already written down once, measured against vim, in internal/mode
// and internal/operator. A second copy here would be a second set of bugs.
//
// Where a command is not a normal-mode one, it is written out. ":d" is not
// dd: it leaves the cursor on the first non-blank of the line that took the
// deleted lines' place whatever column the cursor was in, and ":y" does not
// move the cursor at all. Both were measured:
// "5G:1,3d" leaves 1,3 and "5G:1,3y" leaves 5,3.

// edit runs one buffer change as its own undo step.
//
// The mode machine is synced first so that a half-open block from the keys
// before the colon is closed rather than swallowing this command, and the
// block here is opened and closed around the whole change so that one ex
// command is one press of u.
func (c *Context) edit(cursor text.Pos, f func(b *text.Buffer)) {
	b := c.buffer()
	script := c.scriptInput()
	if c.Ed != nil && !script {
		c.Ed.Sync()
	}
	b.OpenUndoBlock(cursor)
	f(b)
	if !script {
		b.CloseUndoBlock()
	}
}

// scriptInput reports whether the keys are coming from a "-s" file, which is
// the one thing that decides where an undo step ends.
//
// vim's may_sync_undo() does nothing at all while it is reading a script, and
// that is as true of ":$d" twice as it is of "x" twice: both leave one undo
// header and one "u" takes both back. Closing the block at the end of every
// ex command made the oracle's ":$d:$d" two steps where vim has one, and the
// changelist two entries where vim has one.
func (c *Context) scriptInput() bool { return c != nil && c.Ed != nil && c.Ed.ScriptInput() }

// editNoLines numbers an undo header that saved no lines and changes nothing.
//
// It is edit for the failure half of ":put". vim's do_put calls
// u_save(lnum, lnum + 1), which saves the gap BETWEEN two lines rather than a
// line, before it looks the register up, so a put of an empty register leaves a
// header behind: "u" after it says "0 changes; before #1" where an ordinary
// empty header says "1 change" and where no header at all says "Already at
// oldest change". See text.undoNode.noLines, which carries the same
// measurement for normal mode's p.
func (c *Context) editNoLines(cursor text.Pos) {
	b := c.buffer()
	script := c.scriptInput()
	if c.Ed != nil && !script {
		c.Ed.Sync()
	}
	b.OpenUndoBlockNoLines(cursor)
	if !script {
		b.CloseUndoBlock()
	}
}

// editIfChanged is edit for a command that numbers an undo header only when it
// actually changes something.
//
// ":s" is the one that needs it. vim's do_sub calls u_save inside the loop, on
// the first line it is about to replace, so ":s/zzz/x/" over a file with no
// zzz in it leaves undotree().seq_cur at 0 where every other shape of ex
// command leaves it at 1. Measured for the "e" flag, the "n" flag, a plain
// failing pattern and a nomagic one.
//
// Inside a ":g" it is the same rule one line at a time, and it decides two
// things rather than one. ":g/a/s/zzz/Q/" over a file with no zzz in it
// numbers no header at all, so "u" after it says "Already at oldest change";
// and ":g/^/s/b/Y/" over "acc" and "abc" numbers the header on line 2, the
// first line it actually replaced, so "u" leaves the cursor there and not on
// line 1. Both measured. Dropping the empty block on the line that changed
// nothing is what gets both: what survives is the block opened by the first
// line that changed, cursor and all.
func (c *Context) editIfChanged(cursor text.Pos, f func(b *text.Buffer)) {
	b := c.buffer()
	if c.Ed != nil {
		c.Ed.Sync()
	}
	// Whether the step was already open decides what an empty one means: a
	// step this command opened and did not write to is the header vim never
	// numbered, and one it joined belongs to the command before it.
	opened := !b.UndoBlockOpen()

	// A ":g" holds the block open for the length of the global (see exGlobal)
	// and a hold makes DropUndoBlock a no-op, so the hold comes off around a
	// block this command is about to open and goes back on before this
	// returns. Taking it off is safe exactly here and nowhere else: releasing
	// a hold closes the open block, and there is no open block at this point
	// -- that is what "opened" says. Nothing between here and the release
	// closes one either, because the close below is skipped while the hold is
	// off; the global still ends with one undo step for the lot.
	unheld := opened && c.undoHeld
	if unheld {
		b.ReleaseUndoBlock()
	}

	b.OpenUndoBlock(cursor)
	f(b)
	switch {
	case opened && !b.UndoBlockChanged():
		b.DropUndoBlock()
	case unheld:
		// The block belongs to the global now. Leave it open: the hold going
		// back on is what stops the next command in the global closing it.
	case !c.scriptInput():
		b.CloseUndoBlock()
	}

	if unheld {
		b.HoldUndoBlock()
	}
}

// regs returns the register file, or nil when the Context has no editor, which
// is what a parser test builds.
func (c *Context) regs() *register.File {
	if c.Ed == nil {
		return nil
	}
	return c.Ed.Registers()
}

// runKeys feeds normal-mode keystrokes to the mode machine.
//
// It is how ":j" and ":>" borrow J and >>. The keys are decoded the way a
// script's are, so an Escape in a ":normal" argument is an Escape and not the
// start of an arrow key: a string that has already arrived in full cannot pose
// the timing question a terminal poses.
func (c *Context) runKeys(s string) error {
	if c.Ed == nil {
		return nil
	}
	var d key.Decoder
	b := []byte(s)
	var keys []key.Key
	for len(b) > 0 {
		k, n, st := d.Flush(b)
		if st != key.StatusKey || n == 0 {
			break
		}
		keys = append(keys, k)
		b = b[n:]
	}
	return c.Ed.ExecNormal(keys)
}

// exDelete is ":d". The register is linewise and goes through the "1 shift,
// which is what a linewise delete does in normal mode too.
func exDelete(c *Context, cmd Cmd) error {
	first, last := cmd.Lines.First, cmd.Lines.Last
	b := c.buffer()
	if b.LineCount() == 0 {
		return nil
	}
	whole := first <= 1 && last >= b.LineCount()
	n := last - first + 1
	val := register.LineValue(c.lines(first, last)...)
	if r := c.regs(); r != nil {
		if err := r.Delete(cmd.Reg, val, true); err != nil {
			return err
		}
	}
	c.edit(text.Pos{Line: first}, func(b *text.Buffer) { b.DeleteLines(first, last) })

	if whole {
		// vim says this instead of the count when nothing is left. Measured
		// with ":%d" on a ten-line file, which prints "--No lines in buffer--"
		// and no count at all.
		c.moveTo(1)
		c.say("--No lines in buffer--")
		return nil
	}
	c.moveTo(first)
	if c.reportOver(n) {
		c.sayKeep(strconv.Itoa(n) + " fewer " + plural(n, "line", "lines"))
	}
	return nil
}

// exYank is ":y". The cursor does not move, measured: "5G:1,3y" leaves the
// cursor on line 5.
func exYank(c *Context, cmd Cmd) error {
	first, last := cmd.Lines.First, cmd.Lines.Last
	n := last - first + 1
	val := register.LineValue(c.lines(first, last)...)
	if r := c.regs(); r != nil {
		if err := r.Yank(cmd.Reg, val); err != nil {
			return err
		}
	}
	if c.reportOver(n) {
		// The register is named in the message unless it was the unnamed one:
		// ":2,4y b" says `3 lines yanked into "b` and ":2,4y" says
		// "3 lines yanked". The case of the name is kept, so an append into
		// "A says so.
		into := ""
		if cmd.Reg != 0 && cmd.Reg != '"' {
			into = ` into "` + string(cmd.Reg)
		}
		c.say(strconv.Itoa(n) + " " + plural(n, "line", "lines") + " yanked" + into)
	}
	return nil
}

// exPut is ":pu". It is always linewise however the register was filled, which
// is the difference between it and p: ":pu" of a charwise register puts the
// text on a line of its own.
//
// The cursor moves and an undo header is numbered BEFORE the register is looked
// up, and they stay put when the look-up fails. That is vim's ex_put, which
// assigns curwin->w_cursor.lnum = eap->line2 and then calls do_put, and do_put
// which saves undo before it finds the register empty: ":3pu" with nothing in
// the unnamed register says E353, leaves the cursor on line 3, and answers a
// following "u" with "0 changes; before #1" rather than "Already at oldest
// change". Measured for ":3pu", ":3pu x" and ":3pu!", and in normal mode too,
// where a bare "p" on an empty register numbers a header the same way.
func exPut(c *Context, cmd Cmd) error {
	name := cmd.Reg
	// ":0put" works like ":1put!", which is the first thing ex_put does.
	last, bang := cmd.Lines.Last, cmd.Bang
	if last == 0 {
		last, bang = 1, true
	}
	c.moveToKeepCol(last)
	r := c.regs()
	if r == nil {
		return withSpace(ErrNothingInRegister, quoteReg(name))
	}
	val, err := r.ForPut(name)
	if err != nil || val.Empty() {
		c.editNoLines(text.Pos{Line: last})
		return withSpace(ErrNothingInRegister, quoteReg(name))
	}
	lines := val.Lines
	// A charwise register put linewise becomes one line per line it holds,
	// which for the common case of a register with no newline in it is one
	// line.
	put := make([][]byte, len(lines))
	for i, l := range lines {
		cp := make([]byte, len(l))
		copy(cp, l)
		put[i] = cp
	}

	// InsertLines puts its lines BEFORE the line it is given, so "after the
	// last line of the range" is one further on. ":pu!" is the one that does
	// not add the one, which is the whole difference between the two.
	at := last + 1
	end := last + len(put)
	if bang {
		at = last
		end = last + len(put) - 1
	}
	c.edit(text.Pos{Line: max(at, 1)}, func(b *text.Buffer) { b.InsertLines(at, put) })
	c.moveTo(end)
	if c.reportOver(len(put)) {
		c.sayKeep(strconv.Itoa(len(put)) + " more " + plural(len(put), "line", "lines"))
	}
	return nil
}

// quoteReg renders a register name for E353, which vim spells with the quote
// in front of it: `E353: Nothing in register "`.
func quoteReg(name byte) string {
	if name == 0 {
		return `"`
	}
	return string(name)
}

// exCopy is ":co" and ":t". The cursor lands on the first non-blank of the
// last copied line, measured: "5G:2,3co6" leaves line 8.
func exCopy(c *Context, cmd Cmd) error {
	first, last := cmd.Lines.First, cmd.Lines.Last
	dest, err := c.destLine(cmd)
	if err != nil {
		return err
	}
	lines := c.lines(first, last)
	c.edit(text.Pos{Line: max(dest, 1)}, func(b *text.Buffer) { b.InsertLines(dest+1, lines) })
	c.moveTo(dest + len(lines))
	if c.reportOver(len(lines)) {
		c.sayKeep(strconv.Itoa(len(lines)) + " more " + plural(len(lines), "line", "lines"))
	}
	return nil
}

// exMove is ":m". A destination inside the range being moved is E134,
// measured with ":1,2m1".
func exMove(c *Context, cmd Cmd) error {
	first, last := cmd.Lines.First, cmd.Lines.Last
	dest, err := c.destLine(cmd)
	if err != nil {
		return err
	}
	// vim's ex_move test, measured with ":1m0", ":2m1",
	// ":1,3m3", ":1,3m0" and ":1,3m2" against /opt/homebrew/bin/vim: only the
	// last of those is E134. The destination has to be strictly inside the
	// range, so a move to just above it or to its own last line is a no-op and
	// not an error -- and ":g/^/m0", the file-reverse idiom, is ":1m0" once per
	// line and would be an error on every one of them under a wider test.
	if dest >= first && dest < last {
		return ErrMoveIntoItself
	}
	lines := c.lines(first, last)
	n := len(lines)
	end := dest
	if dest < first {
		end = dest + n
	}

	// A move to just above the range or to its own last line puts every line
	// back where it was, and vim treats it as nothing having happened: no undo
	// step, no changelist entry and no "N lines moved". It still moves the
	// cursor, which is why that is below this and not after the edit.
	// Measured: ":1m0", ":2m1", ":1,3m0" and ":1,3m3" all leave undotree().
	// seq_cur at 0 and getchangelist() empty, with the cursor at 1, 2, 3 and 3.
	if dest == first-1 || dest == last {
		c.moveTo(end)
		return nil
	}

	c.edit(text.Pos{Line: first}, func(b *text.Buffer) {
		b.InsertLines(dest+1, lines)
		if dest < first {
			b.DeleteLines(first+n, last+n)
		} else {
			b.DeleteLines(first, last)
		}
		// vim's do_move reports the move once, from the top of the two places
		// it touched: changed_lines(dest + 1) when the lines went up and
		// changed_lines(line1) when they went down. ":5m0" lands on line 1,
		// which is what makes ":g/^/m0" leave the changelist on line 1 rather
		// than on the last line it moved.
		b.ChangedAt(text.Pos{Line: min(first, dest+1)})
	})
	c.moveTo(end)
	if c.reportOver(n) {
		c.say(strconv.Itoa(n) + " " + plural(n, "line", "lines") + " moved")
	}
	return nil
}

// destLine resolves the address after ":m", ":co" and ":t".
//
// A missing one is E14 in vim; here it is E471, because the address parser
// answers "no address" rather than "bad expression" and E471 is the message
// for an argument that had to be there.
func (c *Context) destLine(cmd Cmd) (int, error) {
	if cmd.Addr.Kind == AddrNone && cmd.Addr.Offset == 0 && strings.TrimSpace(cmd.Args) != "" {
		return 0, withName(ErrTrailing, cmd.Args)
	}
	if cmd.Addr.Kind == AddrNone && cmd.Addr.Offset == 0 {
		return 0, ErrArgumentRequired
	}
	n, err := resolveAddr(cmd.Addr, c, c.cursorLine())
	if err != nil {
		return 0, err
	}
	if n < 0 || n > c.buffer().LineCount() {
		return 0, ErrInvalidRange
	}
	return n, nil
}

// exJoin is ":j", which is J with a count and the cursor left on the first
// non-blank rather than at the join, measured: "1G:2,4j" leaves 2,3 on a file
// indented by two spaces.
//
// A range of one line joins it with the next, which is vim's own carve-out:
// ":2j" joins lines 2 and 3. Two addresses naming that one line do not, and
// that is the other half of the same carve-out in ex_join:
//
//	if (eap->line1 == eap->line2) { if (eap->addr_count >= 2) return; ... }
//
// so ":2,2j" and ":1,1j" leave the file alone with no undo header and nothing
// on the changelist. Measured, along with ":0,1j", whose zero clamps to one and
// which is therefore the same case.
func exJoin(c *Context, cmd Cmd) error {
	first, last := cmd.Lines.First, cmd.Lines.Last
	// The cursor moves before the decision, which is ex_join's own order and
	// is why a ":2,2j" that joins nothing still leaves the cursor on line 2.
	c.setCursorLineRaw(first)
	if first == last && cmd.Lines.Given >= 2 {
		return nil
	}
	n := last - first + 1
	if n < 2 {
		n = 2
	}
	keys := strconv.Itoa(n)
	if cmd.Bang {
		keys += "gJ"
	} else {
		keys += "J"
	}
	if err := c.runKeys(keys); err != nil {
		return err
	}
	c.moveTo(first)
	return nil
}

// exShift is ":>" and ":<". The number of angle brackets is how many
// shiftwidths, which is why ":2,3>>" indents by two, measured.
func exShift(c *Context, cmd Cmd) error {
	first, last := cmd.Lines.First, cmd.Lines.Last
	n := last - first + 1
	amount := len(cmd.Typed)
	if amount < 1 {
		amount = 1
	}
	dir := ">>"
	if cmd.Typed[0] == '<' {
		dir = "<<"
	}
	for i := 0; i < amount; i++ {
		c.setCursorLine(first)
		if err := c.runKeys(strconv.Itoa(n) + dir); err != nil {
			return err
		}
	}
	c.moveTo(last)
	return nil
}

// exNormal is ":normal": the argument is played as keystrokes, once per line
// of the range when one was given and once at the cursor when it was not.
//
// The bang is the no-mapping form, and there are no mappings in this package
// to skip, so both spellings do the same thing. The cursor is put on the
// first column of each line before the keys run, which is vim's rule and the
// reason ":%normal A;" appends to every line rather than to the first.
func exNormal(c *Context, cmd Cmd) error {
	if cmd.Args == "" {
		return ErrArgumentRequired
	}
	if cmd.Lines.Given == 0 {
		return c.finishNormal(cmd.Args)
	}
	// vim's ex_normal is a do-while over line1++ with no line-count test in
	// it: it assigns the line to the cursor, lets check_cursor() clamp it and
	// runs the keys anyway. So ":%normal dd" on a four-line file empties the
	// buffer -- the third and fourth iterations run on whatever line is left --
	// where stopping at the end of the shrinking buffer leaves two lines.
	// Measured on "a b c d" and on "x" plus a tab-indented line.
	for n := cmd.Lines.First; n <= cmd.Lines.Last; n++ {
		// The line is clamped here and the column is not. vim assigns the line
		// and then "curwin->w_cursor.col = 0" before check_cursor_moved, so a
		// line past the end of the buffer comes back as the last line at
		// column one; clamping the whole position instead lands on the END of
		// that line, and ":%normal 2dd" over ten lines then finishes in column
		// 9 where vim finishes in column 1.
		line := n
		if last := c.buffer().LineCount(); line > last {
			line = last
		}
		c.setCursorLine(line)
		if err := c.finishNormal(cmd.Args); err != nil {
			return err
		}
	}
	return nil
}

// finishNormal runs one ":normal" argument and then ends the command it left
// half-typed, if it left one.
//
// vim does not append an Escape. exec_normal() runs normal_cmd() until the
// typeahead a ":normal" stuffed is empty, and the Escape appears only when a
// command asks for another key and finds nothing: vgetorpeek answers ESC while
// ex_normal_busy is set. A command that COMPLETED never asks, so ":normal v"
// leaves visual mode running and ":normal qa" leaves the recorder on, and an
// unconditional Escape ended both -- and recorded itself into register a.
// Measured: ":normal v" then "lld" deletes three characters, and ":normal qa"
// then "xq" leaves "x" in @a and not an Escape in front of it.
func (c *Context) finishNormal(args string) error {
	if err := c.runKeys(args); err != nil {
		return err
	}
	if !c.normalWaiting() {
		return nil
	}
	return c.runKeys("\x1b")
}

// normalWaiting reports whether the mode machine would ask for another
// keystroke if one were offered: it is in a mode that reads keys until it is
// told to stop, or it is holding a half-typed command.
//
// It is vim's "!commandDone", assembled from what internal/mode exports.
// Visual mode is not on the list and that is the point: v and V are complete
// commands whose mode outlives the ":normal" that started them.
func (c *Context) normalWaiting() bool {
	if c.Ed == nil {
		return false
	}
	switch c.Ed.Mode() {
	case mode.Insert, mode.Replace, mode.Cmdline, mode.OperatorPending:
		return true
	}
	return c.Ed.Waiting()
}

// exMark is ":k" and ":mark": set a mark on the last line of the range.
// Measured: ":3,4k a" sets mark a on line 4.
//
// The column is the first non-blank and not zero. vim's ex_mark moves to
// line2, calls beginline(BL_WHITE|BL_FIX), sets the mark where that left the
// cursor and puts the cursor back, so ":1k a" on " foo bar" then a backtick-a
// lands in column 3. Measured on a space-indented line and a tab-indented one,
// which rules out a constant offset.
//
// A name longer than one character is E488 quoting the WHOLE argument, which is
// what vim passes to e_trailing_characters_str: ":1k ab" says "ab" and not "b".
func exMark(c *Context, cmd Cmd) error {
	name := strings.TrimSpace(cmd.Args)
	if name == "" {
		return ErrArgumentRequired
	}
	if len(name) != 1 {
		return withName(ErrTrailing, name)
	}
	line := cmd.Lines.Last
	c.buffer().SetMark(name[0], text.Pos{Line: line, Col: firstNonBlank(c.buffer().Line(line))})
	return nil
}

// exPrint is ":p", ":nu"/":#" and ":l", which differ only in what they put in
// front of and behind each line.
//
// ":nu" numbers in a three-wide field and ":l" appends a "$", both measured:
// ":2,3nu" prints " 2 line two" and ":2,3l" prints "line two$".
//
// None of the three prints the line's bytes. vim's print_line ends in
// msg_prt_line, which expands a tab to the next tabstop for ":p" and ":nu" and
// writes it as "^I" for ":l", and renders every other control character as a
// caret and a letter. Measured on "\tbaz foo" at tabstop 8: ":1p" prints eight
// spaces and ":1l" prints "^Ibaz foo$".
func exPrint(kind byte) Handler {
	return func(c *Context, cmd Cmd) error {
		b := c.buffer()
		for n := cmd.Lines.First; n <= cmd.Lines.Last && n <= b.LineCount(); n++ {
			line := b.Line(n)
			switch kind {
			case '#':
				c.say(pad3(n) + " " + printForm(line, c.opts().B.TabStop))
			case 'l':
				c.say(listForm(line))
			default:
				c.say(printForm(line, c.opts().B.TabStop))
			}
		}
		c.moveTo(cmd.Lines.Last)
		return nil
	}
}

// printForm is vim's msg_prt_line with 'list' off: a tab reaches the next
// multiple of 'tabstop' as spaces and every other control character is a caret
// and its letter.
//
// listForm is the same function with 'list' on: a tab is "^I" and the end of
// the line is marked with a "$".
//
// internal/substitute has both of these for the "p" and "l" flags of ":s" and
// they are unexported there, so this is a second copy of a measured rule, which
// is the thing this package otherwise refuses to write. It is here rather than
// imported because the two packages meet at Request and Result and nowhere
// else, and widening that seam to reach one string formatter is the larger
// change. If a third caller ever appears, export one of them and delete this.
func printForm(line []byte, ts int) string {
	if ts < 1 {
		ts = 8
	}
	if len(line) == 0 {
		// An empty line prints as one space: vim puts something under the
		// cursor rather than leaving it on the NUL.
		return " "
	}
	var sb strings.Builder
	col := 0
	for _, ch := range line {
		switch {
		case ch == '\t':
			n := ts - col%ts
			sb.WriteString(strings.Repeat(" ", n))
			col += n
		case ch < 0x20:
			sb.WriteByte('^')
			sb.WriteByte(ch + '@')
			col += 2
		case ch == 0x7f:
			sb.WriteString("^?")
			col += 2
		default:
			sb.WriteByte(ch)
			col++
		}
	}
	return sb.String()
}

func listForm(line []byte) string {
	var sb strings.Builder
	for _, ch := range line {
		switch {
		case ch == '\t':
			sb.WriteString("^I")
		case ch < 0x20:
			sb.WriteByte('^')
			sb.WriteByte(ch + '@')
		case ch == 0x7f:
			sb.WriteString("^?")
		default:
			sb.WriteByte(ch)
		}
	}
	sb.WriteByte('$')
	return sb.String()
}

// pad3 right-aligns a line number in three columns, which is the width ":nu"
// and ":ls" both use.
func pad3(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 3 {
		s = " " + s
	}
	return s
}

// exEquals is ":=", which prints a line number and nothing else. With no range
// it is the last line of the file, measured: ":=" on a ten-line file says 10.
func exEquals(c *Context, cmd Cmd) error {
	n := cmd.Lines.Last
	if cmd.Lines.Given == 0 {
		n = c.buffer().LineCount()
	}
	c.say(strconv.Itoa(n))
	return nil
}

// exUndo and exRedo are ":undo" and ":redo", which are u and CTRL-R with the
// message the mode machine already prints for them.
func exUndo(c *Context, cmd Cmd) error {
	if cmd.Count > 0 {
		return c.runKeys(strconv.Itoa(cmd.Count) + "u")
	}
	return c.runKeys("u")
}

func exRedo(c *Context, cmd Cmd) error { return c.runKeys("\x12") }
