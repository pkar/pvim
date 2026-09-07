package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/screen"
)

// newTestEditor is an editor over some text at the harness's screen size.
func newTestEditor(t *testing.T, text string) *editor {
	t.Helper()
	e, err := newEditor([]byte(text), "buf.txt", 40, 120, "")
	if err != nil {
		t.Fatal(err)
	}
	e.ed.SetScriptInput(true)
	return e
}

// TestCmdlineCollectsALine is the ":" prompt as a prompt: what is typed
// arrives, the editing keys do what they do, and Enter hands the line over.
//
// The two CTRL-W rows are measured, not reasoned: ":set nux" then CTRL-W then
// "foo" leaves vim saying "E518: Unknown option: foo", and so does
// ":set nu " with two trailing spaces, so CTRL-W crosses the white space
// before the word it deletes. testdata/keys/cmdline_ctrl_w_word covers both.
func TestCmdlineCollectsALine(t *testing.T) {
	cases := []struct {
		name string
		keys string
		want string
	}{
		{"plain", ":set nu", "set nu"},
		{"backspace", ":sets\x08", "set"},
		{"ctrl-u clears", ":junk\x15w", "w"},
		{"ctrl-w one word", ":set nu\x17", "set "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newTestEditor(t, "one\ntwo\n")
			if err := e.sess.Run(keysOf(t, c.keys)); err != nil {
				t.Fatal(err)
			}
			if e.sess.line == nil {
				t.Fatal("the prompt closed on its own")
			}
			if got := e.sess.line.String(); got != c.want {
				t.Errorf("line is %q, want %q", got, c.want)
			}
		})
	}
}

// TestCmdlineEscapeAbandons, and so does a backspace over the last character,
// which is the one that surprises people and is vim's.
func TestCmdlineEscapeAbandons(t *testing.T) {
	for _, keys := range []string{":set nu\x1b", ":x\x08\x08"} {
		e := newTestEditor(t, "one\n")
		if err := e.sess.Run(keysOf(t, keys)); err != nil {
			t.Fatal(err)
		}
		if e.sess.line != nil {
			t.Errorf("%q left the prompt open holding %q", keys, e.sess.line.String())
		}
	}
}

// TestCmdlineVisualPrefix: ":" in visual mode gives ":'<,'>", which is how the
// vimrc's own JsonPretty is reached from a selection.
//
// Measured: "Vj:s/^/X/" and "vj:s/^/X/" both mark the first two lines of the
// file and no others, under vim 9.2.0321 through cmd/oracle.
func TestCmdlineVisualPrefix(t *testing.T) {
	e := newTestEditor(t, "one\ntwo\nthree\n")
	if err := e.sess.Run(keysOf(t, "Vj:")); err != nil {
		t.Fatal(err)
	}
	if e.sess.line == nil {
		t.Fatal("no prompt")
	}
	if got := e.sess.line.String(); got != "'<,'>" {
		t.Errorf("prompt holds %q, want %q", got, "'<,'>")
	}
}

// TestCmdlineCountPrefix: "3:" is three lines starting here, which vim spells
// ".,.+2".
//
// Measured the same way: "3:s/^/X/" marks three lines and "1:s/^/X/" marks
// one.
func TestCmdlineCountPrefix(t *testing.T) {
	e := newTestEditor(t, "one\ntwo\nthree\nfour\n")
	if err := e.sess.Run(keysOf(t, "3:")); err != nil {
		t.Fatal(err)
	}
	if got := e.sess.line.String(); got != ".,.+2" {
		t.Errorf("prompt holds %q, want %q", got, ".,.+2")
	}
}

// TestWindowCommandsInOneWindow holds the CTRL-W family to what vim says with
// a single window open, measured through cmd/oracle.
func TestWindowCommandsInOneWindow(t *testing.T) {
	cases := []struct {
		keys string
		msg  string
		fail bool
	}{
		{keys: "\x17w"},
		{keys: "\x17p"},
		{keys: "\x17j"},
		{keys: "\x17="},
		{keys: "\x17+"},
		{keys: "\x17\x1b"},
		{keys: "\x17o", msg: "Already only one window"},
		{keys: "\x17P", msg: "E441: There is no preview window"},
		{keys: "\x17c", msg: "E444: Cannot close last window"},
		// CTRL-W v was on the loud-failure list until the split landed. It
		// splits now and says nothing, which is what vim does with one window
		// on the screen; the row stays so that the family is still counted
		// here and not just where the split is written.
		{keys: "\x17v"},
	}
	for _, c := range cases {
		t.Run(strings.TrimPrefix(c.keys, "\x17"), func(t *testing.T) {
			e := newTestEditor(t, "one\ntwo\n")
			err := e.sess.Run(keysOf(t, c.keys))
			switch {
			case c.fail && err == nil:
				t.Fatal("a CTRL-W command this editor has not written passed silently")
			case c.fail:
				return
			case err != nil:
				t.Fatalf("CTRL-W %q: %v", c.keys[1:], err)
			}
			if got := e.ed.Message(); got != c.msg {
				t.Errorf("CTRL-W %q said %q, vim says %q", c.keys[1:], got, c.msg)
			}
		})
	}
}

