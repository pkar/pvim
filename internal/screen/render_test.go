package screen

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// Every expected block in this file was taken off vim 9.2 patches 1-321,
// running
//
//	vim --clean -n -i NONE --not-a-term -s KEYS FILE
//
// with a keys script that sets ":set lines= columns=", does the thing, and
// then writes the screen out through screenchar() and screenattr() with
// writefile(). The bars down each side are this package's Dump helper and
// vim's dump had them added; everything between them is vim's, cell for cell.
//
// A test that changes has to be re-measured, not re-guessed.

// rig is a screen, a frame and one window over the given lines, at vim's
// --clean defaults.
type rig struct {
	s   *Screen
	f   *Frame
	w   *window.Window
	buf *text.Buffer
	opt *options.Options
}

func newRig(rows, cols int, lines ...string) *rig {
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	b := text.Read([]byte(body))
	o := options.Defaults()
	w := window.New(1, b, o.GW)
	w.View.Cursor = text.Pos{Line: 1}
	tab := window.NewTabPage(w)

	s := NewScreen(rows, cols)
	InitGroups(s.HL)
	f := &Frame{
		Tabs:     window.NewTabs(tab),
		Opt:      &o,
		Names:    map[*text.Buffer]string{b: "f.txt"},
		Display:  "truncate",
		Modified: map[*text.Buffer]bool{},
	}
	return &rig{s: s, f: f, w: w, buf: b, opt: &o}
}

// render draws a frame and returns the character half of its dump, which is
// what most of these tests are about.
func (r *rig) render() string {
	r.s.Render(r.f)
	return chars(Dump(&r.s.Grid))
}

// renderHL draws a frame and returns the whole dump: characters, one mark per
// cell for the highlights, and the legend naming the groups.
func (r *rig) renderHL() string {
	r.s.Render(r.f)
	return dump(&r.s.Grid, r.s.HL)
}

// chars keeps the character block of a dump and drops the highlight block
// under it, for the tests that are about what landed where.
func chars(d string) string {
	lines := strings.SplitAfter(d, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "+-") {
			return strings.Join(lines[:i+1], "")
		}
	}
	return d
}

// wantPic compares a dump against a literal picture, with the leading newline
// and the raw string's own indentation taken off, so an expectation in this
// file reads as the screen it stands for.
func wantPic(t *testing.T, got, block string) {
	t.Helper()
	want(t, got, dedent(block))
}

// dedent strips the leading newline and one tab of indentation from every line
// of a raw string literal, so the expectations read as pictures in the source.
func dedent(s string) string {
	s = strings.TrimPrefix(s, "\n")
	var out []string
	for _, line := range strings.Split(s, "\n") {
		out = append(out, strings.TrimPrefix(line, "\t\t"))
	}
	return strings.Join(out, "\n")
}

func TestPlainBuffer(t *testing.T) {
	r := newRig(12, 40, "one", "two", "three")
	wantPic(t, r.render(), `
		+----------------------------------------+
		|one                                     |
		|two                                     |
		|three                                   |
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|                      1,1           All |
		+----------------------------------------+
		`)
}

// vsplit turns the rig's single window into n side by side, all on the same
// buffer, and returns them left to right.
func (r *rig) vsplit(n int) []*window.Window {
	tab := r.f.Tabs.Current()
	root := &window.Node{Dir: window.Vertical}
	wins := []*window.Window{r.w}
	root.Children = []*window.Node{{Win: r.w}}
	for i := 1; i < n; i++ {
		w := window.New(i+1, r.buf, r.opt.GW)
		w.View.Cursor = text.Pos{Line: 1}
		root.Children = append(root.Children, &window.Node{Win: w})
		wins = append(wins, w)
	}
	tab.Root = root
	tab.Cur = r.w
	return wins
}

// Three windows side by side under 'nowrap' with 'sidescrolloff' 5, the
// leftmost scrolled so the cursor at column 40 has five columns of context.
//
// vim --clean, :set lines=12 columns=60 nowrap sidescrolloff=5, :vsplit twice,
// then 40| in the left window.
func TestThreeWayVerticalSplit(t *testing.T) {
	r := newRig(12, 60,
		"alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi",
		"second line here",
		"\tindented with tab",
		"third",
		"fourth",
		"fifth")
	r.opt.G.SideScrollOff = 5
	wins := r.vsplit(3)
	for _, w := range wins {
		w.Opt.Wrap = false
	}
	r.f.Names[r.buf] = "g.txt"

	// The layout has to be settled before the cursor can be put in a column,
	// because how far a window scrolls sideways depends on how wide it is.
	band, _ := r.s.bands()
	LayoutTree(r.f.Tabs.Current().Root, band, true)
	r.w.View.Cursor = text.Pos{Line: 1, Col: 39}
	if !SideScroll(r.w, r.opt) {
		t.Fatal("SideScroll did not move LeftCol for a cursor 39 columns into a 20-column window")
	}
	if got := r.w.View.LeftCol; got != 29 {
		t.Errorf("LeftCol = %d, vim scrolls to 29", got)
	}

	wantPic(t, r.render(), `
		+------------------------------------------------------------+
		|n zeta eta theta iot|alpha beta gamma de|alpha beta gamma de|
		|                    |second line here   |second line here   |
		|                    |        indented wi|        indented wi|
		|                    |third              |third              |
		|                    |fourth             |fourth             |
		|                    |fifth              |fifth              |
		|~                   |~                  |~                  |
		|~                   |~                  |~                  |
		|~                   |~                  |~                  |
		|~                   |~                  |~                  |
		|g.txt     1,40   All g.txt     1,1   All g.txt     1,1   All|
		|                                                            |
		+------------------------------------------------------------+
		`)
}

