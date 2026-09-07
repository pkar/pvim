// Command smoke is the X11 window this package cannot test: it is what a person
// at a Linux desktop runs to do this package's manual checks.
//
// It is not the editor and it never will be. It holds no buffer, no mode and no
// keymap of its own: it paints the events it was handed, which is exactly what
// a frontend check needs and exactly what makes it useless for anything else.
// cmd/pvim is the real client of this package.
//
//	go run ./internal/gui/x11/smoke a window that echoes what it receives
//	go run ./internal/gui/x11/smoke -font X the same, asking for the font X
//	go run ./internal/gui/x11/smoke -probe no window: print what this box has
//
// The probe opens no window and needs no display, which is why it is here: it
// prints the font this box would resolve 'guifont' to and the search path it
// looked in, and that is answerable on any machine.
//
// It is a separate main from internal/gui/smoke rather than a flag on it,
// because the two frontends are peers and internal/deps_test.go's
// TestFrontendsAreStrangers means neither may import the other. A tool that
// imported both would be the first thing in the tree that knew there were two.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/gui/x11"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/raster"
	"github.com/pkar/pvim/internal/screen"
)

// errDone ends Run. Returning an error is how a handler stops the loop, and
// this one means "as planned" rather than "something broke".
var errDone = errors.New("smoke: done")