// TestTextRowsLeavesRoomForTheCommandLine. The text area is the screen less
// 'cmdheight', and a screen too short for that still gets one row of text
// rather than none.
func TestTextRowsLeavesRoomForTheCommandLine(t *testing.T) {
	e := newTestEditor(t, "one\n")
	cases := []struct{ rows, height int }{{40, 39}, {24, 23}, {2, 1}, {1, 1}}
	for _, c := range cases {
		if got := textRows(c.rows, e.opt); got != c.height {
			t.Errorf("a %d-row screen gives %d rows of text, want %d", c.rows, got, c.height)
		}
	}
	e.opt.G.CmdHeight = 3
	if got := textRows(40, e.opt); got != 37 {
		t.Errorf("cmdheight=3 on 40 rows gives %d rows of text, want 37", got)
	}
}

// TestResizeMovesTheWindow: SIGWINCH has to reach the window, or H and L go on
// answering for the size the terminal used to be.
func TestResizeMovesTheWindow(t *testing.T) {
	e := newTestEditor(t, string(numbered(60)))
	e.resize(11, 40)
	if got := e.sess.win().View.Height; got != 10 {
		t.Errorf("window is %d rows after an 11-row resize, want 10", got)
	}
	if got := e.sess.win().View.Width; got != 40 {
		t.Errorf("window is %d cols after a 40-column resize, want 40", got)
	}
}

// TestDrawPutsTheBufferOnTheScreen is the smallest possible check that the
// compositing seam is joined: the text is in the grid and the rows past the
// end of the buffer carry vim's "~".
func TestDrawPutsTheBufferOnTheScreen(t *testing.T) {
	e := newTestEditor(t, "alpha\nbeta\n")
	s := e.draw(6, 20)
	rows := gridRows(t, s)
	for i, want := range []string{"alpha", "beta", "~"} {
		if rows[i] != want {
			t.Errorf("row %d is %q, want %q\n%s", i, rows[i], want, screen.Dump(&s.Grid))
		}
	}
}

// gridRows is the grid as one trimmed string per row.
//
// screen.Dump draws a box around the grid and a legend under it, which is what
// a screendump test wants to read and not what a test asserting one row wants
// to match against, so the frame comes off here.
func gridRows(t *testing.T, s *screen.Screen) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(screen.Dump(&s.Grid), "\n") {
		// The dump is the characters framed in "|", then, when the screen has
		// more than one highlight on it, the same rows again as legend
		// letters. Only the first block is wanted here.
		if !strings.HasPrefix(l, "|") || len(out) == s.Grid.Rows {
			continue
		}
		out = append(out, strings.TrimRight(strings.Trim(l, "|"), " "))
	}
	if len(out) != s.Grid.Rows {
		t.Fatalf("the dump has %d rows, the grid has %d", len(out), s.Grid.Rows)
	}
	return out
}

// TestDrawPutsTheCursorWhereTheCursorIs, in cells, which is the one thing a
// frontend cannot work out for itself.
func TestDrawPutsTheCursorWhereTheCursorIs(t *testing.T) {
	e := newTestEditor(t, "alpha\nbeta\ngamma\n")
	if err := e.sess.Run(keysOf(t, "jll")); err != nil {
		t.Fatal(err)
	}
	s := e.draw(10, 20)
	if s.CursorRow != 1 || s.CursorCol != 2 {
		t.Errorf("cursor at row %d col %d, want 1 and 2", s.CursorRow, s.CursorCol)
	}
	if s.CursorShape != screen.CursorBlock {
		t.Error("normal mode wants a block cursor")
	}
}

// TestVimrcErrorsDoNotStopStartup is the rule in one test: a line the
// loader does not understand is a message and the rest of the file still runs.
func TestVimrcErrorsDoNotStopStartup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vimrc")
	rc := "set shiftwidth=7\nNotAnEditorCommand foo\nset tabstop=5\n"
	if err := os.WriteFile(path, []byte(rc), 0o644); err != nil {
		t.Fatal(err)
	}

	e := newTestEditor(t, "one\n")
	e.loadVimrc(path, false)

	if e.opt.B.ShiftWidth != 7 {
		t.Errorf("'shiftwidth' is %d, want 7: the line before the bad one did not run", e.opt.B.ShiftWidth)
	}
	if e.opt.B.TabStop != 5 {
		t.Errorf("'tabstop' is %d, want 5: startup stopped at the bad line", e.opt.B.TabStop)
	}
	var said bool
	for _, m := range e.ed.Messages() {
		if strings.Contains(m, "E492") {
			said = true
		}
	}
	if !said {
		t.Errorf("nothing on the message line about the bad line; messages were %q", e.ed.Messages())
	}
}