// 'breakindent' on a wrapped line: the continuation rows start under the
// first row's text and not at the left edge.
//
// vim --clean, :set lines=10 columns=30 wrap nolinebreak breakindent tabstop=2.
func TestBreakIndent(t *testing.T) {
	r := newRig(10, 30,
		"    alpha beta gamma delta epsilon zeta eta theta iota kappa",
		"short")
	r.opt.B.TabStop = 2
	r.w.Opt.Wrap = true
	r.w.Opt.BreakIndent = true

	wantPic(t, r.render(), `
		+------------------------------+
		|    alpha beta gamma delta eps|
		|    ilon zeta eta theta iota k|
		|    appa                      |
		|short                         |
		|~                             |
		|~                             |
		|~                             |
		|~                             |
		|~                             |
		|               1,1        All |
		+------------------------------+
		`)
}

// 'linebreak' with it: the rows break at a space and not mid-word.
//
// vim --clean, :set lines=10 columns=30 wrap linebreak breakindent tabstop=2.
func TestLineBreak(t *testing.T) {
	r := newRig(10, 30,
		"    alpha beta gamma delta epsilon zeta eta theta iota kappa",
		"short")
	r.opt.B.TabStop = 2
	r.w.Opt.Wrap = true
	r.w.Opt.BreakIndent = true
	r.w.Opt.LineBreak = true

	wantPic(t, r.render(), `
		+------------------------------+
		|    alpha beta gamma delta    |
		|    epsilon zeta eta theta    |
		|    iota kappa                |
		|short                         |
		|~                             |
		|~                             |
		|~                             |
		|~                             |
		|~                             |
		|               1,1        All |
		+------------------------------+
		`)
}

// A closed fold stands in for its lines with the text vim's foldtext() makes,
// padded to the window with the 'fold' item of 'fillchars'.
//
// vim --clean, :set lines=12 columns=40 foldmethod=manual, then 2Gzf3j on a
// file of "line 1" to "line 20".
func TestClosedFold(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line " + itoa(i+1)
	}
	r := newRig(12, 40, lines...)
	r.w.Opt.Wrap = false
	folds := NewFolds()
	if !folds.Create(2, 5) {
		t.Fatal("Create(2, 5) refused")
	}
	r.f.Folds = map[int]*Folds{r.w.ID: folds}
	r.w.View.Cursor = text.Pos{Line: 2}

	wantPic(t, r.render(), `
		+----------------------------------------+
		|line 1                                  |
		|+--  4 lines: line 2--------------------|
		|line 6                                  |
		|line 7                                  |
		|line 8                                  |
		|line 9                                  |
		|line 10                                 |
		|line 11                                 |
		|line 12                                 |
		|line 13                                 |
		|line 14                                 |
		|                      2,1           Top |
		+----------------------------------------+
		`)
}

// The tabline is text, because the vimrc says guioptions-=e. Two tabs, the
// second holding a modified buffer, with the current tab first.
//
// vim --clean, :set lines=10 columns=40, :tabnew second.txt, ggIx<Esc>,
// :tabprev.
func TestTablineTwoTabs(t *testing.T) {
	r := newRig(10, 40, "one", "two", "three")
	second := text.Read([]byte("aaa\nbbb\n"))
	r.f.Names[second] = "second.txt"
	r.f.Modified[second] = true
	w2 := window.New(2, second, r.opt.GW)
	w2.View.Cursor = text.Pos{Line: 1}
	r.f.Tabs.Pages = append(r.f.Tabs.Pages, window.NewTabPage(w2))
	r.w.Opt.Wrap = false

	wantPic(t, r.render(), `
		+----------------------------------------+
		| f.txt  + second.txt                   X|
		|one                                     |
		|two                                     |
		|three                                   |
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|                      1,1           All |
		+----------------------------------------+
		`)
}

// A double-width rune that will not fit in the cells left at the right edge is
// not drawn: vim puts a ">" there instead.
//
// vim --clean, :set lines=8 columns=12 nowrap, over "abcdefghijk" and three
// CJK runes.
func TestWideRuneAtTheRightEdge(t *testing.T) {
	r := newRig(8, 12, "abcdefghijk一二san", "plain")
	r.w.Opt.Wrap = false

	wantPic(t, r.render(), `
		+------------+
		|abcdefghijk>|
		|plain       |
		|~           |
		|~           |
		|~           |
		|~           |
		|~           |
		|      1,1   |
		+------------+
		`)
}

// The same file under 'wrap': the rune that did not fit still shows as ">" and
// starts the next row.
func TestWideRuneWraps(t *testing.T) {
	r := newRig(8, 12, "abcdefghijk一二san", "plain")
	r.w.Opt.Wrap = true

	wantPic(t, r.render(), `
		+------------+
		|abcdefghijk>|
		|一二san     |
		|plain       |
		|~           |
		|~           |
		|~           |
		|~           |
		|      1,1   |
		+------------+
		`)
}

// 'hlsearch' over a match that runs across a tab: every cell the tab expands
// to is highlighted, because the match is over bytes and the tab is one byte.
//
// vim --clean, :set lines=8 columns=30 hlsearch tabstop=4, /ab\tcd.
func TestHlSearchAcrossATab(t *testing.T) {
	r := newRig(8, 30, "ab\tcd efgh", "zz ab\tcd zz")
	r.opt.B.TabStop = 4
	r.opt.G.HlSearch = true
	r.w.Opt.Wrap = false
	r.w.View.Cursor = text.Pos{Line: 2, Col: 3}
	r.f.Search = MatchList{
		{Line: 1, Start: 0, End: 5},
		{Line: 2, Start: 3, End: 8},
	}

	wantPic(t, r.renderHL(), `
		+------------------------------+
		|ab  cd efgh                   |
		|zz ab   cd zz                 |
		|~                             |
		|~                             |
		|~                             |
		|~                             |
		|~                             |
		|               2,4        All |
		+------------------------------+
		|aaaaaa........................|
		|...aaaaaaa....................|
		|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb|
		|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb|
		|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb|
		|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb|
		|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb|
		|..............................|
		+------------------------------+
		legend: a=Search b=EndOfBuffer
		`)
}

