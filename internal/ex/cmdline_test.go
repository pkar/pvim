package ex

import (
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/key"
)

// The command line as a mode of its own.

// fakeSource is the CTRL-R half without an editor behind it.
type fakeSource struct {
	regs map[byte]string
	word string
}

func (f fakeSource) Register(name byte) []byte { return []byte(f.regs[name]) }
func (f fakeSource) WordUnderCursor() []byte   { return []byte(f.word) }

// typeLine feeds a string of ordinary characters.
func typeLine(l *Line, s string) {
	for _, r := range s {
		l.Key(key.Rune(r))
	}
}

func TestCmdlineEditing(t *testing.T) {
	l := NewLine(':')
	typeLine(l, "hello world")
	if l.String() != "hello world" {
		t.Fatalf("typed %q", l.String())
	}

	// CTRL-W deletes the word before the cursor.
	l.Key(key.Ctrl('w'))
	if l.String() != "hello " {
		t.Errorf("after CTRL-W: %q", l.String())
	}
	// CTRL-U deletes to the start of the line and keeps what is after the
	// cursor, which is not the same as clearing it.
	typeLine(l, "there")
	l.Key(key.Key{Special: key.KeyLeft})
	l.Key(key.Ctrl('u'))
	if l.String() != "e" {
		t.Errorf("after CTRL-U: %q", l.String())
	}
	// CTRL-E and CTRL-B are end and start.
	l.Key(key.Ctrl('b'))
	if l.Pos != 0 {
		t.Errorf("CTRL-B left Pos %d", l.Pos)
	}
	l.Key(key.Ctrl('e'))
	if l.Pos != len(l.Text) {
		t.Errorf("CTRL-E left Pos %d of %d", l.Pos, len(l.Text))
	}
}

// TestCmdlineFinish covers the three ways a command line ends, including the
// one that surprises people: a backspace on an empty line abandons it.
func TestCmdlineFinish(t *testing.T) {
	l := NewLine(':')
	if done, ok := l.Key(key.Key{Special: key.KeyCR}); !done || !ok {
		t.Error("CR did not accept")
	}
	l = NewLine(':')
	if done, ok := l.Key(key.Key{Special: key.KeyEsc}); !done || ok {
		t.Error("Esc did not abandon")
	}
	l = NewLine(':')
	if done, ok := l.Key(key.Key{Special: key.KeyBS}); !done || ok {
		t.Error("BS on an empty line did not abandon")
	}
	l = NewLine(':')
	typeLine(l, "x")
	if done, _ := l.Key(key.Key{Special: key.KeyBS}); done {
		t.Error("BS on a non-empty line ended it")
	}
}

// TestCmdlineRegisters is CTRL-R and CTRL-R CTRL-W, the second of which the
// vimrc reaches for on line 44.
func TestCmdlineRegisters(t *testing.T) {
	l := NewLine(':')
	l.Src = fakeSource{regs: map[byte]string{'a': "alpha", '+': "clip\nboard"}, word: "cursorword"}

	typeLine(l, "e ")
	l.Key(key.Ctrl('r'))
	l.Key(key.Rune('a'))
	if l.String() != "e alpha" {
		t.Errorf("CTRL-R a gave %q", l.String())
	}

	l = NewLine(':')
	l.Src = fakeSource{word: "cursorword"}
	l.Key(key.Ctrl('r'))
	l.Key(key.Ctrl('w'))
	if l.String() != "cursorword" {
		t.Errorf("CTRL-R CTRL-W gave %q", l.String())
	}

	// A register with a line break becomes a carriage return, which is how vim
	// shows one on a line that cannot hold a break.
	l = NewLine(':')
	l.Src = fakeSource{regs: map[byte]string{'+': "clip\nboard"}}
	l.Key(key.Ctrl('r'))
	l.Key(key.Rune('+'))
	if l.String() != "clip\rboard" {
		t.Errorf("CTRL-R + gave %q", l.String())
	}
}

