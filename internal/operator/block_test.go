package operator

import (
	"testing"

	"github.com/pkar/pvim/internal/register"
)

// The buffers here are the two that were put through vim: one whose short
// lines end exactly at the block's left edge, and one whose short lines stop
// before it. The difference decides three separate rules and looks like
// nothing at all in a diff.
const (
	blockEven  = "abcdefgh\nab\nABCDEFGH\nxy\nzzzzzzzz\n"
	blockShort = "abcdefgh\na\n\nABCDEFGH\n"
)

// blockSpan is CTRL-V from column 3 to column 5 over every line of the buffer.
func blockSpan(last int) Span {
	return Span{Type: register.TypeBlock, Block: Block{First: 1, Last: last, Left: 2, Right: 4}}
}

// TestBlockYank is the padding rule, and it is not the one a reading of the
// code suggests. A line that ends EXACTLY where the block starts yanks the
// empty string; a line that ends BEFORE it yanks the block's full width in
// spaces. "ab" gives "" and "a" gives three spaces, under the same block.
func TestBlockYank(t *testing.T) {
	cases := []struct {
		name string
		in   string
		last int
		want []string
	}{
		{"lines ending at the block's edge", blockEven, 5, []string{"cde", "", "CDE", "", "zzz"}},
		{"lines ending before it", blockShort, 4, []string{"cde", "   ", "   ", "CDE"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := buf(tc.in)
			regs := &recorder{}
			got, err := Apply(Request{Buf: b, Regs: regs, Op: OpYank, Span: blockSpan(tc.last), At: at(1, 3), Opt: lineOpts()})
			if err != nil {
				t.Fatal(err)
			}
			if regs.yanked.Type != register.TypeBlock {
				t.Fatalf("register type %v, want blockwise", regs.yanked.Type)
			}
			if regs.yanked.Width != 3 {
				t.Errorf("register width %d, want 3", regs.yanked.Width)
			}
			if len(regs.yanked.Lines) != len(tc.want) {
				t.Fatalf("%d register lines, want %d", len(regs.yanked.Lines), len(tc.want))
			}
			for i, want := range tc.want {
				if string(regs.yanked.Lines[i]) != want {
					t.Errorf("register line %d = %q, want %q", i+1, regs.yanked.Lines[i], want)
				}
			}
			if dump(b) != dump(buf(tc.in)) {
				t.Error("a yank changed the buffer")
			}
			checkCursor(t, got.Cursor, 1, 3)
		})
	}
}

// TestBlockYankWiderBlock is the same rule at a different width, because "the
// block's full width" and "the register's width" are two numbers an
// implementation can confuse when they are both 3. CTRL-V from column 2 to
// column 6 over "abcdefghij / a / (empty) / ABCDEFGHIJ" gives "bcdef", "",
// five spaces and "BCDEF": the one-character line ends exactly at the block's
// left edge and the empty one ends before it.
func TestBlockYankWiderBlock(t *testing.T) {
	b := buf("abcdefghij\na\n\nABCDEFGHIJ\n")
	span := Span{Type: register.TypeBlock, Block: Block{First: 1, Last: 4, Left: 1, Right: 5}}
	regs := &recorder{}
	if _, err := Apply(Request{Buf: b, Regs: regs, Op: OpYank, Span: span, At: at(1, 2), Opt: lineOpts()}); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"bcdef", "", "     ", "BCDEF"} {
		if string(regs.yanked.Lines[i]) != want {
			t.Errorf("register line %d = %q, want %q", i+1, regs.yanked.Lines[i], want)
		}
	}
	if regs.yanked.Width != 5 {
		t.Errorf("register width %d, want 5", regs.yanked.Width)
	}
}

