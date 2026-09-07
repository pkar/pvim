//go:build linux

package x11

import (
	"fmt"
	"image"
	"os"
	"sync"
	"sync/atomic"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/raster"
	"github.com/pkar/pvim/internal/screen"
)

// The X11 backend. Everything in this file talks to a socket; everything in the
// files without a build tag beside it does not, and that split is the only
// reason any of this could be written on a machine with no X server.
//
// # Three goroutines and what each is allowed to touch
//
// - The reader does nothing but xgb.Conn.WaitForEvent in a loop and hand what
// it gets to the loop. It exists because WaitForEvent cannot be selected
// against anything, and the loop has to select.
// - The loop owns the X connection's reply side: it handles every event, and
// it is what OnMain and PostMain run work on. It never blocks on the
// editor, because everything it sends the editor goes through eventq, which
// cannot block.
// - The handler is the editor. It calls Draw, which rasterises and sends the
// PutImage requests itself rather than posting them to the loop: xgb
// serialises requests from any goroutine, PutImage copies the pixels into
// the request buffer before it returns, and doing the expensive half of a
// frame on the loop would put the window's answers to the window manager
// behind the editor's slowest redraw.
//
// The frame buffer is shared between the handler, which paints it, and the
// loop, which re-sends parts of it on an Expose, so it is under a mutex. That
// mutex is held across a paint, which means an Expose arriving mid-frame waits
// for it. Expose is a window being uncovered and it happens at human speed;
// this is not the place for the three-buffer pool internal/gui needs, because
// there is no compositor here holding a reference to pixels this process
// handed it -- PutImage copies, and once the call returns the buffer is ours
// again.

// runOnce guards against a second Run. The connection, the window and the
// event loop are once-per-process things.
var runOnce sync.Once

// started is whether Run has been called in this process, read by Available.
var started atomic.Bool

// current is the live window, or nil. OnMain and PostMain reach the event loop
// through it, exactly as internal/gui's do.
var current *window

// bufferStep is the granularity the frame buffer is allocated at, in pixels.
// A resize drag is a new size per pixel of window width; rounding up makes it a
// dozen allocations over a drag instead of a few hundred.
const bufferStep = 256

// The initial window, in cells. 80 by 24 rather than internal/gui's 640 by 480
// points, because there are no points here: an X window is created in pixels
// and the pixels depend on the font this box turned out to have, so the size
// that means anything is the one in cells.
const (
	initialCols = 80
	initialRows = 24
)

// atoms are the interned atoms this window needs. Every one is a round trip at
// startup and none of them is one afterwards.
type atoms struct {
	wmProtocols    xproto.Atom
	wmDeleteWindow xproto.Atom
	netWMName      xproto.Atom
	utf8String     xproto.Atom
}

// window is the whole of one pvim window. It implements gui.Client.
type window struct {
	conn   *xgb.Conn
	screen *xproto.ScreenInfo
	win    xproto.Window
	gc     xproto.Gcontext
	format pixelFormat
	atoms  atoms

	// maxImage is how many bytes of pixels one PutImage may carry on this
	// connection, worked out once from the setup's maximum-request-length.
	maxImage int

	q     *eventq
	tasks chan func()
	quit  chan struct{}
	dead  chan struct{}
	xev   chan xgb.Event

	// keymap is owned by the loop and read by the loop. Nothing else touches
	// it, so it is outside the mutex.
	keymap *Keymap

	mu sync.Mutex

	face      raster.Face
	font      raster.GUIFont
	scale     int
	linespace int

	widthPx  int
	heightPx int
	rows     int
	cols     int

	// img is the frame buffer and painted is what it holds, which is what
	// makes a partial repaint safe: raster.Damage diffs against this and not
	// against a notion of "the last frame".
	img     *image.RGBA
	painted raster.Painted
	// wire is the scratch the converted pixels of one chunk go into, kept so
	// that a keystroke does not allocate.
	wire []byte
	// paintedW and paintedH are how much of img holds a frame, which is what
	// an Expose is allowed to re-send.
	paintedW int
	paintedH int

	bg    screen.RGB
	bgSet bool
}

// run is the linux implementation of Run.
func run(h gui.Handler) error {
	var err error
	ran := false
	runOnce.Do(func() {
		ran = true
		started.Store(true)
		err = runWindow(h)
	})
	if !ran {
		return ErrAlreadyRunning
	}
	return err
}

