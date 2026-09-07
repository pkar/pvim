package screen

import (
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// numbered returns a buffer of "line 1" to "line n".
func numbered(n int) *text.Buffer {
	body := ""
	for i := 1; i <= n; i++ {
		body += "line " + itoa(i) + "\n"
	}
	return text.Read([]byte(body))
}

// zf creates a fold closed, which is what vim does: zf on a paragraph
// collapses it there and then.
func TestCreateMakesAClosedFold(t *testing.T) {
	f := NewFolds()
	if !f.Create(2, 5) {
		t.Fatal("Create(2, 5) refused")
	}
	fold := f.ClosedAt(3)
	if fold == nil {
		t.Fatal("line 3 is not in a closed fold")
	}
	if fold.Start != 2 || fold.End != 5 {
		t.Errorf("closed fold is %d..%d, want 2..5", fold.Start, fold.End)
	}
	if f.ClosedAt(1) != nil || f.ClosedAt(6) != nil {
		t.Error("lines outside the fold are folded")
	}
}

// A fold of one line is never displayed closed, because 'foldminlines' is 1
// and a fold has to cover more lines than that. Measured: ":2fold" on a
// 20-line file leaves line 2 showing its own text.
func TestOneLineFoldIsNotClosed(t *testing.T) {
	f := NewFolds()
	if !f.Create(2, 2) {
		t.Fatal("Create(2, 2) refused")
	}
	if got := f.ClosedAt(2); got != nil {
		t.Errorf("a one-line fold displayed closed: %+v", got)
	}
	if f.LevelAt(2) != 1 {
		t.Error("the fold exists but foldlevel() does not see it")
	}
}

// The fold line vim's default 'foldtext' makes, at two nesting levels.
//
// vim --clean, :set foldmethod=manual on "line 1" to "line 20":
//
//	2Gzf3j -> +-- 4 lines: line 2
//	3Gzfj 2Gzf5j zo -> +--- 2 lines: line 3
func TestFoldText(t *testing.T) {
	b := numbered(20)
	cases := []struct {
		fold  *Fold
		level int
		want  string
	}{
		{&Fold{Start: 2, End: 5}, 1, "+--  4 lines: line 2"},
		{&Fold{Start: 3, End: 4}, 2, "+---  2 lines: line 3"},
		{&Fold{Start: 1, End: 1}, 1, "+--  1 line: line 1"},
	}
	for _, c := range cases {
		if got := FoldText(b, c.fold, c.level); got != c.want {
			t.Errorf("FoldText(%d..%d, level %d) = %q, want %q",
				c.fold.Start, c.fold.End, c.level, got, c.want)
		}
	}
}

// The first line of a fold is shown with its indent stripped and any tab in it
// turned into a space, because a fold line is one row and a tab there would
// move everything after it to a stop that means nothing.
func TestFoldTextDeindents(t *testing.T) {
	b := text.Read([]byte("func main() {\n\tone\ttwo\n\tthree\n}\n"))
	if got, wantS := FoldText(b, &Fold{Start: 2, End: 3}, 1), "+--  2 lines: one two"; got != wantS {
		t.Errorf("FoldText = %q, want %q", got, wantS)
	}
}

// A fold inside another becomes its child, and opening the outer one leaves
// the inner one closed. Measured: 3Gzfj, 2Gzf5j, zo shows "+--- 2 lines".
func TestNestedFolds(t *testing.T) {
	f := NewFolds()
	f.Create(3, 4)
	f.Create(2, 7)

	if got := f.ClosedAt(3); got == nil || got.Start != 2 {
		t.Fatalf("the outer fold does not stand for line 3: %+v", got)
	}
	if !f.Open(3) {
		t.Fatal("Open did not open the outer fold")
	}
	inner := f.ClosedAt(3)
	if inner == nil || inner.Start != 3 || inner.End != 4 {
		t.Fatalf("after opening the outer fold, line 3 is in %+v, want 3..4", inner)
	}
	if f.LevelAt(3) != 2 {
		t.Errorf("foldlevel at line 3 is %d, want 2", f.LevelAt(3))
	}
}

// A fold that crosses another's boundary is refused whole. Half a fold is not
// something the renderer can draw and vim will not make one either.
func TestOverlappingFoldRefused(t *testing.T) {
	f := NewFolds()
	f.Create(2, 5)
	if f.Create(4, 8) {
		t.Error("a fold overlapping an existing one was accepted")
	}
	if n := len(f.Top()); n != 1 {
		t.Errorf("%d top-level folds after the refusal, want 1", n)
	}
}

// za is what <Space> is mapped to in the vimrc, so it is the most-pressed fold
// key in the editor: closed becomes open and open becomes closed.
func TestToggle(t *testing.T) {
	f := NewFolds()
	f.Create(2, 5)
	if !f.Toggle(3) || f.ClosedAt(3) != nil {
		t.Error("za did not open a closed fold")
	}
	if !f.Toggle(3) || f.ClosedAt(3) == nil {
		t.Error("za did not close an open fold")
	}
}

// zR, zM, zr and zm move 'foldlevel' and the folds follow it.
func TestFoldLevelKeys(t *testing.T) {
	f := NewFolds()
	f.Create(3, 4)
	f.Create(2, 7)

	f.OpenAll()
	if f.ClosedAt(3) != nil {
		t.Error("zR left a fold closed")
	}
	if f.Level != 2 {
		t.Errorf("zR left 'foldlevel' at %d, want the deepest nesting, 2", f.Level)
	}

	f.CloseAll()
	if got := f.ClosedAt(3); got == nil || got.Start != 2 {
		t.Error("zM did not close the outer fold")
	}
	if f.Level != 0 {
		t.Errorf("zM left 'foldlevel' at %d, want 0", f.Level)
	}

	f.More() // zr
	if got := f.ClosedAt(3); got == nil || got.Start != 3 {
		t.Errorf("after zr, line 3 is in %+v, want the inner fold 3..4", got)
	}
	f.Less() // zm
	if got := f.ClosedAt(3); got == nil || got.Start != 2 {
		t.Errorf("after zm, line 3 is in %+v, want the outer fold", got)
	}
}

// zd removes the innermost fold and its children move up to take its place.
func TestDelete(t *testing.T) {
	f := NewFolds()
	f.Create(3, 4)
	f.Create(2, 7)
	f.OpenAll()

	if !f.Delete(3) {
		t.Fatal("zd deleted nothing")
	}
	if f.LevelAt(3) != 1 {
		t.Errorf("foldlevel at line 3 is %d after deleting the inner fold, want 1", f.LevelAt(3))
	}
	f.DeleteAll()
	if len(f.Top()) != 0 {
		t.Error("zE left folds behind")
	}
}

// 'foldmethod' indent, and the blank-line rule that decides whether two
// indented blocks are one fold or two.
//
// vim --clean, :set shiftwidth=2 tabstop=8 foldmethod=indent.
func TestIndentRecompute(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		folds [][2]int // start, end of each top-level fold
	}{
		{
			// A blank line between two indented blocks joins them.
			"blank between two indented blocks",
			"a\n  b1\n  b2\n\n  c1\nd\n  e1\n",
			[][2]int{{2, 5}, {7, 7}},
		},
		{
			// A blank line between an indented block and an unindented line
			// does not, because the blank takes the lower of its neighbours.
			"blank before an unindented line",
			"a\n  b1\n  b2\n\n\nc\n  d1\n  d2\n",
			[][2]int{{2, 3}, {7, 8}},
		},
		{
			"two levels",
			"a\n  b\n    c\n    d\n  e\nf\n",
			[][2]int{{2, 5}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &Folds{Method: "indent", Enable: true}
			f.Recompute(text.Read([]byte(c.body)), 2, 8)
			got := f.Top()
			if len(got) != len(c.folds) {
				t.Fatalf("%d folds, want %d: %+v", len(got), len(c.folds), got)
			}
			for i, fold := range got {
				if fold.Start != c.folds[i][0] || fold.End != c.folds[i][1] {
					t.Errorf("fold %d is %d..%d, want %d..%d",
						i, fold.Start, fold.End, c.folds[i][0], c.folds[i][1])
				}
			}
		})
	}
}

