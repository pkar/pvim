package screen

import (
	"fmt"

	"github.com/pkar/pvim/internal/text"
)

// Folds are a window's, not a buffer's: two windows on one file fold
// differently, which is the whole point of ":sp" over a long function. So a
// Folds hangs off a window and this package owns it, because a fold is a thing
// that changes what is on the screen and changes nothing in the file.
//
// Two methods, as: 'foldmethod' manual and indent. expr, syntax,
// marker and diff are not built and Recompute leaves the tree alone for them.
//
// Every string and number here was read off vim 9.2.0321 through a screen dump
// rather than out of the documentation. The fold line in particular:
//
//	:set foldmethod=manual, 2Gzf3j, on a file of "line 1".."line 20"
//	+-- 4 lines: line 2--------------------
//
// which is "+-", one dash per fold level, the line count in %3d, " lines: ",
// the first line of the fold with its leading white space removed, and then
// the 'fold' item of 'fillchars' to the right-hand edge.

// FoldNestMax is 'foldnestmax': how deep indent folds go before vim stops
// counting. It is not in internal/options because nothing but this file reads
// it and the vimrc never sets it; vim's default is 20.
const FoldNestMax = 20

// FoldMinLines is 'foldminlines': a fold is displayed closed only when it
// covers MORE than this many lines. Vim's default is 1, and the ">" rather
// than ">=" is not a guess: ":2fold" on a 20-line file leaves line 2 showing
// its own text under vim 9.2.0321, so a one-line fold never closes.
const FoldMinLines = 1

// Fold is one fold: a range of buffer lines that may be shown as one.
type Fold struct {
	// Start and End are buffer lines, 1-based and inclusive.
	Start, End int

	// Closed is whether this fold is displayed as a single line. A closed
	// fold hides its nested folds whatever their own state, which is why
	// ClosedAt returns the outermost.
	Closed bool

	// Nested are the folds wholly inside this one, in line order.
	Nested []*Fold
}

// Lines is how many buffer lines the fold covers.
func (f *Fold) Lines() int { return f.End - f.Start + 1 }

// Contains reports whether buffer line n is inside the fold.
func (f *Fold) Contains(n int) bool { return n >= f.Start && n <= f.End }

// Folds is the fold tree of one window plus the three options that decide
// which of them are closed.
type Folds struct {
	// Method is 'foldmethod'. Only "manual" and "indent" do anything.
	Method string

	// Enable is 'foldenable', which zi toggles: with it off every fold is
	// drawn open and none of the state is lost.
	Enable bool

	// Level is 'foldlevel': folds nested deeper than this are closed. zr
	// raises it, zm lowers it, zR and zM take it to the ends.
	Level int

	// top are the outermost folds, in line order.
	top []*Fold
}

// NewFolds returns an empty manual fold tree with 'foldenable' on and
// 'foldlevel' zero, which is what a new window gets.
func NewFolds() *Folds {
	return &Folds{Method: "manual", Enable: true}
}

// Top returns the outermost folds, in line order. It is for tests and for the
// window renderer; nothing mutates through it.
func (f *Folds) Top() []*Fold {
	if f == nil {
		return nil
	}
	return f.top
}

// Create makes a fold over the given lines, which is zf and ":fold".
//
// A fold wholly inside an existing one becomes its child; one that contains
// existing folds adopts them. Vim refuses a fold that overlaps another
// partially -- E350 in spirit -- and so does this, by returning false and
// changing nothing, because half a fold is not a thing the renderer can draw.
//
// Vim creates the fold closed, which is why zf on a paragraph collapses it.
func (f *Folds) Create(start, end int) bool {
	if start > end {
		start, end = end, start
	}
	if start < 1 {
		return false
	}
	_, ok := insertInto(&f.top, start, end)
	return ok
}

// insertInto puts a fold over start..end into the list, descending into a
// parent that contains it. It reports false when the range crosses an existing
// fold's boundary.
func insertInto(list *[]*Fold, start, end int) (*Fold, bool) {
	for _, f := range *list {
		switch {
		case start >= f.Start && end <= f.End:
			return insertInto(&f.Nested, start, end)
		case end < f.Start || start > f.End:
			// disjoint, keep looking
		case start <= f.Start && end >= f.End:
			// contains it, handled below by adoption
		default:
			return nil, false // partial overlap
		}
	}

	fresh := &Fold{Start: start, End: end, Closed: true}
	var rest []*Fold
	for _, f := range *list {
		if f.Start >= start && f.End <= end {
			fresh.Nested = append(fresh.Nested, f)
			continue
		}
		rest = append(rest, f)
	}
	rest = append(rest, fresh)
	sortFolds(rest)
	*list = rest
	return fresh, true
}

// sortFolds puts a list of siblings in line order. There are never many, and
// an insertion sort keeps the order stable for folds that start on the same
// line, which cannot happen but would be a confusing way to find out.
func sortFolds(list []*Fold) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].Start < list[j-1].Start; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// Delete removes the innermost fold at line n, which is zd. The fold's
// children move up to take its place, as they do in vim.
func (f *Folds) Delete(n int) bool {
	return deleteAt(&f.top, n)
}

