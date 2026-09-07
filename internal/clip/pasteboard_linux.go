//go:build linux

package clip

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/pkar/pvim/internal/register"
)

// The X11 selections: "* is PRIMARY, "+ is CLIPBOARD, and unlike macOS those
// really are two different things.
//
// # Why this opens its own connection instead of using the window's
//
// internal/gui/x11 has an X connection, an event loop and a window, and every
// one of those is what a selection owner needs. Using them would mean this
// package importing that one, and internal/deps_test.go's TestPhase4Layering
// says internal/clip may import internal/register and internal/text and nothing
// else in this module. That rule is not in the way here, it is the point: the
// same rule on darwin is why this package talks to NSPasteboard through purego
// itself rather than through internal/gui, and the arrangement it produces is
// better than the one it forbids. A clipboard with its own connection works
// with no window at all -- on a box running the terminal frontend under a
// desktop session, where "+y should still reach Firefox -- and it cannot wedge
// the window's event loop, because the two loops are separate.
//
// The cost is one more X client per pvim, which is a socket and a goroutine.
//
// # Why a selection is a conversation and not a system call
//
// There is no clipboard on X11. There is a per-selection register in the server
// holding the window id of whoever last claimed it, and the actual text lives
// in the claiming client's memory. A paste is: ask the server who owns
// CLIPBOARD, ask that client to convert the selection into a target you name,
// wait for it to write the answer into a property on your own window and send
// you a SelectionNotify saying it did, then read the property. A copy is:
// claim the selection, and then answer everybody who asks, for as long as you
// hold it.
//
// That last clause is why the file this replaces refused to be a stub. A client
// that claims a selection and then does not answer a SelectionRequest leaves
// the requesting application blocked on a reply that will never come -- vim's
// own X clipboard code waits with a timeout, and several toolkits do not -- so
// half a selection owner is worse than none. Everything below is there to make
// the answering side complete: TARGETS so a requestor can ask what is on
// offer, TIMESTAMP because the ICCCM requires it of an owner, a refusal with
// property None for a target not on offer, and INCR for anything too large for
// one property.
//
// # What is not here
//
// No MULTIPLE target, which lets a requestor ask for several conversions in one
// request. It is optional, nothing this editor talks to uses it, and a
// requestor that asks gets the same clean refusal as any other unknown target.
// No selection ownership across process exit: an X selection dies with the
// client that holds it, so a yank in pvim is not pastable after pvim quits.
// That is X11 and not this package -- it is why desktops ship clipboard
// managers -- and every X application behaves the same way.

// The selection read timeout.
//
// A paste asks another process to do something and that process may be wedged,
// swapped out, or in a modal dialog of its own. Two seconds is what vim uses
// before it gives up on a selection request, and giving up has to be a real
// outcome rather than a hang: an editor whose "+p never returns is an editor
// that has to be killed.
const selectionTimeout = 2 * time.Second

// New returns the X11 clipboard: a Clipboard whose Write claims both PRIMARY
// and CLIPBOARD and whose Read prefers CLIPBOARD.
//
// The run argument is ignored here, and that is a difference from darwin worth
// stating rather than hiding. On macOS the pasteboard must be touched from the
// thread internal/gui owns, so cmd/pvim passes gui.OnMain and this package
// borrows it. Here the connection is this package's own and so is the goroutine
// that owns it, so there is no thread to borrow and nothing to pass. A caller
// that passes one is not wrong, it is just handing over something unneeded, and
// refusing it would make cmd/pvim's two platforms differ for no reason.
//
// It fails with ErrUnsupported on a box with no DISPLAY, which is a Linux
// machine with no desktop session: over ssh, on a build box, in a container.
// cmd/pvim installs no clipboard then and "* and "+ are ordinary registers,
// which is a working editor with no desktop integration rather than a broken
// one.
func New(run Runner) (Clipboard, error) {
	c, err := newX11()
	if err != nil {
		return nil, err
	}
	return bothSelections{c: c}, nil
}

