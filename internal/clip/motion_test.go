package clip

import (
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/register"
)

// TestValueOfMotionIsWhatVimAnswers is the measurement in motion.go turned into
// a test, one row per probe.
//
// How the rows were taken: a Swift program put a string and, where the motion
// column is not "none", a binary plist of [motion, string] under
// "VimPboardType" on the general pasteboard, then
//
//	vim --clean -i NONE --not-a-term -s dump.keys file
//
// with dump.keys calling writefile([getregtype('*'), string(getreg('*'))]).
// The want columns below are that file. Nothing here was read out of vim's
// source, and the two rows that surprised -- MCHAR keeping a trailing newline
// as a second empty line, and an unknown motion number falling back to the
// newline rule rather than being refused -- are the reason the probe was run
// rather than the header read.
func TestValueOfMotionIsWhatVimAnswers(t *testing.T) {
	for _, tc := range []struct {
		motion int
		in     string
		want   register.Type
		lines  []string
		width  int
	}{
		// No VimPboardType at all: every application that is not vim.
		{motionNone, "alpha", register.TypeChar, []string{"alpha"}, 0},
		{motionNone, "alpha\n", register.TypeLine, []string{"alpha"}, 0},
		{motionNone, "alpha\nbeta", register.TypeChar, []string{"alpha", "beta"}, 0},

		// The number wins over the trailing newline.
		{motionChar, "alpha\n", register.TypeChar, []string{"alpha", ""}, 0},
		{motionChar, "a\nb\n", register.TypeChar, []string{"a", "b", ""}, 0},
		{motionLine, "alpha", register.TypeLine, []string{"alpha"}, 0},
		{motionLine, "alpha\n", register.TypeLine, []string{"alpha"}, 0},
		{motionLine, "alpha\nbeta", register.TypeLine, []string{"alpha", "beta"}, 0},
		{motionLine, "alpha\nbeta\n", register.TypeLine, []string{"alpha", "beta"}, 0},

		// Blockwise: one trailing newline goes, and the width is recomputed
		// because the plist does not carry one.
		{motionBlock, "lp\net\n", register.TypeBlock, []string{"lp", "et"}, 2},
		{motionBlock, "abcd\nef\n", register.TypeBlock, []string{"abcd", "ef"}, 4},
		{motionBlock, "ab\ncdef", register.TypeBlock, []string{"ab", "cdef"}, 4},
		{motionBlock, "a\n\nb\n", register.TypeBlock, []string{"a", "", "b"}, 1},
		{motionBlock, "a\tb\ncd\n", register.TypeBlock, []string{"a\tb", "cd"}, 3},

		// A number vim does not know is vim's MAUTO: the newline rule again.
		{-7, "alpha", register.TypeChar, []string{"alpha"}, 0},
		{7, "alpha", register.TypeChar, []string{"alpha"}, 0},
		{7, "alpha\n", register.TypeLine, []string{"alpha"}, 0},
	} {
		t.Run(strings.ReplaceAll(tc.in, "\n", "|"), func(t *testing.T) {
			got := valueOfMotion([]byte(tc.in), tc.motion)
			if got.Type != tc.want {
				t.Errorf("motion %d over %q is %s, want %s", tc.motion, tc.in, got.Type, tc.want)
			}
			if got.Width != tc.width {
				t.Errorf("motion %d over %q has width %d, want %d", tc.motion, tc.in, got.Width, tc.width)
			}
			if len(got.Lines) != len(tc.lines) {
				t.Fatalf("motion %d over %q has %d lines, want %d: %q", tc.motion, tc.in, len(got.Lines), len(tc.lines), got.Lines)
			}
			for i, want := range tc.lines {
				if string(got.Lines[i]) != want {
					t.Errorf("line %d is %q, want %q", i, got.Lines[i], want)
				}
			}
		})
	}
}

// TestEmptyTextIsAnEmptyRegisterWhateverTheMotionSays pins the one place this
// package knowingly does not follow vim. vim answers V and a single blank line
// for an empty string carrying MLINE; here it is an empty register. See the
// note on valueOfMotion for why.
func TestEmptyTextIsAnEmptyRegisterWhateverTheMotionSays(t *testing.T) {
	for _, motion := range []int{motionNone, motionChar, motionLine, motionBlock} {
		if got := valueOfMotion(nil, motion); !got.Empty() {
			t.Errorf("motion %d over no text gives %d lines, want none", motion, len(got.Lines))
		}
	}
}

