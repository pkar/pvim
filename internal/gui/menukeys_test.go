package gui

import (
	"testing"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/screen"
)

// TestMenuBindingsParse is the test that makes the panic in
// mustParseMenuBindings unreachable. The table is vim notation in a string
// literal, so a typo in it is a process that will not start, and this is the
// thing that catches the typo instead.
func TestMenuBindingsParse(t *testing.T) {
	for action, b := range menuBindings {
		for _, notation := range []string{b.normal, b.insert, b.cmdline} {
			if notation == "" {
				continue
			}
			if _, err := key.Parse(notation, ""); err != nil {
				t.Errorf("menu action %d binding %q: %v", action, notation, err)
			}
		}
	}
}

// TestMenuKeys pins what each Edit item types, as notation, because the table
// is the whole feature: Cmd-V that types "+gP is a paste and Cmd-V that types
// anything else is a bug report about a corrupted buffer.
func TestMenuKeys(t *testing.T) {
	for _, tc := range []struct {
		action editAction
		mode   inputMode
		want   string
	}{
		{editCopy, modeNormal, `"+y`},
		{editCopy, modeCmdline, `<C-Y>`},
		{editCopy, modeInsert, ``},
		{editCut, modeNormal, `"+x`},
		{editCut, modeInsert, ``},
		{editPaste, modeNormal, `"+gP`},
		{editPaste, modeInsert, `<C-R><C-O>+`},
		{editPaste, modeCmdline, `<C-R>+`},
		{editSelectAll, modeNormal, `ggVG`},
		{editSelectAll, modeInsert, `<Esc>ggVG`},
		{editSelectAll, modeCmdline, ``},
	} {
		got := key.Format(menuKeys(tc.action, tc.mode))
		if got != tc.want {
			t.Errorf("menuKeys(%d, %d) = %q, want %q", tc.action, tc.mode, got, tc.want)
		}
	}
}

// TestInputModeOf is the mode inference, which is the part of the Edit menu
// that could be wrong without anybody noticing until a Cmd-V in insert mode
// left "+gP in the buffer.
func TestInputModeOf(t *testing.T) {
	// 10 rows: one tabline is off with a single tab, so the text area is rows
	// 0..8, the status line is off with one window, and row 9 is the command
	// line.
	newScreen := func(row int, shape screen.CursorShape) *screen.Screen {
		s := screen.NewScreen(10, 20)
		s.CursorRow, s.CursorShape = row, shape
		return s
	}

	for _, tc := range []struct {
		name string
		s    *screen.Screen
		want inputMode
	}{
		{"nil is normal", nil, modeNormal},
		{"block in the text is normal", newScreen(3, screen.CursorBlock), modeNormal},
		{"bar in the text is insert", newScreen(3, screen.CursorBar), modeInsert},
		{"underline is replace, which types like insert", newScreen(3, screen.CursorUnderline), modeInsert},
		{"hollow is an unfocused window, not a mode", newScreen(3, screen.CursorHollow), modeNormal},
		{"a caret on the command line is cmdline whatever its shape", newScreen(9, screen.CursorBlock), modeCmdline},
		{"a bar on the command line is still cmdline", newScreen(9, screen.CursorBar), modeCmdline},
	} {
		if got := inputModeOf(tc.s); got != tc.want {
			t.Errorf("%s: inputModeOf = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestCmdlineRowIsWhereTheLayoutSaysItIs guards the assumption the test above
// is built on. If internal/screen ever moves the command line, the inference
// silently stops working and every Cmd-V on the command line pastes into the
// buffer instead; this fails first.
func TestCmdlineRowIsWhereTheLayoutSaysItIs(t *testing.T) {
	s := screen.NewScreen(10, 20)
	cmd := s.Regions().Cmdline
	if cmd.Row != 9 || cmd.Rows != 1 {
		t.Fatalf("a 10-row screen puts the command line at row %d, %d rows tall; the mode test assumes row 9", cmd.Row, cmd.Rows)
	}
}
