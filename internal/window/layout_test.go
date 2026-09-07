package window

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/options"
)

// screen is the shape "vim --clean -i NONE --not-a-term -s" gives on this
// machine: a 24-row 80-column terminal with one row taken by the command line,
// so the windows get 23.
var screen = Rect{Row: 1, Col: 1, Rows: 23, Cols: 80}

// tab returns one tab page with one window on a small buffer, laid out over the
// whole screen, which is where every case in the measured table starts.
func tab() (*Tabs, *options.Options, *int) {
	o := options.Defaults()
	o.G.EqualAlways = true
	o.G.SplitBelow = false
	o.G.SplitRight = false

	next := 1
	p := NewTabPage(New(next, lines(400), o.GW))
	p.Layout(screen, &o)
	return NewTabs(p), &o, &next
}

// run applies the compact command notation the layout table uses. It is the
// slice of the ex layer this package is responsible for: everything here is
// window arithmetic, and nothing here reads a file or parses a range.
func run(t *testing.T, tabs *Tabs, o *options.Options, nextID *int, cmds string) {
	t.Helper()
	p := tabs.Current()

	for _, cmd := range strings.Fields(cmds) {
		count := 0
		body := cmd
		if strings.HasPrefix(body, ":") {
			body = body[1:]
			for len(body) > 0 && body[0] >= '0' && body[0] <= '9' {
				count = count*10 + int(body[0]-'0')
				body = body[1:]
			}
			body = ":" + body
		} else {
			for len(body) > 0 && body[0] >= '0' && body[0] <= '9' {
				count = count*10 + int(body[0]-'0')
				body = body[1:]
			}
		}

		newWin := func() *Window {
			*nextID++
			return New(*nextID, p.Cur.Buf, o.GW)
		}

		switch body {
		case "sb":
			o.G.SplitBelow = true
		case "spr":
			o.G.SplitRight = true
		case "noea":
			o.G.EqualAlways = false
		case ":sp":
			fresh := newWin()
			if err := p.Split(p.Cur, fresh, Horizontal, !o.G.SplitBelow, count); err != nil {
				t.Fatalf("%s: %v", cmd, err)
			}
		case ":vs":
			fresh := newWin()
			if err := p.Split(p.Cur, fresh, Vertical, !o.G.SplitRight, count); err != nil {
				t.Fatalf("%s: %v", cmd, err)
			}
		case ":q", ":close":
			if err := p.Close(p.Cur); err != nil {
				t.Fatalf("%s: %v", cmd, err)
			}
		case ":only":
			if err := p.Only(p.Cur); err != nil {
				t.Fatalf("%s: %v", cmd, err)
			}
		case "<C-W>+":
			p.Resize(Horizontal, max(1, count))
		case "<C-W>-":
			p.Resize(Horizontal, -max(1, count))
		case "<C-W>>":
			p.Resize(Vertical, max(1, count))
		case "<C-W><":
			p.Resize(Vertical, -max(1, count))
		case "<C-W>_":
			p.Maximise(Horizontal, count)
		case "<C-W>|":
			p.Maximise(Vertical, count)
		case "<C-W>=":
			p.Equalise()
		case "<C-W>h":
			p.Move(Left, count)
		case "<C-W>j":
			p.Move(Down, count)
		case "<C-W>k":
			p.Move(Up, count)
		case "<C-W>l":
			p.Move(Right, count)
		case "<C-W>w":
			p.Cycle(true, count)
		case "<C-W>W":
			p.Cycle(false, count)
		case "<C-W>t":
			p.First()
		case "<C-W>b":
			p.Last()
		case "<C-W>p":
			p.Back()
		case "<C-W>x":
			p.Exchange(count)
		case "<C-W>r":
			p.Rotate(true, count)
		case "<C-W>R":
			p.Rotate(false, count)
		case "<C-W>H":
			p.MoveTo(Left)
		case "<C-W>J":
			p.MoveTo(Down)
		case "<C-W>K":
			p.MoveTo(Up)
		case "<C-W>L":
			p.MoveTo(Right)
		default:
			t.Fatalf("unknown command %q", cmd)
		}
	}
}

