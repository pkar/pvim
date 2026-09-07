package window

import (
	"bufio"
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
)

// wide returns the corpus the sidescroll table was measured over: forty lines
// of three hundred single-width characters, so that a display column and a byte
// column are the same number and the table reads as arithmetic.
func wide() *text.Buffer {
	var b bytes.Buffer
	for i := 0; i < 40; i++ {
		for j := 0; j < 300; j++ {
			b.WriteByte(byte('a' + (i+j)%26))
		}
		b.WriteByte('\n')
	}
	return text.Read(b.Bytes())
}

// sideCase is one row of testdata/window/sidescroll-vim9.2.0321.txt.
type sideCase struct {
	line                 int
	keys                 string
	width, siso, ss      int
	leftCol, col         int
	wantLeftCol, wantCol int
}

func readSideTable(t *testing.T) []sideCase {
	t.Helper()
	const path = "../../testdata/window/sidescroll-vim9.2.0321.txt"
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()

	var out []sideCase
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 9 || fields[6] != "->" {
			t.Fatalf("%s:%d: want 9 fields with -> in the middle, got %q", path, n, line)
		}
		num := func(i int) int {
			v, err := strconv.Atoi(fields[i])
			if err != nil {
				t.Fatalf("%s:%d: field %d: %v", path, n, i, err)
			}
			return v
		}
		out = append(out, sideCase{
			line: n, keys: fields[0],
			width: num(1), siso: num(2), ss: num(3),
			leftCol: num(4), col: num(5),
			wantLeftCol: num(7), wantCol: num(8),
		})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if len(out) == 0 {
		t.Fatalf("%s held no cases", path)
	}
	return out
}

// TestSideScrollMatchesVim replays the measured horizontal table.
//
// The vimrc sets 'nowrap' and 'sidescrolloff' 5, so this is not an edge case:
// it is what every long line in every Go file does on this machine.
func TestSideScrollMatchesVim(t *testing.T) {
	for _, tc := range readSideTable(t) {
		name := tc.keys + " w" + strconv.Itoa(tc.width) +
			" siso" + strconv.Itoa(tc.siso) + " ss" + strconv.Itoa(tc.ss) +
			" from " + strconv.Itoa(tc.leftCol) + "/" + strconv.Itoa(tc.col)
		t.Run(name, func(t *testing.T) {
			w := New(1, wide(), options.Defaults().GW)
			w.Opt.Wrap = false
			w.SetHeight(23)
			w.View.Width = tc.width
			w.View.LeftCol = tc.leftCol
			w.View.Cursor = text.Pos{Line: 5, Col: tc.col}
			s := Side{ScrollOff: tc.siso, Scroll: tc.ss, TabStop: 8}

			if strings.HasSuffix(tc.keys, "|") || tc.keys == "$" {
				// A motion: the cursor has already moved and the window has to
				// follow, which is curs_columns.
				w.View.Cursor.Col = tc.wantCol
				w.SideScrollToCursor(s)
			} else {
				count, cmd := parseSideKeys(t, tc.keys)
				w.SideScroll(cmd, count, s)
			}

			if w.View.LeftCol != tc.wantLeftCol {
				t.Errorf("leftcol %d, vim says %d (testdata line %d)", w.View.LeftCol, tc.wantLeftCol, tc.line)
			}
			if got := w.CursorDispCol(8); got != tc.wantCol {
				t.Errorf("cursor column %d, vim says %d (testdata line %d)", got, tc.wantCol, tc.line)
			}
		})
	}
}

// parseSideKeys reads an optional count and one of the six horizontal commands.
func parseSideKeys(t *testing.T, keys string) (int, SideCmd) {
	t.Helper()
	count := 0
	for len(keys) > 0 && keys[0] >= '0' && keys[0] <= '9' {
		count = count*10 + int(keys[0]-'0')
		keys = keys[1:]
	}
	switch keys {
	case "zl":
		return count, SideRight
	case "zh":
		return count, SideLeft
	case "zL":
		return count, SideHalfRight
	case "zH":
		return count, SideHalfLeft
	case "zs":
		return count, SideStart
	case "ze":
		return count, SideEnd
	}
	t.Fatalf("unknown horizontal command %q", keys)
	return 0, SideRight
}

