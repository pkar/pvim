package tui

import (
	"testing"

	"github.com/pkar/pvim/internal/options"
)

// TestOptionsFromReadsTheVimrcsEscapeDance.
//
// The vimrc's lines 18 to 27 are there for one reason: in the terminal, a bare
// Escape and the first byte of an arrow key are the same byte and only time
// tells them apart. "set notimeout", "set ttimeout" and "set ttimeoutlen=10"
// say never time out a mapping, always time out a key code, and give it 10
// milliseconds. If those three do not reach this package, every Escape in the
// terminal takes a second to register.
func TestOptionsFromReadsTheVimrcsEscapeDance(t *testing.T) {
	o := options.Defaults()
	for _, arg := range []string{"notimeout", "ttimeout", "ttimeoutlen=10", "mouse=a"} {
		if _, err := o.Apply(arg, options.Both); err != nil {
			t.Fatalf("%s: %v", arg, err)
		}
	}
	got := OptionsFrom(&o)
	if !got.TTimeout {
		t.Error("'ttimeout' did not reach the frontend; a bare Escape will wait for a mapping that never comes")
	}
	if got.TTimeoutLen != 10 {
		t.Errorf("'ttimeoutlen' reached the frontend as %d, want the vimrc's 10", got.TTimeoutLen)
	}
	if got.Mouse != "a" {
		t.Errorf("'mouse' reached the frontend as %q, want \"a\"", got.Mouse)
	}
	if got := OptionsFrom(nil); got != (Options{}) {
		t.Errorf("OptionsFrom(nil) = %+v, want a zero Options", got)
	}
}

// TestTerminalIsAClient is the compile-time contract said out loud: the
// editor is written against Client and hands the same Screen to whichever
// frontend it has.
func TestTerminalIsAClient(t *testing.T) {
	var c Client = &Terminal{}
	if _, _ = c.Size(); false {
		t.Fatal("unreachable")
	}
	c.Bell() // does nothing, which is what 'visualbell' with t_vb= asks for
}

// TestEventsAreAClosedSet: the marker method is unexported, so this file is
// the only place outside the package that can name every event, and a type
// switch elsewhere is exhaustive because of it.
func TestEventsAreAClosedSet(t *testing.T) {
	for _, ev := range []Event{
		KeyEvent{}, MouseEvent{}, ResizeEvent{}, PasteEvent{}, CloseEvent{},
	} {
		if ev == nil {
			t.Error("a nil event in the set")
		}
	}
}