// dump writes a tab page in the same notation the measured table uses.
func dump(p *TabPage) string {
	var parts []string
	for _, w := range p.Windows() {
		star := ""
		if w == p.Cur {
			star = "*"
		}
		parts = append(parts, fmt.Sprintf("%d,%d,%d,%d%s",
			w.Rect.Row, w.Rect.Col, w.View.Height, w.View.Width, star))
	}
	return strings.Join(parts, " ")
}

// TestLayoutMatchesVim replays testdata/window/layout-vim9.2.0321.txt.
func TestLayoutMatchesVim(t *testing.T) {
	const path = "../../testdata/window/layout-vim9.2.0321.txt"
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()

	seen := 0
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cmds, want, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("%s:%d: want a tab between the commands and the windows, got %q", path, n, line)
		}
		seen++
		t.Run(cmds, func(t *testing.T) {
			tabs, o, id := tab()
			run(t, tabs, o, id, cmds)
			if got := dump(tabs.Current()); got != want {
				t.Errorf("\n got %s\nvim says %s\n(testdata line %d)", got, want, n)
			}
		})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if seen == 0 {
		t.Fatalf("%s held no cases", path)
	}
}

// TestEveryWindowFitsTheScreen is the invariant behind every row of that table
// and the one a new split rule breaks first: the regions tile the screen with
// no gap and no overlap, and every window's text plus its chrome is its region.
func TestEveryWindowFitsTheScreen(t *testing.T) {
	for _, cmds := range []string{
		":sp", ":sp :sp", ":vs :sp :vs", ":sp :vs :sp :vs", ":vs :vs :sp",
		":sp :sp <C-W>_", ":vs <C-W>|", ":5sp :vs", ":sp :sp <C-W>j :q",
	} {
		t.Run(cmds, func(t *testing.T) {
			tabs, o, id := tab()
			run(t, tabs, o, id, cmds)
			p := tabs.Current()

			covered := map[[2]int]int{}
			for _, w := range p.Windows() {
				if w.View.Height < 1 || w.View.Width < 1 {
					t.Fatalf("window %d has no text area: %dx%d", w.ID, w.View.Width, w.View.Height)
				}
				for r := w.Rect.Row; r < w.Rect.Row+w.Rect.Rows; r++ {
					for c := w.Rect.Col; c < w.Rect.Col+w.Rect.Cols; c++ {
						covered[[2]int{r, c}]++
					}
				}
			}
			for r := screen.Row; r < screen.Row+screen.Rows; r++ {
				for c := screen.Col; c < screen.Col+screen.Cols; c++ {
					if n := covered[[2]int{r, c}]; n != 1 {
						t.Fatalf("screen cell %d,%d is covered by %d windows, want 1", r, c, n)
					}
				}
			}
			if len(covered) != screen.Rows*screen.Cols {
				t.Errorf("the windows cover %d cells, the screen has %d", len(covered), screen.Rows*screen.Cols)
			}
		})
	}
}

// TestCloseTheLastWindow is what makes ":q" a quit rather than a close: the
// caller sees E444 and decides.
func TestCloseTheLastWindow(t *testing.T) {
	tabs, _, _ := tab()
	p := tabs.Current()
	if err := p.Close(p.Cur); err != ErrLastWindow {
		t.Errorf("closing the only window gave %v, want %v", err, ErrLastWindow)
	}
	if err := tabs.Close(0); err != ErrLastWindow {
		t.Errorf("closing the only tab gave %v, want %v", err, ErrLastWindow)
	}
}

// TestSplitNeedsRoom is E36.
func TestSplitNeedsRoom(t *testing.T) {
	tabs, o, id := tab()
	p := tabs.Current()
	p.Cur.SetHeight(2)
	*id++
	if err := p.Split(p.Cur, New(*id, p.Cur.Buf, o.GW), Horizontal, true, 0); err != ErrNoRoom {
		t.Errorf("splitting a two-row window gave %v, want %v", err, ErrNoRoom)
	}
}

