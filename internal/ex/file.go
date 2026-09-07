package ex

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/text"
)

// The file commands: ":w", ":wq", ":x", ":wa", ":q", ":qa", ":e", ":enew",
// ":find", ":r" and ":file".
//
// Every message here was measured against vim 9.2.0321:
//
//	:w		"NAME" 10L, 99B written
//	:w newfile	"NAME" [New] 10L, 99B written
//	:e file		"NAME" 10L, 99B
//	:e newfile	"NAME" [New]
//	:q modified	E37: No write since last change (add ! to override)
//	:q, 'confirm'	Save changes to "NAME"?
//			[Y]es, (N)o, (C)ancel:
//	:w, 'readonly'	E45: 'readonly' option is set (add ! to override)
//
// vim writes a progress line -- the quoted name and a space, nothing else --
// before the result of a write or a read. The write's result then goes out
// TWICE, because it is one of the messages the redraw after the command puts
// back, and that is Context.sayKeep; the read's goes out once with a carriage
// return between it and the progress line, because readfile() rewrites the
// line it already started rather than opening a new one. Both measured through
// cmd/oracle, which is the only place the difference is visible.
//
// What is not here: vim's 'confirm' turns the E45 into a dialog --
// "'readonly' option is set. Do you wish to write anyway?" -- and this
// answers the E45 whatever 'confirm' says. The dialog cannot be graded
// headlessly, because a -s script that runs out of keys in front of it leaves
// vim waiting for a person, which is why the two E45 cases in testdata/keys
// carry "noconfirm" in their .opts.

// exWrite is ":w" and ":w!".
//
// ":w file" writes somewhere else and leaves the buffer's own name alone,
// which is why the name is resolved into a local and the Buf is only marked
// saved when the two are the same.
func exWrite(c *Context, cmd Cmd) error {
	arg := trimArgs(cmd.Args)
	if strings.HasPrefix(arg, "!") {
		return c.writeToCommand(cmd, strings.TrimSpace(arg[1:]))
	}
	buf := c.current()
	name := buf.Name
	if arg != "" {
		n, err := c.expandName(arg)
		if err != nil {
			return err
		}
		name = Abs(n)
	}
	if name == "" {
		return ErrNoFileName
	}
	if buf.Scratch && arg == "" {
		return ErrNoFileName
	}
	// 'readonly' is a lock and not a label. vim's check_readonly() refuses a
	// write to the buffer's OWN file and says so, and lets ":w somewhere-else"
	// through: the option protects the file this buffer came from, not the
	// bytes in it. The bang is the override, which is what the parenthetical
	// in the message is offering. Measured, all three.
	if c.readOnly(buf) && !cmd.Bang && name == buf.Name {
		return ErrReadOnly
	}

	first, last := cmd.Lines.First, cmd.Lines.Last
	partial := cmd.Lines.Given > 0 && !(first <= 1 && last >= c.buffer().LineCount())

	_, statErr := os.Stat(name)
	isNew := errors.Is(statErr, os.ErrNotExist)

	// Before the bytes are taken. See Context.PreWrite.
	if c.PreWrite != nil {
		if err := c.PreWrite(name); err != nil {
			return err
		}
	}

	data := c.fileBytes(first, last, partial)
	short := ShortName(name)
	c.say(`"` + short + `" `)
	if err := os.WriteFile(name, data, 0o644); err != nil {
		// E212 and not E484: vim's buf_write answers "Can't open file for
		// writing" for a target it cannot create, with the file name quoted in
		// front of it. Measured with ":w nodir/x.txt", which prints the
		// progress line and then `"nodir/x.txt" E212: Can't open file for
		// writing`. E212 is the code a habit or a script keys off.
		return withQuotedName(ErrCannotWrite, short)
	}
	if !partial && (arg == "" || name == buf.Name) {
		buf.MarkSaved()
		buf.NewFile = false
		if buf.Name == "" {
			buf.Name = name
			c.setRegisterNames()
		}
	}
	if c.PostWrite != nil {
		c.PostWrite(name)
	}
	tag := ""
	if isNew {
		tag = "[New] "
	}
	lines := last - first + 1
	if !partial {
		lines = c.buffer().LineCount()
	}
	// sayKeep and not say: vim marks the write report worth keeping across the
	// redraw that follows the command, so ":redir" catches it twice. Measured,
	// `:w other.txt` leaves `"other.txt" [New] 3L, 20B written` in msgs.txt on
	// two lines. The read report ":e" prints is NOT kept and appears once,
	// which is why openFile still uses say.
	c.sayKeep(`"` + short + `" ` + tag + strconv.Itoa(lines) + "L, " + strconv.Itoa(len(data)) + "B written")
	return nil
}

