package gui

import (
	"fmt"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/screen"
)

// The Edit menu, translated back into keystrokes.
//
// macOS will not deliver Cmd-C, Cmd-V, Cmd-X or Cmd-A to a window at all
// unless a menu item claims them: with no item the OS looks for a key
// equivalent, finds none, and beeps. So the menu exists, its four items target
// the first responder, and when one fires this package turns it back into the
// keys vim binds that menu item to. The window never touches a register and
// never learns what a clipboard is; it types.
//
// The bindings are vim's own, out of runtime/menu.vim, unchanged:
//
//	vnoremenu Edit.Cut "+x
//	vnoremenu Edit.Copy "+y
//	cnoremenu Edit.Copy <C-Y>
//	nnoremenu Edit.Paste "+gP
//	cnoremenu Edit.Paste <C-R>+
//	noremenu Edit.Select All ggVG
//
// Two of them are spelled differently here and both are noted below.
//
// # What a frontend can and cannot know about the mode
//
// vim picks the mapping for the mode it is in, and this package cannot: the
// editor is on the other side of a channel and a Screen carries pixels'
// worth of state and not a mode. What it does carry is where the caret was
// drawn and what shape it was drawn in, which separates insert and replace
// from everything else and separates the command line from the text, and that
// is what inputModeOf reads.
//
// It does NOT separate normal from visual, and nothing on a Screen does. So
// Cmd-C in normal mode leaves "+y as an operator waiting for a motion, exactly
// as typing "+y would, and Escape cancels it; Cmd-X in normal mode deletes the
// character under the caret into the + register, exactly as "+x would, and u
// undoes it. Both are what the keys mean and neither is what MacVim does,
// which is to make the menu item inert when there is no selection. Copying
// MacVim would need the mode over the seam, and a mode on the seam is an
// editor fact leaking into a window for the sake of two keys nobody presses in
// normal mode on purpose.
type editAction uint8

// The four Edit menu items.
const (
	editCut editAction = iota
	editCopy
	editPaste
	editSelectAll
)

// inputMode is as much of the editor's mode as the last painted frame reveals.
//
// Three values and not vim's seven: a frontend can see a bar or an underline
// caret, which is insert or replace, and a caret drawn on the command line,
// which is cmdline; everything else is one bucket.
type inputMode uint8

// The modes a frontend can tell apart.
const (
	modeNormal inputMode = iota
	modeInsert
	modeCmdline
)

// menuBinding is what one menu item types in each of the three modes. An empty
// string is an item that does nothing in that mode, which is what a mode with
// no mapping in menu.vim does.
type menuBinding struct {
	normal  string
	insert  string
	cmdline string
}

// menuBindings is the table above in vim notation.
//
// Paste in insert mode is CTRL-R CTRL-O and not menu.vim's paste#paste_cmd,
// which is a vimscript function call and there is no evaluator to call it
// with. CTRL-R CTRL-O inserts the register literally: with the vimrc's
// 'autoindent' on, a plain CTRL-R would re-indent every line of a multi-line
// paste against the one above it, which is the classic staircase and the thing
// paste#Paste is there to avoid.
//
// Cut and Copy are empty in insert mode because menu.vim binds them for Visual
// and command-line mode only, and Select All is empty on the command line for
// the same reason.
var menuBindings = map[editAction]menuBinding{
	editCut:       {normal: `"+x`},
	editCopy:      {normal: `"+y`, cmdline: `<C-y>`},
	editPaste:     {normal: `"+gP`, insert: `<C-r><C-o>+`, cmdline: `<C-r>+`},
	editSelectAll: {normal: `ggVG`, insert: `<Esc>ggVG`},
}

// menuKeyTable is menuBindings parsed once, because parsing vim notation on
// every Cmd-V is work with a known answer.
//
// It is built at init and panics on a table that does not parse. That is a
// constant in this file and TestMenuBindingsParse is what makes the panic
// unreachable, so the choice is between a panic no build can reach and an
// error return every caller would have to ignore.
var menuKeyTable = mustParseMenuBindings()

// mustParseMenuBindings turns the notation table into keys.
func mustParseMenuBindings() map[editAction][3][]key.Key {
	out := make(map[editAction][3][]key.Key, len(menuBindings))
	for action, b := range menuBindings {
		var keys [3][]key.Key
		for i, notation := range [3]string{b.normal, b.insert, b.cmdline} {
			if notation == "" {
				continue
			}
			ks, err := key.Parse(notation, "")
			if err != nil {
				panic(fmt.Sprintf("gui: the Edit menu binding %q does not parse: %v", notation, err))
			}
			keys[i] = ks
		}
		out[action] = keys
	}
	return out
}

// menuKeys returns what the menu item types in the given mode, or nil where
// vim binds nothing.
func menuKeys(a editAction, m inputMode) []key.Key {
	keys, ok := menuKeyTable[a]
	if !ok || int(m) >= len(keys) {
		return nil
	}
	return keys[m]
}

// inputModeOf works out as much of the mode as the last painted frame shows.
//
// The caret shape is vim's own mode signal -- block in normal, bar in insert,
// underline in replace -- and the command line is the one region whose row is
// known from the layout, so a caret drawn there is a caret in cmdline mode
// whatever shape it has. A hollow caret is an unfocused window, which is not a
// mode at all, and it is treated as normal: a menu item cannot fire in a window
// that is not focused.
func inputModeOf(s *screen.Screen) inputMode {
	if s == nil {
		return modeNormal
	}
	if cmd := s.Regions().Cmdline; !cmd.Empty() &&
		s.CursorRow >= cmd.Row && s.CursorRow < cmd.Row+cmd.Rows {
		return modeCmdline
	}
	switch s.CursorShape {
	case screen.CursorBar, screen.CursorUnderline:
		return modeInsert
	default:
		return modeNormal
	}
}