func deleteAt(list *[]*Fold, n int) bool {
	for i, fold := range *list {
		if !fold.Contains(n) {
			continue
		}
		if deleteAt(&fold.Nested, n) {
			return true
		}
		rest := append(append([]*Fold{}, (*list)[:i]...), fold.Nested...)
		rest = append(rest, (*list)[i+1:]...)
		sortFolds(rest)
		*list = rest
		return true
	}
	return false
}

// DeleteAll removes every fold, which is zE.
func (f *Folds) DeleteAll() { f.top = nil }

// InnermostAt returns the deepest fold containing line n.
func (f *Folds) InnermostAt(n int) *Fold {
	if f == nil {
		return nil
	}
	var found *Fold
	list := f.top
	for {
		var next *Fold
		for _, fold := range list {
			if fold.Contains(n) {
				next = fold
				break
			}
		}
		if next == nil {
			return found
		}
		found, list = next, next.Nested
	}
}

// ClosedAt returns the outermost closed fold containing line n.
//
// Outermost is the answer the renderer needs: a closed fold three deep inside
// another closed fold contributes nothing, because its parent already stands
// for every line in it.
func (f *Folds) ClosedAt(n int) *Fold {
	if f == nil || !f.Enable {
		return nil
	}
	list := f.top
	for depth := 1; ; depth++ {
		var here *Fold
		for _, fold := range list {
			if fold.Contains(n) {
				here = fold
				break
			}
		}
		if here == nil {
			return nil
		}
		if here.Closed && here.Lines() > FoldMinLines {
			return here
		}
		list = here.Nested
	}
}

// Open opens the innermost closed fold at line n, which is zo. It returns
// false when there was nothing closed to open.
func (f *Folds) Open(n int) bool {
	fold := f.ClosedAt(n)
	if fold == nil {
		return false
	}
	fold.Closed = false
	return true
}

// Close closes the innermost open fold at line n, which is zc.
func (f *Folds) Close(n int) bool {
	if f == nil {
		return false
	}
	// The innermost open fold, which is the deepest one that is not closed.
	var found *Fold
	list := f.top
	for {
		var here *Fold
		for _, fold := range list {
			if fold.Contains(n) {
				here = fold
				break
			}
		}
		if here == nil {
			break
		}
		if !here.Closed {
			found = here
		}
		if here.Closed {
			break
		}
		list = here.Nested
	}
	if found == nil {
		return false
	}
	found.Closed = true
	f.Enable = true
	return true
}

// Toggle is za: close an open fold, open a closed one. It is what <Space> is
// mapped to in the vimrc, which makes it the most-pressed fold key in the
// editor.
func (f *Folds) Toggle(n int) bool {
	if f.ClosedAt(n) != nil {
		return f.Open(n)
	}
	return f.Close(n)
}

// OpenAll is zR: every fold open, 'foldlevel' at the deepest nesting.
func (f *Folds) OpenAll() {
	f.setAll(false)
	f.Level = f.Depth()
}

// CloseAll is zM: every fold closed, 'foldlevel' zero.
func (f *Folds) CloseAll() {
	f.setAll(true)
	f.Level = 0
	f.Enable = true
}

// setAll writes Closed through the whole tree.
func (f *Folds) setAll(closed bool) {
	var walk func([]*Fold)
	walk = func(list []*Fold) {
		for _, fold := range list {
			fold.Closed = closed
			walk(fold.Nested)
		}
	}
	walk(f.top)
}

// More is zr: raise 'foldlevel' by one and open the folds that reach it.
func (f *Folds) More() { f.SetLevel(f.Level + 1) }

// Less is zm: lower 'foldlevel' by one and close the folds below it.
func (f *Folds) Less() { f.SetLevel(f.Level - 1) }

// SetLevel applies 'foldlevel': a fold nested deeper than level is closed and
// one at or above it is open. Depth is 0-based here, so level 0 closes the
// outermost folds, which is what zM leaves behind.
func (f *Folds) SetLevel(level int) {
	if level < 0 {
		level = 0
	}
	if d := f.Depth(); level > d {
		level = d
	}
	f.Level = level
	var walk func([]*Fold, int)
	walk = func(list []*Fold, depth int) {
		for _, fold := range list {
			fold.Closed = depth >= level
			walk(fold.Nested, depth+1)
		}
	}
	walk(f.top, 0)
	if level == 0 {
		f.Enable = true
	}
}

// Depth is how deep the tree nests: 0 with no folds at all.
func (f *Folds) Depth() int {
	var walk func([]*Fold, int) int
	walk = func(list []*Fold, depth int) int {
		best := depth
		for _, fold := range list {
			if n := walk(fold.Nested, depth+1); n > best {
				best = n
			}
		}
		return best
	}
	if f == nil {
		return 0
	}
	return walk(f.top, 0)
}

// LevelAt is what vim's foldlevel() returns: how many folds contain line n.
func (f *Folds) LevelAt(n int) int {
	if f == nil {
		return 0
	}
	level, list := 0, f.top
	for {
		var here *Fold
		for _, fold := range list {
			if fold.Contains(n) {
				here = fold
				break
			}
		}
		if here == nil {
			return level
		}
		level++
		list = here.Nested
	}
}