func main() {
	probe := flag.Bool("probe", false, "open no window: print the font this box resolves and exit")
	font := flag.String("font", "", "a vim 'guifont' string to ask for, e.g. Monaco:h13")
	flag.Parse()

	if *font != "" {
		g, err := raster.ParseGUIFont(*font)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		x11.Font = g
	}

	var err error
	if *probe {
		err = runProbe()
	} else {
		err = runInteractive()
	}
	if err != nil && !errors.Is(err, errDone) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runProbe answers the font question with no window at all.
//
// It is the first thing to run on a box where the window comes up with the
// wrong glyphs, because it separates the two halves of that: which file was
// found, and what was drawn from it.
func runProbe() error {
	fmt.Printf("guifont requested: %s\n", x11.Font)
	if path, err := x11.FindFontFile(x11.Font.Family); err == nil {
		fmt.Printf("resolved directly:  %s\n", path)
	} else {
		fmt.Printf("not found directly: %v\n", err)
	}
	for _, scale := range []int{1, 2} {
		face, got, err := x11.ResolveFont(x11.Font, scale)
		if err != nil {
			fmt.Printf("scale %d: no usable font: %v\n", scale, err)
			continue
		}
		m := face.Metrics()
		fmt.Printf("scale %d: %s, cell %dx%d, ascent %d\n", scale, got, m.CellW, m.CellH, m.Ascent)
	}
	fmt.Printf("DISPLAY=%q, a window is %v\n", os.Getenv("DISPLAY"), x11.Available())
	return nil
}

// echo is the interactive handler's whole state: the last few events, as text.
type echo struct {
	rows, cols int
	lines      []string
	caret      screen.CursorShape
	row, col   int
	scale      int
	focused    bool
}

// runInteractive opens the window and paints what arrives in it.
func runInteractive() error {
	e := &echo{caret: screen.CursorBlock, focused: true, scale: 1}
	return x11.Run(func(c gui.Client, ev gui.Event) error {
		switch x := ev.(type) {
		case gui.ResizeEvent:
			e.rows, e.cols = x.Rows, x.Cols
			e.note(fmt.Sprintf("resize %dx%d", x.Rows, x.Cols))
		case gui.ScaleEvent:
			e.scale = x.Scale
			e.note(fmt.Sprintf("scale %d  (from Xft.dpi in the resource database)", x.Scale))
		case gui.FocusEvent:
			e.focused = x.Focused
			if x.Focused {
				e.caret = screen.CursorBlock
			} else {
				e.caret = screen.CursorHollow
			}
			e.note(fmt.Sprintf("focus %v", x.Focused))
		case gui.KeyEvent:
			e.note("key " + x.Key.String())
			e.shape(x.Key)
		case gui.MouseEvent:
			e.note(fmt.Sprintf("mouse %s %s at row %d col %d %s",
				buttonName(x.Button), actionName(x.Action), x.Row, x.Col, modName(x.Mod)))
			if x.Action == gui.MousePress || x.Action == gui.MouseDrag {
				e.row, e.col = x.Row, x.Col
			}
		case gui.CloseEvent:
			// The window manager's close button, through WM_DELETE_WINDOW.
			return errDone
		}
		if err := c.SetTitle(gui.Title("smoke", "internal/gui/x11", true)); err != nil {
			return err
		}
		return c.Draw(e.screen())
	})
}

// shape lets F1 through F4 pick a caret, which is the only way to look at all
// four without an editor behind the window.
func (e *echo) shape(k key.Key) {
	switch k.Special {
	case key.KeyF1:
		e.caret = screen.CursorBlock
	case key.KeyF2:
		e.caret = screen.CursorBar
	case key.KeyF3:
		e.caret = screen.CursorUnderline
	case key.KeyF4:
		e.caret = screen.CursorHollow
	}
}

// note records one line, keeping the last screenful.
func (e *echo) note(s string) {
	e.lines = append(e.lines, s)
	if len(e.lines) > 200 {
		e.lines = e.lines[len(e.lines)-200:]
	}
}

// help is what the window says to the person in front of it.
var help = []string{
	"pvim internal/gui/x11 smoke -- not an editor, an echo",
	"",
	"type anything: every key arrives below in vim notation",
	"AltGr, CapsLock, the keypad with and without NumLock: all of them are here",
	"F1 block  F2 bar  F3 underline  F4 hollow -- the four carets, no blink (D-004)",
	"click, drag and scroll: the cell is what the editor would be handed",
	"the window manager's close button: a CloseEvent, and this window exits 0",
	"",
}

// screen renders the current state.
func (e *echo) screen() *screen.Screen {
	rows, cols := e.rows, e.cols
	if rows < 1 || cols < 1 {
		rows, cols = 1, 1
	}
	s := screen.NewScreen(rows, cols)
	r := 0
	put := func(text string) {
		if r >= rows {
			return
		}
		if len(text) > cols {
			text = text[:cols]
		}
		s.Grid.SetString(r, 0, text, screen.Normal)
		r++
	}
	for _, line := range help {
		put(line)
	}
	put(fmt.Sprintf("%dx%d cells, scale %d, focused %v", rows, cols, e.scale, e.focused))
	put("")

	room := rows - r
	lines := e.lines
	if room > 0 && len(lines) > room {
		lines = lines[len(lines)-room:]
	}
	for _, line := range lines {
		put(line)
	}

	s.CursorShape = e.caret
	s.CursorRow, s.CursorCol = clamp(e.row, rows-1), clamp(e.col, cols-1)
	return s
}

// clamp holds v in [0, hi].
func clamp(v, hi int) int {
	if v < 0 {
		return 0
	}
	if v > hi {
		return hi
	}
	return v
}

func buttonName(b gui.MouseButton) string {
	switch b {
	case gui.MouseLeft:
		return "left"
	case gui.MouseMiddle:
		return "middle"
	case gui.MouseRight:
		return "right"
	}
	return "none"
}

func actionName(a gui.MouseAction) string {
	switch a {
	case gui.MousePress:
		return "press"
	case gui.MouseRelease:
		return "release"
	case gui.MouseDrag:
		return "drag"
	case gui.MouseWheelUp:
		return "wheel-up"
	case gui.MouseWheelDown:
		return "wheel-down"
	case gui.MouseWheelLeft:
		return "wheel-left"
	case gui.MouseWheelRight:
		return "wheel-right"
	}
	return "?"
}

func modName(m key.Mod) string {
	if m == 0 {
		return ""
	}
	return "mods " + key.Key{Rune: 'x', Mod: m}.String()
}
