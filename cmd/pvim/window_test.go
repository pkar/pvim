package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/screen"
)

// The window frontend, tested with no window.
//
// Everything here drives the real handler through a fake Client, which is what
// the Client interface is for: the editor behind the window and the editor
// behind the terminal are one struct, and the half that AppKit owns is
// internal/gui's to test. What cannot be reached from here is in the report.

// newWindowEditor is an editor and a started pump over some text, with the
// events going through the real gui.Handler.
func newWindowEditor(t *testing.T, text string) (*editor, *pump, *fakeClient) {
	t.Helper()
	e := newTestEditor(t, text)
	p := newPump(e)
	e.pump = p
	e.sess.getch = p.getch
	c := &fakeClient{rows: 40, cols: 120}
	t.Cleanup(p.stop)
	// The first event is what starts the loop goroutine, and internal/gui
	// sends a ResizeEvent before any key for exactly that reason.
	if err := p.handle(c, gui.ResizeEvent{Rows: 40, Cols: 120}); err != nil {
		t.Fatalf("the first event returned %v", err)
	}
	return e, p, c
}

// send pushes one key through the window's handler.
func send(t *testing.T, p *pump, c *fakeClient, keys string) error {
	t.Helper()
	for _, k := range keysOf(t, keys) {
		if err := p.handle(c, gui.KeyEvent{Key: k}); err != nil {
			return err
		}
	}
	return nil
}

// TestWindowEventsEditTheBuffer is the window frontend in one assertion: it
// runs the same editor the terminal does, and a keystroke that arrives as a
// gui.KeyEvent changes the buffer.
func TestWindowEventsEditTheBuffer(t *testing.T) {
	e, p, c := newWindowEditor(t, "alpha\nbeta\n")
	if err := send(t, p, c, "IX\x1b"); err != nil {
		t.Fatal(err)
	}
	if got, want := string(e.buf.Line(1)), "Xalpha"; got != want {
		t.Errorf("line 1 is %q, want %q", got, want)
	}
	if c.drawn == nil {
		t.Fatal("the handler painted nothing")
	}
}

// TestWindowQuitStopsTheLoop. A quit has to leave through the handler's return
// value, because that is the only thing that ends NSApp.run: a quit decided
// anywhere else would leave the window on screen until somebody pressed a key.
func TestWindowQuitStopsTheLoop(t *testing.T) {
	_, p, c := newWindowEditor(t, "alpha\n")
	err := send(t, p, c, ":q\r")
	if !errors.Is(err, errQuit) {
		t.Fatalf("\":q\" returned %v, want errQuit", err)
	}
}

// TestCloseEventOnACleanBufferQuits is Cmd-Q and the red button, both of which
// internal/gui delivers as a CloseEvent and neither of which it acts on itself.
func TestCloseEventOnACleanBufferQuits(t *testing.T) {
	_, p, c := newWindowEditor(t, "alpha\n")
	if err := p.handle(c, gui.CloseEvent{}); !errors.Is(err, errQuit) {
		t.Fatalf("CloseEvent on a clean buffer returned %v, want errQuit", err)
	}
}