// TestBytesForAppendsTheBlockwiseNewline: what goes on the board for a block is
// what vim puts there, which is one newline more than Bytes gives.
func TestBytesForAppendsTheBlockwiseNewline(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   register.Value
		want string
	}{
		{"charwise", register.Char([]byte("lp"), []byte("et")), "lp\net"},
		{"linewise", register.LineValue([]byte("lp"), []byte("et")), "lp\net\n"},
		{"blockwise", register.BlockValue(2, []byte("lp"), []byte("et")), "lp\net\n"},
		{"empty block", register.Value{Type: register.TypeBlock}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(bytesFor(tc.in)); got != tc.want {
				t.Errorf("bytesFor = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBlockRoundTripKeepsItsShape is the compatibility claim stated as a round
// trip through this package's own two halves: what Write would put on the board
// is what Read would make of it. The width is recomputed rather than carried,
// so the assertion is on the width vim would print and not on the one the yank
// started with.
func TestBlockRoundTripKeepsItsShape(t *testing.T) {
	for _, in := range []register.Value{
		register.Char([]byte("alpha")),
		register.Char([]byte("alpha"), []byte("beta")),
		register.LineValue([]byte("alpha")),
		register.LineValue([]byte("alpha"), []byte("beta")),
		register.BlockValue(4, []byte("abcd"), []byte("ef")),
		register.BlockValue(2, []byte("lp"), []byte("et")),
	} {
		got := valueOfMotion(bytesFor(in), motionOf(in.Type))
		if got.Type != in.Type {
			t.Errorf("%q round trips as %s, want %s", in.String(), got.Type, in.Type)
		}
		if got.Width != in.Width {
			t.Errorf("%q round trips at width %d, want %d", in.String(), got.Width, in.Width)
		}
		if got.String() != in.String() {
			t.Errorf("%q round trips as %q", in.String(), got.String())
		}
	}
}

// TestCellsCountsTheWayVimDoes, in the columns vim counts and not in runes.
//
// Every want is /opt/homebrew/bin/vim 9.2.0321's own answer, taken as
// setreg('z', in . "\nx", 'b') followed by getregtype('z'): setreg with 'b'
// enters str_to_reg, which is the same function that measures a block arriving
// from the pasteboard, so the number after the CTRL-V is this function's
// number. The rows below the latin ones are the interesting half. The two CJK
// rows used to be a knowing divergence with the wrong answer written into the
// want column.
func TestCellsCountsTheWayVimDoes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abcd", 4},
		{"a\tb", 3},     // a tab is one cell here, not nine and not two
		{"ab\tcd", 5},   // and it is one wherever in the line it falls
		{"\t\t", 2},     // so two of them are two and never eight
		{"\x01", 1},     // ^A is one cell here too, not the two it draws as
		{"\x01\x02", 2}, // two of them are two
		{"\x01a\x02", 3},
		{"\x7f", 1}, // and so is ^?
		{"é", 1},    // a two-byte rune is one cell in both
		{"日本", 4},   // an east-asian wide rune is two
		{"日本語", 6},
		{"ａｂ", 4},             // fullwidth latin is wide as well
		{"🙂", 2},              // and so is an emoji
		{"\u0085", 4},         // a C1 control draws as <85>, four cells
		{"\u200b", 6},         // a zero-width space draws as <200b>, six
		{"e\u0301", 1},        // a combining mark belongs to the rune in front
		{"a\u0301b\u0301", 2}, // however many of them there are
		{"\u0301", 1},         // one with nothing in front gets a cell of its own
		{"日\u0301", 2},        // and it does not widen what it sits on
		{"\t\u0301", 1},       // a tab absorbs one too
	} {
		if got := cells([]byte(tc.in)); got != tc.want {
			t.Errorf("cells(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestBlockWidthFromTheBoardIsInDisplayColumns is the bug the row above is the
// unit of, at the level a paste sees it.
//
// register.Value.Width is documented as display columns and is what a put uses
// to pad the short lines of a block, so a width counted in runes pastes a block
// of CJK narrow: vim's own CTRL-V j "*y over "日本" and "x" writes a
// VimPboardType plist of [2, "日本\nx\n"], vim reads it back as CTRL-V 4, and
// "*P into "AAAA"/"BBBB" leaves "x BBBB" where a width of 2 leaves "x BBBB".
func TestBlockWidthFromTheBoardIsInDisplayColumns(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
	}{
		{"日本\nx\n", 4},
		{"x\n日本\n", 4}, // the widest line wins whichever line it is
		{"日本語\nx\n", 6},
		{"abcd\nef\n", 4}, // and latin is what it always was
	} {
		got := valueOfMotion([]byte(tc.in), motionBlock)
		if got.Type != register.TypeBlock {
			t.Fatalf("%q is %s, want %s", tc.in, got.Type, register.TypeBlock)
		}
		if got.Width != tc.width {
			t.Errorf("%q has width %d, want %d", tc.in, got.Width, tc.width)
		}
	}
}

// TestCloneDoesNotShare: what Read hands back may be written through by the
// editor without changing what the next Read answers.
func TestCloneDoesNotShare(t *testing.T) {
	v := register.Char([]byte("alpha"))
	c := clone(v)
	c.Lines[0][0] = 'X'
	if string(v.Lines[0]) != "alpha" {
		t.Errorf("the original is %q; clone shared its lines", v.Lines[0])
	}
	if got := clone(register.Value{}); got.Lines != nil {
		t.Errorf("cloning an empty value made %d lines", len(got.Lines))
	}
}