// available is the linux implementation of Available.
func available() bool {
	if started.Load() {
		return false
	}
	return os.Getenv("DISPLAY") != ""
}

// runWindow opens the connection, creates the window, starts the handler and
// runs the event loop until it stops.
func runWindow(h gui.Handler) error {
	if h == nil {
		return fmt.Errorf("x11: Run needs a handler")
	}
	conn, err := xgb.NewConn()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoDisplay, err)
	}

	w, err := newWindow(conn)
	if err != nil {
		conn.Close()
		return err
	}
	current = w

	done := make(chan struct{})
	var handlerErr error
	go func() {
		defer close(done)
		handlerErr = w.serve(h)
		_ = postMain(w.stop)
	}()

	go w.read()
	w.loop()

	// The loop is gone, so nothing will drain the task queue or the X socket
	// again. Both closes are what stops the goroutines that could otherwise
	// wait forever: the handler, blocked in eventq.pop, and anything inside
	// OnMain.
	close(w.dead)
	w.q.close()
	<-done
	conn.Close()
	return handlerErr
}

// newWindow does everything between a connection and a mapped window.
func newWindow(conn *xgb.Conn) (*window, error) {
	setup := xproto.Setup(conn)
	if setup == nil || len(setup.Roots) == 0 {
		return nil, fmt.Errorf("x11: the server sent no screens")
	}
	sc := &setup.Roots[conn.DefaultScreen]

	format, err := formatFor(setup, sc)
	if err != nil {
		return nil, err
	}

	w := &window{
		conn:     conn,
		screen:   sc,
		format:   format,
		maxImage: maxImageBytes(setup.MaximumRequestLength),
		q:        newEventq(),
		tasks:    make(chan func(), 64),
		quit:     make(chan struct{}),
		dead:     make(chan struct{}),
		xev:      make(chan xgb.Event, 64),
		scale:    1,
	}
	if w.maxImage < 4 {
		return nil, fmt.Errorf("x11: the server's maximum request length is %d, which cannot carry an image",
			setup.MaximumRequestLength)
	}

	w.scale = w.readScale()
	face, font, err := ResolveFont(Font, w.scale)
	if err != nil {
		return nil, err
	}
	w.face, w.font = face, font

	m := face.Metrics()
	w.cols, w.rows = initialCols, initialRows
	w.widthPx = w.cols * m.CellW
	w.heightPx = w.rows * (m.CellH + w.linespace)
	if w.widthPx < 1 || w.heightPx < 1 {
		// CreateWindow with a zero dimension is a BadValue and the window
		// never appears. internal/raster refuses a face with an empty cell, so
		// this is unreachable through it; the guard is here because the
		// failure it prevents is a window that silently does not open.
		return nil, fmt.Errorf("x11: %s has an empty cell, %dx%d", font, m.CellW, m.CellH)
	}

	if err := w.create(); err != nil {
		return nil, err
	}
	if err := w.internAtoms(); err != nil {
		return nil, err
	}
	w.setProtocols()
	w.setName("pvim")
	w.setSizeHints()
	w.loadKeymap()

	if err := xproto.MapWindowChecked(conn, w.win).Check(); err != nil {
		return nil, fmt.Errorf("x11: mapping the window: %w", err)
	}

	// The first event the editor ever sees is its size, before any key, so a
	// handler can allocate its screen in the ResizeEvent case and nowhere
	// else. A ConfigureNotify from the window manager will correct it in a
	// moment if it had other ideas about how big this window is.
	w.q.push(gui.ResizeEvent{Rows: w.rows, Cols: w.cols})
	if w.scale != 1 {
		w.q.push(gui.ScaleEvent{Scale: w.scale})
	}
	return w, nil
}