// NewSelections returns the two selections separately: star is PRIMARY, plus is
// CLIPBOARD, exactly as vim's "* and "+ are on X11.
//
// It exists because register.Clipboard is a single Read and a single Write,
// which is the right interface for macOS -- where "* and "+ are one pasteboard
// and writing twice would be two trips to the main thread for the same bytes --
// and cannot express X11, where a middle-click paste and a Ctrl-V paste come
// from two different places. Until internal/register grows a per-selection
// seam, New is what cmd/pvim installs and this is what it will install when it
// does. Both share one connection and one event loop.
func NewSelections(run Runner) (star, plus Clipboard, err error) {
	c, err := newX11()
	if err != nil {
		return nil, nil, err
	}
	return oneSelection{c: c, sel: xproto.AtomPrimary}, oneSelection{c: c, sel: c.a.clipboard}, nil
}

// bothSelections is the Clipboard register.Clipboard's shape asks for: one
// Read and one Write over two selections.
//
// Write claims both, which is what vim does with 'clipboard' set to
// "unnamed,unnamedplus" and what makes a yank in pvim pastable with a middle
// click and with Ctrl-V. Read prefers CLIPBOARD and falls back to PRIMARY when
// nothing owns CLIPBOARD, which is the order that makes "+p work after a copy
// in a browser and still finds a middle-click selection on a desktop where
// nothing has ever put anything on CLIPBOARD.
type bothSelections struct{ c *x11Clipboard }

func (b bothSelections) Read() (register.Value, error) {
	v, err := b.c.read(b.c.a.clipboard)
	if err != nil || !v.Empty() {
		return v, err
	}
	return b.c.read(xproto.AtomPrimary)
}

func (b bothSelections) Write(v register.Value) error {
	if err := b.c.write(b.c.a.clipboard, v); err != nil {
		return err
	}
	return b.c.write(xproto.AtomPrimary, v)
}

// oneSelection is one selection on its own, which is what NewSelections hands
// back.
type oneSelection struct {
	c   *x11Clipboard
	sel xproto.Atom
}

func (o oneSelection) Read() (register.Value, error) { return o.c.read(o.sel) }
func (o oneSelection) Write(v register.Value) error  { return o.c.write(o.sel, v) }

// atoms are the atoms a selection owner and a selection requestor need. Every
// one is interned once at startup and none of them is a round trip afterwards.
type atoms struct {
	clipboard  xproto.Atom
	targets    xproto.Atom
	timestamp  xproto.Atom
	incr       xproto.Atom
	utf8String xproto.Atom
	textPlain  xproto.Atom
	text       xproto.Atom
	vimText    xproto.Atom
	vimEncText xproto.Atom

	// prop is where an incoming conversion is asked to land, on our own
	// window. It is a name of our own rather than a standard one because the
	// property is ours and two conversions must not collide, which is also why
	// there is only ever one read in flight.
	prop xproto.Atom
	// timeProp is the property a zero-length append is made to at startup, to
	// get a real server timestamp out of the PropertyNotify it produces.
	timeProp xproto.Atom
}

// owned is a selection this process holds: the bytes for each target it offers
// and the timestamp it claimed the selection at.
type owned struct {
	data map[xproto.Atom][]byte
	time xproto.Timestamp
	// value is what was written, kept so that reading a selection we own
	// answers from memory instead of asking ourselves over the socket.
	value register.Value
}

// incrKey identifies one INCR transfer in progress: the requestor's window and
// the property it is being fed through.
type incrKey struct {
	win  xproto.Window
	prop xproto.Atom
}

// incrSend is one INCR transfer this process is the sender of.
type incrSend struct {
	target xproto.Atom
	data   []byte
	sizes  []int
	at     int
	off    int
}

// readState is the one conversion this process has in flight.
//
// One, and not a map keyed by anything: a read is a person pressing p and there
// is never a second one until the first has answered or timed out. Serialising
// them is what lets the property be a single named property rather than a pool.
type readState struct {
	sel     xproto.Atom
	targets []xproto.Atom
	at      int
	incr    bool
	buf     []byte
	out     chan readResult
}

