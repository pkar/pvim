package finder

import (
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/key"
)

func items(paths ...string) []Item {
	out := make([]Item, len(paths))
	for i, p := range paths {
		out[i] = Item{Display: p, Path: "/root/" + p}
	}
	return out
}

func typeIn(f *Finder, s string) {
	for _, r := range s {
		f.Key(key.Rune(r))
	}
}

func newTestFinder() *Finder {
	f := New("/root", 40)
	f.SetItems(Files, items("internal/hue/hue_verify.go", "cmd/pvim/main.go", "README.md", "internal/mode/mode.go"))
	f.SetItems(Buffers, items("main.go", "notes.md"))
	f.SetItems(MRUFiles, items("last.go"))
	return f
}

func TestTypingNarrowsAndCRAccepts(t *testing.T) {
	f := newTestFinder()
	typeIn(f, "hueveri")
	if q := f.Query(); q != "hueveri" {
		t.Fatalf("query is %q", q)
	}
	sel, ok := f.Selected()
	if !ok {
		t.Fatal("nothing selected after typing hueveri")
	}
	if sel.Display != "internal/hue/hue_verify.go" {
		t.Fatalf("selected %q", sel.Display)
	}
	r := f.Key(key.Key{Special: key.KeyCR})
	if r.Action != Open || r.Item.Path != "/root/internal/hue/hue_verify.go" {
		t.Fatalf("CR gave %+v", r)
	}
}

func TestPromptIsCtrlpsPrompt(t *testing.T) {
	f := newTestFinder()
	if got := f.Prompt(); got != ">>> _" {
		t.Errorf("empty prompt is %q, want %q", got, ">>> _")
	}
	typeIn(f, "ab")
	if got := f.Prompt(); got != ">>> ab_" {
		t.Errorf("prompt is %q", got)
	}
}

// TestLinesAreBottomToTop pins ctrlp's default match window order: the best
// match is the LAST line, nearest the prompt, and it is the one marked.
func TestLinesAreBottomToTop(t *testing.T) {
	f := newTestFinder()
	typeIn(f, "go")
	lines := f.Lines()
	if len(lines) < 2 {
		t.Fatalf("only %d lines", len(lines))
	}
	last := string(lines[len(lines)-1])
	if !strings.HasPrefix(last, "> ") {
		t.Errorf("the last line is %q, and the selection marker belongs on it", last)
	}
	for _, l := range lines[:len(lines)-1] {
		if strings.HasPrefix(string(l), "> ") {
			t.Errorf("a second line is marked: %q", l)
		}
	}
	sel, _ := f.Selected()
	if !strings.HasSuffix(last, sel.Display) {
		t.Errorf("the marked line %q is not the selection %q", last, sel.Display)
	}
}

func TestNoMatchSaysSo(t *testing.T) {
	f := newTestFinder()
	typeIn(f, "zzzzz")
	if _, ok := f.Selected(); ok {
		t.Fatal("something was selected with no matches")
	}
	lines := f.Lines()
	if len(lines) != 1 || !strings.Contains(string(lines[0]), "NO ENTRIES") {
		t.Errorf("empty match window drew %q", lines)
	}
	// CR with nothing matched closes rather than opening a file that is not
	// there.
	if r := f.Key(key.Key{Special: key.KeyCR}); r.Action != Close {
		t.Errorf("CR with no match gave %v", r.Action)
	}
}

func TestMovementKeys(t *testing.T) {
	f := newTestFinder()
	typeIn(f, "o") // matches everything with an o in it
	n := len(f.Matches())
	if n < 3 {
		t.Fatalf("only %d matches to move through", n)
	}
	first, _ := f.Selected()

	// CTRL-K walks away from the best match, which is upward on a
	// bottom-to-top window, and CTRL-J walks back.
	f.Key(key.Ctrl('k'))
	second, _ := f.Selected()
	if second.Display == first.Display {
		t.Error("CTRL-K did not move")
	}
	f.Key(key.Ctrl('j'))
	if back, _ := f.Selected(); back.Display != first.Display {
		t.Error("CTRL-J did not come back")
	}
	// CTRL-N and CTRL-P move as well, which is where this departs from ctrlp
	// -- there they are the prompt history.
	f.Key(key.Ctrl('p'))
	if p, _ := f.Selected(); p.Display != second.Display {
		t.Error("CTRL-P did not move like CTRL-K")
	}
	f.Key(key.Ctrl('n'))
	if nn, _ := f.Selected(); nn.Display != first.Display {
		t.Error("CTRL-N did not move like CTRL-J")
	}
	// And neither end wraps, because ctrlp's PrtSelectMove is a bare norm! j.
	for i := 0; i < n+5; i++ {
		f.Key(key.Ctrl('k'))
	}
	if got, _ := f.Selected(); got.Display != f.Matches()[len(f.Matches())-1].Display {
		t.Error("CTRL-K past the end wrapped")
	}
	for i := 0; i < n+5; i++ {
		f.Key(key.Ctrl('j'))
	}
	if got, _ := f.Selected(); got.Display != f.Matches()[0].Display {
		t.Error("CTRL-J past the start wrapped")
	}
}

