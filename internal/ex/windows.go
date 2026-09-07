package ex

import (
	"errors"
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// The window and tab commands: ":sp", ":vs", ":new", ":vnew", ":only",
// ":close", ":tabnew", ":tabclose", ":tabonly", ":tabnext", ":tabprevious"
// and ":tabs".
//
// The layout arithmetic is internal/window's and is delegated to, not
// duplicated: this file decides which window is being split, which buffer the
// new one shows and which way 'splitbelow' and 'splitright' point, and
// TabPage.Split does the tree insert and the rectangles. That split is why
// ":sp file" and ":new" are four lines each here.

// errNoWindow is a window command run against a Context that has no tab pages,
// which is what a unit test and the vimrc loader both build. It is not an
// E-code because vim cannot get into this state.
var errNoWindow = errors.New("no window")

// nextWinID returns an id no live window has. Ids are stable for the life of a
// window and are what vim's winid is, so they are never reused within a
// session.
func nextWinID(c *Context) int {
	n := 0
	if c.Tabs != nil {
		for _, t := range c.Tabs.Pages {
			for _, w := range t.Windows() {
				if w.ID > n {
					n = w.ID
				}
			}
		}
	}
	return n + 1
}

// newWindow makes a window on a buffer, sized from the current one so that a
// split that has not been laid out yet still has a height to scroll against.
//
// A split showing the same buffer inherits the whole view and not just the
// size. vim shows the same lines in both halves of a ":sp", so the new window
// gets the old one's top line, cursor and 'curswant'; without that a split
// halfway down a long file put the new window back at line 1 and H, M and L
// answered against lines that were not on the screen. A split showing a
// different buffer -- ":new", ":sp other" -- starts at the top of it, which is
// what window.New already gives.
//
// The alternate file comes across either way, because it belongs to the window
// and vim's win_split copies it: ":e a" then ":sp" then CTRL-W ^ opens the
// original file in vim, so the new window knows what "#" means.
func newWindow(c *Context, b *Buf) *window.Window {
	w := window.New(nextWinID(c), b.Text, c.opts().GW)
	w.View.Cursor = b.Cursor
	if cur := c.Window(); cur != nil {
		if cur.Buf == b.Text {
			w.View = cur.View
		}
		w.View.Height = cur.View.Height
		w.View.Width = cur.View.Width
		w.Opt = cur.Opt
		w.Alt = cur.Alt
		w.Prev = cur.Prev
	}
	w.SetHeight(w.View.Height)
	return w
}

// splitDir turns ":vertical" into a window.Dir.
func splitDir(vertical bool) window.Dir {
	if vertical {
		return window.Vertical
	}
	return window.Horizontal
}

// splitBefore says whether the new window goes above or to the left.
//
// vim's default is above for ":sp" and left for ":vs", and 'splitbelow' and
// 'splitright' invert them. The modifiers beat the options, which is why
// ":belowright sp" puts it below whatever 'splitbelow' says. internal/window
// takes the answer rather than the options on purpose: it should not have to
// know which of two global options applies to which direction.
func splitBefore(c *Context, vertical bool) bool {
	o := c.opts()
	before := true
	if vertical {
		before = !o.G.SplitRight
	} else {
		before = !o.G.SplitBelow
	}
	return before
}

// exSplit is ":sp", ":vs", ":new" and ":vnew".
//
// fresh says the new window gets an empty buffer rather than this one, which
// is the only difference between ":sp" and ":new".
func exSplit(vertical, fresh bool) Handler {
	return func(c *Context, cmd Cmd) error {
		tab := c.tab()
		cur := c.Window()
		if tab == nil || cur == nil {
			return errNoWindow
		}
		vert := vertical || cmd.Mods.Vertical

		target := c.current()
		switch {
		case trimArgs(cmd.Args) != "":
			name, err := c.expandName(trimArgs(cmd.Args))
			if err != nil {
				return err
			}
			b, err := c.loadBuffer(Abs(name))
			if err != nil {
				return err
			}
			target = b
		case fresh:
			target = c.bufs().Add("", text.New())
		}

		c.leaveWindow()
		w := newWindow(c, target)
		before := splitBefore(c, vert)
		if cmd.Mods.Aboveleft || cmd.Mods.Topleft {
			before = true
		}
		if cmd.Mods.Belowright || cmd.Mods.Botright {
			before = false
		}
		if err := tab.Split(cur, w, splitDir(vert), before, cmd.Count); err != nil {
			return err
		}
		tab.Cur = w
		c.swapBuffer(target)
		return nil
	}
}

// loadBuffer returns the buffer for a file name, reading it if this is the
// first time the name has been seen.
func (c *Context) loadBuffer(name string) (*Buf, error) {
	if b := c.bufs().ByName(name); b != nil {
		return b, nil
	}
	data, err := readIfExists(name)
	if err != nil {
		return nil, withSpace(ErrCannotOpen, ShortName(name))
	}
	b := c.bufs().Add(name, text.Read(data))
	b.NewFile = data == nil
	return b, nil
}

// exCloseWindow is ":close" and CTRL-W c: close this window, leaving the
// buffer alone.
func exCloseWindow(c *Context, cmd Cmd) error {
	tab := c.tab()
	w := c.Window()
	if tab == nil || w == nil {
		return errNoWindow
	}
	if err := tab.Close(w); err != nil {
		return err
	}
	if tab.Cur != nil {
		c.followWindow(tab.Cur)
	}
	return nil
}

// exOnly is ":only" and CTRL-W o.
func exOnly(c *Context, cmd Cmd) error {
	tab := c.tab()
	w := c.Window()
	if tab == nil || w == nil {
		return errNoWindow
	}
	return tab.Only(w)
}

// exTabNew is ":tabnew" and ":tabedit".
func exTabNew(c *Context, cmd Cmd) error {
	if c.Tabs == nil {
		return errNoWindow
	}
	target := c.current()
	if arg := trimArgs(cmd.Args); arg != "" {
		name, err := c.expandName(arg)
		if err != nil {
			return err
		}
		b, err := c.loadBuffer(Abs(name))
		if err != nil {
			return err
		}
		target = b
	} else {
		target = c.bufs().Add("", text.New())
	}
	c.leaveWindow()
	w := newWindow(c, target)
	// After the current tab, which is what ":tabnew" with no count does. A
	// count is where it goes instead, and ":$tabnew" appends; both are the
	// caller's arithmetic on Cur, which is why New takes the index.
	at := c.Tabs.Cur
	if cmd.Count > 0 {
		at = cmd.Count - 1
	}
	c.Tabs.New(window.NewTabPage(w), at)
	c.swapBuffer(target)
	return nil
}

// exTabClose is ":tabclose". The last tab is not closed; the caller turns
// window.ErrLastWindow into a quit.
func exTabClose(c *Context, cmd Cmd) error {
	if c.Tabs == nil {
		return errNoWindow
	}
	if err := c.Tabs.Close(c.Tabs.Cur); err != nil {
		return err
	}
	c.followTab()
	return nil
}

// exTabOnly is ":tabonly": close every tab but this one.
func exTabOnly(c *Context, cmd Cmd) error {
	if c.Tabs == nil {
		return errNoWindow
	}
	keep := c.tab()
	if keep == nil {
		return errNoWindow
	}
	c.Tabs.Pages = []*window.TabPage{keep}
	c.Tabs.Cur = 0
	return nil
}

// exTabStep is ":tabnext" and ":tabprevious", which are gt and gT with a
// count. The two disagree about what a count means and that is vim, not a
// mistake: a count on gt is an absolute tab number and a count on gT is a
// repeat.
func exTabStep(forward bool) Handler {
	return func(c *Context, cmd Cmd) error {
		if c.Tabs == nil {
			return errNoWindow
		}
		c.leaveWindow()
		if forward {
			c.Tabs.Next(cmd.Count)
		} else {
			c.Tabs.Prev(cmd.Count)
		}
		c.followTab()
		return nil
	}
}

// exTabs is ":tabs": the tab pages and the windows in each.
//
// Format measured from vim's own ex_tabs(): a "Tab page N" header, then one
// line per window indented by four, with "> " in front of the current one.
func exTabs(c *Context, cmd Cmd) error {
	if c.Tabs == nil {
		return errNoWindow
	}
	for i, t := range c.Tabs.Pages {
		c.say("Tab page " + strconv.Itoa(i+1))
		for _, w := range t.Windows() {
			marker := "    "
			if w == t.Cur {
				marker = "> "
			}
			c.say(marker + c.windowName(w))
		}
	}
	return nil
}

// windowName is what ":tabs" and the tabline call a window: the file name of
// the buffer in it, or "[No Name]".
func (c *Context) windowName(w *window.Window) string {
	for _, b := range c.bufs().Bufs {
		if b.Text == w.Buf {
			return b.Display()
		}
	}
	return "[No Name]"
}

// followWindow points the editor at what w is showing, leaving w alone.
//
// This is the half of a window switch that swapBuffer must not do. The window
// has its own cursor and its own scroll position and both are older than this
// command; what moves is the editor, which takes up w's cursor and w's
// alternate file. Measured on eight lines: "3G :sp 5G :q" leaves vim on line 3,
// and leaves this one on 3 only because the editor's 5 is dropped here rather
// than written into the surviving window.
//
// A window on a buffer the list has never heard of -- a scratch one would be --
// moves the cursor and nothing else, because there is nothing to make current.
func (c *Context) followWindow(w *window.Window) {
	if w == nil {
		return
	}
	l := c.bufs()
	if nb := c.bufFor(w.Buf); nb != nil && nb != l.Cur {
		if cur := l.Cur; cur != nil && c.Ed != nil {
			cur.Cursor = c.Ed.Cursor()
		}
		l.Cur = nb
		c.adoptBuffer(nb)
	}
	// vim's w_alt_fnum: "#" means whatever it means in the window that is now
	// current, which is not what it meant in the one that was.
	l.Alt = c.bufFor(w.Alt)
	c.setRegisterNames()
	if c.Ed != nil {
		c.Ed.SetCursor(w.View.Cursor)
	}
	c.Sync()
}

// enterWindow makes w the current window of the current tab: the window being
// left keeps the cursor the editor was on, and the editor takes up w's.
func (c *Context) enterWindow(w *window.Window) {
	if w == nil {
		return
	}
	c.leaveWindow()
	if t := c.tab(); t != nil {
		t.Goto(w)
	}
	c.followWindow(w)
}

// leaveWindow writes the editor's cursor back into the window it belongs to,
// which is the current one, before something else becomes current.
//
// The buffer test is not belt and braces. internal/ex's own commands keep the
// two in step, but a frontend that swapped the mode machine and has not
// synced yet would otherwise stamp a position from one buffer onto a window
// showing another, and a line number out of the wrong file is exactly the kind
// of wrong that looks right.
func (c *Context) leaveWindow() {
	w := c.Window()
	if w == nil || c.Ed == nil || w.Buf != c.Ed.Buffer() {
		return
	}
	w.View.Cursor = c.Ed.Cursor()
}

// followTab points the editor at whatever the newly current tab is showing.
func (c *Context) followTab() {
	if t := c.tab(); t != nil {
		c.followWindow(t.Cur)
	}
}

// exWincmd is ":wincmd x", which is CTRL-W x. It is here rather than in the
// mode machine for the same reason the scrolling keys are in cmd/pvim: the
// mode machine has no window to move between.
func exWincmd(c *Context, cmd Cmd) error {
	arg := strings.TrimSpace(cmd.Args)
	if arg == "" {
		return ErrArgumentRequired
	}
	return c.wincmd(arg[0], cmd.Count)
}

// wincmd runs one CTRL-W command.
func (c *Context) wincmd(k byte, count int) error {
	tab := c.tab()
	w := c.Window()
	if tab == nil || w == nil {
		return errNoWindow
	}
	switch k {
	case 'h', 'j', 'k', 'l':
		dirs := map[byte]window.Dir4{'h': window.Left, 'j': window.Down, 'k': window.Up, 'l': window.Right}
		if n := tab.Neighbour(w, dirs[k]); n != nil {
			c.enterWindow(n)
			return nil
		}
		return window.ErrNoSuchWindow
	case 'w', 'W':
		wins := tab.Windows()
		at := 0
		for i, x := range wins {
			if x == w {
				at = i
			}
		}
		step := 1
		if k == 'W' {
			step = -1
		}
		c.enterWindow(wins[((at+step)%len(wins)+len(wins))%len(wins)])
		return nil
	case 's', 'S':
		return exSplit(false, false)(c, Cmd{})
	case 'v':
		return exSplit(true, false)(c, Cmd{})
	case 'n':
		return exSplit(false, true)(c, Cmd{})
	case 'c', 'q':
		return exCloseWindow(c, Cmd{})
	case 'o':
		return exOnly(c, Cmd{})
	}
	return withName(ErrNotImplemented, "CTRL-W "+string(k))
}