// fileBytes is the buffer as it goes to disk.
//
// 'fixendofline' is on by default and means what it says: a file whose last
// line arrived with no separator is written back with one. Only 'nofixeol'
// keeps it as it was. The same rule is in cmd/pvim's headless path, and both
// have to agree or ":w" from the editor and ":w" from --oracle write different
// bytes for the same buffer.
func (c *Context) fileBytes(first, last int, partial bool) []byte {
	b := c.buffer()
	if !partial {
		if c.opts().B.FixEndOfLine {
			b.SetNoEOL(false)
		}
		return b.Bytes()
	}
	var out []byte
	for n := first; n <= last && n <= b.LineCount(); n++ {
		out = append(out, b.Line(n)...)
		out = append(out, '\n')
	}
	return out
}

// exWriteQuit is ":wq" and ":x". They differ in one thing and it is not
// cosmetic: ":x" writes only when the buffer has been changed, so ":x" on an
// unmodified file leaves its mtime alone and ":wq" does not.
func exWriteQuit(always bool) Handler {
	return func(c *Context, cmd Cmd) error {
		if always || c.current().Modified() {
			if err := exWrite(c, cmd); err != nil {
				return err
			}
		}
		return c.quit(false, true)
	}
}

// exWriteAll is ":wa" and ":wqall"/":xall".
func exWriteAll(quit bool) Handler {
	return func(c *Context, cmd Cmd) error {
		for _, b := range c.bufs().Bufs {
			if b.Scratch || !b.Modified() {
				continue
			}
			if b.Name == "" {
				if !cmd.Bang {
					return ErrNoFileName
				}
				continue
			}
			data := b.Text.Bytes()
			if err := os.WriteFile(b.Name, data, 0o644); err != nil {
				return withQuotedName(ErrCannotWrite, b.Display())
			}
			b.MarkSaved()
		}
		if quit {
			return c.quit(true, true)
		}
		return nil
	}
}

// exQuit is ":q" and ":q!".
//
// With more than one window open ":q" closes the window; with one it stops the
// editor. That is what makes window.ErrLastWindow a decision and not a
// failure: this is the one place that turns it into a quit.
func exQuit(all bool) Handler {
	return func(c *Context, cmd Cmd) error {
		if !all {
			if t := c.tab(); t != nil && len(t.Windows()) > 1 {
				return exCloseWindow(c, cmd)
			}
			if c.Tabs != nil && len(c.Tabs.Pages) > 1 {
				return exTabClose(c, cmd)
			}
		}
		return c.quit(all, cmd.Bang)
	}
}

// quit stops the editor, after the 'confirm' prompt or E37 for every buffer
// that would lose changes.
func (c *Context) quit(all, force bool) error {
	if !force {
		for _, b := range c.dirtyBuffers(all) {
			ok, err := c.confirmSave(b)
			if err != nil {
				return err
			}
			if !ok {
				return errSilent
			}
		}
	}
	if c.Quit != nil {
		return c.Quit(all, force)
	}
	return ErrQuit
}

// dirtyBuffers is what a quit would throw away: the current buffer for ":q"
// and every listed one for ":qa".
func (c *Context) dirtyBuffers(all bool) []*Buf {
	var out []*Buf
	if !all {
		if b := c.current(); b.Modified() && !b.Scratch {
			out = append(out, b)
		}
		return out
	}
	for _, b := range c.bufs().Bufs {
		if b.Modified() && !b.Scratch {
			out = append(out, b)
		}
	}
	return out
}

// confirmSave is 'confirm': the cmdline prompt vim shows instead of refusing.
//
// Measured with ":set confirm" and a modified buffer:
//
//	Save changes to "NAME"?
//	[Y]es, (N)o, (C)ancel:
//
// A "Y" writes and carries on, an "N" throws the changes away, and anything
// else cancels. With 'confirm' off there is no prompt and the answer is E37,
// which is the message this returns instead.
func (c *Context) confirmSave(b *Buf) (bool, error) {
	if !c.opts().G.Confirm || c.Prompt == nil {
		return false, ErrNoWriteSince2
	}
	answer, err := c.Prompt(`Save changes to "`+b.Display()+`"?`+"\n"+"[Y]es, (N)o, (C)ancel: ", "ync")
	if err != nil {
		return false, err
	}
	switch answer {
	case 'y', 'Y':
		if b.Name == "" {
			return false, ErrNoFileName
		}
		if err := os.WriteFile(b.Name, b.Text.Bytes(), 0o644); err != nil {
			return false, withQuotedName(ErrCannotWrite, b.Display())
		}
		b.MarkSaved()
		return true, nil
	case 'n', 'N':
		return true, nil
	default:
		return false, nil
	}
}