// readResult is what a read hands back to the goroutine waiting on it.
type readResult struct {
	v   register.Value
	err error
}

// x11Clipboard is the connection, the window nobody sees, and the goroutine
// that owns both.
type x11Clipboard struct {
	conn *xgb.Conn
	win  xproto.Window
	a    atoms

	// maxProp is how many bytes one ChangeProperty may carry on this
	// connection, which is what decides when a transfer becomes an INCR.
	maxProp int

	reqs chan func()
	xev  chan xgb.Event
	dead chan struct{}
	once sync.Once

	// Everything below is owned by the loop goroutine and touched by nothing
	// else. There is no mutex in this type on purpose: a lock would be a
	// second way to reach this state and the whole design is that there is
	// one.
	own   map[xproto.Atom]*owned
	sends map[incrKey]*incrSend
	read1 *readState
	now   xproto.Timestamp
}

// newX11 opens the connection, makes the window, interns the atoms and starts
// the loop.
func newX11() (*x11Clipboard, error) {
	if os.Getenv("DISPLAY") == "" {
		return nil, ErrUnsupported
	}
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	setup := xproto.Setup(conn)
	if setup == nil || len(setup.Roots) == 0 {
		conn.Close()
		return nil, fmt.Errorf("%w: the server sent no screens", ErrUnsupported)
	}
	sc := setup.Roots[conn.DefaultScreen]

	c := &x11Clipboard{
		conn:    conn,
		maxProp: maxPropBytes(setup.MaximumRequestLength),
		reqs:    make(chan func(), 16),
		xev:     make(chan xgb.Event, 64),
		dead:    make(chan struct{}),
		own:     map[xproto.Atom]*owned{},
		sends:   map[incrKey]*incrSend{},
	}

	// An InputOnly window, 1x1, never mapped. A selection owner has to be a
	// window and this is the smallest one that can be: it is never on screen,
	// it draws nothing, and it exists only to be an address the server can
	// route SelectionRequest events to.
	id, err := xproto.NewWindowId(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: no window id: %v", ErrUnsupported, err)
	}
	c.win = id
	const inputOnly = 2
	err = xproto.CreateWindowChecked(conn, 0, id, sc.Root, 0, 0, 1, 1, 0,
		inputOnly, 0, xproto.CwEventMask,
		[]uint32{uint32(xproto.EventMaskPropertyChange)}).Check()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: creating the selection window: %v", ErrUnsupported, err)
	}

	if err := c.internAtoms(); err != nil {
		conn.Close()
		return nil, err
	}
	c.now = c.serverTime()

	go c.readEvents()
	go c.loop()
	return c, nil
}

// maxPropBytes is how many bytes of data one ChangeProperty may carry, given
// the server's maximum-request-length in four-byte units.
//
// The header is 24 bytes, the same shape as PutImage's, and the same
// truncation waits for a caller that ignores it: xgb writes the request length
// into a uint16 and does not check it, so an over-long ChangeProperty corrupts
// the connection rather than failing. The result is floored to a multiple of
// four because a request's length is in four-byte units.
func maxPropBytes(maxRequestLen uint16) int {
	n := int(maxRequestLen)*4 - 24
	if n < 0 {
		return 0
	}
	return n &^ 3
}

