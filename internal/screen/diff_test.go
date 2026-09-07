package screen

import (
	"strings"
	"testing"
)

// internal/diff computes the hunks and measures them against "vim -d"; this
// file is the other half, which is that the Grid actually shows them. Diff mode
// was computed and correct and invisible for a while: ]c and do worked on real
// hunks and the two windows drew their two buffers with no filler rows and no
// colour, which looks exactly like a diff of two identical files.

// fakeDiff is a DiffLines a test can state inline.
type fakeDiff struct {
	filler map[int]int
	kind   map[int]DiffKind
	text   map[int][2]int
}

func (f fakeDiff) Filler(lnum int) int { return f.filler[lnum] }

func (f fakeDiff) Line(lnum int) (DiffKind, bool) {
	k, ok := f.kind[lnum]
	return k, ok
}

func (f fakeDiff) Text(lnum int) (int, int, bool) {
	r, ok := f.text[lnum]
	return r[0], r[1], ok
}

// TestFillerRowsAreDrawn. A filler stands where the other side has lines this
// buffer does not, and it goes above the line it belongs to.
func TestFillerRowsAreDrawn(t *testing.T) {
	r := newRig(8, 20, "alpha", "bravo")
	r.f.Diff = map[int]DiffLines{r.w.ID: fakeDiff{filler: map[int]int{2: 2}}}

	// render() draws a bordered picture, so row 0 is the frame and the text
	// starts at 1.
	got := r.render()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) < 5 {
		t.Fatalf("only %d rows drawn:\n%s", len(lines), got)
	}
	row := func(i int) string { return strings.Trim(lines[i], "|") }
	if !strings.HasPrefix(row(1), "alpha") {
		t.Errorf("first text row is %q, want alpha", row(1))
	}
	// Two filler rows between the two buffer lines, filled with '-'.
	for _, i := range []int{2, 3} {
		if !strings.HasPrefix(row(i), "--") {
			t.Errorf("row %d is %q, want a filler row of '-'", i, row(i))
		}
	}
	if !strings.HasPrefix(row(4), "bravo") {
		t.Errorf("row 4 is %q, want the second line under the filler", row(4))
	}
}

// TestDiffLineTakesItsGroup. A changed line is DiffChange across its width and
// an added one is DiffAdd, both as the BASE, so anything drawn over them still
// wins.
func TestDiffLineTakesItsGroup(t *testing.T) {
	r := newRig(8, 20, "alpha", "bravo", "charlie")
	r.f.Diff = map[int]DiffLines{r.w.ID: fakeDiff{
		kind: map[int]DiffKind{1: DiffKindChange, 2: DiffKindAdd},
	}}
	r.render()

	s, g := r.s, LookupGroups(r.s.HL)
	if got := s.Grid.At(0, 0).HL; got != g.DiffChange {
		t.Errorf("line 1 is highlight %v, want DiffChange %v", got, g.DiffChange)
	}
	if got := s.Grid.At(1, 0).HL; got != g.DiffAdd {
		t.Errorf("line 2 is highlight %v, want DiffAdd %v", got, g.DiffAdd)
	}
	// A line the diff says nothing about is left alone.
	if got, want := s.Grid.At(2, 0).HL, g.Normal(); got != want {
		t.Errorf("line 3 is highlight %v, want Normal %v", got, want)
	}
}

// TestDiffTextCoversOnlyTheChangedColumns is the half that makes a diff
// readable rather than merely visible: vim paints DiffText over the bytes that
// actually differ and leaves the rest of the line DiffChange.
func TestDiffTextCoversOnlyTheChangedColumns(t *testing.T) {
	r := newRig(8, 20, "alpha bravo")
	r.f.Diff = map[int]DiffLines{r.w.ID: fakeDiff{
		kind: map[int]DiffKind{1: DiffKindChange},
		text: map[int][2]int{1: {6, 11}},
	}}
	r.render()

	s, g := r.s, LookupGroups(r.s.HL)
	for col := 0; col < 6; col++ {
		if got := s.Grid.At(0, col).HL; got != g.DiffChange {
			t.Fatalf("column %d is %v, want DiffChange: only 6..11 changed", col, got)
		}
	}
	for col := 6; col < 11; col++ {
		if got := s.Grid.At(0, col).HL; got != g.DiffText {
			t.Fatalf("column %d is %v, want DiffText", col, got)
		}
	}
}

// TestNoDiffSourceChangesNothing. Every window in this editor is not in diff
// mode almost all of the time, and the cost of that has to be nil: a frame with
// no Diff map draws exactly what it drew before this existed.
func TestNoDiffSourceChangesNothing(t *testing.T) {
	a1 := newRig(8, 20, "alpha", "bravo")
	a1.f.Diff = nil

	a2 := newRig(8, 20, "alpha", "bravo")
	a2.f.Diff = map[int]DiffLines{}

	if a, b := a1.renderHL(), a2.renderHL(); a != b {
		t.Errorf("a nil Diff and an empty one drew differently:\n%s\n---\n%s", a, b)
	}
}