// TestBlockDelete leaves a line the block never reaches exactly as it was, and
// puts the cursor on the block's top-left corner.
func TestBlockDelete(t *testing.T) {
	cases := []struct {
		name string
		in   string
		last int
		want string
	}{
		{"lines ending at the block's edge", blockEven, 5, "abfgh\nab\nABFGH\nxy\nzzzzz"},
		{"lines ending before it", blockShort, 4, "abfgh\na\n\nABFGH"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := buf(tc.in)
			regs := &recorder{}
			got, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Span: blockSpan(tc.last), At: at(1, 3), Opt: lineOpts()})
			if err != nil {
				t.Fatal(err)
			}
			if dump(b) != tc.want {
				t.Errorf("buffer\n%q\nwant\n%q", dump(b), tc.want)
			}
			if regs.deleted.Type != register.TypeBlock {
				t.Errorf("register type %v, want blockwise", regs.deleted.Type)
			}
			checkCursor(t, got.Cursor, 1, 3)
		})
	}
}

// TestBlockChange deletes the rectangle and asks for insert at its left edge.
// Repeating the typed text down the other lines is the mode machine's, which
// is why the buffer here is only the delete.
func TestBlockChange(t *testing.T) {
	b := buf(blockEven)
	got, err := Apply(Request{Buf: b, Regs: &recorder{}, Op: OpChange, Span: blockSpan(5), At: at(1, 3), Opt: lineOpts()})
	if err != nil {
		t.Fatal(err)
	}
	if dump(b) != "abfgh\nab\nABFGH\nxy\nzzzzz" {
		t.Errorf("buffer %q", dump(b))
	}
	if !got.Insert {
		t.Error("a blockwise c did not ask for insert mode")
	}
	checkCursor(t, got.Cursor, 1, 3)
}

// TestBlockCaseOperator applies to the rectangle and skips the lines the block
// never reaches: g~ over the block on "abcdefgh / ab / ABCDEFGH / xy /
// zzzzzzzz" leaves "ab" and "xy" alone.
func TestBlockCaseOperator(t *testing.T) {
	b := buf(blockEven)
	got, err := Apply(Request{Buf: b, Op: OpToggle, Span: blockSpan(5), At: at(1, 3), Opt: lineOpts()})
	if err != nil {
		t.Fatal(err)
	}
	if dump(b) != "abCDEfgh\nab\nABcdeFGH\nxy\nzzZZZzzz" {
		t.Errorf("buffer %q", dump(b))
	}
	checkCursor(t, got.Cursor, 1, 3)
}

// TestBlockInsertAt is the skip rule for a blockwise I: strictly shorter than
// the block's left edge is skipped, exactly as long as it is appended to.
func TestBlockInsertAt(t *testing.T) {
	b := buf(blockShort)
	blk := blockSpan(4).Block
	for _, tc := range []struct {
		line, col int
		ok        bool
	}{
		{1, 2, true},  // "abcdefgh"
		{2, 0, false}, // "a" stops before the edge
		{3, 0, false}, // "" likewise
		{4, 2, true},  // "ABCDEFGH"
	} {
		col, ok := BlockInsertAt(b, blk, tc.line, lineOpts())
		if ok != tc.ok || (ok && col != tc.col) {
			t.Errorf("BlockInsertAt(line %d) = %d %v, want %d %v", tc.line, col, ok, tc.col, tc.ok)
		}
	}

	even := buf(blockEven)
	if col, ok := BlockInsertAt(even, blockSpan(5).Block, 2, lineOpts()); !ok || col != 2 {
		t.Errorf(`BlockInsertAt on "ab" = %d %v, want 2 true: a line ending exactly at the edge is appended to`, col, ok)
	}
}

// TestBlockAppendAt is A, which pads every short line out to the block's right
// edge: "ab" under a block ending in column 5 comes back as "ab Q".
func TestBlockAppendAt(t *testing.T) {
	b := buf(blockShort)
	blk := blockSpan(4).Block
	for _, tc := range []struct{ line, col, pad int }{
		{1, 5, 0},
		{2, 1, 4},
		{3, 0, 5},
		{4, 5, 0},
	} {
		col, pad := BlockAppendAt(b, blk, tc.line, lineOpts())
		if col != tc.col || pad != tc.pad {
			t.Errorf("BlockAppendAt(line %d) = col %d pad %d, want col %d pad %d", tc.line, col, pad, tc.col, tc.pad)
		}
	}
}