// TestWrapHasNoHorizontalScroll is the one rule that is not in the table,
// because a wrapped window has no 'leftcol' for winsaveview() to report.
func TestWrapHasNoHorizontalScroll(t *testing.T) {
	w := New(1, wide(), options.Defaults().GW)
	w.Opt.Wrap = true
	w.SetHeight(23)
	w.View.Width = 80
	w.View.LeftCol = 40
	w.View.Cursor = text.Pos{Line: 5, Col: 200}

	s := Side{ScrollOff: 5, Scroll: 0, TabStop: 8}
	w.SideScrollToCursor(s)
	if w.View.LeftCol != 0 {
		t.Errorf("leftcol %d under 'wrap', want 0: a wrapped line has no horizontal scroll to do", w.View.LeftCol)
	}
	if w.SideScroll(SideRight, 10, s) {
		t.Error("zl reported a move under 'wrap'")
	}
}

// TestTabsCountAsDisplayColumns keeps the arithmetic honest for the one case
// the measured corpus deliberately does not have: a line where a byte column
// and a display column are different numbers.
func TestTabsCountAsDisplayColumns(t *testing.T) {
	b := text.Read([]byte(strings.Repeat("\t", 40) + "end\n"))
	w := New(1, b, options.Defaults().GW)
	w.Opt.Wrap = false
	w.SetHeight(23)
	w.View.Width = 80
	w.View.Cursor = text.Pos{Line: 1, Col: 20} // the 21st tab, display column 160

	if got := w.CursorDispCol(8); got != 160 {
		t.Fatalf("display column %d for byte column 20 of a line of tabs, want 160", got)
	}
	w.SideScrollToCursor(Side{ScrollOff: 5, Scroll: 0, TabStop: 8})
	if want := 160 - 40; w.View.LeftCol != want {
		t.Errorf("leftcol %d, want %d: 'sidescroll' 0 centres the cursor", w.View.LeftCol, want)
	}
}

// TestSideScrollStopsAtTheLastColumn is zl and zL scrolled past the end of the
// line.
//
// The margin column does not exist, so vim leaves the cursor on the last
// character and moves 'leftcol' instead. Letting the cursor be clamped to
// len(line) puts it one byte past the last one, which is the insert-mode
// position: this asserts the byte column as well as the display column,
// because that is the half of the bug that does not stay latent.
//
// Measured in an 80- and a 40-column window over one 300-character line, all
// display columns 0-based.
func TestSideScrollStopsAtTheLastColumn(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		width, siso, ss      int
		leftCol, col         int
		count                int
		cmd                  SideCmd
		wantLeftCol, wantCol int
	}{
		{"2zL", 80, 0, 0, 220, 299, 2, SideHalfRight, 299, 299},
		{"100zl", 80, 0, 0, 200, 249, 100, SideRight, 299, 299},
		{"zl already on the last column", 80, 0, 0, 299, 299, 0, SideRight, 299, 299},
		{"zL at 'sidescroll' 1", 80, 5, 1, 259, 299, 0, SideHalfRight, 294, 299},
		{"100zl at 'sidescroll' 1", 80, 5, 1, 259, 264, 100, SideRight, 294, 299},
		{"zL at 'sidescroll' 0 centres", 80, 5, 0, 290, 299, 0, SideHalfRight, 259, 299},
		{"zL in a 40-column window", 40, 5, 0, 290, 295, 0, SideHalfRight, 279, 299},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := New(1, wide(), options.Defaults().GW)
			w.Opt.Wrap = false
			w.SetHeight(23)
			w.View.Width = tc.width
			w.View.LeftCol = tc.leftCol
			w.View.Cursor = text.Pos{Line: 5, Col: tc.col}
			w.SideScroll(tc.cmd, tc.count, Side{ScrollOff: tc.siso, Scroll: tc.ss, TabStop: 8})

			if w.View.LeftCol != tc.wantLeftCol {
				t.Errorf("leftcol %d, vim says %d", w.View.LeftCol, tc.wantLeftCol)
			}
			if got := w.CursorDispCol(8); got != tc.wantCol {
				t.Errorf("cursor display column %d, vim says %d", got, tc.wantCol)
			}
			if n := len(w.Buf.Line(5)); w.View.Cursor.Col >= n {
				t.Errorf("cursor byte column %d on a %d-byte line: that is the insert-mode position, not a normal-mode one",
					w.View.Cursor.Col, n)
			}
		})
	}
}