// TestNeighbourWithNoLayout is the honest answer to a question about a tab page
// nobody has drawn: there is no window to the left of a window that is nowhere.
func TestNeighbourWithNoLayout(t *testing.T) {
	o := options.Defaults()
	p := NewTabPage(New(1, lines(10), o.GW))
	if got := p.Neighbour(p.Cur, Left); got != nil {
		t.Errorf("Neighbour on an unlaid-out tab returned window %d", got.ID)
	}
}

// TestExchangeAndRotate are the two CTRL-W commands that move windows between
// frames rather than moving the cursor between windows.
//
// Both carry the size with the window, which is the half that is easy to get
// backwards: after CTRL-W x on a maximised window the tall one is the one that
// moved, not the position it left. Measured with three windows at 18, 1 and 1
// rows, current at the top.
func TestExchangeAndRotate(t *testing.T) {
	heights := func(p *TabPage) string {
		var out []string
		for _, w := range p.Windows() {
			star := ""
			if w == p.Cur {
				star = "*"
			}
			out = append(out, strconv.Itoa(w.View.Height)+star)
		}
		return strings.Join(out, " ")
	}

	for _, tc := range []struct {
		cmds, want string
	}{
		{":sp :sp <C-W>_", "18* 1 1"},
		{":sp :sp <C-W>_ <C-W>x", "1* 18 1"},
		{":sp :sp <C-W>_ <C-W>r", "1 18* 1"},
		{":sp :sp <C-W>_ <C-W>R", "1 1 18*"},
	} {
		t.Run(tc.cmds, func(t *testing.T) {
			tabs, o, id := tab()
			run(t, tabs, o, id, tc.cmds)
			if got := heights(tabs.Current()); got != tc.want {
				t.Errorf("heights %q, vim says %q", got, tc.want)
			}
		})
	}
}

// TestTabsCountRule pins the asymmetry nobody remembers: a count on gt is an
// absolute tab number and a count on gT is a repeat. The vimrc maps { and } to
// the two of them, so both are two keystrokes away all day.
func TestTabsCountRule(t *testing.T) {
	o := options.Defaults()
	var pages []*TabPage
	for i := 0; i < 4; i++ {
		pages = append(pages, NewTabPage(New(i, nil, o.GW)))
	}
	tabs := &Tabs{Pages: pages}

	tabs.Next(3)
	if tabs.Cur != 2 {
		t.Errorf("3gt went to tab index %d, want 2: a count on gt is a tab number", tabs.Cur)
	}
	tabs.Prev(2)
	if tabs.Cur != 0 {
		t.Errorf("2gT from tab index 2 went to %d, want 0: a count on gT is a repeat", tabs.Cur)
	}
	tabs.Prev(1)
	if tabs.Cur != 3 {
		t.Errorf("gT from the first tab went to %d, want 3: it wraps", tabs.Cur)
	}
	tabs.Next(0)
	if tabs.Cur != 0 {
		t.Errorf("gt from the last tab went to %d, want 0: it wraps too", tabs.Cur)
	}
}