// The completion menu with six entries and 'completeopt' holding noselect: it
// hangs under the line being completed, nothing is drawn in PmenuSel, and it
// is fifteen cells wide even though the widest entry is nine, because fifteen
// is 'pumwidth'.
//
// vim --clean, :set lines=16 columns=30
// completeopt=menu,menuone,noselect,noinsert, then 8GA<C-n>.
//
// The last row is the one place this differs from that dump, and the dump is
// the thing at fault: it was taken from a CompleteChanged autocmd whose
// redraw! wiped vim's own "-- Keyword completion" message off the row and left
// it blank. What belongs there is the ruler, which every other test here
// measures.
func TestPopupMenuNoSelect(t *testing.T) {
	r := newRig(16, 30, "alpha", "albatross", "algebra", "almond", "alpine", "altitude", "", "al")
	r.w.Opt.Wrap = false
	r.w.View.Cursor = text.Pos{Line: 8, Col: 2}
	r.f.Pum = &Pum{
		Items: []PumItem{
			{Word: "alpha"}, {Word: "albatross"}, {Word: "algebra"},
			{Word: "almond"}, {Word: "alpine"}, {Word: "altitude"},
		},
		Selected: -1,
	}

	wantPic(t, r.renderHL(), `
		+------------------------------+
		|alpha                         |
		|albatross                     |
		|algebra                       |
		|almond                        |
		|alpine                        |
		|altitude                      |
		|                              |
		|al                            |
		|alpha                         |
		|albatross                     |
		|algebra                       |
		|almond                        |
		|alpine                        |
		|altitude                      |
		|~                             |
		|               8,3        All |
		+------------------------------+
		|..............................|
		|..............................|
		|..............................|
		|..............................|
		|..............................|
		|..............................|
		|..............................|
		|..............................|
		|aaaaaaaaaaaaaaabbbbbbbbbbbbbbb|
		|aaaaaaaaaaaaaaabbbbbbbbbbbbbbb|
		|aaaaaaaaaaaaaaabbbbbbbbbbbbbbb|
		|aaaaaaaaaaaaaaabbbbbbbbbbbbbbb|
		|aaaaaaaaaaaaaaabbbbbbbbbbbbbbb|
		|aaaaaaaaaaaaaaabbbbbbbbbbbbbbb|
		|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb|
		|..............................|
		+------------------------------+
		legend: a=Pmenu b=EndOfBuffer
		`)
}

// A horizontal split: both windows carry a status line, the current one's is
// drawn in StatusLine and the other's in StatusLineNC, and a modified buffer
// puts "[+]" after the name.
//
// vim --clean, :set lines=12 columns=40, :split, then ggIx<Esc>.
func TestHorizontalSplitStatusLines(t *testing.T) {
	r := newRig(12, 40, "xone", "two", "three")
	r.f.Modified[r.buf] = true
	r.w.Opt.Wrap = false

	tab := r.f.Tabs.Current()
	w2 := window.New(2, r.buf, r.opt.GW)
	w2.View.Cursor = text.Pos{Line: 1}
	w2.Opt.Wrap = false
	tab.Root = &window.Node{Dir: window.Horizontal, Children: []*window.Node{{Win: r.w}, {Win: w2}}}
	tab.Cur = r.w

	wantPic(t, r.renderHL(), `
		+----------------------------------------+
		|xone                                    |
		|two                                     |
		|three                                   |
		|~                                       |
		|~                                       |
		|f.txt [+]             1,1            All|
		|xone                                    |
		|two                                     |
		|three                                   |
		|~                                       |
		|f.txt [+]             1,1            All|
		|                                        |
		+----------------------------------------+
		|........................................|
		|........................................|
		|........................................|
		|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|
		|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|
		|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb|
		|........................................|
		|........................................|
		|........................................|
		|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|
		|cccccccccccccccccccccccccccccccccccccccc|
		|........................................|
		+----------------------------------------+
		legend: a=EndOfBuffer b=StatusLine c=StatusLineNC
		`)
}

// The 'number' column is four cells wide at the default 'numberwidth': the
// number right-aligned in three and a space after it. It stays four up to a
// thousand lines.
//
// vim --clean, :set lines=8 columns=30 number, over "line 1" to "line 20".
func TestNumberColumn(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line " + itoa(i+1)
	}
	r := newRig(8, 30, lines...)
	r.w.Opt.Wrap = false
	r.w.Opt.Number = true

	wantPic(t, r.render(), `
		+------------------------------+
		|  1 line 1                    |
		|  2 line 2                    |
		|  3 line 3                    |
		|  4 line 4                    |
		|  5 line 5                    |
		|  6 line 6                    |
		|  7 line 7                    |
		|               1,1        Top |
		+------------------------------+
		`)
}

// The same at the bottom of a 200-line file: still four cells, and the ruler
// says Bot.
//
// vim --clean, :set lines=8 columns=30 number, then G.
func TestNumberColumnAtTheEnd(t *testing.T) {
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = "line " + itoa(i+1)
	}
	r := newRig(8, 30, lines...)
	r.w.Opt.Wrap = false
	r.w.Opt.Number = true
	r.w.View.TopLine = 194
	r.w.View.Cursor = text.Pos{Line: 200}

	wantPic(t, r.render(), `
		+------------------------------+
		|194 line 194                  |
		|195 line 195                  |
		|196 line 196                  |
		|197 line 197                  |
		|198 line 198                  |
		|199 line 199                  |
		|200 line 200                  |
		|               200,1      Bot |
		+------------------------------+
		`)
}

// 'showcmd' goes at column 12 of a 40-column screen, which is vim's
// comp_col(): Columns minus the ruler's eighteen, minus ten for the command,
// with no separating space because the ruler is on the same row.
//
// Read off the escape sequences vim wrote to a pty while "12" was typed in
// normal mode: ESC[8;13H1 then ESC[8;14H2, which is 1-based column 13.
func TestShowCmdColumn(t *testing.T) {
	r := newRig(8, 40, "one")
	r.w.Opt.Wrap = false
	r.f.ShowCmd = "12"

	wantPic(t, r.render(), `
		+----------------------------------------+
		|one                                     |
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|            12        1,1           All |
		+----------------------------------------+
		`)
	if got := showCmdCol(40, true, false); got != 12 {
		t.Errorf("showCmdCol(40) = %d, vim puts it at 12", got)
	}
}

