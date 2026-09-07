package screen

import "testing"

// want compares a dump against a literal and prints both blocks on a failure,
// because a one-character difference inside a screendump is unreadable as a
// %q pair.
func want(t *testing.T, got, expect string) {
	t.Helper()
	if got != expect {
		t.Errorf("screen mismatch\n--- got ---\n%s--- want ---\n%s", got, expect)
	}
}

// The dump of an untouched grid is blanks inside a border. Everything else in
// this package is asserted through this helper, so if it is wrong every other
// test is wrong in the same direction and none of them notices.
func TestDumpBlankGrid(t *testing.T) {
	g := NewGrid(2, 4)

	want(t, Dump(g), ""+
		"+----+\n"+
		"|    |\n"+
		"|    |\n"+
		"+----+\n")
}

// Text, and no highlight block when every cell is Normal. A block of dots under
// a screen with no colourscheme loaded is noise in every diff that follows.
func TestDumpTextOnly(t *testing.T) {
	g := NewGrid(2, 6)
	g.SetString(0, 0, "hello", Normal)
	g.SetString(1, 1, "~", Normal)

	want(t, Dump(g), ""+
		"+------+\n"+
		"|hello |\n"+
		"| ~    |\n"+
		"+------+\n")
}

// A highlight block appears as soon as one cell is not Normal, with letters
// assigned in the order the ids are first read off the grid.
func TestDumpHighlightBlock(t *testing.T) {
	g := NewGrid(2, 6)
	g.SetString(0, 0, "abc", Normal)
	g.SetString(0, 3, "de", 7)
	g.SetString(1, 0, "f", 3)

	want(t, Dump(g), ""+
		"+------+\n"+
		"|abcde |\n"+
		"|f     |\n"+
		"+------+\n"+
		"|...aa.|\n"+
		"|b.....|\n"+
		"+------+\n"+
		"legend: a=7 b=3\n")
}

// DumpScreen names the groups instead of numbering them, and says where the
// cursor is. That is the form a whole-frame assertion wants:
// `a=Search` survives a colourscheme gaining a group, `a=3` does not.
func TestDumpScreenNamesGroups(t *testing.T) {
	s := NewScreen(2, 5)
	search := s.HL.Set("Search", Highlight{FG: RGB{0, 0, 0}, BG: RGB{0, 0xcd, 0xcd}})
	s.Grid.SetString(0, 0, "foo", Normal)
	s.Grid.SetString(1, 0, "bar", search)
	s.CursorRow, s.CursorCol = 1, 2

	want(t, DumpScreen(s), ""+
		"+-----+\n"+
		"|foo  |\n"+
		"|bar  |\n"+
		"+-----+\n"+
		"|.....|\n"+
		"|aaa..|\n"+
		"+-----+\n"+
		"legend: a=Search\n"+
		"cursor: 1,2\n")
}

// A wide rune is written once in the dump and its trailing cell contributes
// nothing, so the block lines up column for column with what a terminal shows.
func TestDumpWideRuneLinesUp(t *testing.T) {
	g := NewGrid(1, 6)
	g.SetString(0, 0, "a漢b", Normal)

	want(t, Dump(g), ""+
		"+------+\n"+
		"|a漢b  |\n"+
		"+------+\n")
}

// A zero rune outside a wide pair is a cell somebody forgot to fill, and the
// dump has to make that visible rather than pass it off as a space.
func TestDumpShowsStrayNUL(t *testing.T) {
	g := NewGrid(1, 3)
	g.At(0, 1).Rune = 0

	want(t, Dump(g), ""+
		"+---+\n"+
		"| ␀ |\n"+
		"+---+\n")
}
