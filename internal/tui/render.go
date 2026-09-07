package tui

import (
	"strconv"

	"github.com/pkar/pvim/internal/screen"
)

// frame turns a screen into the bytes that paint it, given the frame that is
// already on the terminal.
//
// It is a free function over a byte slice rather than a method on Terminal so
// that the whole renderer can be tested with no terminal, no driver and no
// goroutine: hand it two grids and compare the answer against a literal
// string. That is the only way to assert an escape sequence and mean it.
//
// prev is the last frame painted, or nil for the first one and after a resize
// or a CTRL-L. screen.Grid.Diff already answers "everything" for a nil or
// mismatched prev, so there is no special case here.
func frame(dst []byte, s *screen.Screen, prev *screen.Grid) []byte {
	if s == nil {
		return dst
	}
	g := &s.Grid
	spans := g.Diff(prev)

	if len(spans) > 0 {
		dst = append(dst, seqCursorHide...)
	}

	// hl is the highlight the terminal is currently in. -1 means unknown,
	// which forces the first cell of the frame to write its own, because
	// nothing here knows what the last frame left behind.
	hl := screen.HLID(-1)
	for _, sp := range spans {
		dst = appendCUP(dst, sp.Row, sp.Col)
		for col := sp.Col; col < sp.Col+sp.Len && col < g.Cols; col++ {
			c := *g.At(sp.Row, col)
			if c.Tail {
				// The second half of a double-width pair. The rune was
				// written with the first half and the terminal has already
				// moved the cursor over both cells; writing anything here
				// would overwrite the glyph it belongs to.
				continue
			}
			if c.HL != hl {
				dst = appendSGR(dst, s.Look(c.HL))
				hl = c.HL
			}
			dst = appendRune(dst, c.Rune)
		}
	}

	// The cursor goes last, and always: a frame where nothing changed but the
	// cursor moved is the common case for a motion, and it costs one CUP.
	row, col := clampCursor(s)
	dst = appendCUP(dst, row, col)
	dst = append(dst, cursorShape(s.CursorShape)...)
	if len(spans) > 0 {
		dst = append(dst, seqCursorShow...)
	}
	return dst
}

// appendCUP writes the absolute cursor position. Terminal rows and columns are
// 1-based and the grid's are 0-based, which is the off-by-one this function
// exists to hold in one place.
func appendCUP(dst []byte, row, col int) []byte {
	dst = append(dst, '\x1b', '[')
	dst = strconv.AppendInt(dst, int64(row+1), 10)
	dst = append(dst, ';')
	dst = strconv.AppendInt(dst, int64(col+1), 10)
	return append(dst, 'H')
}

// appendSGR writes a complete appearance: a reset first, then every attribute
// and both colours.
//
// Absolute rather than differential on purpose. A differential SGR saves a
// handful of bytes per highlight change and costs a model of what the terminal
// currently believes, which has to survive a resize, a CTRL-L, a terminal that
// dropped bytes and another program writing to the same tty. 'ttyfast' is set
// in the vimrc and this is exactly the trade it describes.
//
// 24-bit colour unconditionally, as: the highlight table is
// built from a colourscheme's guifg and guibg, there is no palette anywhere in
// this editor, and every terminal it will run in has done truecolor for a
// decade.
func appendSGR(dst []byte, h screen.Highlight) []byte {
	dst = append(dst, '\x1b', '[', '0')
	if h.Attr&screen.AttrBold != 0 {
		dst = append(dst, ";1"...)
	}
	if h.Attr&screen.AttrItalic != 0 {
		dst = append(dst, ";3"...)
	}
	if h.Attr&screen.AttrUnderline != 0 {
		dst = append(dst, ";4"...)
	}
	if h.Attr&(screen.AttrReverse|screen.AttrStandout) != 0 {
		// Standout is reverse video in a terminal: xterm's smso is \x1b[7m,
		// the same sequence rev sends, and vim writes t_so where it wants
		// either.
		dst = append(dst, ";7"...)
	}
	if h.Attr&screen.AttrStrikethrough != 0 {
		dst = append(dst, ";9"...)
	}
	dst = append(dst, ";38;2;"...)
	dst = appendRGB(dst, h.FG)
	dst = append(dst, ";48;2;"...)
	dst = appendRGB(dst, h.BG)
	dst = append(dst, 'm')

	if h.Attr&screen.AttrUndercurl != 0 {
		// Undercurl is SGR 4:3, a sub-parameter, and its colour is SGR 58.
		// Both are extensions: kitty invented them and vte, wezterm, iTerm2
		// and Ghostty took them. They go in their own sequences rather than
		// as parameters of the one above so that a terminal which cannot
		// parse a sub-parameter drops these two and still gets the colours
		// right, instead of swallowing the whole appearance.
		dst = append(dst, "\x1b[4:3m\x1b[58;2;"...)
		dst = appendRGB(dst, h.SP)
		dst = append(dst, 'm')
	}
	return dst
}

// appendRGB writes "r;g;b".
func appendRGB(dst []byte, c screen.RGB) []byte {
	dst = strconv.AppendUint(dst, uint64(c.R), 10)
	dst = append(dst, ';')
	dst = strconv.AppendUint(dst, uint64(c.G), 10)
	dst = append(dst, ';')
	return strconv.AppendUint(dst, uint64(c.B), 10)
}

// appendRune writes one cell's character.
//
// A zero rune is a space: screen.Grid fills with a space and never with a NUL,
// but a Cell somebody built by hand can hold one, and a NUL down a tty is
// either nothing at all or a padding byte depending on the terminal. A control
// character is a space for the same reason, one order of magnitude worse: a
// stray \x1b in a cell would be the start of a sequence and the rest of the
// frame would be interpreted as its parameters.
func appendRune(dst []byte, r rune) []byte {
	if r < 0x20 || r == 0x7f {
		return append(dst, ' ')
	}
	return append(dst, string(r)...)
}

// cursorShape is DECSCUSR: "\x1b[<n> q".
//
// Block in normal, bar in insert, underline in replace, which is what this
// editor asks for and what every terminal vim configuration on earth sets t_SI
// and t_EI to. It is not what vim does out of the box: a capture of vim
// 9.2.0321 under TERM=xterm-256color shows it entering insert mode without
// emitting a DECSCUSR at all, because its builtin termcap leaves t_SI empty.
// This editor sends it always. The shapes are the steady ones -- 2, 4 and 6,
// not 1, 3 and 5 -- because the blink is what D-004 refuses in the window and
// there is no reason for the terminal to disagree with the window about it.
//
// A hollow cursor has no DECSCUSR. It means an unfocused window, which a
// terminal frontend cannot be told about, so it draws as a block.
func cursorShape(s screen.CursorShape) string {
	switch s {
	case screen.CursorBar:
		return "\x1b[6 q"
	case screen.CursorUnderline:
		return "\x1b[4 q"
	default:
		return "\x1b[2 q"
	}
}

// clampCursor keeps the cursor inside the grid.
//
// A screen with the cursor outside it is a bug upstairs, and the answer to it
// is a cursor in the corner rather than a CUP to row 0, which some terminals
// read as "row 1" and others as "no change".
func clampCursor(s *screen.Screen) (row, col int) {
	row, col = s.CursorRow, s.CursorCol
	if row < 0 {
		row = 0
	}
	if col < 0 {
		col = 0
	}
	if s.Grid.Rows > 0 && row >= s.Grid.Rows {
		row = s.Grid.Rows - 1
	}
	if s.Grid.Cols > 0 && col >= s.Grid.Cols {
		col = s.Grid.Cols - 1
	}
	return row, col
}