// Recompute rebuilds an indent fold tree from the buffer.
//
// Vim's rule, from foldlevelIndent(): a line's level is its indent in cells
// divided by 'shiftwidth', capped at 'foldnestmax'. A blank line has no level
// of its own and takes the surrounding one, except at the first and last line
// of the buffer where it is zero. Runs of lines at level n or deeper, with at
// least one line at level n, become a fold at depth n-1.
//
// A fold whose start and end have not moved keeps its Closed flag, so a zo on
// a function survives typing inside it. Every other fold gets the state
// 'foldlevel' asks for.
//
// It does nothing for 'foldmethod' manual, which is the point of manual, and
// nothing for expr, marker, syntax and diff, which are not built.
func (f *Folds) Recompute(b *text.Buffer, shiftwidth, tabstop int) {
	if f == nil || f.Method != "indent" || b == nil {
		return
	}
	if shiftwidth < 1 {
		shiftwidth = 8
	}

	n := b.LineCount()
	levels := make([]int, n+1) // 1-based
	for i := 1; i <= n; i++ {
		levels[i] = indentLevel(b.Line(i), shiftwidth, tabstop)
	}
	// A blank line takes the level of its neighbours: vim marks it undefined
	// and the fold walker carries the previous level forward, which for a
	// blank run between two levels means the lower of the two so that the run
	// does not join two folds that should be separate.
	for i := 1; i <= n; i++ {
		if levels[i] != -1 {
			continue
		}
		before, after := 0, 0
		for j := i - 1; j >= 1; j-- {
			if levels[j] >= 0 {
				before = levels[j]
				break
			}
		}
		for j := i + 1; j <= n; j++ {
			if levels[j] >= 0 {
				after = levels[j]
				break
			}
		}
		levels[i] = min(before, after)
	}

	was := map[[2]int]bool{}
	var note func([]*Fold)
	note = func(list []*Fold) {
		for _, fold := range list {
			was[[2]int{fold.Start, fold.End}] = fold.Closed
			note(fold.Nested)
		}
	}
	note(f.top)

	f.top = buildIndentFolds(levels, 1, n, 1)
	var apply func([]*Fold, int)
	apply = func(list []*Fold, depth int) {
		for _, fold := range list {
			if closed, ok := was[[2]int{fold.Start, fold.End}]; ok {
				fold.Closed = closed
			} else {
				fold.Closed = depth >= f.Level
			}
			apply(fold.Nested, depth+1)
		}
	}
	apply(f.top, 0)
}

// buildIndentFolds turns a per-line level table into a tree of folds at the
// given level, over lines first..last inclusive.
func buildIndentFolds(levels []int, first, last, level int) []*Fold {
	if level > FoldNestMax {
		return nil
	}
	var out []*Fold
	for i := first; i <= last; i++ {
		if levels[i] < level {
			continue
		}
		j := i
		for j+1 <= last && levels[j+1] >= level {
			j++
		}
		fold := &Fold{Start: i, End: j}
		fold.Nested = buildIndentFolds(levels, i, j, level+1)
		out = append(out, fold)
		i = j
	}
	return out
}

// indentLevel is a line's 'foldmethod' indent level, or -1 for a line that has
// none of its own: one that is empty or all white space.
func indentLevel(line []byte, shiftwidth, tabstop int) int {
	if tabstop < 1 {
		tabstop = 8
	}
	cells := 0
	for _, b := range line {
		switch b {
		case ' ':
			cells++
		case '\t':
			cells += tabstop - cells%tabstop
		default:
			n := cells / shiftwidth
			if n > FoldNestMax {
				n = FoldNestMax
			}
			return n
		}
	}
	return -1
}

// FoldText is the line vim draws for a closed fold, before the 'fold' fill
// character pads it to the window width.
//
// This is vim's own foldtext() with 'foldtext' left at its default, which is
// what the vimrc leaves it at:
//
//	"+-" + one dash per level + "%3d" + " lines: " + the first line, deindented
//
// A tab inside that first line becomes a single space, because a fold line is
// one screen row and a tab that reached the renderer would move everything
// after it to a stop that means nothing here.
func FoldText(b *text.Buffer, f *Fold, level int) string {
	if level < 1 {
		level = 1
	}
	dashes := ""
	for i := 0; i < level; i++ {
		dashes += "-"
	}
	word := "lines"
	if f.Lines() == 1 {
		word = "line"
	}

	var first []byte
	if b != nil && f.Start >= 1 && f.Start <= b.LineCount() {
		first = b.Line(f.Start)
	}
	i := 0
	for i < len(first) && (first[i] == ' ' || first[i] == '\t') {
		i++
	}
	body := make([]byte, 0, len(first)-i)
	for _, c := range first[i:] {
		if c == '\t' {
			c = ' '
		}
		body = append(body, c)
	}

	return fmt.Sprintf("+-%s%3d %s: %s", dashes, f.Lines(), word, body)
}
