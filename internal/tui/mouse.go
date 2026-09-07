package tui

import "github.com/pkar/pvim/internal/key"

// mouseEvent turns one of internal/key's mouse pseudo-keys and the
// coordinates that came with it into the frontend's event.
//
// The coordinate change is the only arithmetic: SGR reports are 1-based and
// the grid is 0-based. A report at 1;1 is the top-left cell.
//
// The wheel is deliberately not multiplied here. Vim's model, from scroll.txt,
// is that the wheel arrives as a key and the default action belongs to the
// editor:
//
//	<ScrollWheelUp> scroll N lines up
//	<S-ScrollWheelUp> scroll one page up
//	<C-ScrollWheelUp> scroll one page up
//	<ScrollWheelLeft> scroll N columns left
//
//	The value of N depends on the system. By default Vim scrolls three lines
//	when moving vertically, and six columns when moving horizontally.
//
// So one notch is one event with its modifiers intact, and three lines, six
// columns and "one page" are decided one layer up where 'wrap' and the window
// height are known. Delivering three events per notch would put the number in
// the frontend and make <S-ScrollWheelUp> undeliverable, and horizontal
// scrolling under 'nowrap' would have nowhere to come from: a horizontal wheel
// is its own report, xterm button 6 and 7, which internal/key already decodes
// as KeyScrollWheelLeft and KeyScrollWheelRight.
//
// The second return is false for a report this vocabulary has no event for:
// the two extra buttons, and the bare motion report that a terminal in mode
// 1003 sends and the one in 1002 does not.
func mouseEvent(k key.Key, m key.MouseEvent) (MouseEvent, bool) {
	ev := MouseEvent{
		Row: m.Row - 1,
		Col: m.Col - 1,
		Mod: k.Mod,
	}
	if ev.Row < 0 {
		ev.Row = 0
	}
	if ev.Col < 0 {
		ev.Col = 0
	}

	switch k.Special {
	case key.KeyLeftMouse:
		ev.Button, ev.Action = MouseLeft, MousePress
	case key.KeyLeftDrag:
		ev.Button, ev.Action = MouseLeft, MouseDrag
	case key.KeyLeftRelease:
		ev.Button, ev.Action = MouseLeft, MouseRelease
	case key.KeyMiddleMouse:
		ev.Button, ev.Action = MouseMiddle, MousePress
	case key.KeyMiddleDrag:
		ev.Button, ev.Action = MouseMiddle, MouseDrag
	case key.KeyMiddleRelease:
		ev.Button, ev.Action = MouseMiddle, MouseRelease
	case key.KeyRightMouse:
		ev.Button, ev.Action = MouseRight, MousePress
	case key.KeyRightDrag:
		ev.Button, ev.Action = MouseRight, MouseDrag
	case key.KeyRightRelease:
		ev.Button, ev.Action = MouseRight, MouseRelease
	case key.KeyScrollWheelUp:
		ev.Button, ev.Action = MouseNone, MouseWheelUp
	case key.KeyScrollWheelDown:
		ev.Button, ev.Action = MouseNone, MouseWheelDown
	case key.KeyScrollWheelLeft:
		ev.Button, ev.Action = MouseNone, MouseWheelLeft
	case key.KeyScrollWheelRight:
		ev.Button, ev.Action = MouseNone, MouseWheelRight
	default:
		return MouseEvent{}, false
	}
	return ev, true
}

// mouseEnabled reports whether 'mouse' asks for reporting in any mode.
//
// The option is a set of mode letters -- n, v, i, c, h, a, r -- and this
// frontend turns reporting on for all of them or none, because a terminal
// cannot be told to report the mouse only in normal mode. Which modes actually
// act on a click is the editor's decision, made from the same option, and it
// is the reason this returns a bool rather than the string: nothing below this
// line needs to know what the letters were. The vimrc sets mouse=a inside
// if has('mouse'), so this is on.
func mouseEnabled(mouse string) bool { return mouse != "" }
