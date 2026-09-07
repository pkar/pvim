// Command smoke is the window this package cannot test: it is what a person
// runs to do this package's manual checks, and what a machine
// runs to measure a frame in a real window rather than in a benchmark.
//
// It is not the editor and it never will be. It holds no buffer, no mode and
// no keymap: it paints the events it was handed, which is exactly what a
// frontend check needs and exactly what makes it useless for anything else.
// cmd/pvim is the real client of this package.
//
//	go run ./internal/gui/smoke a window that echoes what it receives
//	go run ./internal/gui/smoke -script open, measure, relayout, exit 0
//	go run ./internal/gui/smoke -png f.png no window at all: write one frame
//
// The scripted run touches nobody's keyboard and finishes on its own, so it can
// be run from a terminal on a box that has a window server; the interactive one
// needs a person, which is the whole point of the file it belongs to. The third
// opens nothing and needs no display: it writes the exact pixels the window
// would have blitted, which is the only way to look at this editor's output on
// a machine where nothing may look at a screen.
package main

import (
	"errors"
	"flag"
	"fmt"
	"image/png"
	"os"
	"runtime"
	"sort"
	"time"

	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/raster"
	"github.com/pkar/pvim/internal/screen"
)

// AppKit runs on the process's first thread and nowhere else. Every main that
// opens a window has this line and cmd/pvim has it too.
func init() { runtime.LockOSThread() }

// errDone ends Run. Returning an error is how a handler stops the loop, and
// this one means "as planned" rather than "something broke".
var errDone = errors.New("smoke: done")

