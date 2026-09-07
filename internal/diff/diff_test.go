package diff

import (
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The tests that need nothing but this package. The measurements against vim
// and against git are in vim_test.go and git_test.go and both are behind
// -short, so these are what `make check-fast` runs.

// TestDiffRebuildsTheOtherSide is the property that makes the rest of the
// package meaningful: applying the blocks to a turns it into b, for random
// pairs. A diff that is not minimal still passes this, which is the point --
// it is the correctness test, and vim_test.go is the minimality test.
func TestDiffRebuildsTheOtherSide(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 2000; i++ {
		a := randLines(r, r.IntN(30))
		b := mutate(r, a)
		cs := Diff(a, b, Options{})
		got := rebuild(a, b, cs)
		if !reflect.DeepEqual(stringLines(got), stringLines(b)) {
			t.Fatalf("case %d:\na  = %q\nb  = %q\ngot = %q\ncs = %+v", i,
				stringLines(a), stringLines(b), stringLines(got), cs)
		}
	}
}

// TestBlocksAreOrderedAndDisjoint is the invariant every caller relies on:
// Filler walks the blocks in order and BlockAt takes the first that matches,
// so overlapping or out-of-order blocks would put filler lines in two places
// and make do act on a block the cursor is not in.
func TestBlocksAreOrderedAndDisjoint(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 2000; i++ {
		a := randLines(r, r.IntN(30))
		b := mutate(r, a)
		cs := Diff(a, b, Options{})
		for j, c := range cs {
			if c.ACount < 0 || c.BCount < 0 || c.AStart < 1 || c.BStart < 1 {
				t.Fatalf("case %d block %d is %+v", i, j, c)
			}
			if c.ACount == 0 && c.BCount == 0 {
				t.Fatalf("case %d block %d changes nothing: %+v", i, j, c)
			}
			if j == 0 {
				continue
			}
			p := cs[j-1]
			// Strictly after, and not touching: a block that starts exactly
			// where the one before it ended is the same block and merge
			// should have joined them.
			if c.AStart <= p.AStart+p.ACount && c.BStart <= p.BStart+p.BCount {
				t.Fatalf("case %d blocks %d and %d touch: %+v then %+v", i, j-1, j, p, c)
			}
		}
	}
}

// TestFillerTotalsMatchTheLineCounts is the arithmetic diff mode depends on:
// with the fillers drawn, the two sides are the same number of screen rows.
func TestFillerTotalsMatchTheLineCounts(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < 500; i++ {
		a := randLines(r, r.IntN(20))
		b := mutate(r, a)
		p := New(a, b, Options{})
		rows := func(s Side) int {
			n := p.Lines(s)
			for lnum := 1; lnum <= n+1; lnum++ {
				n += p.Filler(s, lnum)
			}
			return n
		}
		if x, y := rows(A), rows(B); x != y {
			t.Fatalf("case %d: %d rows on the left, %d on the right\na=%q\nb=%q",
				i, x, y, stringLines(a), stringLines(b))
		}
	}
}

// TestParseOptions reads the vimrc's own 'diffopt' and the words around it.
func TestParseOptions(t *testing.T) {
	got := ParseOptions("vertical,filler,iwhite")
	want := Options{Vertical: true, Filler: true, IWhite: true}
	if got != want {
		t.Errorf("the vimrc's diffopt parsed as %+v, want %+v", got, want)
	}
	got = ParseOptions("internal,filler,closeoff,context:4,algorithm:patience")
	if want := (Options{Filler: true}); got != want {
		t.Errorf("words this package does not act on parsed as %+v, want %+v", got, want)
	}
	if got := ParseOptions("icase,iblank,iwhiteall,iwhiteeol"); !got.ICase ||
		!got.IBlank || !got.IWhiteAll || !got.IWhiteEol {
		t.Errorf("the ignore flags parsed as %+v", got)
	}
}

// TestNormalize is the white space table from the doc comment, as a test, so
// that the third row -- the surprising one -- cannot quietly change.
func TestNormalize(t *testing.T) {
	for _, c := range []struct {
		opt  Options
		a, b string
		same bool
	}{
		{Options{IWhite: true}, "x  y", "x y", true},
		{Options{IWhite: true}, "x ", "x", true},
		{Options{IWhite: true}, "  x", "x", false},
		{Options{IWhite: true}, "x\ty", "x y", true},
		{Options{}, "x  y", "x y", false},
		{Options{IWhiteAll: true}, "  x", "x", true},
		{Options{IWhiteEol: true}, "x  y ", "x  y", true},
		{Options{IWhiteEol: true}, "x  y", "x y", false},
		{Options{ICase: true}, "Hello", "hello", true},
	} {
		name := c.a + "|" + c.b
		t.Run(name, func(t *testing.T) {
			if same := c.opt.normalize([]byte(c.a)) == c.opt.normalize([]byte(c.b)); same != c.same {
				t.Errorf("%q and %q compare same=%v under %+v, want %v", c.a, c.b, same, c.opt, c.same)
			}
		})
	}
}

// TestBigInputFinishes is the budget: two files with nothing in common and
// more lines than the budget still produce a diff, and quickly.
func TestBigInputFinishes(t *testing.T) {
	a := make([][]byte, 20000)
	b := make([][]byte, 20000)
	for i := range a {
		a[i] = []byte("a" + strconv.Itoa(i))
		b[i] = []byte("b" + strconv.Itoa(i))
	}
	cs := Diff(a, b, Options{})
	if len(cs) == 0 {
		t.Fatal("two files with nothing in common diffed to nothing")
	}
	got := rebuild(a, b, cs)
	if len(got) != len(b) {
		t.Fatalf("applying the diff gave %d lines, want %d", len(got), len(b))
	}
	for i := range got {
		if string(got[i]) != string(b[i]) {
			t.Fatalf("line %d is %q, want %q", i+1, got[i], b[i])
		}
	}
}

// rebuild applies the blocks to a and returns what should be b.
func rebuild(a, b [][]byte, cs []Change) [][]byte {
	out := make([][]byte, 0, len(b))
	at := 1
	for _, c := range cs {
		for ; at < c.AStart; at++ {
			out = append(out, a[at-1])
		}
		for i := 0; i < c.BCount; i++ {
			out = append(out, b[c.BStart+i-1])
		}
		at += c.ACount
	}
	for ; at <= len(a); at++ {
		out = append(out, a[at-1])
	}
	return out
}

// randLines is a file of short lines drawn from a small alphabet, so that
// repeated lines and ambiguous diffs happen often.
func randLines(r *rand.Rand, n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte(strings.Repeat(string(rune('a'+r.IntN(4))), 1+r.IntN(3)))
	}
	return out
}

// mutate is a file with some lines changed, some deleted and some inserted.
func mutate(r *rand.Rand, a [][]byte) [][]byte {
	out := make([][]byte, 0, len(a)+4)
	for _, l := range a {
		switch r.IntN(6) {
		case 0: // delete
		case 1: // change
			out = append(out, []byte(string(l)+"'"))
		case 2: // insert before
			out = append(out, []byte("new"+strconv.Itoa(r.IntN(3))), l)
		default:
			out = append(out, l)
		}
	}
	if r.IntN(3) == 0 {
		out = append(out, []byte("tail"))
	}
	return out
}