// TestCloseEventOnAModifiedBufferPromptsAndCancels is the gate item:
// "Cmd-Q on a modified buffer prompts on the cmdline and C cancels."
//
// Both halves are here. The prompt is vim's own, the one internal/ex writes
// under 'confirm' and the one measured -- Save changes to
// "NAME"? / [Y]es, (N)o, (C)ancel: -- and a "C" leaves the editor exactly
// where it was with the change still in the buffer. The event that carries the
// answer is an ordinary keystroke arriving after the CloseEvent, which is what
// the pump in window.go exists to make possible.
func TestCloseEventOnAModifiedBufferPromptsAndCancels(t *testing.T) {
	e, p, c := newWindowEditor(t, "alpha\n")
	if err := send(t, p, c, "IX\x1b"); err != nil {
		t.Fatal(err)
	}
	if !e.modified() {
		t.Fatal("the buffer is not modified, so there is nothing to prompt about")
	}
	// 'confirm' is what turns E37 into a question. The vimrc sets it; a
	// --clean editor does not, so the test says so itself.
	if _, err := e.opt.ApplyLine("confirm", 0); err != nil {
		t.Fatal(err)
	}

	// The close is consumed by the prompt, so the handler returns nil: the
	// window stays up and AppKit delivers the answer as the next event.
	if err := p.handle(c, gui.CloseEvent{}); err != nil {
		t.Fatalf("CloseEvent with a prompt open returned %v, want nil", err)
	}
	if msg := e.ed.Message(); !strings.Contains(msg, "Save changes to") {
		t.Errorf("the message line says %q, want the 'confirm' question", msg)
	}

	// "c" and not "C": internal/ex offers the choices as "ync" and
	// session.prompt matches them by byte, so the letter that answers is the
	// lower-case one. A key that is not a choice is ignored and the question
	// stays up, which is vim's behaviour at this prompt and is why this test
	// goes on to check that the prompt really closed.
	if err := p.handle(c, gui.KeyEvent{Key: key.Rune('c')}); err != nil {
		t.Fatalf("answering c returned %v, want nil: a cancel is not a quit", err)
	}
	if !e.modified() {
		t.Error("the cancel lost the change")
	}
	if got, want := string(e.buf.Line(1)), "Xalpha"; got != want {
		t.Errorf("line 1 is %q, want %q", got, want)
	}
	// The prompt is gone, so an ordinary key is an ordinary key again.
	if err := send(t, p, c, "x"); err != nil {
		t.Fatalf("the key after the cancel returned %v", err)
	}
	if got, want := string(e.buf.Line(1)), "alpha"; got != want {
		t.Errorf("after the cancel the prompt was still up: line 1 is %q, want %q", got, want)
	}
}

