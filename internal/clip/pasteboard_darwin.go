//go:build darwin

package clip

import (
	"fmt"
	"sync"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/register"
)

// The macOS clipboard: the general NSPasteboard, reached through purego the
// same way internal/gui reaches AppKit, with every call marshalled onto the
// thread the run loop owns by the Runner the caller supplied.
//
// # One pasteboard, two register names
//
// On macOS "* and "+ are the same thing. X11 has a PRIMARY selection and a
// CLIPBOARD selection and vim maps them to "* and "+; macOS has one general
// pasteboard, so 'clipboard' containing both "unnamed" and "unnamedplus" -- as
// the vimrc has it -- is redundant and harmless, and internal/register already
// writes one of them and mirrors. Nothing here needs to know which name it was
// asked for.
//
// # What it costs, measured, and why that is not a bug
//
// Every call here is one Runner round trip -- a channel send, a run loop wake,
// the work, and the answer back -- plus the pasteboard work itself. Only the
// second half is measured here. Reproduce it with
//
//	PVIM_CLIP_PASTEBOARD=1 go test -run XXX -bench Pasteboard -benchtime 500x ./internal/clip/
//
// which is what these came from, on Apple silicon under load, four runs:
//
//	a read that finds its own last write 2.1 us
//	a read that has to fetch string and plist 30-35 us
//	a write 215-240 us
//
// BenchmarkPasteboardWriteSteps takes the write apart cumulatively, and the
// answer is that the text is what costs: the clearContents is 45 microseconds,
// the setString:forType: after it adds 135, and vim's private type adds another
// 45. Nothing here is a message send; it is three trips to the pasteboard
// server, which is another process.
//
// Those numbers move with the box. An earlier run of the same benchmarks on the
// same machine with nothing else on it read 27 for a fresh read and 180 for a
// write, so treat them as the shape and not as a budget: a read that hits the
// cache is free, a read that misses costs a tenth of a write, and a write is a
// fifth of a millisecond either way. The Runner half is internal/gui's and has
// never been benchmarked; the estimate for a channel hop each way is
// tens of microseconds, which is noise against the write it carries.
//
// With the vimrc's clipboard=unnamed,unnamedplus,autoselect that is on every
// yank, every put, and every cursor motion in visual mode, because 'autoselect'
// writes the selection each time the selection changes. So a 10,000-iteration
// macro crosses to the run loop 10,000 times whatever it does, and what that
// costs depends on which key it is. A macro of p is 10,000 cached reads,
// because nothing moved the changeCount, and 2.1 microseconds each is 21
// milliseconds of pasteboard for the whole run: nobody will find that. A macro
// of yy is 10,000 writes at 215 microseconds and over two seconds, and holding
// l down in visual mode writes the board on every repeat at the same price.
// MacVim does exactly the same thing at the same cost -- this is what
// 'autoselect' means, not what this implementation does about it -- and the
// it was a known cost the day this was written. A profile that finds it
// should read this paragraph and then look at the write path, because the read
// path is already the cheap one: the changeCount check below means a put after
// a yank never fetches a string at all, and 2 microseconds is a rounding error
// against the trip that carries it.
//
// # What the pasteboard carries, and how the type survives
//
// A register is text plus a type: charwise, linewise, or a block with a width.
// A pasteboard string is text. vim's answer on macOS is a private pasteboard
// type, "VimPboardType", holding a plist of [motion, text] beside the plain
// string, and this writes and reads the same one, so a linewise yank in MacVim
// pastes as whole lines in pvim and the reverse. motion.go carries the
// measurements and the two places the round trip still loses something.

