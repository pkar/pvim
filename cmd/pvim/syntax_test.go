package main

import (
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/screen"
	"github.com/pkar/pvim/internal/text"
)

// The glue between internal/syntax and the highlight table: which colour a
// syntax group comes out, and what ":syntax on" and ":syntax off" do.
//
// internal/syntax has the gate against vim; this is about the two things that
// only exist here, resolving a `hi def link` chain against a colourscheme and
// dropping a group that resolved to Normal.

// colours builds a table with a few groups defined, the way a colourscheme
// would, and the "has this been defined" answer that goes with it.
func colours(defined ...string) (*screen.Table, func(string) bool) {
	t := screen.NewTable()
	set := map[string]bool{}
	for i, name := range defined {
		t.Set(name, screen.Highlight{FG: screen.RGB{R: uint8(i + 1)}})
		set[name] = true
	}
	return t, func(n string) bool { return set[n] }
}

func TestSyntaxResolvesLinkChains(t *testing.T) {
	tbl, defined := colours("Comment", "Statement")
	s := newSyntaxes(tbl, defined)
	if err := s.command("enable"); err != nil {
		t.Fatal(err)
	}

	b := text.Read([]byte("package main\n\n// a comment\n"))
	s.attach(b, "go")
	bs := s.bufs[b]
	if bs == nil {
		t.Skip("no go syntax file in this runtime")
	}

	// goComment links to Comment, which the scheme defined, so the comment
	// draws in Comment and not in a group of its own.
	want := tbl.ID("Comment")
	spans := bs.SpansOn(3)
	if len(spans) != 1 || spans[0].HL != want {
		t.Fatalf("line 3 spans %v, want one in Comment (%d)", spans, want)
	}
	if spans[0].Start != 0 || spans[0].End != 12 {
		t.Errorf("the comment span is %d..%d, want 0..12", spans[0].Start, spans[0].End)
	}

	// goPackage links to Statement, which is also defined.
	if got := bs.SpansOn(1); len(got) == 0 || got[0].HL != tbl.ID("Statement") {
		t.Errorf("line 1 spans %v, want one in Statement", got)
	}
}

// A group whose chain ends at a name nobody gave colours resolves to Normal,
// and a span in Normal is not highlighting: leaving it out lets 'cursorline'
// and the rest of the base show through. On this vimrc's colourscheme that is
// most of them, which is why the screen looks nearly plain and is right.
func TestSyntaxDropsNormalSpans(t *testing.T) {
	tbl, defined := colours() // nothing defined at all
	s := newSyntaxes(tbl, defined)
	s.command("enable")

	b := text.Read([]byte("package main\n\n// a comment\n"))
	s.attach(b, "go")
	bs := s.bufs[b]
	if bs == nil {
		t.Skip("no go syntax file in this runtime")
	}
	for l := 1; l <= 3; l++ {
		if got := bs.SpansOn(l); len(got) != 0 {
			t.Errorf("line %d: %v, want nothing when no group has a colour", l, got)
		}
	}
}

func TestSyntaxCommand(t *testing.T) {
	tbl, defined := colours("Comment")
	s := newSyntaxes(tbl, defined)
	b := text.Read([]byte("// x\n"))

	if s.on {
		t.Fatal("syntax starts on")
	}
	s.attach(b, "go")
	if len(s.bufs) != 0 {
		t.Error("attach did something with syntax off")
	}

	if err := s.command("enable"); err != nil {
		t.Fatal(err)
	}
	s.attach(b, "go")
	if len(s.bufs) == 0 {
		t.Skip("no go syntax file in this runtime")
	}

	// changed and detach are the other two hooks: an edit throws away the
	// cache from a line down, and a:bd forgets the buffer.
	s.changed(b, 1)
	s.detach(b)
	if len(s.bufs) != 0 {
		t.Error("detach left the buffer attached")
	}
	s.attach(b, "go")

	if err := s.command("off"); err != nil {
		t.Fatal(err)
	}
	if s.on || len(s.bufs) != 0 {
		t.Error("off left state behind")
	}
	if src := s.sources(map[int]*text.Buffer{1: b}); src != nil {
		t.Error("off still hands the renderer a source")
	}

	if err := s.command("nonsense"); err == nil || !strings.Contains(err.Error(), "E475") {
		t.Errorf("bad argument gave %v, want E475", err)
	}
	if err := s.command(""); err == nil {
		t.Error("no argument gave no error")
	}
}