// formatFor works out how this server wants pixels, from the root visual and
// the pixmap format that matches the root depth.
func formatFor(setup *xproto.SetupInfo, sc *xproto.ScreenInfo) (pixelFormat, error) {
	var vis *xproto.VisualInfo
	for i := range sc.AllowedDepths {
		d := &sc.AllowedDepths[i]
		if d.Depth != sc.RootDepth {
			continue
		}
		for j := range d.Visuals {
			if d.Visuals[j].VisualId == sc.RootVisual {
				vis = &d.Visuals[j]
			}
		}
	}
	if vis == nil {
		return pixelFormat{}, fmt.Errorf("x11: the root visual %d is not in the screen's depth list", sc.RootVisual)
	}

	f := pixelFormat{
		depth:     sc.RootDepth,
		msbFirst:  setup.ImageByteOrder == 1,
		redMask:   vis.RedMask,
		greenMask: vis.GreenMask,
		blueMask:  vis.BlueMask,
	}
	for _, pf := range setup.PixmapFormats {
		if pf.Depth == sc.RootDepth {
			f.bitsPerPixel = pf.BitsPerPixel
		}
	}
	if err := checkFormat(f); err != nil {
		return pixelFormat{}, err
	}
	return f, nil
}

// create makes the window and its graphics context.
//
// The value list is in the protocol's own order, which is the ascending order
// of the mask bits and not the order they are named here: back-pixel,
// bit-gravity, event-mask. Getting that wrong is a window with the event mask
// in its background colour, which is a black window that answers nothing.
func (w *window) create() error {
	id, err := xproto.NewWindowId(w.conn)
	if err != nil {
		return fmt.Errorf("x11: no window id: %w", err)
	}
	w.win = id

	const mask = xproto.CwBackPixel | xproto.CwBitGravity | xproto.CwEventMask
	events := uint32(xproto.EventMaskKeyPress |
		xproto.EventMaskButtonPress |
		xproto.EventMaskButtonRelease |
		xproto.EventMaskButtonMotion |
		xproto.EventMaskExposure |
		xproto.EventMaskStructureNotify |
		xproto.EventMaskFocusChange)
	values := []uint32{
		pixelOf(w.format, screen.DefaultNormal.BG),
		// NorthWest gravity keeps what is already drawn pinned to the corner
		// the text starts at while a resize is in progress, rather than having
		// the server discard it and expose the whole window on every pixel of
		// a drag.
		xproto.GravityNorthWest,
		events,
	}
	err = xproto.CreateWindowChecked(w.conn, w.screen.RootDepth, w.win, w.screen.Root,
		0, 0, uint16(w.widthPx), uint16(w.heightPx), 0,
		xproto.WindowClassInputOutput, w.screen.RootVisual, mask, values).Check()
	if err != nil {
		return fmt.Errorf("x11: creating the window: %w", err)
	}

	gc, err := xproto.NewGcontextId(w.conn)
	if err != nil {
		return fmt.Errorf("x11: no graphics context id: %w", err)
	}
	w.gc = gc
	if err := xproto.CreateGCChecked(w.conn, gc, xproto.Drawable(w.win), 0, nil).Check(); err != nil {
		return fmt.Errorf("x11: creating the graphics context: %w", err)
	}
	return nil
}

// internAtoms interns the four atoms this window needs.
func (w *window) internAtoms() error {
	get := func(name string) (xproto.Atom, error) {
		r, err := xproto.InternAtom(w.conn, false, uint16(len(name)), name).Reply()
		if err != nil {
			return 0, fmt.Errorf("x11: interning %s: %w", name, err)
		}
		return r.Atom, nil
	}
	var err error
	if w.atoms.wmProtocols, err = get("WM_PROTOCOLS"); err != nil {
		return err
	}
	if w.atoms.wmDeleteWindow, err = get("WM_DELETE_WINDOW"); err != nil {
		return err
	}
	if w.atoms.netWMName, err = get("_NET_WM_NAME"); err != nil {
		return err
	}
	if w.atoms.utf8String, err = get("UTF8_STRING"); err != nil {
		return err
	}
	return nil
}

// setProtocols asks the window manager to send WM_DELETE_WINDOW rather than
// killing the connection when somebody presses the close button.
//
// Without it the red button is a KillClient and the editor's answer to a
// modified buffer under 'confirm' never gets asked for: the process goes away
// mid-keystroke and the work with it. This one property is the whole of the
// difference.
func (w *window) setProtocols() {
	data := make([]byte, 4)
	xgb.Put32(data, uint32(w.atoms.wmDeleteWindow))
	xproto.ChangeProperty(w.conn, xproto.PropModeReplace, w.win,
		w.atoms.wmProtocols, xproto.AtomAtom, 32, 1, data)
}