// Pasteboard is the macOS general pasteboard as a register.Clipboard.
//
// It caches its own last write, keyed on the pasteboard's changeCount, which is
// vim's trick and not an optimisation dressed up as one. changeCount goes up
// when any process clears the board and at no other time, so a count that has
// not moved since this editor last wrote means what is up there is what this
// editor put there -- and the cached value can be handed back with the width of
// a blockwise yank still on it, which the pasteboard itself cannot carry. A
// count that has moved means another application has copied something and the
// board is authoritative, so the string is fetched and typed by what
// VimPboardType says or, when there is none, by whether it ends in a newline.
type Pasteboard struct {
	run Runner

	// mu holds the cache and serialises calls. A frontend may read the
	// clipboard from a goroutine that is not the editor's, and two calls into
	// the run loop at once would interleave a read between a clearContents and
	// its setString.
	mu sync.Mutex
	// count is the changeCount the board was left at by the last call, and
	// known says whether count and value mean anything yet. A failed call
	// clears known: after an error nobody knows what is on the board.
	count int
	known bool
	value register.Value
}

// New returns the pasteboard, or an error when there is no thread to run it on
// or AppKit will not load.
//
// run is how a call gets onto the run loop thread: cmd/pvim passes gui.OnMain
// once the window is up. A caller with no window passes nothing and installs no
// clipboard, and "* and "+ stay ordinary registers.
//
// The frameworks are loaded here rather than on the first yank so that a box
// where AppKit is not where it should be says so at startup, with the path in
// the message, instead of putting an error on the message line the first time
// somebody presses y.
func New(run Runner) (Clipboard, error) {
	if run == nil {
		return nil, ErrNoRunner
	}
	if err := initObjC(); err != nil {
		return nil, err
	}
	return &Pasteboard{run: run}, nil
}

// Read returns what is on the pasteboard.
//
// One round trip, always, and a string copy only when the board has changed
// since this editor last touched it. Both halves happen inside the one closure
// because the changeCount and the contents have to be read without another
// process getting between them; a count read on one trip and a string on the
// next is a value that was never on the board at the same time.
//
// An empty board is an empty value and a nil error. A board holding an image
// and no text is the same case: "+p does nothing, which is what it should do.
// No board at all is ErrNoPasteboard, which is a machine with no login session
// and not an empty clipboard.
func (p *Pasteboard) Read() (register.Value, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var (
		count   int
		text    []byte
		motion  = motionNone
		fetched bool
		absent  bool
	)
	if err := p.run(func() {
		withPool(func() {
			pb := generalPasteboard()
			if pb == 0 {
				absent = true
				return
			}
			count = changeCount(pb)
			if p.known && count == p.count {
				return // still this editor's own write; the cache has the type
			}
			text, motion = contents(pb)
			fetched = true
		})
	}); err != nil {
		p.known = false
		return register.Value{}, fmt.Errorf("clip: reading the pasteboard: %w", err)
	}
	if absent {
		p.known = false
		return register.Value{}, ErrNoPasteboard
	}

	if !fetched {
		return clone(p.value), nil
	}
	p.count, p.known, p.value = count, true, valueOfMotion(text, motion)
	return clone(p.value), nil
}

// Write puts v on the pasteboard, as the plain string every application reads
// and as the plist vim reads.
//
// A refusal from setString:forType: is an error and reaches the message line:
// losing a yank silently is the one thing this package must not do. A refusal
// of the private type is not, because the text is on the board either way and
// what is lost is only the charwise-or-linewise distinction on the way back.
//
// Text that is not valid UTF-8 is refused before the trip and leaves the board
// alone. See ErrNotUTF8: handing that to an NSString is not an error return,
// it is an Objective-C exception with no editor on the other side of it.
func (p *Pasteboard) Write(v register.Value) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	text, motion := bytesFor(v), motionOf(v.Type)
	if !utf8.Valid(text) {
		return ErrNotUTF8
	}
	var (
		count  int
		ok     bool
		absent bool
	)
	if err := p.run(func() {
		withPool(func() {
			pb := generalPasteboard()
			if pb == 0 {
				absent = true
				return
			}
			count, ok = put(pb, text, motion)
		})
	}); err != nil {
		p.known = false
		return fmt.Errorf("clip: writing the pasteboard: %w", err)
	}
	if absent {
		p.known = false
		return ErrNoPasteboard
	}
	if !ok {
		p.known = false
		return fmt.Errorf("clip: the pasteboard refused %d bytes of text", len(text))
	}

	p.count, p.known, p.value = count, true, clone(v)
	return nil
}
