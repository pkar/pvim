package operator

import (
	"testing"

	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// Every expectation here is a run of
//
//	vim --clean -i NONE --not-a-term -s KEYS five.txt
//
// on /opt/homebrew/bin/vim 9.2.0321 over "alpha beta gamma delta epsilon", one
// word per line, with the buffer, getreg('"'), getregtype('"') and
// foldclosed() for every line dumped afterwards. The test names carry the
// keystrokes, so a failure goes straight back in front of the same vim.

// fakeFolds is a flat list of manual folds. screen.Folds nests and this does
// not, because nothing measured here has a fold inside a fold, and a test that
// stood up the real tree would be testing internal/screen through a package
// that cannot import it.
//
// A fold of one line is created and never reported closed, which is
// 'foldminlines' and what screen.Folds.ClosedAt does.
type fakeFolds struct {
	made  [][2]int // every Create call, in order
	folds [][2]int
}

func closedFold(first, last int) *fakeFolds {
	return &fakeFolds{folds: [][2]int{{first, last}}}
}

func (f *fakeFolds) Create(first, last int) bool {
	f.made = append(f.made, [2]int{first, last})
	if last <= first {
		return false
	}
	f.folds = append(f.folds, [2]int{first, last})
	return true
}

func (f *fakeFolds) ClosedFold(line int) (int, int, bool) {
	for _, fold := range f.folds {
		if line >= fold[0] && line <= fold[1] && fold[1] > fold[0] {
			return fold[0], fold[1], true
		}
	}
	return 0, 0, false
}

// five is the buffer every case below runs on.
func five() *text.Buffer { return buf("alpha\nbeta\ngamma\ndelta\nepsilon\n") }

// foldOpts is the vanilla profile with a window that has folds in it.
func foldOpts(f Folds) Options {
	o := DefaultOptions()
	o.Folds = f
	return o
}

// checkDelete compares a buffer and the register a delete wrote against what
// vim's state dump printed: the buffer with \n between lines, getreg('"') and
// getregtype('"').
func checkDelete(t *testing.T, b *text.Buffer, regs *recorder, wantBuf, wantReg, wantType string) {
	t.Helper()
	if got := dump(b); got != wantBuf {
		t.Errorf("buffer %q, want %q", got, wantBuf)
	}
	if got := regs.deleted.String(); got != wantReg {
		t.Errorf(`@" = %q, want %q`, got, wantReg)
	}
	if got := regs.deleted.RegType(); got != wantType {
		t.Errorf("getregtype() = %q, want %q", got, wantType)
	}
}

// TestFoldCreatesTheFold is zf's whole job and the one it was not doing: the
// range came back in the Result and the fold itself was never made, so no fold
// ever existed and every rule below had nothing to fire on.
func TestFoldCreatesTheFold(t *testing.T) {
	b := five()
	folds := &fakeFolds{}
	res, err := Apply(Request{
		Buf:  b,
		Op:   OpFold,
		Span: SpanForLines(b, 1, 2), // zfj
		At:   at(1, 1),
		Opt:  foldOpts(folds),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(folds.made) != 1 || folds.made[0] != [2]int{1, 2} {
		t.Fatalf("Create calls %v, want one over lines 1 to 2", folds.made)
	}
	if res.FoldFirst != 1 || res.FoldLast != 2 {
		t.Errorf("Result fold %d to %d, want 1 to 2", res.FoldFirst, res.FoldLast)
	}
	if _, _, ok := folds.ClosedFold(2); !ok {
		t.Error("line 2 is not in a closed fold after zfj")
	}
	if got := dump(b); got != "alpha\nbeta\ngamma\ndelta\nepsilon" {
		t.Errorf("zf changed the buffer: %q", got)
	}
}

// TestFoldWithNoWindowStillReportsItsRange: a caller with no fold tree -- every
// test in this package before, and any headless run -- gets the range and
// no fold, and nothing panics on the nil.
func TestFoldWithNoWindowStillReportsItsRange(t *testing.T) {
	b := five()
	res, err := Apply(Request{Buf: b, Op: OpFold, Span: SpanForLines(b, 1, 2), At: at(1, 1), Opt: DefaultOptions()})
	if err != nil {
		t.Fatal(err)
	}
	if res.FoldFirst != 1 || res.FoldLast != 2 {
		t.Errorf("Result fold %d to %d, want 1 to 2", res.FoldFirst, res.FoldLast)
	}
}

// TestDeleteTakesTheWholeClosedFold is "zfjdd": the fold is two lines and dd
// takes both of them, which is :help fold-behavior's "a closed fold is included
// as a whole" and the case the finding was written on.
func TestDeleteTakesTheWholeClosedFold(t *testing.T) {
	b := five()
	regs := &recorder{}
	res, err := Apply(Request{
		Buf:  b,
		Regs: regs,
		Op:   OpDelete,
		Span: SpanForLines(b, 1, 1), // dd on line 1
		Opt:  foldOpts(closedFold(1, 2)),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkDelete(t, b, regs, "gamma\ndelta\nepsilon", "alpha\nbeta\n", "V")
	checkCursor(t, res.Cursor, 1, 1)
}

// TestCharwiseDeleteInAClosedFoldComesOutLinewise is "zfjx" and "2lzfjx". The
// span is one character; the fold makes it two whole lines, and op_delete's own
// rule then makes it linewise, so @" comes back "alpha\nbeta\n" and V.
func TestCharwiseDeleteInAClosedFoldComesOutLinewise(t *testing.T) {
	for _, tc := range []struct {
		name string
		col  int
	}{
		{"zfjx", 1},
		{"2lzfjx", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := five()
			regs := &recorder{}
			_, err := Apply(Request{
				Buf:  b,
				Regs: regs,
				Op:   OpDelete,
				Span: SpanForChars(b, at(1, tc.col), 1),
				Opt:  foldOpts(closedFold(1, 2)),
			})
			if err != nil {
				t.Fatal(err)
			}
			checkDelete(t, b, regs, "gamma\ndelta\nepsilon", "alpha\nbeta\n", "V")
		})
	}
}

// TestChangeInAClosedFoldStaysCharwise is "zfjsX<Esc>", which leaves one line
// "X". c is not promoted to linewise the way d is, so the fold's text goes
// charwise and the two lines close up into one.
func TestChangeInAClosedFoldStaysCharwise(t *testing.T) {
	b := five()
	regs := &recorder{}
	res, err := Apply(Request{
		Buf:  b,
		Regs: regs,
		Op:   OpChange,
		Span: SpanForChars(b, at(1, 1), 1),
		Opt:  foldOpts(closedFold(1, 2)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Insert {
		t.Error("change did not ask for insert mode")
	}
	checkDelete(t, b, regs, "\ngamma\ndelta\nepsilon", "alpha\nbeta", "v")
}

// TestBackwardCharwiseDoesNotSeeTheFold is "2lzfjX", "2lzfjdh", "2lzfjd0" and
// "2lzfjdFa": every one of them deletes what it names and leaves the fold
// standing, where the same operator one column forward takes both lines. It is
// not X's quirk, it is the direction.
func TestBackwardCharwiseDoesNotSeeTheFold(t *testing.T) {
	t.Run("2lzfjX", func(t *testing.T) {
		b := five()
		regs := &recorder{}
		_, err := Apply(Request{
			Buf:  b,
			Regs: regs,
			Op:   OpDelete,
			Span: SpanForCharsBefore(b, at(1, 3), 1),
			Opt:  foldOpts(closedFold(1, 2)),
		})
		if err != nil {
			t.Fatal(err)
		}
		checkDelete(t, b, regs, "apha\nbeta\ngamma\ndelta\nepsilon", "l", "v")
	})

	t.Run("2lzfjd0", func(t *testing.T) {
		b := five()
		regs := &recorder{}
		folds := closedFold(1, 2)
		span, err := SpanForMotion(b, at(1, 3), motion.Result{To: at(1, 1), Kind: motion.KindCharExclusive, Ok: true}, motion.ForceNone, foldOpts(folds))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Span: span, Opt: foldOpts(folds)}); err != nil {
			t.Fatal(err)
		}
		checkDelete(t, b, regs, "pha\nbeta\ngamma\ndelta\nepsilon", "al", "v")
	})
}

// TestBackwardLinewiseDoesSeeTheFold is "jjzfjdk" with the fold on lines 3 and
// 4: the k lands on line 2 and the delete takes 2, 3 and 4. Backward stops a
// charwise operator and not a linewise one.
func TestBackwardLinewiseDoesSeeTheFold(t *testing.T) {
	b := five()
	regs := &recorder{}
	folds := closedFold(3, 4)
	span, err := SpanForMotion(b, at(3, 1), motion.Result{To: at(2, 1), Kind: motion.KindLine, Ok: true}, motion.ForceNone, foldOpts(folds))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Span: span, Opt: foldOpts(folds)}); err != nil {
		t.Fatal(err)
	}
	checkDelete(t, b, regs, "alpha\nepsilon", "beta\ngamma\ndelta\n", "V")
}

// TestForwardMotionIntoAClosedFold is "jjzfjggjd/gamma/e" with the fold on
// lines 3 and 4: an inclusive motion that lands inside the fold pulls the
// delete out to line 4 and the whole thing comes back linewise.
func TestForwardMotionIntoAClosedFold(t *testing.T) {
	b := five()
	regs := &recorder{}
	folds := closedFold(3, 4)
	span, err := SpanForMotion(b, at(2, 1), motionAt(3, 5, true), motion.ForceNone, foldOpts(folds))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Span: span, Opt: foldOpts(folds)}); err != nil {
		t.Fatal(err)
	}
	checkDelete(t, b, regs, "alpha\nepsilon", "beta\ngamma\ndelta\n", "V")
}

// TestExclusiveEndInColumnOneStaysOutOfTheFold is "jjzfjggd/gamma" with the
// fold on lines 3 and 4: the motion stops in column 1 of the fold's first line,
// which covers nothing on it, so the delete takes lines 1 and 2 and the fold
// survives. This is the half of the rule an implementation that just widens
// every span gets wrong.
func TestExclusiveEndInColumnOneStaysOutOfTheFold(t *testing.T) {
	b := five()
	regs := &recorder{}
	folds := closedFold(3, 4)
	span, err := SpanForMotion(b, at(1, 1), motion.Result{To: at(3, 1), Kind: motion.KindCharExclusive, Ok: true}, motion.ForceNone, foldOpts(folds))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Span: span, Opt: foldOpts(folds)}); err != nil {
		t.Fatal(err)
	}
	checkDelete(t, b, regs, "gamma\ndelta\nepsilon", "alpha\nbeta\n", "V")
	if _, _, ok := folds.ClosedFold(3); !ok {
		t.Error("the fold was pulled into the delete")
	}
}

