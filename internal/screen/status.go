package screen

import (
	"strings"

	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// The furniture: the tabline, the status lines, the ruler, 'showcmd', the
// command line and the message area.
//
// Every column in here came off a screen dump of vim 9.2.0321, and the two
// that could not be dumped -- the ruler's fill count and 'showcmd''s column --
// were read out of the escape sequences vim wrote to a pty, which is the same
// measurement by a longer route. The arithmetic is vim's comp_col():
//
//	ru_col = Columns - 18 (with 'ruler')
//	sc_col = Columns - 28 (with 'ruler' and 'showcmd', no
//	 status line on the last window)
//
// On a 40-column screen that is 22 and 12, and typing "12" in normal mode put
// them at exactly those columns.

// colRuler is vim's COL_RULER plus one: the width the ruler reserves at the
// right-hand end of a line.
const colRuler = 18

// showCmdCols is vim's SHOWCMD_COLS: how much room 'showcmd' gets.
const showCmdCols = 10

// rulerCol is the screen column the ruler starts at, 0-based.
func rulerCol(cols int) int {
	if n := cols - colRuler; n > 0 {
		return n
	}
	return 1
}

// showCmdCol is the screen column 'showcmd' starts at, 0-based.
//
// lastHasStatus is whether the bottom window carries a status line, because
// that is where the ruler goes when it does and 'showcmd' gets the room back.
func showCmdCol(cols int, ruler, lastHasStatus bool) int {
	n := 0
	if ruler && !lastHasStatus {
		n = colRuler
	}
	n += showCmdCols
	if !ruler || lastHasStatus {
		n++
	}
	if c := cols - n; c > 0 {
		return c
	}
	return 1
}

// drawTabline paints the tabline as text, which is the only way it is ever
// painted here: the vimrc says guioptions-=e, so even the GUI gets this.
//
// Measured on a 40-column screen with two tabs, the second holding a modified
// buffer:
//
//	f.txt + second.txt X
//
// which is, per tab, a leading space, then "+" and a space when the tab holds
// a modified buffer or more than one window, then the name, then a trailing
// space. The X in the last column closes the current tab and is only there
// when more than one is open.
func (f *Frame) drawTabline(s *Screen, g Groups) {
	cols := s.Grid.Cols
	s.Grid.Fill(0, 0, cols, ' ', g.TabLineFill)
	if f.Tabs == nil || len(f.Tabs.Pages) == 0 {
		return
	}

	count := len(f.Tabs.Pages)
	tabWidth := 6
	if n := (cols - 1 + count/2) / count; n > tabWidth {
		tabWidth = n
	}

	col := 0
	for i, p := range f.Tabs.Pages {
		if col >= cols-4 {
			break
		}
		hl := g.TabLine
		if i == f.Tabs.Cur {
			hl = g.TabLineSel
		}
		start := col
		col = s.Grid.Fill(0, col, 1, ' ', hl)

		wins := p.Windows()
		modified := false
		for _, w := range wins {
			if f.modified(w.Buf) {
				modified = true
			}
		}
		if modified || len(wins) > 1 {
			if len(wins) > 1 {
				col = s.Grid.SetString(0, col, itoa(len(wins)), hl)
			}
			if modified {
				col += s.Grid.Set(0, col, '+', hl)
			}
			col = s.Grid.Fill(0, col, 1, ' ', hl)
		}

		name := p.Label
		if name == "" && p.Cur != nil {
			name = f.name(p.Cur.Buf)
		}
		if room := start - col + tabWidth - 1; room > 0 {
			name = tailOf(name, room)
		}
		col = s.Grid.SetString(0, col, name, hl)
		col = s.Grid.Fill(0, col, 1, ' ', hl)
	}

	if count > 1 && cols > 0 {
		s.Grid.Set(0, cols-1, 'X', g.TabLine)
	}
}

// tailOf drops leading characters until a name fits in n cells, which is what
// vim's tabline does with a path too long for its tab. There is no "<" marker:
// vim does not draw one here, unlike the status line, which does.
func tailOf(s string, n int) string {
	for LineWidth([]byte(s), 8) > n && len(s) > 0 {
		_, size := decodeFirst(s)
		s = s[size:]
	}
	return s
}

// decodeFirst returns the first rune of s and its size in bytes.
func decodeFirst(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

// drawStatus paints one window's status line: the buffer name, its flags, and
// the ruler right-aligned in whatever room is left.
//
// Measured, on a 40-column screen split horizontally with the top buffer
// modified:
//
//	f.txt [+] 1,1 All
//
// and on a 60-column screen split three ways, where the window is 20 wide:
//
//	f.txt 1,40 All
//
// width is the window's text width, which is what the ruler is laid out
// against; frame is the whole rectangle including the vertical separator
// column, which is what gets painted.
func (f *Frame) drawStatus(s *Screen, g Groups, w *window.Window, current bool, row, col, width, frame int) {
	hl := g.StatusLineNC
	if current {
		hl = g.StatusLine
	}
	fill := fillChar(f.opts().G.FillChars, statusFillItem(current), ' ')
	s.Grid.Fill(row, col, frame, fill, hl)

	name := f.name(w.Buf)
	var flags string
	if f.readOnly(w.Buf) {
		flags = "[RO]"
	}
	if f.modified(w.Buf) {
		flags = "[+]" + flags
	}
	if flags != "" {
		name += " " + flags
	}
	ruCol := thisRuCol(rulerCol(s.Grid.Cols), s.Grid.Cols, width)
	s.Grid.SetString(row, col, truncName(name, ruCol), hl)

	if f.opts().G.Ruler {
		f.drawRuler(s, g, w, row, col, width, ruCol, true, fill)
	}
}

// thisRuCol is vim's this_ru_col: where the ruler starts inside one window,
// which is the screen's ru_col shifted by everything to this window's left and
// then held to at least half the window.
//
//	this_ru_col = ru_col - (Columns - wp->w_width);
//	if (this_ru_col < (wp->w_width + 1) / 2)
//	 this_ru_col = (wp->w_width + 1) / 2;
//
// On a 40-column screen with one window that is 22, and after ":vsplit" it is
// 10 in each half, because 22 - (40 - 20) is 2 and half of 20 wins.
func thisRuCol(ruCol, cols, width int) int {
	n := ruCol - (cols - width)
	if half := (width + 1) / 2; n < half {
		n = half
	}
	return n
}

// truncName cuts a status line's buffer name down to the room in front of the
// ruler, which is vim's win_redr_status():
//
//	if (this_ru_col <= 1)
//	 p = (char_u *)"<";		// No room for file name!
//	else
//	{
//	 clen = mb_string2cells(p, -1);
//	 for (i = 0; p[i] != NUL && clen >= this_ru_col - 1;
//	 i += (*mb_ptr2len)(p + i))
//	 clen -= (*mb_ptr2cells)(p + i);
//	 if (i > 0) { p = p + i - 1; *p = '<'; }
//	}
//
// It keeps the tail, because the tail is the half that says which file this
// is, and it measures against the ruler's column rather than the window's
// width, because the ruler would paint over anything past it. Measured on vim
// 9.2.0321 at 40 columns with 'laststatus' 2, one character at a time: a name
// 20 cells wide is drawn whole and one 21 cells wide comes out as "<" and its
// last 20 cells.
func truncName(name string, ruCol int) string {
	if ruCol <= 1 {
		return "<"
	}
	clen := LineWidth([]byte(name), 8)
	i := 0
	for _, ch := range Chars([]byte(name), 8) {
		if clen < ruCol-1 {
			break
		}
		clen -= ch.Width
		i = ch.Byte + ch.Size
	}
	if i == 0 {
		return name
	}
	return "<" + name[i:]
}

// statusFillItem is the 'fillchars' item a status line pads with: "stl" for
// the current window and "stlnc" for the others. Both default to a space.
func statusFillItem(current bool) string {
	if current {
		return "stl"
	}
	return "stlnc"
}

// drawRuler paints the "1,1 All" field at the right-hand end of a
// status line or of the last screen row.
//
// The arithmetic is vim's win_redr_ruler(), and the one line of it that is
// easy to miss is why a ruler on the last screen row is one cell shorter than
// the same ruler on a status line: vim cannot use the last character of the
// screen, so it counts one more column as spoken for. On a 40-column screen
// that is the difference between
//
//	f.txt 1,1 All (status line, ends at 39)
//	 1,1 All (last row, ends at 38)
func (f *Frame) drawRuler(s *Screen, g Groups, w *window.Window, row, col, width, ruCol int, hasStatus bool, fill rune) {
	if width <= 0 {
		return
	}
	if half := (width + 1) / 2; ruCol < half {
		ruCol = half
	}
	if ruCol >= width {
		return
	}

	b := bufOf(w)
	line := []byte(nil)
	if n := w.View.Cursor.Line; n >= 1 && n <= b.LineCount() {
		line = b.Line(n)
	}

	// The line number and the column are two independent questions in vim's
	// win_redr_ruler(), and collapsing them into one is how a blank line ends
	// up reported as line zero:
	//
	//	vim_snprintf(buffer, ..., "%ld,",
	//	 (wp->w_buffer->b_ml.ml_flags & ML_EMPTY) ? 0L
	//	 : wp->w_cursor.lnum);
	//	col_print(buffer + len, ...,
	//	 empty_line ? 0 : wp->w_cursor.col + 1, virtcol + 1);
	//
	// So the zero is ML_EMPTY, a buffer that never held anything, and the
	// "0-1" is any empty cursor line. Measured on vim 9.2.0321 over "aaa",
	// three blank lines and "bbb": the blanks report "2,0-1", "3,0-1" and
	// "4,0-1", ":enew" and a zero-length file both report "0,0-1", and a file
	// holding a single newline reports "1,0-1".
	lnum := w.View.Cursor.Line
	if b.Emptied() {
		lnum = 0
	}
	bcol, vcol := 0, 1
	if len(line) > 0 {
		ts := max(1, f.opts().B.TabStop)
		bcol = w.View.Cursor.Col + 1
		vcol = text.VirtCol(line, w.View.Cursor.Col, ts)
	}
	head := itoa(lnum) + "," + itoa(bcol)
	if bcol != vcol {
		head += "-" + itoa(vcol)
	}

	rel := relPos(w, b, f.BotLine(w.ID))
	o := len(head) + len(rel)
	if !hasStatus {
		o++ // vim: "can't use last char of screen"
	}

	// Vim writes the relative position only when there is room for it after
	// the fill, and drops it whole when there is not. On a 12-column screen
	// the ruler is "1,1" and nothing else, because half of 12 is 6 and
	// 6 + 3 + 3 + 1 does not fit.
	txt := head
	if ruCol+o < width {
		pad := 0
		for ruCol+o < width {
			pad++
			o++
		}
		txt = head + strings.Repeat(string(fill), pad) + rel
	}
	s.Grid.SetString(row, col+ruCol, txt, statusHL(g, hasStatus, f, w))
}

// statusHL is the highlight a ruler is drawn in: the status line's when it is
// on one, and Normal when it is on the last screen row.
func statusHL(g Groups, hasStatus bool, f *Frame, w *window.Window) HLID {
	if !hasStatus || f.Tabs == nil {
		return Normal
	}
	tab := f.Tabs.Current()
	if tab != nil && tab.Cur == w {
		return g.StatusLine
	}
	return g.StatusLineNC
}

// relPos is vim's get_rel_pos(): where the window is in the buffer.
//
// bot is the last buffer line the window actually showed, which under 'wrap'
// and with folds closed is not window.BotLine().
//
//	All the whole buffer is on screen
//	Top the first line is on screen and the last is not
//	Bot the last line is on screen and the first is not
//	nn% neither, as a percentage of the lines above the window
func relPos(w *window.Window, b *text.Buffer, bot int) string {
	above := w.View.TopLine - 1
	if above < 0 {
		above = 0
	}
	if bot < 1 {
		bot = w.BotLine()
	}
	below := b.LineCount() - bot
	if below < 0 {
		below = 0
	}
	switch {
	case below <= 0 && above == 0:
		return "All"
	case below <= 0:
		return "Bot"
	case above <= 0:
		return "Top"
	default:
		pct := above * 100 / (above + below)
		s := itoa(pct)
		if len(s) < 2 {
			s = " " + s
		}
		return s + "%"
	}
}

// drawCmdArea paints the command line, the message area and the mode message,
// and grows the message area upward when there is more to say than 'cmdheight'
// rows to say it in.
//
// Vim's overflow behaviour, captured off a pty by running ":ls" with
// cmdheight=1: the screen scrolls, the messages land on the bottom rows, and
//
//	Press ENTER or type command to continue
//
// goes under them in the Question highlight, which is the green one.
func (f *Frame) drawCmdArea(s *Screen, g Groups, cmd Region) {
	rows := f.Message.rows()

	if f.msgOverflows(cmd.Rows) {
		// The message area takes rows off the bottom of the screen.
		if rows > s.Grid.Rows {
			rows = s.Grid.Rows
		}
		top := s.Grid.Rows - rows
		for i, row := 0, top; row < s.Grid.Rows; i, row = i+1, row+1 {
			s.Grid.Fill(row, 0, s.Grid.Cols, ' ', Normal)
			if i < len(f.Message.Lines) {
				m := f.Message.Lines[i]
				s.Grid.SetString(row, 0, m.Text, f.msgHL(s, m.Group))
			}
		}
		if f.Message.PressEnter {
			last := s.Grid.Rows - 1
			end := s.Grid.SetString(last, 0, PressEnterText, g.Question)
			s.CursorRow, s.CursorCol = last, min(end, s.Grid.Cols-1)
		}
		return
	}

	for i := 0; i < cmd.Rows; i++ {
		s.Grid.Fill(cmd.Row+i, 0, cmd.Cols, ' ', Normal)
	}
	for i, m := range f.Message.Lines {
		if i >= cmd.Rows {
			break
		}
		s.Grid.SetString(cmd.Row+i, 0, m.Text, f.msgHL(s, m.Group))
	}

	switch {
	case f.Cmdline.Active:
		row := cmd.Row + cmd.Rows - 1
		s.Grid.Fill(row, 0, cmd.Cols, ' ', Normal)
		s.Grid.SetString(row, 0, f.Cmdline.Prefix+f.Cmdline.Text, Normal)
		at := LineWidth([]byte(f.Cmdline.Prefix), 8) +
			LineWidth([]byte(f.Cmdline.Text[:min(f.Cmdline.Pos, len(f.Cmdline.Text))]), 8)
		s.CursorRow, s.CursorCol = row, min(at, s.Grid.Cols-1)
	case !f.Message.hasText() && f.Mode != "":
		s.Grid.SetString(cmd.Row, 0, f.Mode, g.ModeMsg)
	}

	// 'showcmd' sits at the right-hand end of the last row, out of the
	// command line's way. Vim stops drawing it while a command line is being
	// typed, because the two share the row.
	if !f.Cmdline.Active && f.opts().G.ShowCmd && f.ShowCmd != "" {
		s.Grid.SetString(s.Grid.Rows-1,
			showCmdCol(s.Grid.Cols, f.opts().G.Ruler, s.Layout.showStatus()),
			f.ShowCmd, Normal)
	}
}

// msgHL resolves a message line's highlight group name.
func (f *Frame) msgHL(s *Screen, group string) HLID {
	if group == "" {
		return Normal
	}
	return s.HL.ID(group)
}