// internAtoms interns every atom this package uses.
func (c *x11Clipboard) internAtoms() error {
	get := func(name string) (xproto.Atom, error) {
		r, err := xproto.InternAtom(c.conn, false, uint16(len(name)), name).Reply()
		if err != nil {
			return 0, fmt.Errorf("%w: interning %s: %v", ErrUnsupported, name, err)
		}
		return r.Atom, nil
	}
	names := []struct {
		name string
		dst  *xproto.Atom
	}{
		{"CLIPBOARD", &c.a.clipboard},
		{"TARGETS", &c.a.targets},
		{"TIMESTAMP", &c.a.timestamp},
		{"INCR", &c.a.incr},
		{"UTF8_STRING", &c.a.utf8String},
		{"text/plain;charset=utf-8", &c.a.textPlain},
		{"TEXT", &c.a.text},
		{vimTextTarget, &c.a.vimText},
		{vimEncTextTarget, &c.a.vimEncText},
		{"PVIM_SELECTION", &c.a.prop},
		{"PVIM_TIME", &c.a.timeProp},
	}
	for _, n := range names {
		a, err := get(n.name)
		if err != nil {
			return err
		}
		*n.dst = a
	}
	return nil
}

// serverTime returns a timestamp from the server, which is what SetSelectionOwner
// needs and what CurrentTime is explicitly not.
//
// The ICCCM forbids CurrentTime in SetSelectionOwner, because a selection is
// arbitrated by timestamp and a client claiming with CurrentTime cannot lose a
// race it should have lost. The way every toolkit gets a real one is this: make
// a zero-length append to a property on your own window, which changes nothing
// and produces a PropertyNotify, and take the timestamp off that event.
//
// This runs before the loop goroutine starts, so it may read events straight
// off the connection. Anything that is not the PropertyNotify it is waiting for
// is dropped, which is safe because nothing has been claimed or requested yet
// and there is nothing else this window could be receiving. A server that never
// answers leaves the timestamp at CurrentTime, which is worse than a real one
// and better than not having a clipboard.
func (c *x11Clipboard) serverTime() xproto.Timestamp {
	const appendMode = 2 // PropModeAppend
	xproto.ChangeProperty(c.conn, appendMode, c.win, c.a.timeProp, xproto.AtomString, 8, 0, nil)
	for i := 0; i < 16; i++ {
		ev, err := c.conn.WaitForEvent()
		if ev == nil && err == nil {
			return xproto.TimeCurrentTime
		}
		if p, ok := ev.(xproto.PropertyNotifyEvent); ok && p.Atom == c.a.timeProp {
			xproto.DeleteProperty(c.conn, c.win, c.a.timeProp)
			return p.Time
		}
	}
	return xproto.TimeCurrentTime
}

// readEvents is the reader goroutine: xgb's WaitForEvent cannot be selected
// against anything, so it gets a goroutine and a channel of its own.
func (c *x11Clipboard) readEvents() {
	defer close(c.xev)
	for {
		ev, err := c.conn.WaitForEvent()
		if ev == nil && err == nil {
			return // the connection closed
		}
		if ev == nil {
			continue // a protocol error with nothing to deliver
		}
		select {
		case c.xev <- ev:
		case <-c.dead:
			return
		}
	}
}

// loop owns every field below the comment in x11Clipboard and every request
// this package sends after startup.
func (c *x11Clipboard) loop() {
	for {
		select {
		case <-c.dead:
			return
		case fn := <-c.reqs:
			fn()
		case ev, ok := <-c.xev:
			if !ok {
				c.fail(ErrUnsupported)
				return
			}
			c.handle(ev)
		}
	}
}

// close stops the loop and the connection. Nothing in the editor calls it: a
// clipboard lives as long as the process. It is here for tests and for a
// shutdown path that may one day want it.
func (c *x11Clipboard) close() {
	c.once.Do(func() {
		close(c.dead)
		c.conn.Close()
	})
}

// fail answers a read in flight when the connection has gone, so that a paste
// during shutdown returns rather than waiting out its timeout.
func (c *x11Clipboard) fail(err error) {
	if c.read1 != nil {
		c.read1.out <- readResult{err: err}
		c.read1 = nil
	}
}

// post queues work for the loop, refusing rather than blocking once it is gone.
func (c *x11Clipboard) post(fn func()) error {
	select {
	case <-c.dead:
		return ErrUnsupported
	default:
	}
	select {
	case c.reqs <- fn:
		return nil
	case <-c.dead:
		return ErrUnsupported
	}
}

