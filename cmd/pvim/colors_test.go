package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFindTheVimrcsColorscheme is the search that stands between the vimrc's
// line 5 and a screen in the wrong colours.
//
// nofrils-dark is under a package directory, ~/.vim/pack/pkar/start/nofrils/,
// which vim puts on 'runtimepath' during startup and "vim --clean" does not,
// so this is the entry in colorschemePaths that earns its keep.
func TestFindTheVimrcsColorscheme(t *testing.T) {
	got, ok := findColorscheme("nofrils-dark")
	if !ok {
		t.Skip("the nofrils package is not on this machine")
	}
	if !strings.HasSuffix(got, filepath.Join("colors", "nofrils-dark.vim")) {
		t.Errorf("found %q, which is not a colors/nofrils-dark.vim", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("found %q and it is not there: %v", got, err)
	}
}

// TestAColorschemeThatIsNotThereIsE185, which is the code vim prints. Measured
// "silent! colorscheme nosuchscheme" leaves
// "E185: Cannot find color scheme 'nosuchscheme'" on the message line.
func TestAColorschemeThatIsNotThereIsE185(t *testing.T) {
	if _, ok := findColorscheme("nosuchscheme"); ok {
		t.Fatal("a scheme called nosuchscheme was found, which makes the rest of this test meaningless")
	}
	e := newTestEditor(t, "one\n")
	dir := t.TempDir()
	rc := filepath.Join(dir, "vimrc")
	if err := os.WriteFile(rc, []byte("colorscheme nosuchscheme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.loadVimrc(rc, false)

	msgs := e.ed.Messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0], "E185: Cannot find color scheme 'nosuchscheme'") {
		t.Errorf("the message line says %q, want one line with E185 on it", msgs)
	}
	if e.colorscheme != "" {
		t.Errorf("colorscheme is %q; a scheme that was not found is not the scheme in use", e.colorscheme)
	}
}

// TestAColorschemeNameIsNotAPath. The name comes out of a config file and the
// search joins it into a glob, so a name with a separator in it would read a
// file outside the list. That is not a feature anybody asked for.
func TestAColorschemeNameIsNotAPath(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../../etc/passwd", "a/b"} {
		if got, ok := findColorscheme(name); ok {
			t.Errorf("findColorscheme(%q) found %q", name, got)
		}
	}
}

// TestSourcingStopsGoingRound is the guard on the loader now that it recurses:
// a colourscheme naming itself, or a file that sources itself, is one message
// and not a stack overflow.
func TestSourcingStopsGoingRound(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, "loop.vim")
	if err := os.WriteFile(rc, []byte("source "+rc+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newTestEditor(t, "one\n")
	e.loadVimrc(rc, false)

	msgs := e.ed.Messages()
	if len(msgs) == 0 {
		t.Fatal("a file that sources itself said nothing")
	}
	if !strings.Contains(msgs[0], "E169") {
		t.Errorf("it said %q, want E169 on the first line", msgs[0])
	}
}

// TestTheGuiAnswerFollowsIntoASourcedFile. Source() used to hand false to every
// file the vimrc sourced whatever the frontend was, which would have run the
// terminal half of a "so $MYGVIMRC" inside a window.
func TestTheGuiAnswerFollowsIntoASourcedFile(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "inner.vim")
	outer := filepath.Join(dir, "outer.vim")
	if err := os.WriteFile(inner, []byte("if has('gui_running')\n  set guifont=Monaco:h13\nendif\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outer, []byte("source "+inner+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, gui := range []bool{false, true} {
		e := newTestEditor(t, "one\n")
		e.loadVimrc(outer, gui)
		v, err := e.opt.Get("guifont")
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		if gui {
			want = "Monaco:h13"
		}
		if v.Str != want {
			t.Errorf("gui_running=%v: the sourced file left 'guifont' %q, want %q", gui, v.Str, want)
		}
	}
}
