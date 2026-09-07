package ex

import (
	"os"
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// The commands that print state: ":ls", ":marks", ":registers", ":jumps",
// ":changes", ":set", ":pwd" and ":version".
//
// Every format below was measured against vim 9.2.0321 and the
// column arithmetic is written out rather than eyeballed, because these are
// the commands where "close enough" is a diff on every run:
//
//	marks " %c %6d %4d %s" header "mark line col file/text"
//	jumps "%c %2d %5d %4d %s" header " jump line col file/text"
//	changes "%c %3d %5d %4d %s" header "change line col text"
//	registers " %c \"%c %s" header "Type Name Content"
//	ls "%3d%c%c%c%c%c \"%s\" line %d"
//
// The three cursor-history commands print a lone ">" after the list when the
// index sits past the end of it, which is what vim does and what says "you are
// not inside the list".

// pad right-aligns n in a field w wide, which is what every one of the formats
// above is made of.
func pad(n, w int) string {
	s := strconv.Itoa(n)
	for len(s) < w {
		s = " " + s
	}
	return s
}

// exMarks is ":marks".
//
// The order is vim's: "'" first, then a to z, then A to Z, then 0 to 9, then
// the punctuation marks, and only the ones that are set. Measured: after
// "3Gmaggyy2Gx" vim lists ', a, ", [, ] and . in that order.
func exMarks(c *Context, cmd Cmd) error {
	want := strings.TrimSpace(cmd.Args)
	c.say("mark line  col file/text")
	order := []byte{'\''}
	for ch := byte('a'); ch <= 'z'; ch++ {
		order = append(order, ch)
	}
	for ch := byte('A'); ch <= 'Z'; ch++ {
		order = append(order, ch)
	}
	for ch := byte('0'); ch <= '9'; ch++ {
		order = append(order, ch)
	}
	order = append(order, '"', '[', ']', '^', '.', '<', '>')
	for _, name := range order {
		if want != "" && !strings.ContainsRune(want, rune(name)) {
			continue
		}
		p, ok := c.markFor(name)
		if !ok {
			continue
		}
		c.say(" " + string(name) + " " + pad(p.Line, 6) + " " + pad(p.Col, 4) + " " + c.markText(p))
	}
	return nil
}

// markFor reads one mark for ":marks", which has two the address parser does
// not: "'" is where a jump would go back to, and "<" and ">" are the visual
// selection.
func (c *Context) markFor(name byte) (text.Pos, bool) {
	switch name {
	case '<', '>':
		start, end, ok := c.Ed.LastVisual()
		if !ok {
			return text.Pos{}, false
		}
		if name == '<' {
			return start, true
		}
		return end, true
	}
	return c.buffer().Mark(name)
}

// markText is the "file/text" column: the line the mark is on, with its
// leading white space kept, or nothing when the mark points past the end.
func (c *Context) markText(p text.Pos) string {
	b := c.buffer()
	if p.Line < 1 || p.Line > b.LineCount() {
		return ""
	}
	return string(b.Line(p.Line))
}

// exJumps is ":jumps".
//
// The list arrived in internal/mode; this reads it. Three details are vim's and
// were measured rather than reasoned about:
//
// - The first column is the DISTANCE from the index, not the index, so the
// entry CTRL-O would reach next prints 1 and the one under the cursor
// prints 0. Entries after the index print the same distances going back up,
// which is why the column has no sign.
// - A lone ">" goes UNDER the list when the index is past the end, and
// against the row it names otherwise.
// - The text column runs the line through skipwhite(), so leading white
// space is stripped: a line indented eight spaces prints as its first word
// with 8 still in the col column.
func exJumps(c *Context, cmd Cmd) error {
	c.say(" jump line  col file/text")
	if c.Ed == nil {
		c.say(">")
		return nil
	}
	list, idx := c.Ed.JumpList()
	for i, j := range list {
		d := idx - i
		if d < 0 {
			d = -d
		}
		mark := " "
		if i == idx {
			mark = ">"
		}
		// Measured, not guessed. vim on eight lines after 4G8G2G:
		//
		//	 jump line col file/text
		//	 3 1 0 one
		//	 2 4 0 four
		//	 1 8 0 eight
		//	>
		//
		// The mark is column 0, the distance is right-aligned in the three
		// after it, the line in the next five and the column in the next four.
		c.say(mark + pad(d, 3) + pad(j.Pos.Line, 6) + pad(j.Pos.Col, 5) + " " + c.jumpText(j))
	}
	if idx >= len(list) {
		c.say(">")
	}
	return nil
}

// jumpText is the text column of a ":jumps" row: the line itself for an entry
// in this buffer, and the file name for one that names another. Leading white
// space is stripped, which is vim's mark_line() running skipwhite().
func (c *Context) jumpText(j mode.Jump) string {
	if b := c.current(); j.File != "" && (b == nil || j.File != b.Name) {
		return j.File
	}
	return strings.TrimLeft(c.markText(j.Pos), " \t")
}

// exClearJumps is ":clearjumps".
func exClearJumps(c *Context, cmd Cmd) error {
	if c.Ed != nil {
		c.Ed.ClearJumps()
	}
	return nil
}

// exChanges is ":changes", over internal/text's changelist.
//
// The number in the first column is the distance from the current entry, which
// for a list nobody has walked is the distance from the end. Measured: after
// one change vim prints " 1 2 0 ine two" and then ">".
func exChanges(c *Context, cmd Cmd) error {
	c.say("change line  col text")
	list := c.buffer().ChangeList()
	for i, p := range list {
		c.say(" " + pad(len(list)-i, 3) + " " + pad(p.Line, 5) + " " + pad(p.Col, 4) + " " + c.markText(p))
	}
	c.say(">")
	return nil
}

// registerOrder is the order ":registers" walks, which is vim's own from
// get_register_name(): the unnamed one, the numbered ones, the named ones, the
// small delete, the two clipboards and then the read-only ones.
var registerOrder = func() []byte {
	out := []byte{'"'}
	for ch := byte('0'); ch <= '9'; ch++ {
		out = append(out, ch)
	}
	for ch := byte('a'); ch <= 'z'; ch++ {
		out = append(out, ch)
	}
	return append(out, '-', '*', '+', '.', ':', '%', '#', '/', '=')
}()

// exRegisters is ":registers" and ":display", with an optional list of names
// to restrict it to: ":reg z" shows only z.
func exRegisters(c *Context, cmd Cmd) error {
	want := strings.ReplaceAll(strings.TrimSpace(cmd.Args), " ", "")
	c.say("Type Name Content")
	r := c.regs()
	if r == nil {
		return nil
	}
	for _, name := range registerOrder {
		if want != "" && !strings.ContainsRune(want, rune(name)) {
			continue
		}
		v, err := r.Get(name)
		if err != nil || v.Empty() {
			continue
		}
		body := escapeControl(v.Bytes())
		if body == "" {
			continue
		}
		c.say("  " + regTypeChar(v) + "  \"" + string(name) + "   " + body)
	}
	return nil
}

// regTypeChar is the first column of ":registers": c, l or b, which is not the
// same spelling as getregtype()'s v, V and b that the oracle's state dump uses.
func regTypeChar(v register.Value) string {
	switch {
	case strings.HasPrefix(v.RegType(), "\x16"), strings.HasPrefix(v.RegType(), "b"):
		return "b"
	case v.RegType() == "V":
		return "l"
	default:
		return "c"
	}
}

// escapeControl renders a register's contents the way ":registers" does: a
// newline is "^J" and every other control character is "^" and the letter it
// is made from.
func escapeControl(b []byte) string {
	var out strings.Builder
	for _, ch := range b {
		if ch < 0x20 {
			out.WriteByte('^')
			out.WriteByte(ch + '@')
			continue
		}
		out.WriteByte(ch)
	}
	return out.String()
}

// exLs is ":ls" and ":buffers".
//
// The flags, in vim's order: 'buflisted' (a space, or "u" for one that is
// not), the current buffer ("%") or the alternate one ("#"), whether it is
// loaded ("a" for a window has it, "h" for hidden), 'readonly' ("=" or "-"),
// and 'modified' ("+"). Measured: " 1 %a + "/path/f.txt" line 2".
func exLs(c *Context, cmd Cmd) error {
	// The buffer being edited may never have been put in the list: nothing
	// adds it until a command needs a Buf for it, and ":ls" as the first
	// command of a session used to print the header and no rows at all.
	c.current()
	l := c.bufs()
	for _, b := range l.Bufs {
		if !b.Listed && !cmd.Bang {
			continue
		}
		listed := " "
		if !b.Listed {
			listed = "u"
		}
		which := " "
		switch b {
		case l.Cur:
			which = "%"
		case l.Alt:
			which = "#"
		}
		loaded := "a"
		if b != l.Cur {
			loaded = "h"
		}
		ro := " "
		// The buffer flag, or the 'readonly' option for the buffer the option
		// state belongs to. Both are 'readonly' and ":ls" prints "=" for
		// either: measured, ":set ro" then ":ls" gives `1 %a= "buf.txt"`. See
		// Context.readOnly for why there are two of them.
		if b.ReadOnly || (b == l.Cur && c.opts().B.ReadOnly) {
			ro = "="
		}
		mod := " "
		if b.Modified() {
			mod = "+"
		}
		line := b.Cursor.Line
		if b == l.Cur {
			line = c.cursorLine()
		}
		name := `"` + b.Display() + `"`
		row := pad3(b.Num) + listed + which + loaded + ro + mod + " " + name
		// "line N" starts in column 41, with at least one space in front of
		// it: measured against vim on a nine-character name, which leaves
		// twenty-two spaces between the closing quote and the "l".
		for len(row) < 39 {
			row += " "
		}
		c.say(row + " line " + strconv.Itoa(line))
	}
	return nil
}

// exBuffer is ":b", which takes a number, a "#" or part of a file name.
func exBuffer(c *Context, cmd Cmd) error {
	arg := trimArgs(cmd.Args)
	if arg == "" && cmd.Count > 0 {
		arg = strconv.Itoa(cmd.Count)
	}
	b, err := c.bufs().Match(arg)
	if err != nil {
		return err
	}
	return c.gotoBuffer(b, cmd.Bang)
}

// exBufferStep is ":bn", ":bp", ":bf" and ":bl".
func exBufferStep(step int, absolute int) Handler {
	return func(c *Context, cmd Cmd) error {
		l := c.bufs()
		var target *Buf
		switch {
		case absolute < 0:
			// ":blast": the last listed buffer.
			for _, b := range l.Bufs {
				if b.Listed {
					target = b
				}
			}
		case absolute > 0:
			// ":bfirst".
			for _, b := range l.Bufs {
				if b.Listed {
					target = b
					break
				}
			}
		default:
			n := step
			if cmd.Count > 0 {
				n = step * cmd.Count
			}
			target = l.Step(l.Cur, n)
		}
		if target == nil {
			return ErrNoSuchBuffer
		}
		return c.gotoBuffer(target, cmd.Bang)
	}
}

// gotoBuffer switches to a buffer, refusing to abandon a modified one without
// a bang or 'hidden'.
func (c *Context) gotoBuffer(b *Buf, force bool) error {
	if b == c.bufs().Cur {
		return nil
	}
	if cur := c.current(); cur.Modified() && !force && !c.opts().G.Hidden {
		ok, err := c.confirmSave(cur)
		if err != nil {
			return err
		}
		if !ok {
			return errSilent
		}
	}
	c.swapBuffer(b)
	return nil
}

// exBdelete is ":bd": take a buffer out of the list. A modified one is E89
// without a bang.
func exBdelete(c *Context, cmd Cmd) error {
	l := c.bufs()
	target := l.Cur
	if arg := trimArgs(cmd.Args); arg != "" {
		b, err := c.bufs().Match(arg)
		if err != nil {
			return err
		}
		target = b
	}
	if target == nil {
		return ErrNoSuchBuffer
	}
	if target.Modified() && !cmd.Bang {
		return withName(ErrBufferModified, strconv.Itoa(target.Num))
	}
	next := l.Step(target, 1)
	l.Remove(target)
	if l.Cur == target {
		if next == nil || next == target {
			next = c.bufs().Add("", text.New())
		}
		l.Cur = nil
		c.swapBuffer(next)
	}
	return nil
}

// exSet is ":set", ":setlocal" and ":setglobal".
//
// The whole of vim's syntax -- "?", "!", "&", "+=", "-=", "^=" and E518 for a
// name that is not an option -- is internal/options's, which is where it can
// be table-tested with no editor. This is the four lines that hand it the
// argument and put what it printed on the message line, and the one that
// matters: the mode machine is told about the change, because an option the
// ex layer set that internal/mode never heard about is an option that parses
// and does nothing.
func exSet(where options.Where) Handler {
	return func(c *Context, cmd Cmd) error {
		out, err := c.opts().ApplyLine(cmd.Args, where)
		for _, line := range out {
			c.say(line)
		}
		c.pushOptions()
		return err
	}
}

// pushOptions copies the option state into the mode machine and the window.
func (c *Context) pushOptions() {
	if c.Ed != nil {
		c.Ed.SetOptions(ModeOptions(c.opts(), c.Window()))
	}
	ApplyWindowOptions(c.opts(), c.Window())
	c.Sync()
}

// exPwd is ":pwd".
func exPwd(c *Context, cmd Cmd) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	c.say(wd)
	return nil
}