// TestVimrcOptionsReachTheModeMachine. An option that parses and does not
// reach the editor is an option that does nothing, which is the failure the
// the gate is looking for; 'shiftwidth' is the one with a visible answer,
// because ">>" indents by it.
func TestVimrcOptionsReachTheModeMachine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vimrc")
	if err := os.WriteFile(path, []byte("set shiftwidth=7 expandtab\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := newTestEditor(t, "one\n")
	e.loadVimrc(path, false)
	if err := e.sess.Run(keysOf(t, ">>")); err != nil {
		t.Fatal(err)
	}
	if got, want := string(e.buf.Line(1)), "       one"; got != want {
		t.Errorf(">> gave %q, want %q: 'shiftwidth' did not reach the mode machine", got, want)
	}
}

// TestNoVimrcIsNotAnError: --clean arriving by another route, and a new
// machine's first launch.
func TestNoVimrcIsNotAnError(t *testing.T) {
	e := newTestEditor(t, "one\n")
	e.loadVimrc(filepath.Join(t.TempDir(), "nothing-here"), false)
	if got := e.ed.Messages(); len(got) != 0 {
		t.Errorf("a missing vimrc said %q", got)
	}
}

// TestReadFileOnAMissingFileOpensABuffer: "pvim newfile" is half of every
// editing session and it must not be an error.
func TestReadFileOnAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")
	data, name, err := readFile(path)
	if err != nil {
		t.Fatalf("readFile on a missing file = %v, want nil", err)
	}
	if data != nil {
		t.Errorf("readFile on a missing file read %d bytes", len(data))
	}
	if name != path {
		t.Errorf("readFile named the buffer %q, want %q", name, path)
	}
}

// TestReadFileRefusesADirectory, for now puts the tree browser behind
// one. Silently opening a directory as text is how netrw's replacement gets
// forgotten about.
func TestReadFileRefusesADirectory(t *testing.T) {
	if _, _, err := readFile(t.TempDir()); err == nil {
		t.Error("readFile on a directory = nil, want an error")
	}
}

// realVimrc is the config this editor exists to run. It is read from where it
// lives and never copied into the repository, for the same reason cmd/oracle
// reads it rather than transcribing it: a copy goes stale the first time a
// line in the real file changes, and then the test is certifying an editor
// against a config nobody has.
// It is the copy internal/vimrc keeps rather than ~/.vimrc, so the gate grades
// the same bytes every run; see internal/vimrc/load_test.go for why. Absolute,
// because this path is also handed to vim as a ":source" argument.
var realVimrc = absPath(filepath.Join("..", "..", "internal", "vimrc", "testdata", "vimrc"))

// absPath makes a path absolute or dies trying; a relative fixture handed to a
// subprocess is a bug that only shows up on the machine with a different cwd.
func absPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		panic(err)
	}
	return a
}

// TestTheRealVimrcLoadsClean is the gate's vimrc clause, from the frontend's
// side: the 243-line file this editor exists to run loads with nothing on the
// message line, and the settings the gate names come out right.
//
// The values here are what the file says, and they are the ones worth checking
// because each is a different path through the loader: 'tabstop' is a plain
// set and 'shiftwidth' another that disagrees with it, 'scrolloff' is set to 3
// and then guarded by an ":if !&scrolloff" whose block sets 1 and must not be
// taken, JsonPretty is the one ":command!" in the file, and nofrils-dark is
// the ":colorscheme".
func TestTheRealVimrcLoadsClean(t *testing.T) {
	if _, err := os.Stat(realVimrc); err != nil {
		t.Skipf("no vimrc at %s", realVimrc)
	}
	e := newTestEditor(t, "one\n")
	e.loadVimrc(realVimrc, false)

	if msgs := e.ed.Messages(); len(msgs) != 0 {
		t.Errorf("the vimrc said %q on the message line; the gate is that it says nothing", msgs)
	}
	if got := e.opt.B.TabStop; got != 2 {
		t.Errorf("'tabstop' is %d, the vimrc says 2", got)
	}
	if got := e.opt.B.ShiftWidth; got != 4 {
		t.Errorf("'shiftwidth' is %d, the vimrc says 4", got)
	}
	if got := e.opt.ScrollOffValue(); got != 3 {
		t.Errorf("'scrolloff' is %d, the vimrc says 3 and the \"if !&scrolloff\" under it must not be taken", got)
	}
	if _, ok := e.ctx.Cmds["JsonPretty"]; !ok {
		t.Errorf("no JsonPretty; the vimrc defines it and it is the whole of the user-command feature")
	}
	if e.colorscheme != "nofrils-dark" {
		t.Errorf("colorscheme is %q, the vimrc says nofrils-dark", e.colorscheme)
	}
	if len(e.maps) == 0 || len(e.autocmds) == 0 {
		t.Errorf("%d mappings and %d autocmds; the vimrc has both", len(e.maps), len(e.autocmds))
	}
}