// setName sets the window title, both ways.
//
// _NET_WM_NAME in UTF8_STRING is what every window manager written this century
// reads and is the one that can carry a filename with a non-ASCII character in
// it. WM_NAME in STRING is Latin-1 by the ICCCM and is set as well, because a
// window manager that reads only the old one shows an empty title otherwise and
// because the ICCCM says a client sets it.
func (w *window) setName(title string) {
	b := []byte(title)
	xproto.ChangeProperty(w.conn, xproto.PropModeReplace, w.win,
		w.atoms.netWMName, w.atoms.utf8String, 8, uint32(len(b)), b)
	xproto.ChangeProperty(w.conn, xproto.PropModeReplace, w.win,
		xproto.AtomWmName, xproto.AtomString, 8, uint32(len(b)), b)

	// WM_CLASS is two NUL-terminated strings, instance then class, and it is
	// what a window manager keys its per-application rules and its taskbar
	// grouping off. Set once with the title so there is one place that writes
	// the window's identity.
	class := []byte("pvim\x00pvim\x00")
	xproto.ChangeProperty(w.conn, xproto.PropModeReplace, w.win,
		xproto.AtomWmClass, xproto.AtomString, 8, uint32(len(class)), class)
}

// setSizeHints tells the window manager that this window resizes in whole
// cells.
//
// WM_NORMAL_HINTS is eighteen 32-bit words in a fixed order and the flags word
// says which of them mean anything. The three set here are PMinSize (one cell,
// so a tiling manager cannot squeeze the window to nothing), PResizeInc (a
// cell, so a drag snaps to columns and rows the way every terminal emulator
// does) and PBaseSize (zero, which is what the increments are measured from).
func (w *window) setSizeHints() {
	// The face and 'linespace' belong to the editor goroutine and this runs on
	// the loop, so they are read under the lock rather than off the struct.
	// setFont posts this after it has swapped the face in, which is exactly
	// the moment the two goroutines would otherwise be reading and writing the
	// same field.
	w.mu.Lock()
	face, linespace := w.face, w.linespace
	w.mu.Unlock()
	if face == nil {
		return
	}
	m := face.Metrics()
	rowH := m.CellH + linespace

	const (
		pMinSize   = 1 << 4
		pResizeInc = 1 << 6
		pBaseSize  = 1 << 8
	)
	hints := make([]uint32, 18)
	hints[0] = pMinSize | pResizeInc | pBaseSize
	hints[5] = uint32(m.CellW) // min width
	hints[6] = uint32(rowH)    // min height
	hints[9] = uint32(m.CellW) // width increment
	hints[10] = uint32(rowH)   // height increment
	hints[15] = 0              // base width
	hints[16] = 0              // base height

	data := make([]byte, 4*len(hints))
	for i, v := range hints {
		xgb.Put32(data[i*4:], v)
	}
	xproto.ChangeProperty(w.conn, xproto.PropModeReplace, w.win,
		xproto.AtomWmNormalHints, xproto.AtomWmSizeHints, 32, uint32(len(hints)), data)
}

// readScale asks the resource database what this desktop calls a normal
// display. See dpi.go for why that is where the answer lives.
func (w *window) readScale() int {
	r, err := xproto.GetProperty(w.conn, false, w.screen.Root, xproto.AtomResourceManager,
		xproto.AtomString, 0, 1<<16).Reply()
	if err != nil || r == nil {
		return 1
	}
	dpi, ok := xftDPI(string(r.Value))
	if !ok {
		return 1
	}
	return scaleForDPI(dpi)
}

// loadKeymap fetches the whole keyboard from the server and builds a Keymap.
//
// Both requests, every time, on startup and on every MappingNotify. A
// MappingNotify is somebody running setxkbmap and it happens seconds apart at
// the very most; a keymap that is stale after one is an editor whose keys have
// quietly become somebody else's layout.
func (w *window) loadKeymap() {
	setup := xproto.Setup(w.conn)
	count := int(setup.MaxKeycode) - int(setup.MinKeycode) + 1
	if count < 1 || count > 256 {
		return
	}
	km, err := xproto.GetKeyboardMapping(w.conn, setup.MinKeycode, byte(count)).Reply()
	if err != nil || km == nil {
		return
	}
	syms := make([]uint32, len(km.Keysyms))
	for i, ks := range km.Keysyms {
		syms[i] = uint32(ks)
	}
	m := NewKeymap(byte(setup.MinKeycode), int(km.KeysymsPerKeycode), syms)

	if mm, err := xproto.GetModifierMapping(w.conn).Reply(); err == nil && mm != nil {
		codes := make([]byte, len(mm.Keycodes))
		for i, kc := range mm.Keycodes {
			codes[i] = byte(kc)
		}
		m.SetModifiers(int(mm.KeycodesPerModifier), codes)
	}
	w.keymap = m
}