// exCd is ":cd" and ":lcd", both of which change the process's directory here
// because there is one of it. ":lcd"'s window-local directory would need a
// field on window.Window, which is that package's to add.
func exCd(c *Context, cmd Cmd) error {
	arg := trimArgs(cmd.Args)
	if arg == "" || arg == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		arg = home
	}
	name, err := c.expandName(arg)
	if err != nil {
		return err
	}
	if err := os.Chdir(Abs(name)); err != nil {
		return withName(errCannotChdir, arg)
	}
	return exPwd(c, cmd)
}

// errCannotChdir is E344, which is what vim says for a ":cd" to somewhere that
// is not there.
var errCannotChdir = errStr("E344: Can't find directory in 'cdpath'")

// errStr is an error that is only its message, for the codes that appear once.
type errStr string

func (e errStr) Error() string { return string(e) }

// exNohlsearch is ":noh".
//
// The highlight itself lives in internal/screen, which decides what a match
// looks like, and the flag that says "stop drawing it until the next search"
// has nowhere to live in this package yet. So this is a no-op rather than an
// error: the command is real, the state it toggles is one layer down, and
// answering E319 for it would be worse than doing nothing quietly.
func exNohlsearch(c *Context, cmd Cmd) error { return nil }

// exVersion is ":version".
func exVersion(c *Context, cmd Cmd) error {
	c.say("pvim, an editor behind vim's keys")
	return nil
}