// A message area with more to say than 'cmdheight' rows grows upward over the
// text and ends in the hit-return prompt.
//
// The prompt's wording and its Question highlight came off a pty running ":ls"
// with cmdheight=1; the row arithmetic under it is this editor's, because vim
// scrolls its terminal to make the room and a Grid has nowhere to scroll to.
func TestPressEnter(t *testing.T) {
	r := newRig(8, 40, "one", "two")
	r.w.Opt.Wrap = false
	r.f.Message = Message{
		Lines: []MsgLine{
			{Text: ":ls"},
			{Text: `  1 %a   "f.txt"                line 1`},
		},
		PressEnter: true,
	}

	wantPic(t, r.renderHL(), `
		+----------------------------------------+
		|one                                     |
		|two                                     |
		|~                                       |
		|~                                       |
		|~                                       |
		|:ls                                     |
		|  1 %a   "f.txt"                line 1  |
		|Press ENTER or type command to continue |
		+----------------------------------------+
		|........................................|
		|........................................|
		|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|
		|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|
		|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|
		|........................................|
		|........................................|
		|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.|
		+----------------------------------------+
		legend: a=EndOfBuffer b=Question
		`)
	if r.s.CursorRow != 7 {
		t.Errorf("cursor row %d, the prompt is on row 7", r.s.CursorRow)
	}
}

// Indent folds, and the two things about them that are not obvious: a blank
// line between two indented blocks joins them into one fold, and a blank line
// between an indented block and an unindented one does not.
//
// vim --clean, :set shiftwidth=2 tabstop=8 foldmethod=indent.
func TestIndentFolds(t *testing.T) {
	r := newRig(10, 40, "a", "  b1", "  b2", "", "  c1", "d", "  e1")
	r.opt.B.ShiftWidth = 2
	r.w.Opt.Wrap = false
	folds := &Folds{Method: "indent", Enable: true}
	folds.Recompute(r.buf, 2, 8)
	r.f.Folds = map[int]*Folds{r.w.ID: folds}

	wantPic(t, r.render(), `
		+----------------------------------------+
		|a                                       |
		|+--  4 lines: b1------------------------|
		|d                                       |
		|  e1                                    |
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|                      1,1           All |
		+----------------------------------------+
		`)
}

// The same file with a blank line between the indented block and an
// unindented one: two folds, not one, and the single-line fold at the end is
// not closed at all, because 'foldminlines' is 1 and a fold has to cover more
// lines than that.
//
// vim --clean, same options, over "a", " b1", " b2", "", "", "c", " d1",
// " d2".
func TestIndentFoldsSplitByABlankLine(t *testing.T) {
	r := newRig(12, 40, "a", "  b1", "  b2", "", "", "c", "  d1", "  d2")
	r.opt.B.ShiftWidth = 2
	r.w.Opt.Wrap = false
	folds := &Folds{Method: "indent", Enable: true}
	folds.Recompute(r.buf, 2, 8)
	r.f.Folds = map[int]*Folds{r.w.ID: folds}

	wantPic(t, r.render(), `
		+----------------------------------------+
		|a                                       |
		|+--  2 lines: b1------------------------|
		|                                        |
		|                                        |
		|c                                       |
		|+--  2 lines: d1------------------------|
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|~                                       |
		|                      1,1           All |
		+----------------------------------------+
		`)
}

// Under 'nowrap', a double-width rune the left edge cuts in half shows as "<".
//
// vim --clean, :set lines=6 columns=12 nowrap sidescroll=1 sidescrolloff=0,
// over "ab" and eight CJK runes, after three zl.
func TestWideRuneAtTheLeftEdge(t *testing.T) {
	r := newRig(6, 12, "ab一二三四五六七八ZY", "plain")
	r.w.Opt.Wrap = false
	r.w.View.LeftCol = 3

	wantPic(t, r.render(), `
		+------------+
		|<二三四五六>|
		|in          |
		|~           |
		|~           |
		|~           |
		|      1,1   |
		+------------+
		`)
}

// A buffer line too tall for the rows left at the bottom of the window under
// 'wrap', in each of the three shapes 'display' asks for.
//
// vim --clean, :set lines=7 columns=20 wrap, over "aaa", "bbb" and a hundred
// x's, with 'display' set three ways.
func TestLastLineDoesNotFit(t *testing.T) {
	body := []string{"aaa", "bbb", strings.Repeat("x", 100), "last"}
	cases := []struct {
		display string
		pic     string
	}{
		{"truncate", `
		+--------------------+
		|aaa                 |
		|bbb                 |
		|xxxxxxxxxxxxxxxxxxxx|
		|xxxxxxxxxxxxxxxxxxxx|
		|xxxxxxxxxxxxxxxxxxxx|
		|@@@                 |
		|          1,1   Top |
		+--------------------+
		`},
		{"", `
		+--------------------+
		|aaa                 |
		|bbb                 |
		|@                   |
		|@                   |
		|@                   |
		|@                   |
		|          1,1   Top |
		+--------------------+
		`},
		{"lastline", `
		+--------------------+
		|aaa                 |
		|bbb                 |
		|xxxxxxxxxxxxxxxxxxxx|
		|xxxxxxxxxxxxxxxxxxxx|
		|xxxxxxxxxxxxxxxxxxxx|
		|xxxxxxxxxxxxxxxxx@@@|
		|          1,1   Top |
		+--------------------+
		`},
	}
	for _, c := range cases {
		t.Run("display="+c.display, func(t *testing.T) {
			r := newRig(7, 20, body...)
			r.f.Display = c.display
			r.w.Opt.Wrap = true
			wantPic(t, r.render(), c.pic)
		})
	}
}

