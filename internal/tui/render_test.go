package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/screen"
)

// normalSGR is what appendSGR writes for screen.DefaultNormal, which is
// nofrils-dark's Normal: #eeeeee on #262626. Spelled out rather than computed
// so that a change to either colour or to the SGR shape fails a test with the
// old string in the message.
const normalSGR = "\x1b[0;38;2;238;238;238;48;2;38;38;38m"

// newTestScreen returns a screen of the given size with its cursor at the top
// left and a block cursor, which is where every case below starts.
func newTestScreen(rows, cols int) *screen.Screen {
	return screen.NewScreen(rows, cols)
}

// TestFirstFramePaintsEverything: with no previous frame there is nothing to
// diff against, so every row is one span and every cell is written. This is
// what a resize and a CTRL-L both produce, and it is the frame that has to be
// right before any diff can be.
func TestFirstFramePaintsEverything(t *testing.T) {
	s := newTestScreen(2, 3)
	s.Grid.SetString(0, 0, "hi", screen.Normal)

	want := seqCursorHide +
		"\x1b[1;1H" + normalSGR + "hi " +
		"\x1b[2;1H" + "   " +
		"\x1b[1;1H" + "\x1b[2 q" +
		seqCursorShow
	if got := string(frame(nil, s, nil)); got != want {
		t.Errorf("first frame\n got %q\nwant %q", got, want)
	}
}

// TestDiffWritesOnlyWhatChanged is the reason this package renders from a Grid
// and not from a list of draw calls. A 40,000-line log scrolled over ssh is
// this loop running a few hundred times a second, and a frontend that repainted
// the screen for a one-cell change would be unusable over anything slower than
// a local pipe.
func TestDiffWritesOnlyWhatChanged(t *testing.T) {
	prev := screen.NewGrid(3, 4)
	s := newTestScreen(3, 4)
	s.Grid.Set(1, 2, 'x', screen.Normal)
	s.CursorRow, s.CursorCol = 1, 3

	want := seqCursorHide +
		"\x1b[2;3H" + normalSGR + "x" +
		"\x1b[2;4H" + "\x1b[2 q" +
		seqCursorShow
	if got := string(frame(nil, s, prev)); got != want {
		t.Errorf("one changed cell\n got %q\nwant %q", got, want)
	}
}

// TestUnchangedFrameStillMovesTheCursor. Every motion in normal mode is a
// frame where no cell changed and the cursor is somewhere else, and it is the
// single most common frame this editor draws. The cursor hide and show are not
// in it: there is nothing to hide the cursor for, and blinking it off and on
// for a plain "l" would be visible.
func TestUnchangedFrameStillMovesTheCursor(t *testing.T) {
	prev := screen.NewGrid(3, 4)
	s := newTestScreen(3, 4)
	s.CursorRow, s.CursorCol = 2, 1

	want := "\x1b[3;2H" + "\x1b[2 q"
	if got := string(frame(nil, s, prev)); got != want {
		t.Errorf("cursor-only frame\n got %q\nwant %q", got, want)
	}
}

// TestHighlightIsWrittenOncePerRun: the SGR goes out when the highlight
// changes and not per cell. A statusline is one Set per cell and would
// otherwise carry forty copies of a thirty-byte colour sequence.
func TestHighlightIsWrittenOncePerRun(t *testing.T) {
	s := newTestScreen(1, 6)
	status := s.HL.Set("StatusLine", screen.Highlight{
		FG:   screen.RGB{R: 0, G: 0, B: 0},
		BG:   screen.RGB{R: 255, G: 128, B: 1},
		Attr: screen.AttrBold,
	})
	s.Grid.SetString(0, 0, "ab", screen.Normal)
	s.Grid.SetString(0, 2, "cd", status)
	s.Grid.SetString(0, 4, "ef", screen.Normal)

	want := seqCursorHide +
		"\x1b[1;1H" + normalSGR + "ab" +
		"\x1b[0;1;38;2;0;0;0;48;2;255;128;1m" + "cd" +
		normalSGR + "ef" +
		"\x1b[1;1H" + "\x1b[2 q" +
		seqCursorShow
	if got := string(frame(nil, s, nil)); got != want {
		t.Errorf("three runs\n got %q\nwant %q", got, want)
	}
}

