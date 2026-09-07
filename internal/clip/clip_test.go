package clip

import (
	"testing"

	"github.com/pkar/pvim/internal/register"
)

// TestValueOfTypesByTrailingNewline pins the one rule that decides what a paste
// from another application does. Text ending in a newline is linewise and
// everything else is charwise; there is no third answer, because a pasteboard
// string cannot say "blockwise, four columns wide".
func TestValueOfTypesByTrailingNewline(t *testing.T) {
	for _, tc := range []struct {
		in    string
		typ   register.Type
		lines []string
	}{
		{"", register.TypeChar, nil}, // nothing copied at all: an empty register
		{"alpha", register.TypeChar, []string{"alpha"}},
		{"alpha\n", register.TypeLine, []string{"alpha"}},
		{"alpha\nbeta", register.TypeChar, []string{"alpha", "beta"}},
		{"alpha\nbeta\n", register.TypeLine, []string{"alpha", "beta"}},
		{"\n", register.TypeLine, []string{""}}, // one blank line, not empty
		{"alpha\r\nbeta\r\n", register.TypeLine, []string{"alpha", "beta"}},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got := ValueOf([]byte(tc.in))
			if len(got.Lines) != len(tc.lines) {
				t.Fatalf("ValueOf(%q) has %d lines, want %d: %q", tc.in, len(got.Lines), len(tc.lines), got.Lines)
			}
			for i, want := range tc.lines {
				if string(got.Lines[i]) != want {
					t.Errorf("line %d is %q, want %q", i, got.Lines[i], want)
				}
			}
			if len(tc.lines) > 0 && got.Type != tc.typ {
				t.Errorf("ValueOf(%q) is %s, want %s", tc.in, got.Type, tc.typ)
			}
		})
	}
}

// TestRoundTripKeepsCharwiseAndLinewise, and loses blockwise, over the plain
// string alone: this is the pair of functions an application that is not vim
// sees, and a plain string has no way to say "blockwise, four columns wide". A
// block that goes out this way comes back as the lines it was made of.
//
// It is not what pvim and vim do to each other. Both put a second pasteboard
// type beside the string carrying the motion, and motion_test.go is that path.
func TestRoundTripKeepsCharwiseAndLinewise(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   register.Value
		want register.Type
	}{
		{"charwise", register.Char([]byte("alpha")), register.TypeChar},
		{"linewise", register.LineValue([]byte("alpha")), register.TypeLine},
		{"blockwise", register.BlockValue(2, []byte("al"), []byte("be")), register.TypeChar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValueOf(Bytes(tc.in)).Type; got != tc.want {
				t.Errorf("%s round trips as %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// TestMemoryCountsCalls is the property a test of 'autoselect' needs: not what
// is on the clipboard but how many times the editor put it there.
func TestMemoryCountsCalls(t *testing.T) {
	var m Memory
	var c Clipboard = &m // the whole point: it satisfies register.Clipboard

	if err := c.Write(register.Char([]byte("alpha"))); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(); err != nil {
		t.Fatal(err)
	}
	if m.Writes() != 1 || m.Reads() != 1 {
		t.Errorf("after one write and one read: %d writes, %d reads", m.Writes(), m.Reads())
	}
	if m.Text() != "alpha" {
		t.Errorf("Text is %q, want %q", m.Text(), "alpha")
	}

	m.SetText("beta\n")
	v, err := c.Read()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != register.TypeLine {
		t.Errorf("text set from outside reads %s, want linewise", v.Type)
	}
}

// TestNewNeedsARunner keeps the wiring mistake and the unsupported platform
// apart. On darwin a nil Runner is cmd/pvim forgetting to pass gui.OnMain;
// everywhere else there is nothing to pass and nothing to run.
func TestNewNeedsARunner(t *testing.T) {
	c, err := New(nil)
	if err == nil {
		t.Fatalf("New(nil) returned %v and no error", c)
	}
	if c != nil {
		t.Errorf("New returned an error and a clipboard: %v", c)
	}
}