// read is the reader goroutine: pull events off the socket and hand them to the
// loop, until the connection goes away.
func (w *window) read() {
	defer close(w.xev)
	for {
		ev, err := w.conn.WaitForEvent()
		if ev == nil && err == nil {
			// xgb's documented "the connection closed" answer.
			return
		}
		if ev == nil {
			// A protocol error with no event beside it. There is nothing to
			// deliver and nothing useful to do: an error from a request this
			// package sent unchecked is a bug in this package, and dropping it
			// keeps a window alive that would otherwise die over a stale
			// PutImage during a resize.
			continue
		}
		select {
		case w.xev <- ev:
		case <-w.quit:
			return
		}
	}
}

// loop is the event loop goroutine: X events, posted work, and nothing else.
func (w *window) loop() {
	for {
		select {
		case <-w.quit:
			return
		case fn := <-w.tasks:
			fn()
		case ev, ok := <-w.xev:
			if !ok {
				return
			}
			w.handle(ev)
		}
	}
}

// stop ends the loop. It runs on the loop, posted there by whoever wanted out.
func (w *window) stop() {
	select {
	case <-w.quit:
	default:
		close(w.quit)
	}
}

// serve is the handler goroutine: pop, call, repeat, until the handler returns
// an error or the queue closes.
func (w *window) serve(h gui.Handler) error {
	for {
		ev, ok := w.q.pop()
		if !ok {
			return nil
		}
		if err := h(w, ev); err != nil {
			return err
		}
	}
}

// handle turns one X event into whatever the editor is owed.
func (w *window) handle(ev xgb.Event) {
	switch e := ev.(type) {
	case xproto.KeyPressEvent:
		if k, ok := w.keymap.Key(byte(e.Detail), e.State); ok {
			w.q.push(gui.KeyEvent{Key: k})
		}

	case xproto.ButtonPressEvent:
		w.button(byte(e.Detail), e.State, int(e.EventX), int(e.EventY), true)
	case xproto.ButtonReleaseEvent:
		w.button(byte(e.Detail), e.State, int(e.EventX), int(e.EventY), false)
	case xproto.MotionNotifyEvent:
		w.motion(e.State, int(e.EventX), int(e.EventY))

	case xproto.ConfigureNotifyEvent:
		w.configured(int(e.Width), int(e.Height))
	case xproto.ExposeEvent:
		// Count is how many more Expose events are coming for this one
		// contiguous region. Repainting on each is correct and repainting on
		// the last is cheaper, so wait for the last.
		if e.Count == 0 {
			w.expose()
		}

	case xproto.FocusInEvent:
		w.q.push(gui.FocusEvent{Focused: true})
	case xproto.FocusOutEvent:
		w.q.push(gui.FocusEvent{Focused: false})

	case xproto.MappingNotifyEvent:
		if e.Request == xproto.MappingKeyboard || e.Request == xproto.MappingModifier {
			w.loadKeymap()
		}

	case xproto.ClientMessageEvent:
		if e.Type == w.atoms.wmProtocols && e.Format == 32 &&
			len(e.Data.Data32) > 0 && xproto.Atom(e.Data.Data32[0]) == w.atoms.wmDeleteWindow {
			// The close button. The editor decides whether to obey; a modified
			// buffer under 'confirm' prompts on the command line and may
			// refuse, which is why this is an event and not an exit.
			w.q.push(gui.CloseEvent{})
		}
	}
}