// TestAttributesAndUndercurl covers every bit screen.Attr has. Undercurl is
// the one that leaves the main sequence, because 4:3 is a sub-parameter and a
// terminal that cannot parse one would drop the colours with it.
func TestAttributesAndUndercurl(t *testing.T) {
	for _, tc := range []struct {
		name string
		hl   screen.Highlight
		want string
	}{
		{"plain", screen.Highlight{}, "\x1b[0;38;2;0;0;0;48;2;0;0;0m"},
		{"bold", screen.Highlight{Attr: screen.AttrBold}, "\x1b[0;1;38;2;0;0;0;48;2;0;0;0m"},
		{"italic", screen.Highlight{Attr: screen.AttrItalic}, "\x1b[0;3;38;2;0;0;0;48;2;0;0;0m"},
		{"underline", screen.Highlight{Attr: screen.AttrUnderline}, "\x1b[0;4;38;2;0;0;0;48;2;0;0;0m"},
		{"reverse", screen.Highlight{Attr: screen.AttrReverse}, "\x1b[0;7;38;2;0;0;0;48;2;0;0;0m"},
		{"standout", screen.Highlight{Attr: screen.AttrStandout}, "\x1b[0;7;38;2;0;0;0;48;2;0;0;0m"},
		{"strikethrough", screen.Highlight{Attr: screen.AttrStrikethrough}, "\x1b[0;9;38;2;0;0;0;48;2;0;0;0m"},
		{
			"undercurl",
			screen.Highlight{Attr: screen.AttrUndercurl, SP: screen.RGB{R: 255, G: 0, B: 0}},
			"\x1b[0;38;2;0;0;0;48;2;0;0;0m\x1b[4:3m\x1b[58;2;255;0;0m",
		},
		{
			"all",
			screen.Highlight{
				FG:   screen.RGB{R: 1, G: 2, B: 3},
				BG:   screen.RGB{R: 4, G: 5, B: 6},
				SP:   screen.RGB{R: 7, G: 8, B: 9},
				Attr: screen.AttrBold | screen.AttrItalic | screen.AttrUnderline | screen.AttrUndercurl | screen.AttrReverse | screen.AttrStrikethrough,
			},
			"\x1b[0;1;3;4;7;9;38;2;1;2;3;48;2;4;5;6m\x1b[4:3m\x1b[58;2;7;8;9m",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(appendSGR(nil, tc.hl)); got != tc.want {
				t.Errorf("appendSGR\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestWideRuneIsWrittenOnce. A double-width rune owns two cells and the
// terminal's own cursor advances over both of them, so writing the trailing
// half would overwrite the glyph with whatever that cell nominally holds.
// screen.Grid.Diff already widens a span to whole pairs; this is the other
// half of the same rule.
func TestWideRuneIsWrittenOnce(t *testing.T) {
	s := newTestScreen(1, 4)
	if w := s.Grid.Set(0, 0, '世', screen.Normal); w != 2 {
		t.Fatalf("screen.Grid.Set returned width %d for a double-width rune", w)
	}

	got := string(frame(nil, s, nil))
	if strings.Count(got, "世") != 1 {
		t.Errorf("wide rune written %d times, want once: %q", strings.Count(got, "世"), got)
	}
	want := seqCursorHide +
		"\x1b[1;1H" + normalSGR + "世  " +
		"\x1b[1;1H" + "\x1b[2 q" +
		seqCursorShow
	if got != want {
		t.Errorf("wide rune frame\n got %q\nwant %q", got, want)
	}
}

// TestCursorShapes is the vimrc's three modes. Steady and not blinking: the
// window refuses the blink in D-004 and there is no reason for the terminal to
// disagree with the window about it.
func TestCursorShapes(t *testing.T) {
	for _, tc := range []struct {
		shape screen.CursorShape
		want  string
	}{
		{screen.CursorBlock, "\x1b[2 q"},
		{screen.CursorBar, "\x1b[6 q"},
		{screen.CursorUnderline, "\x1b[4 q"},
		{screen.CursorHollow, "\x1b[2 q"},
	} {
		if got := cursorShape(tc.shape); got != tc.want {
			t.Errorf("cursorShape(%d) = %q, want %q", tc.shape, got, tc.want)
		}
	}
}

// TestControlCharactersNeverReachTheTerminal. A cell holding an Escape would
// turn the rest of the frame into that sequence's parameters, which is a
// screenful of garbage from one bad byte in a file name.
func TestControlCharactersNeverReachTheTerminal(t *testing.T) {
	for _, r := range []rune{0, 0x1b, '\n', '\r', '\t', 0x7f} {
		if got := string(appendRune(nil, r)); got != " " {
			t.Errorf("appendRune(%q) = %q, want a space", r, got)
		}
	}
	if got := string(appendRune(nil, 'a')); got != "a" {
		t.Errorf("appendRune('a') = %q", got)
	}
}

// TestCursorIsClamped. A screen whose cursor is off the grid is a bug
// upstairs, and the answer is a cursor in the corner rather than a CUP to row
// zero, which terminals disagree about.
func TestCursorIsClamped(t *testing.T) {
	s := newTestScreen(2, 3)
	s.CursorRow, s.CursorCol = 9, 9
	if row, col := clampCursor(s); row != 1 || col != 2 {
		t.Errorf("clampCursor past the end = %d,%d, want 1,2", row, col)
	}
	s.CursorRow, s.CursorCol = -4, -1
	if row, col := clampCursor(s); row != 0 || col != 0 {
		t.Errorf("clampCursor before the start = %d,%d, want 0,0", row, col)
	}
}

// TestRenderDiffsAgainstWhatItPainted: two Renders of the same screen write
// the whole thing once and then nothing but the cursor. That is the property
// the snapshot exists for, and getting it wrong is invisible until a file is
// big enough to feel it.
func TestRenderDiffsAgainstWhatItPainted(t *testing.T) {
	var out strings.Builder
	term := &Terminal{Out: &out}
	s := newTestScreen(4, 8)
	s.Grid.SetString(0, 0, "hello", screen.Normal)

	if err := term.Render(s); err != nil {
		t.Fatalf("first Render: %v", err)
	}
	first := out.Len()
	if first == 0 {
		t.Fatal("first Render wrote nothing")
	}

	out.Reset()
	if err := term.Render(s); err != nil {
		t.Fatalf("second Render: %v", err)
	}
	if got, want := out.String(), "\x1b[1;1H\x1b[2 q"; got != want {
		t.Errorf("second Render of an unchanged screen wrote %q, want %q", got, want)
	}

	out.Reset()
	term.Invalidate()
	if err := term.Render(s); err != nil {
		t.Fatalf("third Render: %v", err)
	}
	if out.Len() != first {
		t.Errorf("Render after Invalidate wrote %d bytes, want the full frame's %d", out.Len(), first)
	}
}

// errWriter is a terminal that has gone away, which is what a hangup looks
// like to the writing half.
type errWriter struct{ err error }

func (w errWriter) Write(p []byte) (int, error) { return 0, w.err }

// TestRenderReportsAWriteFailure. A frame that could not be written has to
// come back as an error and not as a silent success: the editor's next diff is
// against a frame the terminal never received, and every frame after it would
// be wrong.
func TestRenderReportsAWriteFailure(t *testing.T) {
	boom := errors.New("terminal went away")
	term := &Terminal{Out: errWriter{err: boom}}
	if err := term.Render(newTestScreen(2, 2)); !errors.Is(err, boom) {
		t.Errorf("Render onto a dead terminal = %v, want %v", err, boom)
	}
}

// TestRenderWithNothingToDo. A nil screen is not an error: the editor asking
// for a repaint before it has one is a startup ordering question and not a
// failure, and a Terminal with no output is what a test builds.
func TestRenderWithNothingToDo(t *testing.T) {
	if err := (&Terminal{Out: &strings.Builder{}}).Render(nil); err != nil {
		t.Errorf("Render(nil) = %v", err)
	}
	if err := (&Terminal{}).Render(newTestScreen(1, 1)); !errors.Is(err, ErrNoTerminal) {
		t.Errorf("Render with no output = %v, want ErrNoTerminal", err)
	}
}

// TestAFrameCostsWhatTheDiffCosts puts numbers on the claim this package is
// built around, at the size the editor actually runs at.
//
// Three frames on a 60x200 screen. A one-cell change is a cursor move and a
// character; a one-row change is what typing on a line costs; a scroll by one
// line changes every row and is the expensive frame, because this renderer has
// no hardware scroll -- no scroll region, no \x1b[S -- and repaints the text
// instead. That last number is the one to look at the day somebody says
// scrolling over a slow link feels heavy, and the answer then is a scroll
// region and not a bigger diff.
func TestAFrameCostsWhatTheDiffCosts(t *testing.T) {
	const rows, cols = 60, 200
	line := strings.Repeat("package tui, one line of ordinary source code. ", 5)[:cols]

	fill := func(off int) *screen.Screen {
		s := newTestScreen(rows, cols)
		for r := 0; r < rows; r++ {
			s.Grid.SetString(r, 0, line[(r+off)%len(line):]+line[:(r+off)%len(line)], screen.Normal)
		}
		return s
	}

	base := fill(0)
	prev := screen.NewGrid(rows, cols)
	copy(prev.Cells, base.Grid.Cells)

	one := fill(0)
	one.Grid.Set(30, 100, 'X', screen.Normal)
	// Around 68: the cursor hide and show, two absolute cursor positions, one
	// character, the cursor shape, and a 33-byte truecolor SGR, which is the
	// price of writing the appearance absolutely instead of keeping a model
	// of what the terminal believes.
	if n := len(frame(nil, one, prev)); n > 96 {
		t.Errorf("a one-cell change cost %d bytes, want under 96", n)
	} else {
		t.Logf("a one-cell change costs %d bytes", n)
	}

	row := fill(0)
	row.Grid.SetString(30, 0, strings.Repeat("y", cols), screen.Normal)
	if n := len(frame(nil, row, prev)); n > 2*cols {
		t.Errorf("a one-row change cost %d bytes, want under %d: it is repainting more than the row", n, 2*cols)
	} else {
		t.Logf("a one-row change on a %dx%d screen costs %d bytes", rows, cols, n)
	}

	scrolled := fill(1)
	n := len(frame(nil, scrolled, prev))
	t.Logf("a scroll by one line on a %dx%d screen costs %d bytes", rows, cols, n)
	if n > rows*(cols+16)+64 {
		t.Errorf("a scroll cost %d bytes, which is more than repainting the text", n)
	}
}