// handle is the whole event loop body.
func (c *x11Clipboard) handle(ev xgb.Event) {
	switch e := ev.(type) {
	case xproto.SelectionRequestEvent:
		c.at(e.Time)
		c.answer(e)
	case xproto.SelectionClearEvent:
		c.at(e.Time)
		// Somebody else claimed it. Dropping the data is not tidiness: holding
		// it would mean answering requests for a selection this process no
		// longer owns, and the server would not route them here anyway.
		delete(c.own, e.Selection)
	case xproto.SelectionNotifyEvent:
		c.at(e.Time)
		c.notified(e)
	case xproto.PropertyNotifyEvent:
		c.at(e.Time)
		c.property(e)
	}
}

// at keeps the last server timestamp seen, which is what a selection claim uses.
func (c *x11Clipboard) at(t xproto.Timestamp) {
	if t != xproto.TimeCurrentTime {
		c.now = t
	}
}

// answer replies to one SelectionRequest.
//
// The reply is always a SelectionNotify, even when the answer is no: a
// requestor is waiting and the ICCCM's way of saying no is a SelectionNotify
// whose property is None. A requestor left waiting is the failure this whole
// file exists to avoid.
func (c *x11Clipboard) answer(e xproto.SelectionRequestEvent) {
	prop := e.Property
	if prop == 0 {
		// An obsolete requestor, from before the ICCCM: the property to use is
		// the target's own name. Answering it costs nothing and refusing it
		// would leave a very old application unable to paste.
		prop = e.Target
	}
	if !c.convert(e, prop) {
		prop = 0
	}
	notify := xproto.SelectionNotifyEvent{
		Time:      e.Time,
		Requestor: e.Requestor,
		Selection: e.Selection,
		Target:    e.Target,
		Property:  prop,
	}
	// Event mask zero, per the ICCCM: this event goes to the one client that
	// asked and not to whoever happens to have selected for it.
	xproto.SendEvent(c.conn, false, e.Requestor, 0, string(notify.Bytes()))
}

// convert writes the answer to one request into the requestor's property, and
// says whether there was an answer at all.
func (c *x11Clipboard) convert(e xproto.SelectionRequestEvent, prop xproto.Atom) bool {
	own := c.own[e.Selection]
	if own == nil {
		return false
	}

	switch e.Target {
	case c.a.targets:
		list := c.targetList()
		data := make([]byte, 4*len(list))
		for i, a := range list {
			xgb.Put32(data[i*4:], uint32(a))
		}
		xproto.ChangeProperty(c.conn, xproto.PropModeReplace, e.Requestor, prop,
			xproto.AtomAtom, 32, uint32(len(list)), data)
		return true

	case c.a.timestamp:
		// The ICCCM requires an owner to answer TIMESTAMP with the time it
		// acquired the selection. A clipboard manager asks for it to decide
		// whether what it has cached is still current.
		data := make([]byte, 4)
		xgb.Put32(data, uint32(own.time))
		xproto.ChangeProperty(c.conn, xproto.PropModeReplace, e.Requestor, prop,
			xproto.AtomInteger, 32, 1, data)
		return true
	}

	data, ok := own.data[e.Target]
	if !ok {
		return false
	}
	if len(data) <= c.maxProp {
		xproto.ChangeProperty(c.conn, xproto.PropModeReplace, e.Requestor, prop,
			e.Target, 8, uint32(len(data)), data)
		return true
	}
	return c.startINCR(e, prop, data)
}

