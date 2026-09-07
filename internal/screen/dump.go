package screen

import (
	"fmt"
	"sort"
	"strings"
)

// legendRunes are the marks a dump gives to non-Normal highlight ids, in the
// order the ids are first seen scanning the grid. Normal is always '.', so a
// dump of a screen with no colourscheme loaded has no highlight block at all.
const legendRunes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// Dump renders a grid the way vim's screendump tests render one: a bordered
// block of text with one line per row, ready to be compared against a literal
// string in a test.
//
// The border is there so that trailing blanks are visible and so that a wrong
// width fails on the same line it happens. A double-width rune is written once
// and its trailing cell contributes nothing, which makes the dump line up
// column for column in any editor showing it, including this one.
//
// When any cell carries a highlight other than Normal, a second block follows
// with one mark per column and a legend naming the ids. Highlights are left out
// entirely when there are none, because most tests are about what characters
// landed where and a block of dots under every line is noise.
func Dump(g *Grid) string {
	return dump(g, nil)
}

// DumpScreen is Dump with the highlight legend naming groups rather than
// numbers, so a test reads `a=Search` instead of `a=3`, and with the cursor
// position appended. It is what a test asserting a whole frame wants.
func DumpScreen(s *Screen) string {
	out := dump(&s.Grid, s.HL)
	return out + fmt.Sprintf("cursor: %d,%d\n", s.CursorRow, s.CursorCol)
}

// dump does the work for both. names is nil when ids should be printed as
// numbers.
func dump(g *Grid, names *Table) string {
	marks := assignMarks(g)

	var b strings.Builder
	rule := "+" + strings.Repeat("-", g.Cols) + "+\n"
	b.WriteString(rule)
	for row := 0; row < g.Rows; row++ {
		b.WriteByte('|')
		for col := 0; col < g.Cols; col++ {
			c := *g.At(row, col)
			if c.Tail {
				continue // already drawn as the second half of its pair
			}
			b.WriteRune(printable(c.Rune))
		}
		b.WriteString("|\n")
	}
	b.WriteString(rule)

	if len(marks) == 0 {
		return b.String()
	}

	for row := 0; row < g.Rows; row++ {
		b.WriteByte('|')
		for col := 0; col < g.Cols; col++ {
			c := *g.At(row, col)
			if c.Tail {
				continue
			}
			b.WriteByte(marks[c.HL])
		}
		b.WriteString("|\n")
	}
	b.WriteString(rule)
	b.WriteString(legend(marks, names))
	return b.String()
}

// assignMarks gives every non-Normal id in the grid a legend letter, in the
// order the ids appear reading the grid top to bottom, left to right, so that
// adding a highlight to the bottom of a screen does not renumber the top.
func assignMarks(g *Grid) map[HLID]byte {
	var order []HLID
	seen := map[HLID]bool{Normal: true}
	for _, c := range g.Cells {
		if !seen[c.HL] {
			seen[c.HL] = true
			order = append(order, c.HL)
		}
	}
	if len(order) == 0 {
		return nil
	}
	marks := map[HLID]byte{Normal: '.'}
	for i, id := range order {
		if i < len(legendRunes) {
			marks[id] = legendRunes[i]
		} else {
			marks[id] = '?' // more than 52 highlights on one screen; say so rather than lie
		}
	}
	return marks
}

// legend prints the mark-to-id mapping on one line, sorted by mark so the
// output is stable.
func legend(marks map[HLID]byte, names *Table) string {
	var parts []string
	for id, m := range marks {
		if id == Normal {
			continue
		}
		parts = append(parts, fmt.Sprintf("%c=%s", m, name(id, names)))
	}
	sort.Strings(parts)
	return "legend: " + strings.Join(parts, " ") + "\n"
}

// name is the id's group name when a table was supplied and it has one, and the
// number otherwise.
func name(id HLID, t *Table) string {
	if t != nil {
		if ns := t.Names(); int(id) < len(ns) && ns[id] != "" {
			return ns[id]
		}
	}
	return fmt.Sprint(int(id))
}

// printable is what a dump shows for a rune. A zero rune outside a wide pair is
// a cell somebody forgot to fill and shows as a NUL sign rather than as a space,
// because the whole point of the helper is that a bug is visible in the diff.
func printable(r rune) rune {
	if r == 0 {
		return '␀' // SYMBOL FOR NULL
	}
	return r
}