// A fold whose ends have not moved keeps whatever the reader did to it, so a
// zo on a function survives typing inside it.
func TestIndentRecomputeKeepsOpenFolds(t *testing.T) {
	b := text.Read([]byte("a\n  b\n  c\nd\n"))
	f := &Folds{Method: "indent", Enable: true}
	f.Recompute(b, 2, 8)
	if f.ClosedAt(2) == nil {
		t.Fatal("the indent fold was not closed at 'foldlevel' 0")
	}
	f.Open(2)
	f.Recompute(b, 2, 8)
	if f.ClosedAt(2) != nil {
		t.Error("a recompute closed a fold the reader had opened")
	}
}

// 'foldmethod' manual means the tree is the reader's and a recompute leaves it
// alone. That is the whole point of manual.
func TestRecomputeIgnoresManual(t *testing.T) {
	f := NewFolds()
	f.Create(2, 5)
	f.Recompute(text.Read([]byte("a\n  b\n  c\nd\ne\nf\n")), 2, 8)
	if got := f.ClosedAt(3); got == nil || got.Start != 2 || got.End != 5 {
		t.Errorf("a recompute changed a manual fold: %+v", got)
	}
}

// zi: with 'foldenable' off every fold is drawn open and none of the state is
// lost.
func TestFoldEnable(t *testing.T) {
	f := NewFolds()
	f.Create(2, 5)
	f.Enable = false
	if f.ClosedAt(3) != nil {
		t.Error("'nofoldenable' still shows a closed fold")
	}
	f.Enable = true
	if f.ClosedAt(3) == nil {
		t.Error("'foldenable' lost the closed state")
	}
}