// button turns a ButtonPress or ButtonRelease into a mouse event.
//
// A wheel is buttons 4 to 7 and arrives as a press and a release of a button
// that does not exist; only the press is delivered, because a wheel notch has
// no duration and the editor would otherwise scroll twice per notch.
func (w *window) button(n byte, state uint16, x, y int, press bool) {
	row, col := w.cellAt(x, y)
	mod := w.keymap.Mod(state)

	if action, ok := wheelOf(n); ok {
		if press {
			w.q.push(gui.MouseEvent{Button: gui.MouseNone, Action: action, Row: row, Col: col, Mod: mod})
		}
		return
	}
	b, ok := buttonOf(n)
	if !ok {
		return
	}
	action := gui.MouseRelease
	if press {
		action = gui.MousePress
	}
	w.q.push(gui.MouseEvent{Button: b, Action: action, Row: row, Col: col, Mod: mod})
}

// The button bits in an event's state mask, which say which buttons were down
// when a motion happened. Buttons 4 and 5 have bits too and a motion with only
// those set is impossible, so they are not here.
const (
	button1Mask = 1 << 8
	button2Mask = 1 << 9
	button3Mask = 1 << 10
)

// motion turns a drag into a MouseDrag. Only motion with a button held is
// selected for, so there is no move-with-no-button to filter out.
func (w *window) motion(state uint16, x, y int) {
	var b gui.MouseButton
	switch {
	case state&button1Mask != 0:
		b = gui.MouseLeft
	case state&button2Mask != 0:
		b = gui.MouseMiddle
	case state&button3Mask != 0:
		b = gui.MouseRight
	default:
		return
	}
	row, col := w.cellAt(x, y)
	w.q.push(gui.MouseEvent{Button: b, Action: gui.MouseDrag, Row: row, Col: col, Mod: w.keymap.Mod(state)})
}

// cellAt is the window's own pixel-to-cell conversion, reading the metrics
// under the lock.
func (w *window) cellAt(x, y int) (row, col int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.face == nil {
		return 0, 0
	}
	m := w.face.Metrics()
	return cellAt(x, y, m.CellW, m.CellH+w.linespace, w.rows, w.cols)
}

// configured takes a new window size and tells the editor if it changed the
// grid.
func (w *window) configured(widthPx, heightPx int) {
	w.mu.Lock()
	w.widthPx, w.heightPx = widthPx, heightPx
	rows, cols := w.rows, w.cols
	if w.face != nil {
		m := w.face.Metrics()
		rows, cols = cellsFor(widthPx, heightPx, m.CellW, m.CellH+w.linespace)
	}
	changed := rows != w.rows || cols != w.cols
	w.rows, w.cols = rows, cols
	w.mu.Unlock()

	if changed {
		w.q.push(gui.ResizeEvent{Rows: rows, Cols: cols})
	}
}

// expose re-sends what the frame buffer already holds.
//
// It sends the whole painted area rather than the exposed rectangle. That is a
// deliberate simplification and it costs one full PutImage per uncovering: the
// exposed rectangle is in window coordinates and the buffer is in the same
// coordinates, so clipping would be arithmetic and not a difficulty, but an
// Expose arrives when a window is uncovered, dragged out from behind something
// or mapped, and none of those happen at a rate anybody can measure. What it
// buys is that there is exactly one path that puts a whole frame on screen.
func (w *window) expose() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.img == nil || w.paintedW == 0 || w.paintedH == 0 {
		return
	}
	w.putLocked(image.Rect(0, 0, w.paintedW, w.paintedH))
}

// Draw paints s into the frame buffer and sends the pixels that changed.
//
// It is called on the editor goroutine. The rasterising happens here rather
// than on the loop for the reason internal/gui gives: turning a grid into
// pixels is the expensive half of a frame and doing it on the loop would put
// the window's answers to the window manager behind the editor's slowest
// redraw.
func (w *window) Draw(s *screen.Screen) error {
	if s == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.face == nil {
		return fmt.Errorf("x11: Draw before the font was loaded")
	}

	opt := raster.PaintOptions{Face: w.face, Linespace: w.linespace}
	frame := raster.Frame{
		Grid:      &s.Grid,
		HL:        s.HL,
		CursorRow: s.CursorRow,
		CursorCol: s.CursorCol,
		Cursor:    rasterCursor(s.CursorShape),
	}
	pw, ph := raster.Size(frame, opt)
	w.ensureBuffer(pw, ph)

	spans, now, full := raster.Damage(w.painted, frame, opt)
	if err := raster.PaintRects(w.img, frame, opt, spans); err != nil {
		// The buffer now holds a half-painted frame and the only safe
		// description of it is "nothing that can be kept".
		w.painted = raster.Painted{}
		return err
	}
	w.painted = now
	w.paintedW, w.paintedH = pw, ph

	if bg := s.Look(screen.Normal).BG; !w.bgSet || w.bg != bg {
		w.bg, w.bgSet = bg, true
		w.setBackground(bg)
		full = true
	}

	if full {
		w.putLocked(image.Rect(0, 0, pw, ph))
	} else {
		m := w.face.Metrics()
		rowH := m.CellH + w.linespace
		for _, sp := range spans {
			// One cell of margin each side, because raster.PaintRects widens a
			// span onto the leading half of a double-width pair before it
			// paints and this has to cover what was actually painted rather
			// than what was asked for.
			r := spanRect(sp.Row, sp.Col-1, sp.Len+2, m.CellW, rowH, pw, ph)
			w.putLocked(r)
		}
	}
	w.clearMarginsLocked(pw, ph)
	return nil
}