// TestHistory: a repeat moves rather than duplicating, and walking down past
// the newest entry gives back the line that was being typed.
func TestHistory(t *testing.T) {
	h := &History{Max: 3}
	h.Add("one")
	h.Add("two")
	h.Add("one")
	if strings.Join(h.Lines, ",") != "two,one" {
		t.Errorf("history = %q, want two,one", h.Lines)
	}
	h.Add("three")
	h.Add("four")
	if strings.Join(h.Lines, ",") != "one,three,four" {
		t.Errorf("history after the cap = %q", h.Lines)
	}

	l := NewLine(':')
	l.Hist = h
	typeLine(l, "half")
	l.Key(key.Ctrl('p'))
	if l.String() != "four" {
		t.Errorf("CTRL-P gave %q, want four", l.String())
	}
	l.Key(key.Ctrl('p'))
	if l.String() != "three" {
		t.Errorf("second CTRL-P gave %q, want three", l.String())
	}
	l.Key(key.Ctrl('n'))
	l.Key(key.Ctrl('n'))
	if l.String() != "half" {
		t.Errorf("walking back down gave %q, want the typed line back", l.String())
	}
}

// listCompleter is a fixed candidate list, so that a completion test does not
// have to touch a disk.
type listCompleter []string

func (l listCompleter) Complete(kind CompKind, prefix string) []string {
	var out []string
	for _, n := range l {
		if strings.HasPrefix(n, prefix) {
			out = append(out, n)
		}
	}
	return out
}

// TestWildmenuCycles is 'wildmode' at its default of "full": Tab walks the
// matches and the step after the last one puts back what was typed.
func TestWildmenuCycles(t *testing.T) {
	l := NewLine(':')
	typeLine(l, "e fo")
	c := listCompleter{"foo.txt", "foobar.txt", "other.txt"}

	l.Complete(c, "full", "", false)
	if l.String() != "e foo.txt" {
		t.Fatalf("first Tab gave %q", l.String())
	}
	l.Complete(c, "full", "", false)
	if l.String() != "e foobar.txt" {
		t.Fatalf("second Tab gave %q", l.String())
	}
	l.Complete(c, "full", "", false)
	if l.String() != "e fo" {
		t.Fatalf("third Tab gave %q, want the typed text back", l.String())
	}
	// Shift-Tab from there goes to the last match.
	l.Complete(c, "full", "", true)
	if l.String() != "e foobar.txt" {
		t.Fatalf("Shift-Tab gave %q", l.String())
	}
}

// TestCompletionKind decides what is being completed from what has been typed,
// which is what makes ":set " offer option names and ":e " file names.
func TestCompletionKind(t *testing.T) {
	for _, tc := range []struct {
		typed string
		kind  CompKind
		start int
	}{
		{"e", CompCommand, 0},
		{"se", CompCommand, 0},
		{"e foo", CompFile, 2},
		{"e! foo", CompFile, 3},
		{"set ic", CompOption, 4},
		{"b oth", CompBuffer, 2},
		{"help ins", CompTag, 5},
		{"1,2d ", CompNone, 5},
		{"sp a b", CompFile, 5},
	} {
		l := NewLine(':')
		typeLine(l, tc.typed)
		kind, start := l.completionAt()
		if kind != tc.kind || start != tc.start {
			t.Errorf("%q -> kind %d start %d, want kind %d start %d", tc.typed, kind, start, tc.kind, tc.start)
		}
	}
}

// TestWildignoreGlob is the matcher the vimrc's thirteen patterns run through.
//
// "*" crosses a directory separator here and does not in path.Match, which is
// the whole reason this is not one line of the standard library: "*/tmp/*" is
// in the vimrc and would match nothing under path.Match's rules.
func TestWildignoreGlob(t *testing.T) {
	const wig = "*.o,*.obj,*.bak,*.exe,*.pyc,*.swp,*/tmp/*,*.so,*.zip,*.tgz,*.tar.gz,*.iso"
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"main.o", true},
		{"build/deep/main.o", true},
		{"a/tmp/b", true},
		{"x/y/tmp/z/w", true},
		{"main.go", false},
		{"tmpfile", false},
		{"archive.tar.gz", true},
		{"notes.swp", true},
	} {
		got := filterIgnored([]string{tc.name}, wig, CompFile)
		hidden := len(got) == 0
		if hidden != tc.want {
			t.Errorf("wildignore on %q hid it = %v, want %v", tc.name, hidden, tc.want)
		}
	}

	// 'wildignore' applies to files and not to buffer names, which is vim's
	// rule and the reason a buffer you are editing never disappears from ":b"
	// completion.
	if got := filterIgnored([]string{"main.o"}, wig, CompBuffer); len(got) != 1 {
		t.Error("wildignore hid a buffer name")
	}
}
