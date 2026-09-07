package main

import (
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/screen"
	"github.com/pkar/pvim/internal/text"
)

// What the editor looks like, handed to internal/screen to be drawn.
//
// The drawing itself is not here and must not be: internal/screen owns the
// grid, the layout, the window tree, 'nowrap' and 'wrap', the status line, the
// ruler and the message area, and it takes one struct of plain data per frame.
// This file fills that struct in. Everything it does is a lookup on state the
// editor already has, which is why there is no arithmetic in it and why the
// day 'wrap' or folds start working nothing here changes.

// draw composites the editor onto a screen of the given size.
func (e *editor) draw(rows, cols int) *screen.Screen {
	s := screen.NewScreen(rows, cols)
	s.HL = e.hl
	s.Render(e.frame())
	// Kept for the mouse, which is the one input that arrives in screen
	// coordinates and has to be turned back into a buffer position.
	e.regions = s.Regions()
	e.placeCursor(s)
	return s
}

// frame is one redraw's worth of editor state.
func (e *editor) frame() *screen.Frame {
	f := &screen.Frame{
		Tabs:     e.tabs,
		Opt:      e.opt,
		Names:    e.names(),
		Modified: e.modifiedSet(),
		ShowCmd:  e.ed.Showcmd(),
		Mode:     e.modeText(),
		Message:  screen.Message{Lines: []screen.MsgLine{{Text: e.message()}}},
	}
	// The spans internal/screen paints under hlsearch and Visual and over the
	// base. Nil when ":syntax off" or when nothing is attached, which is the
	// whole of how the feature stays off.
	if e.syn != nil {
		f.Syntax = e.syn.forTabs(e.tabs)
	}
	// The spelling undercurl, over the top of those. It rides in the same slot
	// for the reason cmd/pvim/spell.go's spellSpans gives: a run of buffer
	// bytes with a highlight id on it is what this field is, and screen.Frame
	// has no second one. Nil in every window without 'spell' set, which is
	// every window but a .txt or a .md one.
	f.Syntax = e.sess.spellSpans(e.tabs, f.Syntax, e.hl)
	switch {
	case e.sess.line != nil:
		f.Cmdline = screen.Cmdline{
			Active: true,
			Prefix: ":",
			Text:   e.sess.line.String(),
			Pos:    e.sess.line.Pos,
		}
	case e.ed.Cmdline() != "":
		// internal/mode owns the "/" and "?" prompts because a search is a
		// motion; it hands back the prefix and the text as one string.
		c := e.ed.Cmdline()
		f.Cmdline = screen.Cmdline{Active: true, Prefix: c[:1], Text: c[1:], Pos: len(c) - 1}
	}
	return f
}

// names is what every buffer is called, which is what the status line of each
// window and the label of each tab are drawn from.
//
// Every buffer and not just the current one: with a second tab open over the
// socket the tabline names both, and a map holding one entry left the other
// tab labelled "[No Name]" over a file with a perfectly good name.
func (e *editor) names() map[*text.Buffer]string {
	m := map[*text.Buffer]string{}
	if e.ctx == nil || e.ctx.Bufs == nil {
		return m
	}
	for _, b := range e.ctx.Bufs.Bufs {
		m[b.Text] = b.Display()
	}
	return m
}

// modifiedSet is the "[+]" marker, per buffer.
func (e *editor) modifiedSet() map[*text.Buffer]bool {
	m := map[*text.Buffer]bool{}
	if e.ctx == nil || e.ctx.Bufs == nil {
		return m
	}
	for _, b := range e.ctx.Bufs.Bufs {
		m[b.Text] = b.Modified()
	}
	return m
}

// modeText is 'showmode': what vim writes on the message line about the mode
// it is in, with the recording indicator after it.
func (e *editor) modeText() string {
	var s string
	switch e.ed.Mode() {
	case mode.Insert:
		s = "-- INSERT --"
	case mode.Replace:
		s = "-- REPLACE --"
	case mode.VisualChar:
		s = "-- VISUAL --"
	case mode.VisualLine:
		s = "-- VISUAL LINE --"
	case mode.VisualBlock:
		s = "-- VISUAL BLOCK --"
	}
	if r := e.ed.Recording(); r != 0 {
		s += "recording @" + string(rune(r))
	}
	return s
}

// placeCursor puts the caret where the cursor is, in cells, and gives it the
// shape the mode asks for.
//
// The row and the column come off the window's own view rather than off
// anything internal/screen worked out, because the two agree by construction:
// the renderer drew line TopLine on the first row of the window's region and
// under 'nowrap' there is one row per line.
func (e *editor) placeCursor(s *screen.Screen) {
	r := s.Regions()
	if e.sess.line != nil || e.ed.Cmdline() != "" {
		s.CursorRow = r.Cmdline.Row
		s.CursorCol = 1 + e.cmdlinePos()
		s.CursorShape = screen.CursorBar
		return
	}
	w := e.tabs.Window()
	if w == nil {
		return
	}
	cur := e.ed.Cursor()
	col := text.VirtCol(e.buf.Line(cur.Line), cur.Col, e.opt.B.TabStop) - 1 - w.View.LeftCol
	s.CursorRow = clampInt(r.Text.Row+cur.Line-w.View.TopLine, r.Text.Row, r.Text.Row+r.Text.Rows-1)
	s.CursorCol = clampInt(col, 0, max(0, r.Text.Cols-1))
	switch {
	case e.unfocused:
		// vim draws a hollow caret in a window that is not the key window,
		// whatever mode it is in. It is the only way to tell at a glance which
		// of two editors a keystroke would go to.
		s.CursorShape = screen.CursorHollow
	case e.ed.Mode() == mode.Insert:
		s.CursorShape = screen.CursorBar
	case e.ed.Mode() == mode.Replace:
		s.CursorShape = screen.CursorUnderline
	default:
		s.CursorShape = screen.CursorBlock
	}
}

// cmdlinePos is how far along the open command line the cursor is.
func (e *editor) cmdlinePos() int {
	if e.sess.line != nil {
		return e.sess.line.Pos
	}
	return len(e.ed.Cmdline()) - 1
}

func clampInt(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