// A command line being typed takes the last row and the cursor with it, and
// the ruler stays off it while it is there.
func TestCmdline(t *testing.T) {
	r := newRig(6, 30, "one", "two")
	r.w.Opt.Wrap = false
	r.f.Cmdline = Cmdline{Active: true, Prefix: ":", Text: "wq", Pos: 2}

	wantPic(t, r.render(), `
		+------------------------------+
		|one                           |
		|two                           |
		|~                             |
		|~                             |
		|~                             |
		|:wq                           |
		+------------------------------+
		`)
	if r.s.CursorRow != 5 || r.s.CursorCol != 3 {
		t.Errorf("cursor at %d,%d, want 5,3: after the \":wq\" it is typing",
			r.s.CursorRow, r.s.CursorCol)
	}
}

// 'showmode' goes on the message row when there is no message and no command
// line to put there instead, and it shares the row with the ruler.
//
// Read off a pty running vim --clean at 30 columns with "i" typed:
//
//	ESC[5;1H ESC[1m -- INSERT -- ESC[m ESC[5;16H 1,1 ESC[8C All
//
// which is the mode message bold at column 0 and the ruler at 1-based column
// 16. The same sequence shows the "i" echoed at 1-based column 3, which is
// 'showcmd' at column 2, and 30 - 28 is 2.
func TestShowMode(t *testing.T) {
	r := newRig(5, 30, "one")
	r.w.Opt.Wrap = false
	r.f.Mode = "-- INSERT --"

	wantPic(t, r.renderHL(), `
		+------------------------------+
		|one                           |
		|~                             |
		|~                             |
		|~                             |
		|-- INSERT --   1,1        All |
		+------------------------------+
		|..............................|
		|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|
		|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|
		|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|
		|bbbbbbbbbbbb..................|
		+------------------------------+
		legend: a=EndOfBuffer b=ModeMsg
		`)
}

// 'showmatch' moves the drawn cursor and nothing else: the buffer does not
// move, and the frontend puts it back when the Hop's timer fires.
func TestShowMatchMovesOnlyTheCursor(t *testing.T) {
	r := newRig(6, 30, "call(a, b)")
	r.w.Opt.Wrap = false
	r.w.View.Cursor = text.Pos{Line: 1, Col: 9}

	r.render()
	if r.s.CursorCol != 9 {
		t.Fatalf("cursor at column %d before the hop, want 9", r.s.CursorCol)
	}

	hop, ok := ShowMatch(r.buf, r.w.View.Cursor, r.opt.B.MatchPairs, 1, r.w.BotLine())
	if !ok {
		t.Fatal("no hop for a typed ')'")
	}
	r.f.CursorOver = &hop.Pos
	r.render()
	if r.s.CursorCol != 4 {
		t.Errorf("cursor at column %d during the hop, want 4, the matching '('", r.s.CursorCol)
	}
	if r.w.View.Cursor.Col != 9 {
		t.Error("the hop moved the buffer cursor")
	}
}

// 'conceallevel' is accepted and does nothing, because with no syntax nothing
// ever sets a conceal attribute. The test is here so that the day somebody
// wires it up they have to come and delete this, rather than the option
// silently starting to hide text.
func TestConcealLevelIsInert(t *testing.T) {
	r := newRig(6, 30, "a |link| b")
	r.w.Opt.Wrap = false
	before := r.render()

	r.w.Opt.ConcealLevel = 2
	r.w.Opt.ConcealCursor = "niv"
	if after := r.render(); after != before {
		t.Errorf("'conceallevel' changed the screen\n--- with ---\n%s--- without ---\n%s", after, before)
	}
}

// A menu with more items than rows: it goes above the cursor line because
// there is more room there, it shows eleven of the twenty, and it grows a
// scrollbar in the column after it, six rows of PmenuThumb over five of
// PmenuSbar.
//
// The legend letters differ from vim's dump and the cells do not: vim's dump
// hands its first letter to whichever highlight it meets first, which there
// was Pmenu, so its Normal reads as "b" and this one's as ".".
//
// vim --clean, :set lines=16 columns=30
// completeopt=menu,menuone,noselect,noinsert, over "al01" to "al20" then "al"
// and three "z", with 21GA<C-n>.
func TestPopupMenuScrollbar(t *testing.T) {
	body := make([]string, 0, 24)
	for i := 1; i <= 20; i++ {
		body = append(body, "al"+pad2(i))
	}
	body = append(body, "al", "z", "z", "z")

	r := newRig(16, 30, body...)
	r.w.Opt.Wrap = false
	r.w.View.TopLine = 10
	r.w.View.Cursor = text.Pos{Line: 21, Col: 2}

	items := make([]PumItem, 20)
	for i := range items {
		items[i] = PumItem{Word: "al" + pad2(i+1)}
	}
	r.f.Pum = &Pum{Items: items, Selected: -1}

	wantPic(t, r.renderHL(), `
		+------------------------------+
		|al01                          |
		|al02                          |
		|al03                          |
		|al04                          |
		|al05                          |
		|al06                          |
		|al07                          |
		|al08                          |
		|al09                          |
		|al10                          |
		|al11                          |
		|al                            |
		|z                             |
		|z                             |
		|z                             |
		|               21,3       Bot |
		+------------------------------+
		|aaaaaaaaaaaaaaab..............|
		|aaaaaaaaaaaaaaab..............|
		|aaaaaaaaaaaaaaab..............|
		|aaaaaaaaaaaaaaab..............|
		|aaaaaaaaaaaaaaab..............|
		|aaaaaaaaaaaaaaab..............|
		|aaaaaaaaaaaaaaac..............|
		|aaaaaaaaaaaaaaac..............|
		|aaaaaaaaaaaaaaac..............|
		|aaaaaaaaaaaaaaac..............|
		|aaaaaaaaaaaaaaac..............|
		|..............................|
		|..............................|
		|..............................|
		|..............................|
		|..............................|
		+------------------------------+
		legend: a=Pmenu b=PmenuThumb c=PmenuSbar
		`)
}