// ensureBuffer grows the frame buffer to hold a pw by ph frame. Caller holds
// w.mu.
//
// It only ever grows, and a growth throws away what the old buffer held, which
// is what makes the first frame after a resize a full repaint.
func (w *window) ensureBuffer(pw, ph int) {
	if w.img != nil {
		b := w.img.Bounds()
		if b.Dx() >= pw && b.Dy() >= ph {
			return
		}
		if b.Dx() > pw {
			pw = b.Dx()
		}
		if b.Dy() > ph {
			ph = b.Dy()
		}
	}
	pw, ph = roundUp(pw, bufferStep), roundUp(ph, bufferStep)
	w.img = image.NewRGBA(image.Rect(0, 0, pw, ph))
	w.painted = raster.Painted{}
	w.paintedW, w.paintedH = 0, 0
}

// roundUp returns n rounded up to a multiple of step.
func roundUp(n, step int) int {
	if n <= 0 {
		return step
	}
	return ((n + step - 1) / step) * step
}

// putLocked sends one rectangle of the frame buffer to the server, in as many
// PutImage requests as the maximum request length demands. Caller holds w.mu.
func (w *window) putLocked(r image.Rectangle) {
	if w.img == nil {
		return
	}
	r = r.Intersect(w.img.Bounds())
	if r.Empty() {
		return
	}
	for _, c := range chunkRect(r, w.maxImage) {
		n := c.Dx() * c.Dy() * 4
		if cap(w.wire) < n {
			w.wire = make([]byte, n)
		}
		buf := w.wire[:n]
		if err := convert(buf, w.img, c, w.format); err != nil {
			return
		}
		xproto.PutImage(w.conn, xproto.ImageFormatZPixmap, xproto.Drawable(w.win), w.gc,
			uint16(c.Dx()), uint16(c.Dy()), int16(c.Min.X), int16(c.Min.Y),
			0, w.format.depth, buf)
	}
}

// clearMarginsLocked paints the strip of window that no whole cell reaches.
//
// A window is rarely an exact multiple of the cell size, so there is a bar up
// to one cell wide down the right and one row tall along the bottom that the
// frame does not cover. ClearArea fills it with the window's background pixel,
// which setBackground keeps equal to the colourscheme's Normal background, so
// the strip is the same colour as the text area instead of whatever was on the
// screen before this window was mapped. Caller holds w.mu.
func (w *window) clearMarginsLocked(pw, ph int) {
	if w.widthPx > pw {
		xproto.ClearArea(w.conn, false, w.win, int16(pw), 0, uint16(w.widthPx-pw), uint16(w.heightPx))
	}
	if w.heightPx > ph {
		xproto.ClearArea(w.conn, false, w.win, 0, int16(ph), uint16(pw), uint16(w.heightPx-ph))
	}
}

// setBackground changes the window's background pixel, which is what the server
// fills with on a resize and what ClearArea paints the margins in. Caller holds
// w.mu.
func (w *window) setBackground(bg screen.RGB) {
	xproto.ChangeWindowAttributes(w.conn, w.win, xproto.CwBackPixel,
		[]uint32{pixelOf(w.format, bg)})
}