// TestFoldMovesTheStartBeforeTheExclusiveRule is "2lzfjy/gamma", which yanks
// "alpha\nbeta\n" LINEWISE from column 3. Without the fold the same keys yank
// "pha\nbeta" charwise: the fold moves the start to column 1, and vim's
// exclusive rule then finds it at the first non-blank and makes the span
// linewise. It is the case that decides where the fold pass runs.
func TestFoldMovesTheStartBeforeTheExclusiveRule(t *testing.T) {
	b := five()
	regs := &recorder{}
	folds := closedFold(1, 2)
	span, err := SpanForMotion(b, at(1, 3), motion.Result{To: at(3, 1), Kind: motion.KindCharExclusive, Ok: true}, motion.ForceNone, foldOpts(folds))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Request{Buf: b, Regs: regs, Op: OpYank, Span: span, Opt: foldOpts(folds)}); err != nil {
		t.Fatal(err)
	}
	if got := regs.yanked.String(); got != "alpha\nbeta\n" {
		t.Errorf(`@" = %q, want "alpha\nbeta\n"`, got)
	}
	if got := regs.yanked.RegType(); got != "V" {
		t.Errorf("getregtype() = %q, want V", got)
	}
	if got := dump(b); got != "alpha\nbeta\ngamma\ndelta\nepsilon" {
		t.Errorf("a yank changed the buffer: %q", got)
	}
}