// TestBlockToEOL is the $ block: every line runs to its own end, nothing is
// padded, and the register's width is the widest line the yank took, which is
// what getregtype() prints after the CTRL-V.
func TestBlockToEOL(t *testing.T) {
	b := buf(blockShort)
	span := Span{Type: register.TypeBlock, Block: Block{First: 1, Last: 3, Left: 0, ToEOL: true}}
	regs := &recorder{}
	got, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Span: span, At: at(1, 1), Opt: lineOpts()})
	if err != nil {
		t.Fatal(err)
	}
	if dump(b) != "\n\n\nABCDEFGH" {
		t.Errorf("buffer %q", dump(b))
	}
	want := []string{"abcdefgh", "a", ""}
	for i, w := range want {
		if string(regs.deleted.Lines[i]) != w {
			t.Errorf("register line %d = %q, want %q", i+1, regs.deleted.Lines[i], w)
		}
	}
	if regs.deleted.Width != 8 || !regs.deleted.ToEOL {
		t.Errorf("register width %d ToEOL %v, want 8 true", regs.deleted.Width, regs.deleted.ToEOL)
	}
	checkCursor(t, got.Cursor, 1, 1)
}

// TestBlockOverAWholeTab is a block that covers a tab exactly: CTRL-V from
// column 2 to column 8 over "a\tb" and "abcdefghij" with tabstop 8. vim takes
// the tab byte for byte and does not turn it into spaces, and it deletes it
// whole. Run, and the geometry is the one vim itself produced: a visual block
// widens to cover any character it overlaps, which is why this is the tab case
// that can be measured at all.
func TestBlockOverAWholeTab(t *testing.T) {
	b := buf("abcdefghij\na\tb\n")
	span := Span{Type: register.TypeBlock, Block: Block{First: 1, Last: 2, Left: 1, Right: 7}}

	regs := &recorder{}
	got, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Span: span, At: at(1, 2), Opt: lineOpts()})
	if err != nil {
		t.Fatal(err)
	}
	if dump(b) != "aij\nab" {
		t.Errorf("buffer %q, want %q", dump(b), "aij\nab")
	}
	if string(regs.deleted.Lines[0]) != "bcdefgh" || string(regs.deleted.Lines[1]) != "\t" {
		t.Errorf("register %q and %q, want \"bcdefgh\" and a tab", regs.deleted.Lines[0], regs.deleted.Lines[1])
	}
	if regs.deleted.Width != 7 {
		t.Errorf("register width %d, want 7", regs.deleted.Width)
	}
	checkCursor(t, got.Cursor, 1, 2)
}

// TestBlockInsideATab is the geometry vim's own UI cannot produce, because a
// visual block widens to whole characters. It is reachable from a scripted
// operator, so it has a defined answer: the block is entirely inside one tab,
// the register holds the block's width in spaces, and what is left of the tab
// goes back into the buffer as the cells that were outside the block.
func TestBlockInsideATab(t *testing.T) {
	b := buf("a\tb\n")
	span := Span{Type: register.TypeBlock, Block: Block{First: 1, Last: 1, Left: 2, Right: 5}}

	regs := &recorder{}
	if _, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Span: span, At: at(1, 1), Opt: lineOpts()}); err != nil {
		t.Fatal(err)
	}
	// The tab covers cells 1 to 7. The block takes four of them, so three are
	// left and they come back as three spaces.
	if got := string(b.Line(1)); got != "a   b" {
		t.Errorf("line = %q, want %q", got, "a   b")
	}
	if got := string(regs.deleted.Lines[0]); got != "    " {
		t.Errorf("register line = %q, want four spaces", got)
	}
}