// TestCloseEventOnAModifiedBufferSaves is the other answer: "Y" writes and then
// quits, which is what makes Cmd-Q on an unsaved file do the thing a person
// pressing it means.
func TestCloseEventOnAModifiedBufferSaves(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := newEditor([]byte("alpha\n"), file, 40, 120, "confirm")
	if err != nil {
		t.Fatal(err)
	}
	e.ed.SetScriptInput(true)
	p := newPump(e)
	e.pump, e.sess.getch = p, p.getch
	t.Cleanup(p.stop)
	c := &fakeClient{rows: 40, cols: 120}
	if err := p.handle(c, gui.ResizeEvent{Rows: 40, Cols: 120}); err != nil {
		t.Fatal(err)
	}
	if err := send(t, p, c, "IX\x1b"); err != nil {
		t.Fatal(err)
	}
	if err := p.handle(c, gui.CloseEvent{}); err != nil {
		t.Fatalf("CloseEvent returned %v, want nil while the prompt is up", err)
	}
	if err := p.handle(c, gui.KeyEvent{Key: key.Rune('y')}); !errors.Is(err, errQuit) {
		t.Fatalf("answering y returned %v, want errQuit", err)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "Xalpha\n" {
		t.Errorf("the file holds %q, want %q", got, "Xalpha\n")
	}
}

// TestAResizeWhileThePromptIsUpDoesNotAnswerIt. An event that is not a key
// arrives at getch like any other and must not be mistaken for one: a window
// dragged while ":q" is asking about a modified buffer would otherwise answer
// the question with whatever the fallback choice is, which for 'confirm' is
// cancel and for the ":4,2d" swap prompt is "n".
func TestAResizeWhileThePromptIsUpDoesNotAnswerIt(t *testing.T) {
	e, p, c := newWindowEditor(t, "alpha\n")
	if err := send(t, p, c, "IX\x1b"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.opt.ApplyLine("confirm", 0); err != nil {
		t.Fatal(err)
	}
	if err := p.handle(c, gui.CloseEvent{}); err != nil {
		t.Fatal(err)
	}
	c.rows, c.cols = 30, 100
	if err := p.handle(c, gui.ResizeEvent{Rows: 30, Cols: 100}); err != nil {
		t.Fatalf("a resize during the prompt returned %v", err)
	}
	if msg := e.ed.Message(); !strings.Contains(msg, "Save changes to") {
		t.Errorf("the resize answered the prompt; the message line says %q", msg)
	}
	if got := e.sess.win().View.Height; got != 29 {
		t.Errorf("the resize was dropped: the window is %d rows, want 29", got)
	}
	if err := p.handle(c, gui.KeyEvent{Key: key.Rune('c')}); err != nil {
		t.Fatal(err)
	}
}

// TestWindowTitle is the shape vim uses, measured off a pty with
// 'title' set: "bar.txt (~/.pvimtitle) - VIM", "foo.txt + (/tmp/x) - VIM" for a
// modified buffer, and "[No Name] - VIM" with no file at all. Same shape, this
// editor's name.
func TestWindowTitle(t *testing.T) {
	home := t.TempDir()
	old := userHome
	userHome = func() string { return home }
	t.Cleanup(func() { userHome = old })

	e, err := newEditor([]byte("alpha\n"), filepath.Join(home, "work", "bar.txt"), 40, 120, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "bar.txt (~" + string(filepath.Separator) + "work) - pvim"
	if got := e.windowTitle(); got != want {
		t.Errorf("title is %q, want %q", got, want)
	}
	if err := e.sess.Run(keysOf(t, "IX\x1b")); err != nil {
		t.Fatal(err)
	}
	want = "bar.txt + (~" + string(filepath.Separator) + "work) - pvim"
	if got := e.windowTitle(); got != want {
		t.Errorf("modified title is %q, want %q", got, want)
	}

	empty, err := newEditor(nil, "", 40, 120, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := empty.windowTitle(), "[No Name] - pvim"; got != want {
		t.Errorf("nameless title is %q, want %q", got, want)
	}
}

// TestTheTitleIsOnlySetWhenItChanged. A title crosses to the run loop thread
// and, in the terminal, goes down the wire as an OSC 2 sequence; a redraw
// happens on every keystroke and re-sending the same string sixty times a
// second is sixty pointless hops.
func TestTheTitleIsOnlySetWhenItChanged(t *testing.T) {
	e := newTestEditor(t, "alpha\n")
	c := &titleCounter{fakeClient: fakeClient{rows: 40, cols: 120}}
	if err := e.repaint(c); err != nil {
		t.Fatal(err)
	}
	first := c.titles
	if first != 1 {
		t.Fatalf("the first paint set the title %d times, want 1", first)
	}
	if err := e.repaint(c); err != nil {
		t.Fatal(err)
	}
	if c.titles != 1 {
		t.Errorf("an unchanged title was set again: %d calls", c.titles)
	}
	// A change to the buffer moves the "+" into it, and then it must be sent.
	if err := e.sess.Run(keysOf(t, "IX\x1b")); err != nil {
		t.Fatal(err)
	}
	if err := e.repaint(c); err != nil {
		t.Fatal(err)
	}
	if c.titles != 2 {
		t.Errorf("the modified marker did not reach the title: %d calls", c.titles)
	}
}

type titleCounter struct {
	fakeClient
	titles int
}

func (c *titleCounter) SetTitle(s string) error { c.titles++; return c.fakeClient.SetTitle(s) }

// TestTheUnfocusedCaretIsHollow. vim draws a hollow caret in a window that is
// not the key window whatever mode it is in, and it is the only way to tell at
// a glance which of two editors a keystroke would go to.
func TestTheUnfocusedCaretIsHollow(t *testing.T) {
	_, p, c := newWindowEditor(t, "alpha\n")
	if got := c.drawn.CursorShape; got != screen.CursorBlock {
		t.Errorf("the focused caret is %v, want a block", got)
	}
	if err := p.handle(c, gui.FocusEvent{Focused: false}); err != nil {
		t.Fatal(err)
	}
	if got := c.drawn.CursorShape; got != screen.CursorHollow {
		t.Errorf("the unfocused caret is %v, want hollow", got)
	}
	if err := p.handle(c, gui.FocusEvent{Focused: true}); err != nil {
		t.Fatal(err)
	}
	if got := c.drawn.CursorShape; got != screen.CursorBlock {
		t.Errorf("the refocused caret is %v, want a block", got)
	}
}

// TestGUIFontIsCheckedBeforeItIsAccepted. Vim answers a font it cannot load
// with E596 and keeps the option's old value; a window left with no face at
// all would be a window nobody could read the error on.
func TestGUIFontIsCheckedBeforeItIsAccepted(t *testing.T) {
	e := newTestEditor(t, "alpha\n")
	if _, err := e.opt.ApplyLine("guifont=NoSuchFamily:h13", 0); err != nil {
		t.Fatal(err)
	}
	e.applyGUIFont()
	if e.guifont == "NoSuchFamily:h13" {
		t.Error("a font that cannot be loaded was accepted")
	}
	if msg := e.ed.Message(); !strings.HasPrefix(msg, "E596:") {
		t.Errorf("the message line says %q, want E596", msg)
	}
}

// TestGUIFontIsAppliedOnce. The check runs on every frame, which is what makes
// a ":set guifont=" from anywhere -- the vimrc, a colon command, a sourced
// file, a ":g" -- take effect with one call site instead of four. It must
// therefore not re-read the font file sixty times a second.
func TestGUIFontIsAppliedOnce(t *testing.T) {
	e := newTestEditor(t, "alpha\n")
	if _, err := e.opt.ApplyLine("guifont=Monaco:h13", 0); err != nil {
		t.Fatal(err)
	}
	e.applyGUIFont()
	if e.guifont != "Monaco:h13" {
		t.Fatalf("'guifont' was not applied: %q", e.guifont)
	}
	before := e.ed.Message()
	e.applyGUIFont()
	if e.ed.Message() != before {
		t.Errorf("the second call said something: %q", e.ed.Message())
	}
}

// TestABufferSwapCarriesTheEditorWithIt. Without cmd/pvim supplying
// ex.Context.Open, internal/ex takes its own fallback: it builds a new
// mode.Editor over the new buffer and assigns it to Context.Ed, and nothing
// here read Context.Ed back, so the keys went on editing the buffer that was
// no longer on screen while ":w" wrote the one that was. Measured on "alpha":
// ":e g.txt" then x leaves vim's buffer alpha and left this one "lpha".
func TestABufferSwapCarriesTheEditorWithIt(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "f.txt")
	second := filepath.Join(dir, "g.txt")
	if err := os.WriteFile(first, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := newEditor([]byte("alpha\n"), first, 40, 120, "")
	if err != nil {
		t.Fatal(err)
	}
	e.ed.SetScriptInput(true)
	if err := e.sess.Run(keysOf(t, ":e "+second+"\rIHELLO\x1b:w\r")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(first); err != nil || string(got) != "alpha\n" {
		t.Errorf("the first file is %q (%v), want it untouched", got, err)
	}
	got, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("the second file was never written: %v", err)
	}
	if string(got) != "HELLO\n" {
		t.Errorf("the second file holds %q, want %q", got, "HELLO\n")
	}
}

// TestABufferSwapKeepsTheRegisters. internal/mode has no setter for its buffer,
// so a swap builds a new Editor and the register file has to be carried across
// by hand. Vim keeps registers across a ":e" and losing them would be a new
// difference nobody registered.
func TestABufferSwapKeepsTheRegisters(t *testing.T) {
	dir := t.TempDir()
	e, err := newEditor([]byte("alpha\nbeta\n"), filepath.Join(dir, "f.txt"), 40, 120, "")
	if err != nil {
		t.Fatal(err)
	}
	e.ed.SetScriptInput(true)
	if err := e.sess.Run(keysOf(t, "yy:e "+filepath.Join(dir, "g.txt")+"\rp")); err != nil {
		t.Fatal(err)
	}
	if got := string(e.buf.Bytes()); !strings.Contains(got, "alpha") {
		t.Errorf("the new buffer holds %q; the yank did not survive the ':e'", got)
	}
}

// TestTheEditMessageStillReachesTheMessageLine.
//
// internal/ex says `"g.txt" [New]` through the hooks it was given at startup,
// which are bound to the mode machine this editor began with; a ":e" swaps
// that machine for one over the new buffer, so the message and the machine the
// frontend draws from come apart for exactly one command. The message line
// falls back to the sink, which is what a person sees and what makes ":e" on a
// new file say something rather than nothing.
func TestTheEditMessageStillReachesTheMessageLine(t *testing.T) {
	e := newTestEditor(t, "alpha\n")
	if err := e.ctx.RunLine("edit " + escapeExArg(filepath.Join(t.TempDir(), "g.txt"))); err != nil {
		t.Fatal(err)
	}
	if got := e.message(); !strings.Contains(got, "g.txt") {
		t.Errorf("the message line says %q after a :e, want the file it opened in it", got)
	}
	// And the keys go to the buffer that is now on the screen, which is the
	// half the message hides: before ex.Context.Open existed, ":e" left the
	// frontend editing the old buffer while ":w" wrote the new one.
	if got := e.cur().Name; !strings.HasSuffix(got, "g.txt") {
		t.Errorf("the current buffer is %q after a :e", got)
	}
}

// TestResizeToOneCellAndBack is later work's resize gate, from the editor's side.
//
// internal/gui's TestResizeToNothingIsOneCell is the other half: a real
// NSWindow dragged to one point square, clamped to one cell, and back. What it
// cannot say is whether the editor survives being handed that grid, and one
// cell is where every off-by-one in the layout comes due at once -- a text
// region of zero rows once the command line has taken its own, a status line
// with nowhere to go, a cursor to place inside a window of one column, and a
// horizontal scroll that has to hold a 78-column line in it.
//
// So it types at every size rather than only resizing: a key at 1x1 that
// panics is the failure this is looking for, and a key at 1x1 that quietly
// does nothing is not one -- vim in a one-cell window does very little either.
// The assertion at the end is that the buffer is what the same keys produce at
// a normal size, which is what says the small sizes were survived rather than
// merely not crashed in.
func TestResizeToOneCellAndBack(t *testing.T) {
	const line = "the quick brown fox jumps over the lazy dog and keeps on going past column eighty"
	e, p, c := newWindowEditor(t, line+"\n"+line+"\n"+line+"\n")

	sizes := []struct{ rows, cols int }{
		{40, 120}, {1, 1}, {1, 120}, {40, 1}, {2, 2}, {1, 1}, {40, 120},
	}
	for _, s := range sizes {
		c.rows, c.cols = s.rows, s.cols
		if err := p.handle(c, gui.ResizeEvent{Rows: s.rows, Cols: s.cols}); err != nil {
			t.Fatalf("resizing to %dx%d returned %v", s.rows, s.cols, err)
		}
		// Motions that reach for a window: G and H are answered out of the
		// layout, CTRL-F and zl out of the scroll arithmetic, and $ off the
		// end of a line that does not fit.
		if err := send(t, p, c, "GggH$\x06zl"); err != nil {
			t.Fatalf("typing at %dx%d returned %v", s.rows, s.cols, err)
		}
		if c.drawn == nil {
			t.Fatalf("nothing was drawn at %dx%d", s.rows, s.cols)
		}
	}

	// Back at a real size the editor still edits, and the buffer is untouched
	// by everything above, none of which changes a byte.
	if err := send(t, p, c, "ggIX\x1b"); err != nil {
		t.Fatal(err)
	}
	if got, want := string(e.buf.Line(1)), "X"+line; got != want {
		t.Errorf("line 1 is %q, want %q", got, want)
	}
	if got := e.buf.LineCount(); got != 3 {
		t.Errorf("the buffer has %d lines, want 3", got)
	}
}