// ErrNoWriteSince2 is E37 with the parenthetical vim actually prints. ex.go
// declares the short form for callers that only want the code; this is the one
// the message line gets.
var ErrNoWriteSince2 = errors.New("E37: No write since last change (add ! to override)")

// exEdit is ":e", ":e!" and ":e file".
//
// ":e" with no argument rereads the file, which is what the bang is for: an
// unmodified buffer rereads silently and a modified one is E37 without it.
func exEdit(c *Context, cmd Cmd) error {
	arg := trimArgs(cmd.Args)
	if arg == "" {
		buf := c.current()
		if buf.Modified() && !cmd.Bang {
			return ErrNoWriteSince2
		}
		if buf.Name == "" {
			return ErrNoFileName
		}
		return c.openFile(buf.Name, true)
	}
	if c.current().Modified() && !cmd.Bang && !c.opts().G.Hidden {
		if ok, err := c.confirmSave(c.current()); err != nil || !ok {
			if err != nil {
				return err
			}
			return errSilent
		}
	}
	name, err := c.expandName(arg)
	if err != nil {
		return err
	}
	return c.openFile(Abs(name), false)
}

// exEnew is ":enew": a buffer with no name and no file behind it.
func exEnew(c *Context, cmd Cmd) error {
	if c.current().Modified() && !cmd.Bang && !c.opts().G.Hidden {
		if ok, err := c.confirmSave(c.current()); err != nil || !ok {
			if err != nil {
				return err
			}
			return errSilent
		}
	}
	nb := c.bufs().Add("", text.New())
	c.swapBuffer(nb)
	return nil
}

// exFind is ":find": open a file by name, looked for the way vim's 'path'
// says.
//
// 'path' is not in internal/options, so this searches vim's own default and
// only that: the directory of the current file, then the working directory,
// neither recursively. A ":find" that wants more says E345 rather than
// guessing at a search this editor was not configured for.
func exFind(c *Context, cmd Cmd) error {
	arg := trimArgs(cmd.Args)
	if arg == "" {
		return ErrArgumentRequired
	}
	var dirs []string
	if name := c.current().Name; name != "" {
		dirs = append(dirs, filepath.Dir(name))
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd)
	}
	for _, d := range dirs {
		p := filepath.Join(d, arg)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return c.openFile(p, false)
		}
	}
	return withName(errFindNotFound, arg)
}

// errFindNotFound is E345, which is what vim says when ":find" walks the whole
// of 'path' and finds nothing.
var errFindNotFound = errors.New("E345: Can't find file in path")

// openFile reads a file into a buffer and makes it current.
//
// reread is ":e" with no argument: the same Buf is refilled rather than a new
// one being made, so the buffer number and the alternate file do not move.
func (c *Context) openFile(name string, reread bool) error {
	data, err := os.ReadFile(name)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return withSpace(ErrCannotOpen, ShortName(name))
	}

	nb := c.bufs().ByName(name)
	if nb == nil || reread {
		fresh := text.Read(data)
		if nb == nil {
			nb = c.bufs().Add(name, fresh)
		} else {
			nb.Text = fresh
			nb.MarkSaved()
			nb.Cursor = text.Pos{Line: 1}
		}
		nb.NewFile = missing
	}
	c.swapBuffer(nb)

	short := ShortName(name)
	if missing {
		c.say(`"` + short + `" [New]`)
		return nil
	}
	c.say(`"` + short + `" `)
	// The carriage return between the progress line and the report. vim's
	// readfile() prints the name, reads the file and then rewrites the whole
	// line over the top of it, and the redirect catches the CR that does the
	// rewriting: `:e` of an existing file leaves `"f" \r\n"f" 3L, 20B` in
	// msgs.txt and not the two lines without it. ":w" does not do this and
	// has no CR; both measured.
	c.sayRaw("\r")
	c.say(`"` + short + `" ` + strconv.Itoa(nb.Text.LineCount()) + "L, " + strconv.Itoa(len(data)) + "B")
	return nil
}