func main() {
	script := flag.Bool("script", false, "run the measurement script and exit instead of waiting for a person")
	pngPath := flag.String("png", "", "write one rasterised frame to this file and exit, opening no window")
	scale := flag.Int("scale", 2, "backingScaleFactor to rasterise the -png frame at")
	flag.Parse()

	var err error
	switch {
	case *pngPath != "":
		err = writePNG(*pngPath, *scale)
	case *script:
		err = runScript()
	default:
		err = runInteractive()
	}
	if err != nil && !errors.Is(err, errDone) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ----------------------------------------------------------------------------
// The interactive window: manual checks 2 through 7.

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
	return gui.Run(func(c gui.Client, ev gui.Event) error {
		switch x := ev.(type) {
		case gui.ResizeEvent:
			e.rows, e.cols = x.Rows, x.Cols
			e.note(fmt.Sprintf("resize %dx%d", x.Rows, x.Cols))
		case gui.ScaleEvent:
			e.scale = x.Scale
			e.note(fmt.Sprintf("scale %d  (the face was rebuilt; a window dragged between displays lands here)", x.Scale))
		case gui.FocusEvent:
			e.focused = x.Focused
			// The hollow caret is the editor's answer to this event, so the
			// window that is not focused shows one here too.
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
			// Cmd-Q and the red button both arrive here rather than as
			// terminate:, which is what lets a real editor prompt first.
			return errDone
		}
		if err := c.SetTitle(gui.Title("smoke", "internal/gui", true)); err != nil {
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
	"pvim internal/gui smoke -- not an editor, an echo",
	"",
	"type anything: every key arrives below in vim notation",
	"F1 block  F2 bar  F3 underline  F4 hollow -- the four carets, no blink (D-004)",
	"click, drag and scroll: the cell is what the editor would be handed",
	"Cmd-C, Cmd-V, Cmd-X, Cmd-A come through the Edit menu as the keys vim binds",
	"Cmd-Q or the red button: a CloseEvent, and this window exits 0",
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
	put(fmt.Sprintf("%dx%d cells, backingScaleFactor %d, focused %v", rows, cols, e.scale, e.focused))
	put("")

	// The newest lines, oldest first, filling what is left of the window.
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
	return "mod " + key.Key{Rune: 'x', Mod: m}.String()
}

// ----------------------------------------------------------------------------
// The scripted run: what a machine can check about a live window.

// script state: which step of the run the next ResizeEvent belongs to.
type run struct {
	step    int
	resizes int
	focus   []bool
	scales  []int
}

// runScript opens the window, measures a frame, changes the font and the line
// spacing, and quits. Every number it prints goes in the manual checks.
func runScript() error {
	// Nothing may outlive this run. A wedged AppKit call would otherwise leave
	// a process behind with a window on somebody's screen and no keyboard.
	go func() {
		time.Sleep(30 * time.Second)
		fmt.Fprintln(os.Stderr, "smoke: watchdog fired, the run loop never finished")
		os.Exit(3)
	}()

	st := &run{}
	err := gui.Run(func(c gui.Client, ev gui.Event) error {
		switch e := ev.(type) {
		case gui.FocusEvent:
			st.focus = append(st.focus, e.Focused)
		case gui.ScaleEvent:
			st.scales = append(st.scales, e.Scale)
		case gui.ResizeEvent:
			st.resizes++
			return st.at(c, e)
		case gui.CloseEvent:
			return errDone
		}
		return nil
	})
	fmt.Printf("resizes=%d focus=%v scales=%v\n", st.resizes, st.focus, st.scales)
	if err != nil && !errors.Is(err, errDone) {
		return fmt.Errorf("Run: %w", err)
	}
	fmt.Println("Run returned:", err)
	return nil
}

// at runs the step of the script this ResizeEvent belongs to.
func (st *run) at(c gui.Client, e gui.ResizeEvent) error {
	rows, cols := e.Rows, e.Cols
	s := filled(rows, cols)

	switch st.step {
	case 0:
		st.step++
		t0 := time.Now()
		if err := c.Draw(s); err != nil {
			return err
		}
		first := time.Since(t0)

		// Warm the pool: three buffers, so it takes four frames before one
		// comes round holding a grid it can diff against.
		for i := 0; i < 4; i++ {
			if err := c.Draw(s); err != nil {
				return err
			}
		}

		// Typing: one cell changes and the caret moves one column, which is
		// the frame the dirty-rect blit exists for.
		var keys []time.Duration
		for i := 0; i < 300; i++ {
			s.Grid.Set(i%rows, i%cols, rune('a'+i%26), screen.Normal)
			s.CursorRow, s.CursorCol = i%rows, (i+1)%cols
			t := time.Now()
			if err := c.Draw(s); err != nil {
				return err
			}
			keys = append(keys, time.Since(t))
		}
		p50, p99, max := quantiles(keys)
		fmt.Printf("window %dx%d  first frame %.2fms  keystroke frames p50 %.1fus p99 %.1fus max %.1fus\n",
			rows, cols, ms(first), us(p50), us(p99), us(max))

		// A recolour changes no cell and every pixel. If this comes out at
		// keystroke speed, raster.Stamp is not doing its job and the window is
		// about to be left in the old colours.
		s.HL.Set(screen.NormalName, screen.Highlight{
			FG: screen.RGB{R: 0xd0, G: 0xd0, B: 0xd0},
			BG: screen.RGB{R: 0x10, G: 0x20, B: 0x30},
		})
		t := time.Now()
		if err := c.Draw(s); err != nil {
			return err
		}
		fmt.Printf("recolour frame %.2fms (has to be a full repaint, not a keystroke)\n", ms(time.Since(t)))

		// One frame per caret shape, so a person watching sees all four.
		for _, shape := range []screen.CursorShape{
			screen.CursorBlock, screen.CursorBar, screen.CursorUnderline, screen.CursorHollow,
		} {
			s.CursorShape = shape
			if err := c.Draw(s); err != nil {
				return err
			}
		}

		if err := c.SetTitle(gui.Title("smoke.go", "/tmp/pvim", true)); err != nil {
			return err
		}
		// A smaller font relayouts the window: one ResizeEvent, and exactly
		// one, which is what the next step checks.
		return gui.SetFont(raster.GUIFont{Family: "Monaco", Size: 9})

	case 1:
		st.step++
		fmt.Printf("after guifont=Monaco:h9 the grid is %dx%d\n", rows, cols)
		if err := c.Draw(s); err != nil {
			return err
		}
		return gui.SetLinespace(6)

	case 2:
		st.step++
		fmt.Printf("after linespace=6 the grid is %dx%d\n", rows, cols)
		if err := c.Draw(s); err != nil {
			return err
		}
		// The size the event carried is the size the window has, which holds
		// because each relayout delivers one event and not two. It was two
		// until and this is what caught it.
		if r, cl := c.Size(); r != rows || cl != cols {
			return fmt.Errorf("Size() = %dx%d but the ResizeEvent said %dx%d: a stale relayout event", r, cl, rows, cols)
		}
		c.Bell() // must do nothing at all, audibly or otherwise
		return errDone
	}
	return errDone
}

// filled is a screenful of text, which is what a frame costs in practice.
func filled(rows, cols int) *screen.Screen {
	s := screen.NewScreen(rows, cols)
	line := "package main // the quick brown fox jumps over the lazy dog 0123456789"
	for r := 0; r < rows; r++ {
		n := cols
		if n > len(line) {
			n = len(line)
		}
		s.Grid.SetString(r, 0, line[:n], screen.Normal)
	}
	s.CursorShape = screen.CursorBlock
	return s
}

// quantiles returns the median, the 99th percentile and the worst of d. It
// sorts in place, which nothing else reads afterwards.
func quantiles(d []time.Duration) (p50, p99, max time.Duration) {
	if len(d) == 0 {
		return
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[len(d)/2], d[(len(d)*99)/100], d[len(d)-1]
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
func us(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1000 }

// ----------------------------------------------------------------------------
// One frame, to a file, with no window.

// writePNG rasterises a frame exactly as window.paint would and writes it out.
//
// It is the only way to look at what this editor draws on a machine that may
// not look at a screen: no window server, no Screen Recording permission and no
// screenshot, but the same Face, the same Frame and the same raster.Paint that
// the CGImage is built over. What it cannot show is anything AppKit does with
// those pixels afterwards -- contentsScale, layer gravity, the window's own
// colour -- and those are item 1 of the manual checks.
func writePNG(path string, scale int) error {
	face, err := raster.NewFace(gui.Font, scale)
	if err != nil {
		return err
	}
	s := screen.NewScreen(13, 60)
	lines := []string{
		"package main",
		"",
		"import \"fmt\"",
		"",
		"func main() {",
		"    fmt.Println(\"pvim: a window with no C compiler\")",
		"}",
		"",
		"the quick brown fox jumps over the lazy dog",
		"THE QUICK BROWN FOX JUMPS OVER THE LAZY DOG",
		"0123456789 (){}[]<> !@#$%^&*-=_+|\\/?.,;:'\"`~",
		"",
		// Monaco has no CJK, which in as many words. Each of
		// these renders as the font's .notdef box in the first of the two
		// cells a wide rune takes, and seeing that is the point of the line:
		// it is the expected answer and not a bug.
		"not in Monaco, so .notdef: 日本語",
	}
	for i, line := range lines {
		s.Grid.SetString(i, 0, line, screen.Normal)
	}
	s.CursorRow, s.CursorCol = 5, 8
	s.CursorShape = screen.CursorBlock

	opt := raster.PaintOptions{Face: face}
	frame := raster.Frame{
		Grid:      &s.Grid,
		HL:        s.HL,
		CursorRow: s.CursorRow,
		CursorCol: s.CursorCol,
		Cursor:    raster.CursorBlock,
	}
	img := raster.NewImage(frame, opt)
	if err := raster.Paint(img, frame, opt); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	w, h := raster.Size(frame, opt)
	fmt.Printf("%s: %dx%d pixels, %dx%d cells at scale %d\n", path, w, h, s.Grid.Rows, s.Grid.Cols, scale)
	return nil
}