// startINCR begins a large transfer.
//
// The property is written as type INCR holding the total size, the requestor is
// watched for it deleting that property, and each deletion is answered with the
// next chunk until a zero-length one ends it. The requestor learns it is an
// INCR transfer by reading the property type, which is why the type matters
// here and the value is only a hint.
func (c *x11Clipboard) startINCR(e xproto.SelectionRequestEvent, prop xproto.Atom, data []byte) bool {
	sizes := chunkSizes(len(data), c.maxProp)
	if sizes == nil {
		return false
	}
	total := make([]byte, 4)
	xgb.Put32(total, uint32(len(data)))

	// The requestor's event mask has to include PropertyChange or its
	// deletions never reach us. An event mask is per client, so setting ours
	// on somebody else's window does not disturb what they selected for.
	xproto.ChangeWindowAttributes(c.conn, e.Requestor, xproto.CwEventMask,
		[]uint32{uint32(xproto.EventMaskPropertyChange)})
	xproto.ChangeProperty(c.conn, xproto.PropModeReplace, e.Requestor, prop,
		c.a.incr, 32, 1, total)

	c.sends[incrKey{win: e.Requestor, prop: prop}] = &incrSend{
		target: e.Target,
		data:   data,
		sizes:  sizes,
	}
	return true
}

// property handles a PropertyNotify, which means one of two things and never
// both: a requestor we are feeding has taken a chunk, or a selection we are
// reading has given us one.
func (c *x11Clipboard) property(e xproto.PropertyNotifyEvent) {
	const (
		newValue = 0
		deleted  = 1
	)
	if e.State == deleted {
		c.sendNext(incrKey{win: e.Window, prop: e.Atom})
		return
	}
	if e.State == newValue && e.Window == c.win && e.Atom == c.a.prop &&
		c.read1 != nil && c.read1.incr {
		c.incrChunk()
	}
}

// sendNext writes the next chunk of an INCR transfer, or ends it.
func (c *x11Clipboard) sendNext(k incrKey) {
	s := c.sends[k]
	if s == nil {
		return
	}
	n := s.sizes[s.at]
	chunk := s.data[s.off : s.off+n]
	xproto.ChangeProperty(c.conn, xproto.PropModeReplace, k.win, k.prop,
		s.target, 8, uint32(n), chunk)
	s.off += n
	s.at++
	if s.at >= len(s.sizes) {
		// The zero-length write has gone out, so the transfer is over. The
		// requestor's event mask is left alone: another transfer to the same
		// window is about to want it, and clearing it would be this process
		// deciding what events somebody else's window gets.
		delete(c.sends, k)
	}
}

// read is the paste side, called from the editor goroutine.
//
// It waits on a channel with a timeout rather than on the loop, so the loop
// stays free to run the conversation the answer is coming out of.
func (c *x11Clipboard) read(sel xproto.Atom) (register.Value, error) {
	out := make(chan readResult, 1)
	if err := c.post(func() { c.startRead(sel, out) }); err != nil {
		return register.Value{}, err
	}
	select {
	case r := <-out:
		return r.v, r.err
	case <-time.After(selectionTimeout):
		// Clear the read on the loop rather than here, because the loop owns
		// it. A late answer then finds no read in flight and is dropped, which
		// is what should happen to an answer nobody is waiting for.
		_ = c.post(func() {
			if c.read1 != nil && c.read1.out == out {
				c.read1 = nil
			}
		})
		return register.Value{}, fmt.Errorf("clip: the owner of the selection did not answer within %s", selectionTimeout)
	}
}

// startRead begins a conversion. Loop only.
func (c *x11Clipboard) startRead(sel xproto.Atom, out chan readResult) {
	if own := c.own[sel]; own != nil {
		// We own it. Asking the server to ask us is a legal round trip and a
		// pointless one, and answering from memory is also what keeps a
		// blockwise value's exact width rather than the one recomputed from
		// the text.
		out <- readResult{v: clone(own.value)}
		return
	}
	owner, err := xproto.GetSelectionOwner(c.conn, sel).Reply()
	if err != nil || owner == nil || owner.Owner == 0 {
		// Nothing owns the selection, which is not an error: it is a desktop
		// where nothing has been copied yet, and vim reads it as an empty
		// register.
		out <- readResult{}
		return
	}
	if c.read1 != nil {
		// A previous read is still in flight. It cannot be, in an editor with
		// one goroutine doing the pasting, but a second one would collide over
		// the single property, so the new one loses rather than corrupting the
		// old one.
		out <- readResult{err: errors.New("clip: a selection read is already in flight")}
		return
	}
	c.read1 = &readState{sel: sel, targets: c.readTargets(), out: out}
	c.convertNext()
}