// pixelOf turns a colourscheme colour into one pixel value in the server's
// visual, for the two places that need a colour and not an image: the window's
// background attribute and the margins.
func pixelOf(f pixelFormat, c screen.RGB) uint32 {
	rs, rw := shiftOf(f.redMask)
	gs, gw := shiftOf(f.greenMask)
	bs, bw := shiftOf(f.blueMask)
	return scaleComponent(c.R, rw)<<rs | scaleComponent(c.G, gw)<<gs | scaleComponent(c.B, bw)<<bs
}

// Size returns the drawable size in cells.
func (w *window) Size() (rows, cols int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rows, w.cols
}

// SetTitle sets the window title. The property write goes on the loop, which is
// the goroutine that owns the reply side of this connection.
func (w *window) SetTitle(title string) error {
	return postMain(func() { w.setName(title) })
}

// Bell does nothing, which is what the vimrc's `set visualbell t_vb=` asks for
// and the cheapest feature in this editor.
func (w *window) Bell() {}

// onMain is the linux implementation of OnMain.
func onMain(fn func()) error {
	w := current
	if w == nil {
		return ErrNoEventLoop
	}
	done := make(chan struct{})
	if err := w.post(func() {
		defer close(done)
		fn()
	}); err != nil {
		return err
	}
	select {
	case <-done:
		return nil
	case <-w.dead:
		return ErrNoEventLoop
	}
}

// postMain is the linux implementation of PostMain.
func postMain(fn func()) error {
	w := current
	if w == nil {
		return ErrNoEventLoop
	}
	return w.post(fn)
}

// post queues work for the loop, refusing rather than blocking once the loop is
// gone.
//
// The queue is buffered and the select falls through to a goroutine when it is
// full, rather than blocking the caller: the caller is usually the editor and
// the editor blocking on the window is the thing this whole arrangement exists
// to prevent.
func (w *window) post(fn func()) error {
	select {
	case <-w.dead:
		return ErrNoEventLoop
	default:
	}
	select {
	case w.tasks <- fn:
		return nil
	case <-w.dead:
		return ErrNoEventLoop
	default:
	}
	go func() {
		select {
		case w.tasks <- fn:
		case <-w.dead:
		}
	}()
	return nil
}

// setFont is the linux implementation of SetFont.
//
// It builds the face on the calling goroutine, which is the editor's, because
// that is where the font file is read; only the relayout crosses to the loop. A
// font that cannot be loaded leaves the window in the one it has.
func setFont(f raster.GUIFont) error {
	w := current
	if w == nil {
		return ErrNoBackend
	}
	w.mu.Lock()
	scale := w.scale
	w.mu.Unlock()

	face, got, err := ResolveFont(f, scale)
	if err != nil {
		return err
	}

	w.mu.Lock()
	w.face, w.font = face, got
	w.painted = raster.Painted{}
	rows, cols := w.relayoutLocked()
	w.mu.Unlock()

	_ = postMain(w.setSizeHints)
	// A ResizeEvent even when the cell count did not change: a window whose
	// font changed has to be repainted whatever its size did, and this is the
	// event the editor repaints on.
	w.q.push(gui.ResizeEvent{Rows: rows, Cols: cols})
	return nil
}

// setLinespace is the linux implementation of SetLinespace.
func setLinespace(n int) error {
	w := current
	if w == nil {
		return ErrNoBackend
	}
	if n < 0 {
		n = 0
	}
	w.mu.Lock()
	w.linespace = n
	w.painted = raster.Painted{}
	rows, cols := w.relayoutLocked()
	w.mu.Unlock()

	_ = postMain(w.setSizeHints)
	w.q.push(gui.ResizeEvent{Rows: rows, Cols: cols})
	return nil
}

// inForce is the linux implementation of InForce.
func inForce() (raster.GUIFont, bool) {
	w := current
	if w == nil {
		return raster.GUIFont{}, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.face == nil {
		return raster.GUIFont{}, false
	}
	return w.font, true
}

// relayoutLocked recomputes the cell count for the window's current pixel size.
// Caller holds w.mu.
func (w *window) relayoutLocked() (rows, cols int) {
	if w.face == nil {
		return w.rows, w.cols
	}
	m := w.face.Metrics()
	w.rows, w.cols = cellsFor(w.widthPx, w.heightPx, m.CellW, m.CellH+w.linespace)
	return w.rows, w.cols
}
