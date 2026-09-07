package mode

import (
	"errors"
	"testing"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// TestModeString is what :echo mode() prints, which a statusline shows and the
// oracle will eventually diff. Blockwise visual is a literal CTRL-V and not
// the three letters "C-V".
func TestModeString(t *testing.T) {
	for m, want := range map[Mode]string{
		Normal: "n", Insert: "i", Replace: "R",
		VisualChar: "v", VisualLine: "V", VisualBlock: "\x16",
		OperatorPending: "no", Cmdline: "c",
	} {
		if got := m.String(); got != want {
			t.Errorf("Mode(%d).String() = %q, want %q", m, got, want)
		}
	}
}

// TestPendingCount is the whole of why there are two counts. vim multiplies
// the one typed before the operator by the one typed after it, so 2d3w deletes
// six words, and an editor with one count field deletes three and looks right
// until the day it matters.
func TestPendingCount(t *testing.T) {
	cases := []struct {
		name           string
		count1, count2 int
		want           int
	}{
		{"neither", 0, 0, 0},
		{"before the operator", 3, 0, 3},
		{"after the operator", 0, 4, 4},
		{"2d3w", 2, 3, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := pending{count1: tc.count1, count2: tc.count2}
			if got := p.count(); got != tc.want {
				t.Errorf("count() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestNewStartsAtTheTop: line 1, column 0, normal mode, and a buffer of its own
// when handed none.
func TestNew(t *testing.T) {
	e := New(nil)
	if e.Buffer() == nil {
		t.Fatal("New(nil) has no buffer")
	}
	if got := e.Cursor(); got != (text.Pos{Line: 1}) {
		t.Errorf("cursor = %+v, want line 1 column 0", got)
	}
	if e.Mode() != Normal {
		t.Errorf("mode = %v, want normal", e.Mode())
	}
	if e.Registers() == nil {
		t.Error("New has no register file")
	}
}

// TestKeyIsNotSilent: a key nothing implements yet has to fail rather than
// beep. A no-op looks exactly like a working editor until three keystrokes
// later, when the oracle reports a buffer that is wrong for a reason nobody
// can trace back to a key nothing handled.
func TestKeyIsNotSilent(t *testing.T) {
	e := New(text.Read([]byte("hello\n")))
	if err := e.Key(key.Rune(':')); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Key(:) = %v, want ErrNotImplemented", err)
	}
}

// TestKeysStopsAtTheFirstError: a script that hits an unhandled key does not
// carry on feeding the rest of it into an editor that has lost its place.
func TestKeysStopsAtTheFirstError(t *testing.T) {
	e := New(text.Read([]byte("hello\n")))
	err := e.Keys([]key.Key{key.Rune(':'), key.Rune('w')})
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Keys = %v, want ErrNotImplemented", err)
	}
}

// TestSetCursorClamps: a frontend that places the cursor with a mouse can name
// a position off the end of the buffer, and the editor may not hold one.
func TestSetCursorClamps(t *testing.T) {
	e := New(text.Read([]byte("one\ntwo\n")))
	e.SetCursor(text.Pos{Line: 99, Col: 99})
	if got := e.Cursor(); got.Line > 2 {
		t.Errorf("cursor = %+v, want it inside a two-line buffer", got)
	}
}

// TestMessages is what the oracle diffs against vim's:redir output. An editor
// with a perfect buffer and no E486 fails a case on this file alone.
func TestMessages(t *testing.T) {
	e := New(nil)
	if e.Message() != "" {
		t.Error("a new editor has already said something")
	}
	e.Say("E486: Pattern not found: foo")
	e.Say("3 fewer lines")
	if got := e.Message(); got != "3 fewer lines" {
		t.Errorf("Message = %q, want the last one", got)
	}
	if len(e.Messages()) != 2 {
		t.Errorf("Messages has %d entries, want 2", len(e.Messages()))
	}
}

// TestOptionsReachTheRegisters: SetOptions has to push 'clipboard' down, or
// :set clipboard=unnamed parses, sets a field and changes nothing about where
// a yank goes.
func TestOptionsReachTheRegisters(t *testing.T) {
	e := New(nil)
	o := DefaultOptions()
	o.Clipboard = "unnamed,autoselect"
	e.SetOptions(o)
	if got := e.Registers().UnnamedName(); got != register.ClipboardStar {
		t.Errorf("UnnamedName = %q, want %q", got, register.ClipboardStar)
	}
}

// TestHasFlag is why 'clipboard' is not read with strings.Contains:
// "unnamedplus" contains "unnamed", and on a platform where * and + are two
// selections that mistake puts every yank on the wrong one.
func TestHasFlag(t *testing.T) {
	cases := []struct {
		value, flag string
		want        bool
	}{
		{"unnamed", "unnamed", true},
		{"unnamedplus", "unnamed", false},
		{"unnamed,unnamedplus,autoselect", "unnamed", true},
		{"unnamed,unnamedplus,autoselect", "unnamedplus", true},
		{"unnamed,unnamedplus,autoselect", "autoselect", true},
		{"", "unnamed", false},
		{"exclude:cons\\|linux", "unnamed", false},
	}
	for _, tc := range cases {
		if got := hasFlag(tc.value, tc.flag); got != tc.want {
			t.Errorf("hasFlag(%q, %q) = %v, want %v", tc.value, tc.flag, got, tc.want)
		}
	}
}

// TestOptionViews is the seam between one option bag and five packages. Every
// field below has one reader somewhere under this package, and a mapping that
// drops one is an option that parses and does nothing, which is this layer's
// characteristic failure.
func TestOptionViews(t *testing.T) {
	o := DefaultOptions()
	o.TabStop = 2
	o.ShiftWidth = 4
	o.ShiftRound = true
	o.IsKeyword = "@,48-57,_"
	o.SmartCase = true
	o.IgnoreCase = true
	o.Report = 5

	if got := o.Motion(); got.TabStop != 2 || got.IsKeyword != "@,48-57,_" {
		t.Errorf("Motion() = %+v, want tabstop 2 and the iskeyword through", got)
	}
	if got := o.TextObj(); got.IsKeyword != "@,48-57,_" || got.TabStop != 2 {
		t.Errorf("TextObj() = %+v", got)
	}
	if got := o.Operator(); got.ShiftWidth != 4 || !got.ShiftRound || got.Report != 5 {
		t.Errorf("Operator() = %+v, want sw 4, shiftround on, report 5", got)
	}
	if got := o.Search(); !got.IgnoreCase || !got.SmartCase || !got.WrapScan {
		t.Errorf("Search() = %+v, want both case options and wrapscan", got)
	}
}

// TestDefaultOptions is the oracle's vanilla profile: vim --clean and nothing
// else. The four below are the ones the vimrc changes and therefore the ones a
// wrong default hides behind.
func TestDefaultOptions(t *testing.T) {
	o := DefaultOptions()
	if o.TabStop != 8 || o.ShiftWidth != 8 {
		t.Errorf("tabstop %d shiftwidth %d, want 8 and 8", o.TabStop, o.ShiftWidth)
	}
	if o.ShiftRound {
		t.Error("shiftround is on by default, want off")
	}
	if !o.WrapScan {
		t.Error("wrapscan is off by default, want on")
	}
	if o.Report != 2 {
		t.Errorf("report = %d, want 2", o.Report)
	}
}