// swapBuffer puts nb in the window that is already current, which is what
// ":e", ":b" and ":enew" do.
//
// The three things that go with it are all consequences of the window keeping
// its identity while its contents change: the buffer being left becomes this
// window's alternate file, the window is pointed at the new text and scrolled
// back to the top, and the editor takes up the cursor the new buffer was last
// left at.
//
// It is emphatically not what a command that changes WHICH window is current
// does. A window switch finds a window that has been sitting on its own buffer
// with its own cursor and its own scroll position, and writing the outgoing
// window's cursor into it, or scrolling it to the top, is wrong on both
// counts: ":sp" then "5G" then ":q" left the surviving window on line 5 where
// vim leaves it on the line it was on. That path is followWindow, and the two
// were one function until this comment was written.
//
// The guard on the view is what makes ":sp" and ":new" safe to route through
// here: those set tab.Cur to a window that already shows the target text, and
// resetting a top line that internal/window has just worked out would undo it.
func (c *Context) swapBuffer(nb *Buf) {
	l := c.bufs()
	if cur := l.Cur; cur != nil && cur != nb {
		if c.Ed != nil {
			cur.Cursor = c.Ed.Cursor()
		}
		l.Alt = cur
		// vim's w_alt_fnum, which belongs to the window and not to the
		// editor: ":e a" in one window does not change what "#" means in
		// another, and a window that is closed takes its alternate with it.
		// Measured, and it is why ":new" then ":q" then CTRL-W ^ says
		// "E23: No alternate file" rather than splitting the [No Name]
		// buffer the ":new" made.
		if w := c.Window(); w != nil {
			w.Alt = cur.Text
		}
	}
	l.Cur = nb
	c.adoptBuffer(nb)
	if w := c.Window(); w != nil && w.Buf != nb.Text {
		w.Buf = nb.Text
		w.View.TopLine = 1
	}
	c.setRegisterNames()
	c.Sync()
}

// adoptBuffer makes nb the buffer the mode machine acts on.
//
// The fallback when Context.Open is nil builds a fresh mode.Editor around the
// new buffer and copies the options across. That loses the registers, the
// search state and the last insert, because internal/mode has no way to be
// handed a second buffer: an Editor is built around one and there is no
// setter. It is the honest lossy thing rather than a ":e" that does nothing,
// and the day internal/mode grows a SetBuffer this function is four lines
// shorter.
func (c *Context) adoptBuffer(nb *Buf) {
	if c.Open != nil {
		c.Open(nb)
		return
	}
	if c.Ed == nil || c.Ed.Buffer() == nb.Text {
		// Already on it. ":sp" comes through here with the buffer it started
		// with, and rebuilding the mode machine to arrive back where it began
		// would throw away the cursor, the registers and the message line for
		// nothing. A frontend's Open hook makes the same test for itself.
		return
	}
	opt := c.Ed.Options()
	ne := mode.New(nb.Text)
	ne.SetOptions(opt)
	ne.SetCursor(nb.Cursor)
	c.Ed = ne
}

// bufFor is the buffer list's entry for a text.Buffer, or nil for one the list
// has never been told about, which is what a window on a scratch buffer has.
func (c *Context) bufFor(b *text.Buffer) *Buf {
	if b == nil {
		return nil
	}
	for _, x := range c.bufs().Bufs {
		if x.Text == b {
			return x
		}
	}
	return nil
}

// readOnly reports whether writing this buffer's own file is refused.
//
// Two flags and not one, because 'readonly' has two homes here and both are
// real. options.Options carries the buffer-local option ":set ro" writes and
// ":set ro?" reads; Buf.ReadOnly is the per-buffer flag ":help" sets and the
// crash-recovery prompt's [O]pen Read-Only sets, and it survives a swap to
// another buffer and back where the option state does not.
//
// The option half is global in practice, because there is one options.Buffer
// for the editor rather than one per buffer: ":set ro" then ":e other" then
// ":w" refuses here and writes in vim. Per-buffer option storage is
// internal/options's to grow and this is the honest reading of what exists.
func (c *Context) readOnly(b *Buf) bool {
	if b != nil && b.ReadOnly {
		return true
	}
	return c.opts().B.ReadOnly
}

// setRegisterNames keeps "% and "# in step with the buffer list, which is what
// makes "%" and "#" expand in a ":w" argument and what ":registers" shows.
func (c *Context) setRegisterNames() {
	if r := c.regs(); r != nil {
		alt := ""
		if a := c.bufs().Alt; a != nil {
			alt = a.Name
		}
		r.SetFilename(c.current().Name, alt)
	}
}

