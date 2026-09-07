package motion

import (
	"sort"
	"testing"
)

// TestForceApply is :help o_v, which is four sentences and the single most
// likely thing to be written from memory: v after an operator does not mean
// "charwise", it means "the other one", and dvj is not dj.
func TestForceApply(t *testing.T) {
	cases := []struct {
		force Force
		in    Kind
		want  Kind
	}{
		{ForceNone, KindCharExclusive, KindCharExclusive},
		{ForceNone, KindLine, KindLine},
		{ForceChar, KindCharExclusive, KindCharInclusive},
		{ForceChar, KindCharInclusive, KindCharExclusive},
		{ForceChar, KindLine, KindCharExclusive},
		{ForceChar, KindBlock, KindCharExclusive},
		{ForceLine, KindCharExclusive, KindLine},
		{ForceLine, KindCharInclusive, KindLine},
		{ForceBlock, KindLine, KindBlock},
	}
	for _, tc := range cases {
		if got := tc.force.Apply(tc.in); got != tc.want {
			t.Errorf("Force(%d).Apply(%v) = %v, want %v", tc.force, tc.in, got, tc.want)
		}
	}
}

// TestKindsAgainstVimDoc is the table's whole reason for existing. Every kind
// below is the tag on that command in vim 9.2's own motion.txt, written out
// here so that a change to the table has to disagree with a second copy of the
// documentation before it can land.
//
// The pairs are what an implementation gets wrong: f is inclusive and F is
// exclusive, e is inclusive and w is exclusive, gj is exclusive and j is
// linewise.
func TestKindsAgainstVimDoc(t *testing.T) {
	want := map[string]Kind{
		"h": KindCharExclusive, "l": KindCharExclusive,
		"0": KindCharExclusive, "^": KindCharExclusive,
		"$": KindCharInclusive, "g_": KindCharInclusive,
		"g0": KindCharExclusive, "g$": KindCharInclusive,
		"f": KindCharInclusive, "F": KindCharExclusive,
		"t": KindCharInclusive, "T": KindCharExclusive,
		"j": KindLine, "k": KindLine,
		"gj": KindCharExclusive, "gk": KindCharExclusive,
		"+": KindLine, "-": KindLine, "_": KindLine,
		"G": KindLine, "gg": KindLine,
		"w": KindCharExclusive, "W": KindCharExclusive,
		"b": KindCharExclusive, "B": KindCharExclusive,
		"e": KindCharInclusive, "E": KindCharInclusive,
		"ge": KindCharInclusive, "gE": KindCharInclusive,
		"(": KindCharExclusive, ")": KindCharExclusive,
		"{": KindCharExclusive, "}": KindCharExclusive,
		"]]": KindCharExclusive, "[[": KindCharExclusive,
		"`": KindCharExclusive, "'": KindLine,
		"%": KindCharInclusive,
		"H": KindLine, "M": KindLine, "L": KindLine,
		"n": KindCharExclusive, "N": KindCharExclusive,
		"|": KindCharExclusive, "go": KindCharExclusive,
	}
	for keys, kind := range want {
		m, ok := ByKeys(keys)
		if !ok {
			t.Errorf("%q is not in the table", keys)
			continue
		}
		if m.Kind != kind {
			t.Errorf("%q is %v, motion.txt says %v", keys, m.Kind, kind)
		}
	}
}

// TestJumps is what decides whether ” takes you back. A motion wrongly marked
// a jump makes ” land somewhere nobody asked for; one wrongly not marked
// loses the way back.
//
// This list is measured and not quoted. :help jumplist names sixteen commands
// and is short: gg, go, ][, [], [(, [{, ]) and ]} all set the ' mark on vim
// 9.2 here, checked by running "20G<motion>”" through vim --clean and asking
// where the cursor ended up. j, +, |, gj and ge were measured the same way and
// do not.
func TestJumps(t *testing.T) {
	jumps := map[string]bool{
		"'": true, "`": true, "G": true, "gg": true, "go": true,
		"n": true, "N": true, "%": true,
		"(": true, ")": true, "{": true, "}": true,
		"[[": true, "]]": true, "][": true, "[]": true,
		"[(": true, "[{": true, "])": true, "]}": true,
		"H": true, "M": true, "L": true,
	}
	for _, m := range All() {
		if got := m.Jump; got != jumps[m.Keys] {
			t.Errorf("%q Jump = %v, want %v", m.Keys, got, jumps[m.Keys])
		}
	}
}

// TestNeedsArg: the four finds and the two mark motions read a character after
// themselves, and nothing else does. A motion wrongly marked here eats the next
// keystroke.
func TestNeedsArg(t *testing.T) {
	want := map[string]bool{"f": true, "F": true, "t": true, "T": true, "'": true, "`": true}
	for _, m := range All() {
		if m.NeedsArg != want[m.Keys] {
			t.Errorf("%q NeedsArg = %v, want %v", m.Keys, m.NeedsArg, want[m.Keys])
		}
	}
}

// TestIsPrefix is how the mode machine knows to wait after g rather than beep,
// and how it knows not to wait after w.
func TestIsPrefix(t *testing.T) {
	for _, keys := range []string{"g", "[", "]"} {
		if !IsPrefix(keys) {
			t.Errorf("IsPrefix(%q) = false, want true", keys)
		}
	}
	for _, keys := range []string{"w", "gg", "zz"} {
		if IsPrefix(keys) {
			t.Errorf("IsPrefix(%q) = true, want false", keys)
		}
	}
}

// TestEveryMotionHasABody catches the merge that adds a table row and forgets
// the function, which would be a nil Do and a panic on that keystroke.
func TestEveryMotionHasABody(t *testing.T) {
	var names []string
	for _, m := range All() {
		if m.Do == nil {
			names = append(names, m.Keys)
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		t.Errorf("motions with no Do: %v", names)
	}
}

// TestCount1 is vim's default of one, which is not the same as a count of one:
// % with no count is the matchpair jump and 1% is the first line of the file,
// so Request keeps the zero and only this fills it in.
func TestCount1(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{0, 1}, {-3, 1}, {1, 1}, {7, 7}} {
		if got := (Request{Count: tc.in}).Count1(); got != tc.want {
			t.Errorf("Request{Count: %d}.Count1() = %d, want %d", tc.in, got, tc.want)
		}
	}
}