// TestLinewiseOperatorFromInsideAFold is "jzf2j:3<CR>dd" with the fold on lines
// 2 to 4 and the cursor put on line 3, which only: and a mark can do: the
// delete takes all three lines and not the one the cursor names.
func TestLinewiseOperatorFromInsideAFold(t *testing.T) {
	b := five()
	regs := &recorder{}
	_, err := Apply(Request{
		Buf:  b,
		Regs: regs,
		Op:   OpDelete,
		Span: SpanForLines(b, 3, 1),
		Opt:  foldOpts(closedFold(2, 4)),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkDelete(t, b, regs, "alpha\nepsilon", "beta\ngamma\ndelta\n", "V")
}

// TestUpperCaseTakesTheWholeFold is "jjzfjggjjgUU": every operator goes through
// the same pass, so gU on a closed fold upper-cases all of it.
func TestUpperCaseTakesTheWholeFold(t *testing.T) {
	b := five()
	_, err := Apply(Request{
		Buf:  b,
		Op:   OpUpper,
		Span: SpanForLines(b, 3, 1),
		Opt:  foldOpts(closedFold(3, 4)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := dump(b); got != "alpha\nbeta\nGAMMA\nDELTA\nepsilon" {
		t.Errorf("buffer %q", got)
	}
}

// TestVisualCharwiseTakesTheFoldAndItsNewline is "jjzfjvd" with the fold on
// lines 3 and 4: @" is "gamma\ndelta\n" and CHARWISE, the trailing newline
// included, because 'selection' is "inclusive" and the fold's end lands one
// past the last character. The same delete from normal mode is linewise. Both
// were run; they really do differ.
func TestVisualCharwiseTakesTheFoldAndItsNewline(t *testing.T) {
	b := five()
	regs := &recorder{}
	_, err := Apply(Request{
		Buf:    b,
		Regs:   regs,
		Op:     OpDelete,
		Visual: true,
		Span: Span{
			Type:  register.TypeChar,
			Range: text.Range{Start: at(3, 1), End: at(3, 2)},
		},
		Opt: foldOpts(closedFold(3, 4)),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkDelete(t, b, regs, "alpha\nbeta\nepsilon", "gamma\ndelta\n", "v")
}

// TestVisualCharwiseOnAFoldAtTheEndOfTheBuffer is "3jzfjvd" with the fold on
// lines 4 and 5: there is no newline after the last line to take, so the two
// lines become one empty one and @" has no trailing newline.
func TestVisualCharwiseOnAFoldAtTheEndOfTheBuffer(t *testing.T) {
	b := five()
	regs := &recorder{}
	_, err := Apply(Request{
		Buf:    b,
		Regs:   regs,
		Op:     OpDelete,
		Visual: true,
		Span: Span{
			Type:  register.TypeChar,
			Range: text.Range{Start: at(4, 1), End: at(4, 2)},
		},
		Opt: foldOpts(closedFold(4, 5)),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkDelete(t, b, regs, "alpha\nbeta\ngamma\n", "delta\nepsilon", "v")
}

// TestVisualBlockTakesTheFold is "jjzfjgg<C-v>jjd" with the fold on lines 3 and
// 4: the block was one column wide and two lines tall and comes out four lines
// tall and six wide, because foldAdjustVisual puts the far corner one past the
// last character of the fold's last line. @" is CTRL-V 6.
func TestVisualBlockTakesTheFold(t *testing.T) {
	b := five()
	regs := &recorder{}
	_, err := Apply(Request{
		Buf:    b,
		Regs:   regs,
		Op:     OpDelete,
		Visual: true,
		Span: Span{
			Type:  register.TypeBlock,
			Block: Block{First: 1, Last: 3, Left: 0, Right: 1},
		},
		Opt: foldOpts(closedFold(3, 4)),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkDelete(t, b, regs, "\n\n\n\nepsilon", "alpha\nbeta\ngamma\ndelta", "\x166")
}

// TestForcedBlockwiseIsLeftAlone: "d<C-v>jj" over a closed fold does something
// in vim that is neither the block that was typed nor the fold -- it comes out
// two lines tall with the fold untouched -- and nothing here reproduces it, so
// a forced block span goes through unchanged. Written down as a test because
// the next person to read fold.go will wonder whether it was forgotten.
func TestForcedBlockwiseIsLeftAlone(t *testing.T) {
	b := five()
	folds := closedFold(3, 4)
	span, err := SpanForMotion(b, at(1, 1), motion.Result{To: at(3, 1), Kind: motion.KindCharExclusive, Ok: true}, motion.ForceBlock, foldOpts(folds))
	if err != nil {
		t.Fatal(err)
	}
	if span.Block.Last != 3 {
		t.Errorf("block last line %d, want 3 unchanged", span.Block.Last)
	}
}

// TestPutReachesAroundAClosedFold is "yyjjzfjggjjp", where the cursor sits on
// the fold's first line, and "yyjjzfj:4<CR>p" and its P, where it sits on the
// last: with the fold on lines 3 and 4, p lands after delta wherever in the
// fold the cursor is, and P before gamma.
func TestPutReachesAroundAClosedFold(t *testing.T) {
	for _, tc := range []struct {
		name   string
		line   int
		before bool
		want   string
	}{
		{"p from the fold's first line", 3, false, "alpha\nbeta\ngamma\ndelta\nalpha\nepsilon"},
		{"p from its last", 4, false, "alpha\nbeta\ngamma\ndelta\nalpha\nepsilon"},
		{"P from its last", 4, true, "alpha\nbeta\nalpha\ngamma\ndelta\nepsilon"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := five()
			_, err := Put(PutRequest{
				Buf:    b,
				Val:    register.LineValue([]byte("alpha")),
				At:     at(tc.line, 1),
				Before: tc.before,
				Count:  1,
				Opt:    foldOpts(closedFold(3, 4)),
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := dump(b); got != tc.want {
				t.Errorf("buffer %q, want %q", got, tc.want)
			}
		})
	}
}

// TestOpenFoldChangesNothing: ClosedFold answers for closed folds only, and an
// open one leaves every operator exactly where it was. "zfjzodd" deletes one
// line.
func TestOpenFoldChangesNothing(t *testing.T) {
	b := five()
	regs := &recorder{}
	open := &fakeFolds{folds: [][2]int{{1, 1}}} // a one-line fold is never closed
	_, err := Apply(Request{
		Buf:  b,
		Regs: regs,
		Op:   OpDelete,
		Span: SpanForLines(b, 1, 1),
		Opt:  foldOpts(open),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkDelete(t, b, regs, "beta\ngamma\ndelta\nepsilon", "alpha\n", "V")
}

// TestNormalJoinIgnoresTheClosedFold: J and gJ typed in normal mode take the
// count's lines and nothing else, where every real operator over the same fold
// takes it whole.
//
// The fold is lines 2 to 4 -- "jzf2j" -- and the keys are the run:
//
//	jzf2jJ		"beta gamma", and delta stays a line of its own
//	jzf2jgJ		"betagamma", the same lines without the space
//	jzf2jggJ	"alpha beta", one line taken out of the middle of a
//			closed fold
//	jzf2j:4<CR>J	"delta epsilon", joined from a cursor inside the fold
//			across its last line
//	jzf2jgg3J	"alpha beta gamma": the count is lines, still not folds
//
// while "jzf2jdd", "jzf2jyy", "jzf2jx", "jzf2jD", "jzf2j>>", "jzf2jgUU" and
// "jzf2jgqq" over the same fold all take three lines. The visual case is the
// next test and it does widen.
func TestNormalJoinIgnoresTheClosedFold(t *testing.T) {
	cases := []struct {
		name        string
		first, last int
		spaces      bool
		want        string
		line, col   int
	}{
		{name: "jzf2jJ", first: 2, last: 3, spaces: true, want: "alpha\nbeta gamma\ndelta\nepsilon", line: 2, col: 5},
		{name: "jzf2jgJ", first: 2, last: 3, want: "alpha\nbetagamma\ndelta\nepsilon", line: 2, col: 5},
		{name: "jzf2jggJ", first: 1, last: 2, spaces: true, want: "alpha beta\ngamma\ndelta\nepsilon", line: 1, col: 6},
		{name: "jzf2j:4<CR>J", first: 4, last: 5, spaces: true, want: "alpha\nbeta\ngamma\ndelta epsilon", line: 4, col: 6},
		{name: "jzf2jgg3J", first: 1, last: 3, spaces: true, want: "alpha beta gamma\ndelta\nepsilon", line: 1, col: 11},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := five()
			res, err := Apply(Request{
				Buf:    b,
				Op:     OpJoin,
				Spaces: tc.spaces,
				At:     at(tc.first, 1),
				Span:   SpanForLines(b, tc.first, tc.last-tc.first+1),
				Opt:    foldOpts(closedFold(2, 4)),
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := dump(b); got != tc.want {
				t.Errorf("buffer\n%q\nwant\n%q", got, tc.want)
			}
			checkCursor(t, res.Cursor, tc.line, tc.col)
		})
	}
}

// TestVisualJoinTakesTheWholeFold is "jzf2jggVjJ": a Visual selection of lines
// 1 and 2 with the fold on 2 to 4 joins all four lines into one. vim's nv_join
// hands a Visual join to nv_operator and normal mode's J straight to do_join,
// and only the first of those two goes through do_pending_operator, which is
// where the fold widening lives.
func TestVisualJoinTakesTheWholeFold(t *testing.T) {
	b := five()
	res, err := Apply(Request{
		Buf:    b,
		Op:     OpJoin,
		Spaces: true,
		Visual: true,
		At:     at(1, 1),
		Span:   SpanForLines(b, 1, 2),
		Opt:    foldOpts(closedFold(2, 4)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := dump(b); got != "alpha beta gamma delta\nepsilon" {
		t.Errorf("buffer %q, want %q", got, "alpha beta gamma delta\nepsilon")
	}
	checkCursor(t, res.Cursor, 1, 17)
}