// A filetype with no syntax file is not an error and prints nothing: 767 of
// vim's 773 files are for languages nobody here edits, and a buffer with no
// rules draws in Normal exactly as vim draws it.
func TestSyntaxUnknownFiletypeIsQuiet(t *testing.T) {
	tbl, defined := colours("Comment")
	s := newSyntaxes(tbl, defined)
	s.command("enable")
	b := text.Read([]byte("x\n"))
	s.attach(b, "no-such-filetype-anywhere")
	if len(s.bufs) != 0 {
		t.Error("attached rules for a filetype with no file")
	}
	if got := s.refusals(); len(got) != 0 {
		t.Errorf("refusals: %v", got)
	}
}

// The go syntax file has to load with nothing refused. It is the file this
// editor is used on most and every refusal in it is a group that will not be
// coloured.
func TestSyntaxGoLoadsClean(t *testing.T) {
	tbl, defined := colours("Comment")
	s := newSyntaxes(tbl, defined)
	s.command("enable")
	b := text.Read([]byte("package main\n"))
	s.attach(b, "go")
	if len(s.bufs) == 0 {
		t.Skip("no go syntax file in this runtime")
	}
	if got := s.refusals(); len(got) != 0 {
		t.Errorf("syntax/go.vim refused %d things:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// sources answers per window, so two windows on two buffers get two
// highlighters and a window on a buffer with no rules gets none.
func TestSyntaxSourcesPerWindow(t *testing.T) {
	tbl, defined := colours("Comment")
	s := newSyntaxes(tbl, defined)
	s.command("enable")

	code := text.Read([]byte("package main\n"))
	plain := text.Read([]byte("hello\n"))
	s.attach(code, "go")
	s.attach(plain, "")
	if len(s.bufs) == 0 {
		t.Skip("no go syntax file in this runtime")
	}

	src := s.sources(map[int]*text.Buffer{1: code, 2: plain})
	if _, ok := src[1]; !ok {
		t.Error("the go window has no source")
	}
	if _, ok := src[2]; ok {
		t.Error("the plain window got one anyway")
	}
}

// TestSyntaxOnTheGrid is the whole path in one test: vim's own syntax file,
// this package's matcher, the link chain, the highlight table and the renderer,
// with a colourscheme that has real colours in it.
//
// The colours matter. nofrils-dark, which this vimrc loads, gives every group
// but Comment and Todo guifg=NONE guibg=NONE, so a correct highlighter under it
// draws almost nothing and looking at the screen tells you nothing at all. Here
// Comment, Statement and Type are three different colours, so a comment, a
// keyword and a type each land on their own mark in the dump.
func TestSyntaxOnTheGrid(t *testing.T) {
	e := newTestEditor(t, "package main\n// c\nvar x int\n")
	tbl, defined := colours("Comment", "Statement", "Type")
	e.hl = tbl

	s := newSyntaxes(tbl, defined)
	s.command("enable")
	s.attach(e.buf, "go")
	if len(s.bufs) == 0 {
		t.Skip("no go syntax file in this runtime")
	}

	w := e.tabs.Window()
	f := e.frame()
	f.Syntax = s.sources(map[int]*text.Buffer{w.ID: e.buf})

	sc := screen.NewScreen(6, 16)
	sc.HL = tbl
	sc.Render(f)
	got := screen.DumpScreen(sc)

	for _, want := range []string{"=Comment", "=Statement", "=Type"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s on the grid:\n%s", want, got)
		}
	}
	// The comment line is one run in one colour from its first cell.
	if !strings.Contains(got, "|// c") {
		t.Fatalf("the buffer is not on the grid:\n%s", got)
	}
}

// forTabs is what draw.go calls: the same answer as sources, over the windows
// of the current tab page.
func TestSyntaxForTabs(t *testing.T) {
	e := newTestEditor(t, "package main\n")
	tbl, defined := colours("Comment", "Statement")
	e.hl = tbl
	s := newSyntaxes(tbl, defined)
	s.command("enable")
	s.attach(e.buf, "go")
	if len(s.bufs) == 0 {
		t.Skip("no go syntax file in this runtime")
	}
	src := s.forTabs(e.tabs)
	if _, ok := src[e.tabs.Window().ID]; !ok {
		t.Errorf("forTabs gave %v, want a source for the current window", src)
	}
	s.command("off")
	if got := s.forTabs(e.tabs); got != nil {
		t.Errorf("forTabs with syntax off gave %v", got)
	}
}