// readTargets is the order this package asks for a selection in.
//
// vim's own two first, because they are the only ones that carry whether the
// text was yanked linewise, and a linewise paste that arrives charwise is the
// most visible thing a clipboard can get wrong. Then UTF8_STRING, which every
// application written this century offers. Then STRING, which is Latin-1 and is
// what an application too old for UTF8_STRING has.
//
// TEXT is not in the list. It means "whatever encoding you like, tell me which"
// and the answer comes back as a target atom this would then have to interpret,
// which is a charset table this package does not have. Every owner that offers
// TEXT offers STRING as well.
func (c *x11Clipboard) readTargets() []xproto.Atom {
	return []xproto.Atom{
		c.a.vimEncText,
		c.a.vimText,
		c.a.utf8String,
		xproto.AtomString,
	}
}

// convertNext asks for the current target, or gives up. Loop only.
func (c *x11Clipboard) convertNext() {
	r := c.read1
	if r.at >= len(r.targets) {
		// Every target refused. The selection exists and holds nothing this
		// editor can read, which reads as an empty register rather than as an
		// error: it is a copy of an image or a file list, and vim pastes
		// nothing for those too.
		r.out <- readResult{}
		c.read1 = nil
		return
	}
	r.incr, r.buf = false, nil
	xproto.DeleteProperty(c.conn, c.win, c.a.prop)
	xproto.ConvertSelection(c.conn, c.win, r.sel, r.targets[r.at], c.a.prop, c.now)
}

// notified handles the SelectionNotify that answers a conversion. Loop only.
func (c *x11Clipboard) notified(e xproto.SelectionNotifyEvent) {
	r := c.read1
	if r == nil || e.Requestor != c.win || e.Selection != r.sel {
		return
	}
	if e.Property == 0 {
		// The owner refused this target. Try the next one.
		r.at++
		c.convertNext()
		return
	}
	data, typ, ok := c.takeProperty()
	if !ok {
		r.at++
		c.convertNext()
		return
	}
	if typ == c.a.incr {
		// The property held the size and not the data, and taking it deleted
		// it, which is what tells a user to send the first chunk.
		r.incr = true
		r.buf = nil
		return
	}
	c.finish(data)
}

// incrChunk takes one chunk of an INCR transfer. Loop only.
func (c *x11Clipboard) incrChunk() {
	r := c.read1
	data, _, ok := c.takeProperty()
	if !ok {
		r.at++
		c.convertNext()
		return
	}
	if len(data) == 0 {
		// The zero-length write that ends a transfer.
		c.finish(r.buf)
		return
	}
	r.buf = append(r.buf, data...)
}

// finish decodes what arrived and answers the waiting read. Loop only.
func (c *x11Clipboard) finish(data []byte) {
	r := c.read1
	v, ok := c.decode(r.targets[r.at], data)
	if !ok {
		r.at++
		c.convertNext()
		return
	}
	r.out <- readResult{v: v}
	c.read1 = nil
}

// takeProperty reads the whole of our own property and deletes it.
//
// Two requests and not one. A GetProperty has to be told how much to read, and
// the only way to find out is to ask for nothing and read bytes-after off the
// reply. Deleting on the second is what an INCR transfer needs: the deletion is
// the acknowledgement that asks for the next chunk.
func (c *x11Clipboard) takeProperty() (data []byte, typ xproto.Atom, ok bool) {
	head, err := xproto.GetProperty(c.conn, false, c.win, c.a.prop,
		xproto.GetPropertyTypeAny, 0, 0).Reply()
	if err != nil || head == nil {
		return nil, 0, false
	}
	words := (head.BytesAfter + 3) / 4
	body, err := xproto.GetProperty(c.conn, true, c.win, c.a.prop,
		xproto.GetPropertyTypeAny, 0, words).Reply()
	if err != nil || body == nil {
		return nil, 0, false
	}
	return body.Value, body.Type, true
}

