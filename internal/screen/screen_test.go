package screen

import "testing"

// The default layout is vim's, and the arithmetic is vim's: text rows are
// lines minus the tabline minus the status line minus 'cmdheight'.
//
// The numbers come from vim 9.2.0321, not from reading:help. Under --clean at
// 24 lines, &showtabline and &laststatus and &cmdheight are all 1 and
// winheight(0) is 23; after:set laststatus=2 it is 22; after:set cmdheight=3
// and:tabnew, with the tabline now showing, it is 19.
func TestLayoutMatchesVimArithmetic(t *testing.T) {
	cases := []struct {
		name   string
		layout Layout
		want   Regions
	}{
		{
			name:   "clean defaults, one tab, one window",
			layout: DefaultLayout(24, 80),
			want: Regions{
				Text:    Region{Row: 0, Rows: 23, Cols: 80},
				Cmdline: Region{Row: 23, Rows: 1, Cols: 80},
			},
		},
		{
			name: "laststatus=2 takes a row from the text",
			layout: func() Layout {
				l := DefaultLayout(24, 80)
				l.LastStatus = 2
				return l
			}(),
			want: Regions{
				Text:    Region{Row: 0, Rows: 22, Cols: 80},
				Status:  Region{Row: 22, Rows: 1, Cols: 80},
				Cmdline: Region{Row: 23, Rows: 1, Cols: 80},
			},
		},
		{
			name: "two tabs, two windows, cmdheight=3",
			layout: Layout{
				Rows: 24, Cols: 80,
				ShowTabline: 1, Tabs: 2,
				LastStatus: 1, Windows: 2,
				CmdHeight: 3,
			},
			want: Regions{
				Tabline: Region{Row: 0, Rows: 1, Cols: 80},
				Text:    Region{Row: 1, Rows: 19, Cols: 80},
				Status:  Region{Row: 20, Rows: 1, Cols: 80},
				Cmdline: Region{Row: 21, Rows: 3, Cols: 80},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.layout.Regions(); got != c.want {
				t.Errorf("Regions() = %+v, want %+v", got, c.want)
			}
		})
	}
}

// showtabline=1 shows the tabline only with more than one tab open, and
// showtabline=2 always; laststatus does the same over the window count. Getting
// either backwards moves every row on screen by one.
func TestLayoutOptionTriStates(t *testing.T) {
	cases := []struct {
		name       string
		layout     Layout
		tabRows    int
		statusRows int
	}{
		{"showtabline=0 with two tabs", Layout{Rows: 10, Cols: 10, ShowTabline: 0, Tabs: 2, LastStatus: 0, Windows: 2, CmdHeight: 1}, 0, 0},
		{"showtabline=1 with one tab", Layout{Rows: 10, Cols: 10, ShowTabline: 1, Tabs: 1, LastStatus: 1, Windows: 1, CmdHeight: 1}, 0, 0},
		{"showtabline=1 with two tabs", Layout{Rows: 10, Cols: 10, ShowTabline: 1, Tabs: 2, LastStatus: 1, Windows: 2, CmdHeight: 1}, 1, 1},
		{"showtabline=2 with one tab", Layout{Rows: 10, Cols: 10, ShowTabline: 2, Tabs: 1, LastStatus: 2, Windows: 1, CmdHeight: 1}, 1, 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs := c.layout.Regions()
			if rs.Tabline.Rows != c.tabRows {
				t.Errorf("tabline rows = %d, want %d", rs.Tabline.Rows, c.tabRows)
			}
			if rs.Status.Rows != c.statusRows {
				t.Errorf("status rows = %d, want %d", rs.Status.Rows, c.statusRows)
			}
		})
	}
}

// A screen too small for everything still gets one text row and a command line,
// because a frontend has to draw something and vim's answer, refusing the
// resize, is not open to a window manager that has already made the window that
// size. The shrink order is a choice: cmdheight first, then the tabline, then
// the status line.
func TestLayoutShrinksInAFixedOrder(t *testing.T) {
	l := Layout{Rows: 4, Cols: 10, ShowTabline: 2, Tabs: 3, LastStatus: 2, Windows: 2, CmdHeight: 5}

	rs := l.Regions()
	want := Regions{
		Tabline: Region{Row: 0, Rows: 1, Cols: 10},
		Text:    Region{Row: 1, Rows: 1, Cols: 10},
		Status:  Region{Row: 2, Rows: 1, Cols: 10},
		Cmdline: Region{Row: 3, Rows: 1, Cols: 10},
	}
	if rs != want {
		t.Fatalf("Regions() = %+v, want %+v", rs, want)
	}

	l.Rows = 3
	if rs := l.Regions(); !rs.Tabline.Empty() || rs.Text.Rows != 1 || rs.Status.Rows != 1 {
		t.Errorf("at 3 rows: %+v, want the tabline dropped and one text row", rs)
	}

	l.Rows = 2
	if rs := l.Regions(); !rs.Tabline.Empty() || !rs.Status.Empty() || rs.Text.Rows != 1 || rs.Cmdline.Rows != 1 {
		t.Errorf("at 2 rows: %+v, want text and cmdline only", rs)
	}
}

// The bands are contiguous and cover the screen exactly. A gap is a row nobody
// ever paints and an overlap is a row two things fight over.
func TestRegionsTileTheScreen(t *testing.T) {
	for rows := 1; rows <= 40; rows++ {
		for _, cmd := range []int{1, 2, 5} {
			l := Layout{Rows: rows, Cols: 80, ShowTabline: 2, Tabs: 2, LastStatus: 2, Windows: 2, CmdHeight: cmd}
			rs := l.Regions()
			next := 0
			for _, r := range []Region{rs.Tabline, rs.Text, rs.Status, rs.Cmdline} {
				if r.Rows == 0 {
					continue
				}
				if r.Row != next {
					t.Fatalf("rows=%d cmdheight=%d: region at row %d, want %d (%+v)", rows, cmd, r.Row, next, rs)
				}
				next += r.Rows
			}
			if next != rows {
				t.Fatalf("rows=%d cmdheight=%d: bands cover %d rows (%+v)", rows, cmd, next, rs)
			}
		}
	}
}

// A screen has to be usable before any colourscheme has been parsed, which is
// the state startup runs in for its first few milliseconds.
func TestNewScreenHasNormal(t *testing.T) {
	s := NewScreen(2, 3)

	if s.HL.Len() != 1 {
		t.Errorf("table has %d entries, want just Normal", s.HL.Len())
	}
	if got := s.Look(Normal); got != DefaultNormal {
		t.Errorf("Look(Normal) = %+v, want %+v", got, DefaultNormal)
	}
	if s.Grid.Rows != 2 || s.Grid.Cols != 3 {
		t.Errorf("grid = %dx%d, want 2x3", s.Grid.Rows, s.Grid.Cols)
	}
}

// Resizing the screen resizes the grid and the layout together. Two sources of
// truth for the screen size is a class of bug on its own.
func TestScreenResizeMovesTheLayoutToo(t *testing.T) {
	s := NewScreen(24, 80)
	s.Grid.SetString(0, 0, "gone", Normal)

	s.Resize(10, 20)

	if s.Grid.Rows != 10 || s.Grid.Cols != 20 {
		t.Errorf("grid = %dx%d, want 10x20", s.Grid.Rows, s.Grid.Cols)
	}
	if got := s.Regions().Cmdline.Row; got != 9 {
		t.Errorf("cmdline row = %d, want 9", got)
	}
	if s.Grid.At(0, 0).Rune != ' ' {
		t.Error("the old frame survived the resize")
	}
}