// pad2 is a two-digit decimal, for the completion fixtures above.
func pad2(n int) string {
	s := itoa(n)
	if len(s) < 2 {
		return "0" + s
	}
	return s
}

// Horizontal scrolling under 'nowrap' over lines with tabs in them: LeftCol is
// a display column, so the tabs are expanded first and then the window slides
// across the result.
//
// vim --clean, :set lines=8 columns=20 nowrap tabstop=4 sidescrolloff=0, then
// $ on "one<Tab>ab<Tab>cd<Tab>efghijklmnop".
func TestNowrapScrolledOverTabs(t *testing.T) {
	r := newRig(8, 20, "one\tab\tcd\tefghijklmnop", "short", "\tleading tab and text")
	r.opt.B.TabStop = 4
	r.opt.G.SideScrollOff = 0
	r.w.Opt.Wrap = false
	r.w.View.Width = 20
	r.w.View.Cursor = text.Pos{Line: 1, Col: 21}

	if !SideScroll(r.w, r.opt) {
		t.Fatal("SideScroll did not move for a cursor at display column 23")
	}
	if r.w.View.LeftCol != 13 {
		t.Errorf("LeftCol = %d, vim scrolls to 13", r.w.View.LeftCol)
	}

	wantPic(t, r.render(), `
		+--------------------+
		|fghijklmnop         |
		|                    |
		|ab and text         |
		|~                   |
		|~                   |
		|~                   |
		|~                   |
		|          1,22-24   |
		+--------------------+
		`)
}

// A frame with nothing in it draws an empty buffer rather than panicking. The
// editor hands the renderer whatever it has, including the state before a
// window exists, and a nil dereference there is a crash on startup.
func TestEmptyFrame(t *testing.T) {
	s := NewScreen(5, 20)
	InitGroups(s.HL)
	s.Render(&Frame{})

	wantPic(t, chars(Dump(&s.Grid)), `
		+--------------------+
		|                    |
		|~                   |
		|~                   |
		|~                   |
		|          0,0-1 All |
		+--------------------+
		`)
}

// Render over random buffers, sizes and options.
//
// Not a comparison against vim: this one is looking for the panic, the
// negative slice index and the row that came out the wrong width, which are
// the failures a screendump test cannot find because a screendump test only
// ever draws the six pictures somebody wrote down. A one-column window, a
// window one row tall, a buffer of one empty line and a line of tabs wider
// than the screen all live in here.
func TestRenderSurvivesRandomInput(t *testing.T) {
	rng := rand.New(rand.NewSource(20260904))
	bodies := []string{
		"", "\n", "a\n", "\t\t\t\t\t\t\t\t\t\t\n", strings.Repeat("x", 200) + "\n",
		"one\ntwo\nthree\n", "一二三\n", "  indented\n\tmixed\n",
		"a\x01b\n", strings.Repeat("word ", 60) + "\n",
	}
	for i := 0; i < 2000; i++ {
		rows := 1 + rng.Intn(14)
		cols := 1 + rng.Intn(50)
		body := bodies[rng.Intn(len(bodies))]

		o := options.Defaults()
		o.B.TabStop = 1 + rng.Intn(8)
		o.G.ShowTabline = rng.Intn(3)
		o.G.LastStatus = rng.Intn(3)
		o.G.CmdHeight = 1 + rng.Intn(3)
		o.G.Ruler = rng.Intn(2) == 0
		o.G.ShowCmd = rng.Intn(2) == 0

		b := text.Read([]byte(body))
		w := window.New(1, b, o.GW)
		w.Opt.Wrap = rng.Intn(2) == 0
		w.Opt.LineBreak = rng.Intn(2) == 0
		w.Opt.BreakIndent = rng.Intn(2) == 0
		w.Opt.Number = rng.Intn(2) == 0
		w.View.TopLine = 1 + rng.Intn(b.LineCount())
		w.View.Cursor = text.Pos{Line: w.View.TopLine, Col: rng.Intn(4)}
		w.View.LeftCol = rng.Intn(6)

		s := NewScreen(rows, cols)
		InitGroups(s.HL)
		f := &Frame{
			Tabs:    window.NewTabs(window.NewTabPage(w)),
			Opt:     &o,
			Names:   map[*text.Buffer]string{b: "f.txt"},
			Display: []string{"", "truncate", "lastline"}[rng.Intn(3)],
			ShowCmd: "2d",
		}
		if rng.Intn(3) == 0 {
			folds := NewFolds()
			folds.Create(1, min(b.LineCount(), 1+rng.Intn(4)))
			f.Folds = map[int]*Folds{w.ID: folds}
		}
		if rng.Intn(3) == 0 {
			f.Search = MatchList{{Line: w.View.TopLine, Start: 0, End: 3}}
		}
		if rng.Intn(4) == 0 {
			f.Pum = &Pum{Items: []PumItem{{Word: "one"}, {Word: "two"}}, Selected: rng.Intn(2)}
		}

		s.Render(f)

		if !s.Grid.InBounds(s.CursorRow, s.CursorCol) && !(rows == 0 || cols == 0) {
			t.Fatalf("%dx%d %q: cursor at %d,%d is off the grid",
				rows, cols, body, s.CursorRow, s.CursorCol)
		}
		for row := 0; row < rows; row++ {
			for col := 0; col < cols; col++ {
				if c := s.Grid.At(row, col); c.Rune == 0 && !c.Tail {
					t.Fatalf("%dx%d %q: cell %d,%d was never written", rows, cols, body, row, col)
				}
			}
		}
	}
}

// rowOf returns one row of a dump with the border bars taken off, for the
// tests that are about one line of furniture rather than a whole screen.
func rowOf(t *testing.T, d string, n int) string {
	t.Helper()
	lines := strings.Split(d, "\n")
	if n+1 >= len(lines) {
		t.Fatalf("dump has no row %d:\n%s", n, d)
	}
	return strings.TrimSuffix(strings.TrimPrefix(lines[n+1], "|"), "|")
}