// TestTabsInsertAndClose covers ":tabnew" landing after the current tab and
// ":tabclose" leaving the next one current.
func TestTabsInsertAndClose(t *testing.T) {
	o := options.Defaults()
	page := func(id int) *TabPage { return NewTabPage(New(id, nil, o.GW)) }

	tabs := NewTabs(page(1))
	tabs.New(page(2), tabs.Cur)
	tabs.New(page(3), tabs.Cur)
	if got := len(tabs.Pages); got != 3 {
		t.Fatalf("%d tabs, want 3", got)
	}
	if tabs.Cur != 2 {
		t.Errorf("after two :tabnew the current tab is %d, want 2", tabs.Cur)
	}
	tabs.Goto(0)
	tabs.New(page(4), tabs.Cur)
	if ids := tabIDs(tabs); ids != "1 4 2 3" {
		t.Errorf(":tabnew from the first tab gave %q, want \"1 4 2 3\"", ids)
	}

	tabs.Goto(1)
	if err := tabs.Close(1); err != nil {
		t.Fatalf(":tabclose: %v", err)
	}
	if ids := tabIDs(tabs); ids != "1 2 3" {
		t.Errorf("after :tabclose the tabs are %q, want \"1 2 3\"", ids)
	}
	if tabs.Cur != 1 {
		t.Errorf("after closing tab 1 the current tab is %d, want 1: the next one takes over", tabs.Cur)
	}

	tabs.Goto(2)
	if err := tabs.Close(2); err != nil {
		t.Fatalf(":tabclose on the last tab: %v", err)
	}
	if tabs.Cur != 1 {
		t.Errorf("after closing the last tab the current one is %d, want 1", tabs.Cur)
	}
}

// tabIDs is the window ids of each tab's first window, which is enough to
// identify the tabs in a test.
func tabIDs(t *Tabs) string {
	var out []string
	for _, p := range t.Pages {
		out = append(out, strconv.Itoa(p.Windows()[0].ID))
	}
	return strings.Join(out, " ")
}

// TestRelayoutKeepsScroll is the bug a Layout that runs every frame creates:
// 'scroll' follows the window height on a resize, and if a redraw counts as a
// resize then the value a count on CTRL-U set is thrown away one frame later.
// Measured behaviour is that "5<C-U>" sticks until the window is actually
// resized.
func TestRelayoutKeepsScroll(t *testing.T) {
	tabs, o, id := tab()
	p := tabs.Current()
	run(t, tabs, o, id, ":sp")

	p.Cur.Scroll(HalfUp, 5, 3)
	if p.Cur.Opt.Scroll != 5 {
		t.Fatalf("'scroll' after 5<C-U> is %d, want 5", p.Cur.Opt.Scroll)
	}
	p.Relayout()
	if got := p.Cur.Opt.Scroll; got != 5 {
		t.Errorf("'scroll' after a redraw is %d, want 5: a relayout is not a resize", got)
	}

	p.Resize(Horizontal, 4)
	if got := p.Cur.Opt.Scroll; got != DefaultScroll(p.Cur.View.Height) {
		t.Errorf("'scroll' after CTRL-W + is %d, want %d: a resize does recompute it", got, DefaultScroll(p.Cur.View.Height))
	}
}

// TestTabsBackIsTheLastAccessedPage is g<Tab> and CTRL-W g<Tab>: the page that
// was current before this one, which is a history and not the page before it
// in the list.
//
// Measured against vim 9.2.0321 on three tab pages: ":tabnew", ":tabnew",
// ":tabfirst" then "g<Tab>" lands on tab 3.
func TestTabsBackIsTheLastAccessedPage(t *testing.T) {
	o := options.Defaults()
	page := func(id int) *TabPage { return NewTabPage(New(id, nil, o.GW)) }

	tabs := NewTabs(page(1))
	if tabs.Back() {
		t.Error("Back on one tab page went somewhere; vim says nothing and moves nothing")
	}

	tabs.New(page(2), tabs.Cur)
	tabs.New(page(3), tabs.Cur)
	tabs.Goto(0)
	if !tabs.Back() || tabs.Cur != 2 {
		t.Fatalf("Back from the first tab landed on %d, want the third: it is the page that was current", tabs.Cur)
	}
	// And back again: the page it just left is the one to go back to.
	if !tabs.Back() || tabs.Cur != 0 {
		t.Fatalf("a second Back landed on %d, want the first", tabs.Cur)
	}

	// A closed page is not somewhere to go back to, and the pointer survives
	// the renumbering that closing one does.
	tabs.Goto(2)
	if err := tabs.Close(0); err != nil {
		t.Fatal(err)
	}
	if tabs.Back() {
		t.Error("Back went to the page that was just closed")
	}
}
