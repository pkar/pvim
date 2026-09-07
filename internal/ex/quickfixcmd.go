package ex

import (
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/quickfix"
	"github.com/pkar/pvim/internal/text"
)

// The quickfix commands: ":cc", ":cn", ":cp", ":cnf", ":copen", ":cclose" and
// ":clist".
//
// The list itself is internal/quickfix's and the 'errorformat' parser with it.
// The walking is here rather than there, over the exported Entries and Idx,
// because "which entry is next" is a decision about what ":cn" means and
// because internal/quickfix's own Next, Prev, At and Buffer are still stubs
// that answer ErrNoMore. When those are written this file loses twenty lines
// and gains nothing else.
//
// The two message formats were measured by filling a list with
// ":cexpr" and walking it:
//
//	:cc, :cn	(2 of 3): second thing
//	:clist		 2 f.txt:2: second thing
//	past the end	E553: No more items

// list returns the current quickfix list, or E42 when nothing has filled one.
func (c *Context) list() (*quickfix.List, error) {
	if c.QF == nil {
		return nil, quickfix.ErrNoList
	}
	l := c.QF.Current()
	if l == nil || len(l.Entries) == 0 {
		return nil, quickfix.ErrNoList
	}
	return l, nil
}

// exCC is ":cc": go to entry n, or to the current one when no number was
// given.
func exCC(c *Context, cmd Cmd) error {
	l, err := c.list()
	if err != nil {
		return err
	}
	idx := l.Idx
	if n := entryNumber(cmd); n > 0 {
		idx = n - 1
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(l.Entries) {
		return quickfix.ErrNoMore
	}
	return c.gotoEntry(l, idx)
}

// entryNumber reads the count off a ":cc 3" or a ":3cc", both of which vim
// accepts and both of which mean the third entry.
func entryNumber(cmd Cmd) int {
	if cmd.Count > 0 {
		return cmd.Count
	}
	if n, err := strconv.Atoi(trimArgs(cmd.Args)); err == nil {
		return n
	}
	return 0
}

// exCStep is ":cn" and ":cp". Entries with no location are skipped, which is
// what makes a compiler's "In file included from" lines invisible to ":cn" and
// visible to ":copen".
func exCStep(step int) Handler {
	return func(c *Context, cmd Cmd) error {
		l, err := c.list()
		if err != nil {
			return err
		}
		n := 1
		if cmd.Count > 0 {
			n = cmd.Count
		}
		idx := l.Idx
		for i := 0; i < n; i++ {
			next := nextValid(l, idx, step)
			if next < 0 {
				return quickfix.ErrNoMore
			}
			idx = next
		}
		return c.gotoEntry(l, idx)
	}
}

// nextValid returns the index of the next entry in the given direction that
// points at a real place, or -1 when there is none.
func nextValid(l *quickfix.List, from, step int) int {
	for i := from + step; i >= 0 && i < len(l.Entries); i += step {
		if l.Entries[i].Valid {
			return i
		}
	}
	return -1
}

// exCNextFile is ":cnf": the first entry of the next file in the list.
func exCNextFile(c *Context, cmd Cmd) error {
	l, err := c.list()
	if err != nil {
		return err
	}
	at := l.Idx
	if at < 0 {
		at = 0
	}
	name := ""
	if at < len(l.Entries) {
		name = l.Entries[at].FileName
	}
	for i := at + 1; i < len(l.Entries); i++ {
		if l.Entries[i].FileName != name && l.Entries[i].Valid {
			return c.gotoEntry(l, i)
		}
	}
	return quickfix.ErrNoMore
}

// gotoEntry opens the entry's file, puts the cursor on its line and column and
// prints the "(N of M): text" line.
func (c *Context) gotoEntry(l *quickfix.List, idx int) error {
	e := l.Entries[idx]
	l.Idx = idx
	if e.FileName != "" {
		name := Abs(e.FileName)
		if name != c.current().Name {
			if err := c.openFile(name, false); err != nil {
				return err
			}
		}
	}
	if e.LNum > 0 {
		col := e.Col - 1
		if col < 0 {
			col = 0
		}
		c.Ed.SetCursor(text.Pos{Line: e.LNum, Col: col})
		c.Sync()
	}
	c.say("(" + strconv.Itoa(idx+1) + " of " + strconv.Itoa(len(l.Entries)) + "): " + e.Text)
	return nil
}

// exCList is ":clist".
func exCList(c *Context, cmd Cmd) error {
	l, err := c.list()
	if err != nil {
		return err
	}
	for i, e := range l.Entries {
		c.say(pad(i+1, 2) + " " + entryLine(e))
	}
	return nil
}

// entryLine renders one entry the way ":clist" and ":copen" both show it:
// "file:line: message" for one with a location and "|| message" for one
// without.
func entryLine(e quickfix.Entry) string {
	if !e.Valid || e.FileName == "" {
		return "|| " + e.Text
	}
	var b strings.Builder
	b.WriteString(e.FileName)
	b.WriteByte(':')
	if e.LNum > 0 {
		b.WriteString(strconv.Itoa(e.LNum))
	}
	if e.Col > 0 {
		b.WriteString(" col ")
		b.WriteString(strconv.Itoa(e.Col))
	}
	b.WriteString(": ")
	b.WriteString(e.Text)
	return b.String()
}

// quickfixName is the name of the scratch buffer ":copen" fills. It is a
// constant so that ":cclose" can find the window again without keeping a
// pointer that a ":q" could have invalidated.
const quickfixName = "[Quickfix List]"

// exCOpen is ":copen": the list in a scratch split.
func exCOpen(c *Context, cmd Cmd) error {
	l, err := c.list()
	if err != nil {
		return err
	}
	lines := make([][]byte, 0, len(l.Entries))
	for _, e := range l.Entries {
		lines = append(lines, []byte(entryLine(e)))
	}
	if b := c.bufs().ByName(quickfixName); b != nil {
		c.bufs().Remove(b)
	}
	return c.openScratch(quickfixName, lines)
}

// exCClose is ":cclose": close the quickfix window if it is open.
func exCClose(c *Context, cmd Cmd) error {
	tab := c.tab()
	if tab == nil {
		return errNoWindow
	}
	b := c.bufs().ByName(quickfixName)
	if b == nil {
		return nil
	}
	for _, w := range tab.Windows() {
		if w.Buf == b.Text {
			if err := tab.Close(w); err != nil {
				return err
			}
			c.bufs().Remove(b)
			if tab.Cur != nil {
				c.followWindow(tab.Cur)
			}
			return nil
		}
	}
	return nil
}

// Push makes l the current quickfix list, dropping the oldest when the stack
// is full.
//
// It is here and not on quickfix.Stack because that method is a stub and this
// package is the only thing that fills a list. The rule it encodes:
// after a push, ":colder" goes to the list that was current before, so a
// ":grep" run before a ":make" is one step back and not two.
func Push(s *quickfix.Stack, l *quickfix.List) {
	if s == nil || l == nil {
		return
	}
	if s.Cur < len(s.Lists)-1 {
		s.Lists = s.Lists[:s.Cur+1]
	}
	s.Lists = append(s.Lists, l)
	if len(s.Lists) > quickfix.MaxDepth {
		s.Lists = s.Lists[len(s.Lists)-quickfix.MaxDepth:]
	}
	s.Cur = len(s.Lists) - 1
}