// decode turns the bytes of one target into a register value.
func (c *x11Clipboard) decode(target xproto.Atom, data []byte) (register.Value, bool) {
	switch target {
	case c.a.vimEncText:
		return decodeVimEncText(data)
	case c.a.vimText:
		return decodeVimText(data)
	case c.a.utf8String, c.a.textPlain:
		if !utf8.Valid(data) {
			// A UTF8_STRING that is not UTF-8 is a broken owner. Falling
			// through to STRING reads the same bytes as Latin-1, which is at
			// least a defined answer.
			return register.Value{}, false
		}
		return ValueOf(data), true
	case xproto.AtomString:
		return ValueOf(latin1ToUTF8(data)), true
	}
	return register.Value{}, false
}

// write is the yank side, called from the editor goroutine.
//
// The UTF-8 check happens here, before anything is posted, for the reason the
// darwin half gives about NSString: a value that cannot go on the selection
// must not also destroy what is on it. pvim's buffer is bytes and can hold what
// vim's cannot, so this is reachable in a way it is not in vim.
func (c *x11Clipboard) write(sel xproto.Atom, v register.Value) error {
	text := bytesFor(v)
	if !utf8.Valid(text) {
		return ErrNotUTF8
	}
	data := c.offer(v, text)

	// The claim is made on the loop, because c.own is the loop's; the wait for
	// the server to acknowledge it happens here. Checking a cookie blocks
	// until the server answers, and blocking the loop on that would stop it
	// answering the SelectionRequest that a fast requestor sends the instant
	// the claim lands.
	claimed := make(chan xproto.SetSelectionOwnerCookie, 1)
	if err := c.post(func() {
		c.own[sel] = &owned{data: data, time: c.now, value: clone(v)}
		claimed <- xproto.SetSelectionOwnerChecked(c.conn, c.win, sel, c.now)
	}); err != nil {
		return err
	}
	select {
	case cookie := <-claimed:
		return cookie.Check()
	case <-c.dead:
		return ErrUnsupported
	}
}

// offer builds the target-to-bytes table this process will answer requests
// from.
//
// STRING is offered only when the text fits in Latin-1, because the ICCCM says
// STRING is Latin-1 and handing UTF-8 bytes over it is what makes an accented
// character arrive in another application as two. A requestor that asked for
// STRING and is refused asks for something else, which is what the protocol is
// for.
func (c *x11Clipboard) offer(v register.Value, text []byte) map[xproto.Atom][]byte {
	data := map[xproto.Atom][]byte{
		c.a.utf8String: text,
		c.a.textPlain:  text,
		c.a.text:       text,
		c.a.vimText:    encodeVimText(v),
		c.a.vimEncText: encodeVimEncText(v),
	}
	if latin1, ok := utf8ToLatin1(text); ok {
		data[xproto.AtomString] = latin1
	}
	return data
}

// targetList is what a TARGETS request is answered with: every target this
// process will convert to, in the order a requestor should prefer them.
//
// TARGETS and TIMESTAMP are in it because the ICCCM says an owner supports
// both and a requestor is entitled to see them listed. STRING is in it
// unconditionally even though offer may not have produced it, which is the one
// place this list is a promise rather than a fact: a requestor that asks for a
// STRING this text cannot be spelled in gets a clean refusal, which is a case
// the protocol handles and the alternative -- a TARGETS list that changes with
// the content -- is one that confuses caching clipboard managers.
func (c *x11Clipboard) targetList() []xproto.Atom {
	return []xproto.Atom{
		c.a.targets,
		c.a.timestamp,
		c.a.vimEncText,
		c.a.vimText,
		c.a.utf8String,
		c.a.textPlain,
		c.a.text,
		xproto.AtomString,
	}
}