// A message on the last row does not take the ruler off it. Vim's showmode()
// ends with win_redr_ruler() and msg_end() leaves the ruler alone, so the two
// sit side by side: the message from column 0, the ruler at ru_col.
//
// Measured on vim 9.2.0321 in a 40x10 terminal over "aaa" and three blank
// lines and "bbb", driven through term_start() and read back with
// term_getline(). With nothing typed the last row is
//
//	1,1 All
//
// and after ":set ruler?" it is
//
//	ruler 1,1 All
//
// The empty-message case is the shape every pvim frame has, because
// cmd/pvim/draw.go builds Message.Lines with one entry that is empty when
// there is nothing to say.
func TestRulerSharesTheLastRowWithAMessage(t *testing.T) {
	for _, c := range []struct{ name, msg, want string }{
		{"empty message line", "", "                      1,1           All "},
		{"set ruler?", "  ruler", "  ruler               1,1           All "},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(10, 40, "aaa", "", "", "", "bbb")
			r.w.Opt.Wrap = false
			r.f.Message = Message{Lines: []MsgLine{{Text: c.msg}}}
			if got := rowOf(t, r.render(), 9); got != c.want {
				t.Errorf("last row\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// 'showmode' still shares the row with the ruler when the message area holds
// an empty line rather than no line at all, which is the shape cmd/pvim
// builds every frame with.
func TestShowModeWithAnEmptyMessageLine(t *testing.T) {
	r := newRig(5, 30, "one")
	r.w.Opt.Wrap = false
	r.f.Mode = "-- INSERT --"
	r.f.Message = Message{Lines: []MsgLine{{Text: ""}}}

	want := "-- INSERT --   1,1        All "
	if got := rowOf(t, r.render(), 4); got != want {
		t.Errorf("last row\n got %q\nwant %q", got, want)
	}
}

// The ruler's line number is the cursor's line whatever is on it. Vim's
// win_redr_ruler() prints 0 only for ML_EMPTY -- a buffer that never had any
// content -- and the "0-1" column half is a separate test on the cursor line
// being empty:
//
//	vim_snprintf(buffer, ..., "%ld,",
//	 (wp->w_buffer->b_ml.ml_flags & ML_EMPTY) ? 0L : wp->w_cursor.lnum);
//	col_print(buffer + len, ..., empty_line ? 0 : wp->w_cursor.col + 1,
//	 virtcol + 1);
//
// Measured on vim 9.2.0321, 40x10, over "aaa" and three blank lines and "bbb",
// stepping the cursor down with:normal! NG and a redraw! between each.
func TestRulerLineNumberOnABlankLine(t *testing.T) {
	want := []string{
		"                      1,1           All ",
		"                      2,0-1         All ",
		"                      3,0-1         All ",
		"                      4,0-1         All ",
		"                      5,1           All ",
	}
	for i, w := range want {
		r := newRig(10, 40, "aaa", "", "", "", "bbb")
		r.w.Opt.Wrap = false
		r.w.View.Cursor = text.Pos{Line: i + 1}
		if got := rowOf(t, r.render(), 9); got != w {
			t.Errorf("cursor on line %d\n got %q\nwant %q", i+1, got, w)
		}
	}
}

// A buffer that never had any content is vim's ML_EMPTY and is the one case
// that does print line 0. Measured: ":enew" and a buffer read from a
// zero-length file both give "0,0-1", and a file holding a single newline
// gives "1,0-1".
func TestRulerLineNumberOnAnEmptyBuffer(t *testing.T) {
	r := newRig(10, 40)
	r.w.Opt.Wrap = false
	want := "                      0,0-1         All "
	if got := rowOf(t, r.render(), 9); got != want {
		t.Errorf("empty buffer\n got %q\nwant %q", got, want)
	}
}

// A buffer name too long for its status line is cut against the ruler's own
// column, not against the window's width, and what survives is the tail with
// a "<" in front of it.
//
// Vim's win_redr_status():
//
//	this_ru_col = ru_col - (Columns - wp->w_width);
//	if (this_ru_col < (wp->w_width + 1) / 2)
//	 this_ru_col = (wp->w_width + 1) / 2;
//	...
//	for (i = 0; p[i] != NUL && clen >= this_ru_col - 1; i += len)
//	 clen -= cells(p + i);
//	if (i > 0) { p = p + i - 1; *p = '<'; }
//
// Measured on vim 9.2.0321, 40x12, 'laststatus' 2, over the file
// abcdefghij_klmnopqrst_uvwxyz.txt, before and after ":vsplit".
func TestStatusNameTruncatedAgainstTheRulerColumn(t *testing.T) {
	const long = "abcdefghij_klmnopqrst_uvwxyz.txt"

	t.Run("one window", func(t *testing.T) {
		r := newRig(12, 40, "aaa", "", "", "", "bbb")
		r.w.Opt.Wrap = false
		r.opt.G.LastStatus = 2
		r.f.Names[r.buf] = long
		want := "<lmnopqrst_uvwxyz.txt 1,1            All"
		if got := rowOf(t, r.render(), 10); got != want {
			t.Errorf("status line\n got %q\nwant %q", got, want)
		}
	})

	t.Run("vsplit", func(t *testing.T) {
		r := newRig(12, 40, "aaa", "", "", "", "bbb")
		r.opt.G.LastStatus = 2
		for _, w := range r.vsplit(2) {
			w.Opt.Wrap = false
		}
		r.f.Names[r.buf] = long
		want := "<wxyz.txt 1,1    All <wxyz.txt 1,1   All"
		if got := rowOf(t, r.render(), 10); got != want {
			t.Errorf("status line\n got %q\nwant %q", got, want)
		}
	})
}

// The boundary, measured one character at a time on a 40-column screen with
// 'laststatus' 2: a name 20 cells wide is drawn whole and one 21 cells wide
// comes out as "<" and its last 20 cells. this_ru_col is 22 there.
func TestStatusNameTruncationBoundary(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"abcdefghijklmnopqr", "abcdefghijklmnopqr    1,1            All"},
		{"abcdefghijklmnopqrs", "abcdefghijklmnopqrs   1,1            All"},
		{"abcdefghijklmnopqrst", "abcdefghijklmnopqrst  1,1            All"},
		{"abcdefghijklmnopqrstu", "<bcdefghijklmnopqrstu 1,1            All"},
		{"abcdefghijklmnopqrstuv", "<cdefghijklmnopqrstuv 1,1            All"},
		{"abcdefghijklmnopqrstuvw", "<defghijklmnopqrstuvw 1,1            All"},
	} {
		r := newRig(6, 40, "x")
		r.w.Opt.Wrap = false
		r.opt.G.LastStatus = 2
		r.f.Names[r.buf] = c.name
		if got := rowOf(t, r.render(), 4); got != c.want {
			t.Errorf("%s\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// Under 'wrap' a tab that straddles the right edge is split: the cells before
// the edge stay on this row and the rest opens the next one, with every
// display column after it carrying straight across.
//
// Measured on vim 9.2.0321, 12 columns, 'wrap' 'tabstop' 8, four shapes. The
// last two have a tab wider than the whole window, at 'tabstop' 16, which vim
// splits the same way.
func TestTabSplitAcrossAWrap(t *testing.T) {
	for _, c := range []struct {
		name string
		ts   int
		line string
		pic  string
	}{
		{"tab crosses the edge", 8, "aaaaaaaaaa\tZbbbbbbbbbbbbbbb", `
		+------------+
		|aaaaaaaaaa  |
		|    Zbbbbbbb|
		|bbbbbbbb    |
		|~           |
		|~           |
		|~           |
		|~           |
		|~           |
		|~           |
		|            |
		+------------+
		`},
		{"tab starts in the last column", 8, "aaaaaaaaaaa\tZbbbbbbbbbb", `
		+------------+
		|aaaaaaaaaaa |
		|    Zbbbbbbb|
		|bbb         |
		|~           |
		|~           |
		|~           |
		|~           |
		|~           |
		|~           |
		|            |
		+------------+
		`},
		{"tab wider than the window", 16, "\tZbbbbbbbbbb", `
		+------------+
		|            |
		|    Zbbbbbbb|
		|bbb         |
		|~           |
		|~           |
		|~           |
		|~           |
		|~           |
		|~           |
		|            |
		+------------+
		`},
		{"wide tab after two letters", 16, "ab\tZcd", `
		+------------+
		|ab          |
		|    Zcd     |
		|~           |
		|~           |
		|~           |
		|~           |
		|~           |
		|~           |
		|~           |
		|            |
		+------------+
		`},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(10, 12, c.line)
			r.w.Opt.Wrap = true
			r.opt.B.TabStop = c.ts
			r.opt.G.Ruler = false
			wantPic(t, r.render(), c.pic)
		})
	}
}

// A last line the window only showed part of is not counted as displayed, so
// the ruler says Top where counting it says All.
//
// Measured on vim 9.2.0321, 40x8, 'wrap' 'display' truncate, over "a" to "e"
// and a hundred x's: line('w$') is 5, not 6, and the ruler reads Top.
func TestPartlyShownLastLineIsNotCounted(t *testing.T) {
	r := newRig(8, 40, "a", "b", "c", "d", "e", strings.Repeat("x", 100))
	r.w.Opt.Wrap = true
	wantPic(t, r.render(), `
		+----------------------------------------+
		|a                                       |
		|b                                       |
		|c                                       |
		|d                                       |
		|e                                       |
		|xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx|
		|@@@                                     |
		|                      1,1           Top |
		+----------------------------------------+
		`)
	if got := r.f.BotLine(r.w.ID); got != 5 {
		t.Errorf("BotLine = %d, vim's line('w$') is 5", got)
	}
}

// A match whose end runs past the last byte of a line highlights the cell
// after it, which is the newline. It is what makes a linewise Visual
// selection on a short line look different from a charwise one that stopped
// at the end of the text, and it is the only way an empty line in a selection
// shows at all.
//
// Measured on vim 9.2.0321, 20 columns, over "abc", an empty line and "ijk",
// reading screenattr() per cell: "Vjj" highlights four cells on row 1, one on
// row 2 and the whole of row 3, while "v$" on row 1 highlights three.
func TestVisualHighlightsTheNewlineCell(t *testing.T) {
	r := newRig(7, 20, "abc", "", "ijk")
	r.w.Opt.Wrap = false
	r.f.Visual = MatchList{
		{Line: 1, Start: 0, End: 4},
		{Line: 2, Start: 0, End: 1},
		{Line: 3, Start: 0, End: 4},
	}
	wantPic(t, r.renderHL(), `
		+--------------------+
		|abc                 |
		|                    |
		|ijk                 |
		|~                   |
		|~                   |
		|~                   |
		|          1,1   All |
		+--------------------+
		|aaaa................|
		|a...................|
		|aaaa................|
		|bbbbbbbbbbbbbbbbbbbb|
		|bbbbbbbbbbbbbbbbbbbb|
		|bbbbbbbbbbbbbbbbbbbb|
		|....................|
		+--------------------+
		legend: a=Visual b=EndOfBuffer
		`)
}

// A charwise selection that stops at the end of the text does not reach the
// newline cell, which is the control for the test above.
func TestCharwiseVisualStopsAtTheEndOfTheText(t *testing.T) {
	r := newRig(5, 20, "abc")
	r.w.Opt.Wrap = false
	r.f.Visual = MatchList{{Line: 1, Start: 0, End: 3}}
	wantPic(t, r.renderHL(), `
		+--------------------+
		|abc                 |
		|~                   |
		|~                   |
		|~                   |
		|          1,1   All |
		+--------------------+
		|aaa.................|
		|bbbbbbbbbbbbbbbbbbbb|
		|bbbbbbbbbbbbbbbbbbbb|
		|bbbbbbbbbbbbbbbbbbbb|
		|....................|
		+--------------------+
		legend: a=Visual b=EndOfBuffer
		`)
}