// exRead is ":r file" and ":r !command".
//
// Line 0 is meaningful here -- ":0r file" puts the file above the first line --
// which is what the table's Zero flag on ":read" is for.
func exRead(c *Context, cmd Cmd) error {
	arg := trimArgs(cmd.Args)
	if cmd.Bang {
		// ":r!cmd" reads the output of a command, and the "!" is part of the
		// syntax rather than the force bang every other command's is. The
		// parser has already taken it off the name, so it arrives here as
		// Bang and not as the first character of the argument -- which is
		// what made ":r!/bin/echo hi" look for a file called "/bin/echo hi".
		return c.readCommand(cmd, arg)
	}
	if strings.HasPrefix(arg, "!") {
		return c.readCommand(cmd, strings.TrimSpace(arg[1:]))
	}
	name := c.current().Name
	if arg != "" {
		n, err := c.expandName(arg)
		if err != nil {
			return err
		}
		name = Abs(n)
	}
	if name == "" {
		return ErrNoFileName
	}
	// vim's ex_read calls u_save(line2, line2 + 1) BEFORE it opens the file, so
	// a ":r" of a file that is not there still numbers an undo header:
	// undotree().seq_last is 1 afterwards where the same buffer with no ":r" in
	// it reads 0. Measured.
	c.editNoLines(text.Pos{Line: max(cmd.Lines.Last, 1)})
	data, err := os.ReadFile(name)
	if err != nil {
		// The name as typed and joined with a space, which is vim's
		// e_notopen ("E484: Can't open file %s") over eap->arg:
		// ":r nosuch.txt" says `E484: Can't open file nosuch.txt`.
		return withSpace(ErrCannotOpen, ShortName(name))
	}
	lines := splitLines(data)
	at := cmd.Lines.Last
	c.edit(text.Pos{Line: max(at, 1)}, func(b *text.Buffer) { b.InsertLines(at+1, lines) })
	c.moveTo(at + 1)
	if c.reportOver(len(lines)) {
		c.sayKeep(strconv.Itoa(len(lines)) + " more " + plural(len(lines), "line", "lines"))
	}
	return nil
}

// exFile is ":f", which prints the current file name and position, or sets it
// when given an argument.
func exFile(c *Context, cmd Cmd) error {
	arg := trimArgs(cmd.Args)
	buf := c.current()
	if arg != "" {
		n, err := c.expandName(arg)
		if err != nil {
			return err
		}
		buf.Name = Abs(n)
		c.setRegisterNames()
		return nil
	}
	mod := ""
	if buf.Modified() {
		mod = " [Modified]"
	}
	n := c.buffer().LineCount()
	c.say(`"` + buf.Display() + `"` + mod + " " + strconv.Itoa(n) + " " + plural(n, "line", "lines") +
		" --" + strconv.Itoa(percent(c.cursorLine(), n)) + "%--")
	return nil
}

// percent is the position indicator vim puts on the ruler and in ":f".
func percent(line, total int) int {
	if total < 1 {
		return 0
	}
	return line * 100 / total
}

// expandName substitutes "%" and "#" in a file-name argument, which are the
// two every ":w %" and ":e #" needs. A backslash protects either.
//
// A "#" with no alternate file is E194 and not an empty string. That is vim,
// and the empty string was doing real damage: ":e#" with nothing to go back to
// opened a nameless buffer and reported `"" [New]`, which looks like a file
// that was created and is not one.
func (c *Context) expandName(arg string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(arg); i++ {
		switch arg[i] {
		case '\\':
			if i+1 < len(arg) {
				out.WriteByte(arg[i+1])
				i++
				continue
			}
			out.WriteByte('\\')
		case '%':
			out.WriteString(c.current().Name)
		case '#':
			a := c.bufs().Alt
			if a == nil {
				return "", ErrNoAltName
			}
			out.WriteString(a.Name)
		default:
			out.WriteByte(arg[i])
		}
	}
	return out.String(), nil
}

// readIfExists reads a file, answering nil bytes and no error when it is not
// there. ":sp newfile" and ":e newfile" both open an empty buffer for a name
// that does not exist yet, which is not the same as failing to read one that
// does.
func readIfExists(name string) ([]byte, error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// splitLines turns a file's bytes into buffer lines, dropping the separator
// after the last one so that a file ending in a newline does not read as
// having a trailing empty line.
func splitLines(data []byte) [][]byte {
	if len(data) == 0 {
		return [][]byte{{}}
	}
	if data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}
	parts := strings.Split(string(data), "\n")
	out := make([][]byte, len(parts))
	for i, p := range parts {
		out[i] = []byte(strings.TrimSuffix(p, "\r"))
	}
	return out
}