func TestOpenKeys(t *testing.T) {
	cases := []struct {
		k    key.Key
		want Action
	}{
		{key.Key{Special: key.KeyCR}, Open},
		{key.Ctrl('t'), OpenTab},
		{key.Ctrl('v'), OpenVSplit},
		{key.Ctrl('x'), OpenSplit},
	}
	for _, c := range cases {
		f := newTestFinder()
		typeIn(f, "hueveri")
		r := f.Key(c.k)
		if r.Action != c.want {
			t.Errorf("%v gave %v, want %v", c.k, r.Action, c.want)
		}
		if r.Item.Display != "internal/hue/hue_verify.go" {
			t.Errorf("%v carried %q", c.k, r.Item.Display)
		}
	}
}

func TestCloseKeys(t *testing.T) {
	for _, k := range []key.Key{{Special: key.KeyEsc}, key.Ctrl('c'), key.Ctrl('g')} {
		f := newTestFinder()
		if r := f.Key(k); r.Action != Close {
			t.Errorf("%v gave %v, want Close", k, r.Action)
		}
	}
}

func TestModeCycleIsFilesBuffersMRU(t *testing.T) {
	f := newTestFinder()
	want := []Mode{Buffers, MRUFiles, Files}
	for _, w := range want {
		f.Key(key.Ctrl('f'))
		if f.Mode() != w {
			t.Fatalf("CTRL-F landed on %v, want %v", f.Mode(), w)
		}
	}
	back := []Mode{MRUFiles, Buffers, Files}
	for _, w := range back {
		f.Key(key.Ctrl('b'))
		if f.Mode() != w {
			t.Fatalf("CTRL-B landed on %v, want %v", f.Mode(), w)
		}
	}
	if got := []string{Files.String(), Buffers.String(), MRUFiles.String()}; got[0] != "files" || got[1] != "buffers" || got[2] != "mru files" {
		t.Errorf("mode names are %q", got)
	}
}

func TestModeSwitchSearchesTheOtherList(t *testing.T) {
	f := newTestFinder()
	typeIn(f, "notes")
	if len(f.Matches()) != 0 {
		t.Fatalf("notes matched a file: %v", f.Matches())
	}
	f.Key(key.Ctrl('f')) // buffers
	sel, ok := f.Selected()
	if !ok || sel.Display != "notes.md" {
		t.Fatalf("after CTRL-F the selection is %+v", sel)
	}
}

func TestEditingKeys(t *testing.T) {
	f := newTestFinder()
	typeIn(f, "abc")
	f.Key(key.Key{Special: key.KeyBS})
	if f.Query() != "ab" {
		t.Errorf("after backspace the query is %q", f.Query())
	}
	f.Key(key.Ctrl('u'))
	if f.Query() != "" {
		t.Errorf("after CTRL-U the query is %q", f.Query())
	}
	typeIn(f, "cmd/pvim/ma")
	f.Key(key.Ctrl('w'))
	if f.Query() != "cmd/pvim/" {
		t.Errorf("after CTRL-W the query is %q", f.Query())
	}
	f.Key(key.Ctrl('w'))
	if f.Query() != "cmd/" {
		t.Errorf("after a second CTRL-W the query is %q", f.Query())
	}
	// Backspace over a multi-byte character takes the whole rune.
	f.Key(key.Ctrl('u'))
	typeIn(f, "é")
	f.Key(key.Key{Special: key.KeyBS})
	if f.Query() != "" {
		t.Errorf("backspace left %q of a two-byte rune", f.Query())
	}
}

func TestCurrentFileIsNotOffered(t *testing.T) {
	f := newTestFinder()
	f.Current = "/root/README.md"
	f.SetMode(Files)
	for _, m := range f.Matches() {
		if m.Path == f.Current {
			t.Fatal("the file the finder was opened from is in its own list")
		}
	}
}

func TestHeightCapsTheResults(t *testing.T) {
	f := New("/root", 2)
	f.SetItems(Files, items("a.go", "b.go", "c.go", "d.go"))
	if n := len(f.Matches()); n != 2 {
		t.Errorf("with a height of 2 the finder kept %d matches", n)
	}
}

func TestRefreshKey(t *testing.T) {
	f := newTestFinder()
	if r := f.Key(key.Key{Special: key.KeyF5}); r.Action != Refresh {
		t.Errorf("F5 gave %v", r.Action)
	}
}

func TestMRU(t *testing.T) {
	var m MRU
	m.Max = 3
	for _, p := range []string{"/a", "/b", "/c", "/d"} {
		m.Push(p)
	}
	if got := m.List(); len(got) != 3 || got[0] != "/d" || got[2] != "/b" {
		t.Errorf("MRU is %q, want the last three newest first", got)
	}
	m.Push("/b")
	if got := m.List(); got[0] != "/b" || len(got) != 3 {
		t.Errorf("re-pushing did not move it to the front: %q", got)
	}
	before := m.List()
	m.Push("/b")
	if got := m.List(); len(got) != len(before) || got[0] != "/b" {
		t.Errorf("pushing the front entry again changed the list: %q", got)
	}
	m.Push("")
	if len(m.List()) != len(before) {
		t.Error("an empty name went into the list")
	}
}
