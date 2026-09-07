//go:build darwin

package gui

import (
	"structs"

	"github.com/ebitengine/purego/objc"
	"github.com/pkar/pvim/internal/key"
)

// Basic input-method support: enough for dead keys and the press-and-hold
// accent popup, and no more.
//
// # What is here
//
// The view conforms to NSTextInputClient, which is what makes -inputContext
// non-nil, which is what makes -interpretKeyEvents: do anything at all. A key
// that could be text -- see imeCandidate -- is handed to the input method
// first. If the method turns it into text it comes back through
// -insertText:replacementRange: and is pushed as runes; if it does not, the
// key falls through to the ordinary mapping and arrives as the key it always
// was. Nothing is ever delivered twice: imeHandled is the whole of the
// bookkeeping and it is set only where text actually arrived.
//
// So Option-e then e produces one 'é' rather than two 'e's, and holding 'a'
// down brings up the accent popup, whose position comes from
// -firstRectForCharacterRange:, which this package answers with the caret cell
// of the last frame it painted.
//
// # What is deliberately not here
//
// Marked text is recorded and not drawn. in as many words that
// full marked-text composition is out of scope, and drawing it would mean the
// window compositing text of its own over a Grid the editor believes it owns.
// A Japanese or Chinese input method therefore composes invisibly and delivers
// the finished string on commit: usable, not good, and honest about which.
// Every place that would change is in this file.
//
// The five keys an input method would want while composing -- Escape, Return,
// Tab, Backspace, Delete -- never reach it, because imeCandidate keeps them
// for vim. With no marked text on screen there is nothing for Escape to
// cancel that the editor would not rather have, and a marked-text run left
// dangling by one of them is discarded on the spot: see discardComposition.
//
// # Threading
//
// Every method in this file runs on the AppKit thread and nowhere else:
// AppKit calls them, from inside -interpretKeyEvents:, from inside the key
// window's event dispatch. The imeState fields are therefore not guarded, and
// that is a rule and not an oversight.

// NSRange is Foundation's range, two NSUIntegers passed and returned by value.
//
// structs.HostLayout for the same reason NSPoint has it: purego marshals a Go
// struct by C's field layout only when it is told the struct is a C one.
type NSRange struct {
	_        structs.HostLayout
	Location uint64
	Length   uint64
}

// nsNotFound is Foundation's NSNotFound, NSIntegerMax. A range located there
// is Cocoa's "there is none", which is what markedRange answers when nothing
// is being composed.
const nsNotFound = uint64(1)<<63 - 1

// notFoundRange is the empty range, built once because it is returned from
// three methods.
var notFoundRange = NSRange{Location: nsNotFound}

// imeEnabled is whether the view class conforms to NSTextInputClient.
//
// It is false when the protocol could not be looked up, which would mean an
// AppKit that has moved NSTextInputClient somewhere else. The window still
// works in that case; it just types the key AppKit gave it, with no dead keys
// and no accent popup.
var imeEnabled bool

// imeState is the composition in flight. All of it is main-thread only.
type imeState struct {
	// handled is set by insertText: and read by keyDown once
	// interpretKeyEvents: has returned. It says whether the input method
	// turned this key into text, which is the only thing that stops the key
	// being delivered a second time as itself.
	handled bool

	// marked is the text an input method is composing, recorded so that
	// hasMarkedText and markedRange can answer consistently. It is never
	// drawn.
	marked []rune
}

// textInputMethods returns the NSTextInputClient method set for the view
// class.
//
// It is a function rather than a literal in registerViewClass so that the
// eleven methods and their reasons sit next to each other. Every one of them
// is required by the protocol: an input context that finds a method missing
// does not degrade, it throws.
func textInputMethods() []objc.MethodDef {
	return []objc.MethodDef{
		// The text itself. The argument is an NSString or an
		// NSAttributedString depending on the input method, which is why
		// nsStringRunes asks before it reads.
		{Cmd: objc.RegisterName("insertText:replacementRange:"), Fn: func(self objc.ID, cmd objc.SEL, text objc.ID, _ NSRange) {
			if current != nil {
				current.insertText(text)
			}
		}},

		// A key the input method decided was a command: Return, an arrow, a
		// Ctrl chord. This editor sends none of those through the input
		// method, so reaching here means the method invented one, and the
		// answer is silence. It must NOT call super: NSResponder answers an
		// unhandled command by ringing the system bell, and the vimrc's
		// `set visualbell t_vb=` says there is no bell in this editor at all.
		{Cmd: objc.RegisterName("doCommandBySelector:"), Fn: func(self objc.ID, cmd objc.SEL, sel objc.SEL) {}},

		{Cmd: objc.RegisterName("setMarkedText:selectedRange:replacementRange:"), Fn: func(self objc.ID, cmd objc.SEL, text objc.ID, _ NSRange, _ NSRange) {
			if current != nil {
				current.ime.marked = nsStringRunes(text)
			}
		}},
		{Cmd: objc.RegisterName("unmarkText"), Fn: func(self objc.ID, cmd objc.SEL) {
			if current != nil {
				current.ime.marked = nil
			}
		}},
		{Cmd: objc.RegisterName("hasMarkedText"), Fn: func(self objc.ID, cmd objc.SEL) bool {
			return current != nil && len(current.ime.marked) > 0
		}},
		{Cmd: objc.RegisterName("markedRange"), Fn: func(self objc.ID, cmd objc.SEL) NSRange {
			if current == nil || len(current.ime.marked) == 0 {
				return notFoundRange
			}
			return NSRange{Location: 0, Length: uint64(len(current.ime.marked))}
		}},

		// The selection, in the input method's coordinates rather than the
		// editor's. There is no text behind this client -- the buffer is on
		// the other side of a channel and the window has no idea what is in
		// it -- so the answer is the empty range at the caret, which is what a
		// client with nothing selected returns.
		{Cmd: objc.RegisterName("selectedRange"), Fn: func(self objc.ID, cmd objc.SEL) NSRange {
			return NSRange{Location: 0, Length: 0}
		}},

		// Nothing can be read back out of this client, so no attributes and no
		// substring. Both are allowed to answer nil and both do.
		{Cmd: objc.RegisterName("validAttributesForMarkedText"), Fn: func(self objc.ID, cmd objc.SEL) objc.ID {
			return objc.ID(objc.GetClass("NSArray")).Send(selArray)
		}},
		{Cmd: objc.RegisterName("attributedSubstringForProposedRange:actualRange:"), Fn: func(self objc.ID, cmd objc.SEL, _ NSRange, _ uintptr) objc.ID {
			return 0
		}},

		// Where to put the accent popup and an input method's candidate
		// window: the caret cell of the last painted frame, in screen points.
		{Cmd: objc.RegisterName("firstRectForCharacterRange:actualRange:"), Fn: func(self objc.ID, cmd objc.SEL, _ NSRange, _ uintptr) NSRect {
			if current == nil {
				return NSRect{}
			}
			return current.caretRectOnScreen()
		}},

		// The inverse mapping, from a screen point back to a character index.
		// A client with no text has none to give, and NSNotFound is how that
		// is spelled.
		{Cmd: objc.RegisterName("characterIndexForPoint:"), Fn: func(self objc.ID, cmd objc.SEL, _ NSPoint) uint64 {
			return nsNotFound
		}},
	}
}

// insertText pushes the text an input method produced, as keys.
//
// Runes and not one paste: every rune goes through keyFromNS so that a newline
// or a tab inside an input method's output arrives as <CR> or <Tab> rather
// than as a raw byte the mode machine has no case for. The flags are zero
// because whatever modifier produced the text has already been consumed in
// producing it -- Option-e and e make 'é', and 'é' carries no Option.
func (w *window) insertText(text objc.ID) {
	runes := nsStringRunes(text)
	if len(runes) == 0 {
		return
	}
	w.ime.handled = true
	w.ime.marked = nil
	for _, r := range runes {
		if k, ok := keyFromNS(r, 0); ok {
			w.pushKey(k)
		}
	}
}

// keyDown is the view's key path: the input method first where the key could
// be text, and the ordinary key mapping otherwise or after.
//
// view is the NSView the event arrived at, which is the one to call
// -interpretKeyEvents: on; it is passed in rather than kept on the window
// because the callback already has it and a second copy is a second thing to
// keep right.
func (w *window) keyDown(view objc.ID, ev objc.ID) {
	flags := uint(objc.Send[uint64](ev, selModifierFlags))
	w.setMods(flags)

	ch, ok := eventChar(ev)
	if !ok {
		return
	}

	if imeEnabled && imeCandidate(rune(ch), flags) {
		w.ime.handled = false
		arr := objc.Send[objc.ID](objc.ID(objc.GetClass("NSArray")), selArrayWithObject, ev)
		view.Send(selInterpretKeyEvents, arr)
		if w.ime.handled {
			return
		}
		// The input method took the key and produced nothing: a dead key
		// waiting for its second half, or a composition still open. Either way
		// there is nothing to type and delivering the key as itself would put
		// the accent's own letter into the buffer.
		if len(w.ime.marked) > 0 {
			return
		}
	} else {
		// A key the input method never sees, with a composition open behind
		// it. Nothing on screen shows the marked text, so leaving it open
		// would mean the next composed character silently carried a prefix
		// nobody typed.
		w.discardComposition(view)
	}

	if k, ok := keyFromNS(rune(ch), flags); ok {
		w.pushKey(k)
	}
}

// discardComposition throws away any marked text and tells the input context
// it has gone.
func (w *window) discardComposition(view objc.ID) {
	if len(w.ime.marked) == 0 {
		return
	}
	w.ime.marked = nil
	if ctx := view.Send(selInputContext); ctx != 0 {
		ctx.Send(selDiscardMarkedText)
	}
}

// caretRectOnScreen is the cell the caret was last painted in, in screen
// points, which is where macOS wants the accent popup.
//
// It reads the position the last Draw recorded rather than asking the editor,
// because the editor is on the other side of a channel and this is called from
// inside AppKit's event dispatch, which must not block on it. One frame of lag
// on the popup's position is invisible; a main thread waiting on an editor is
// a beachball.
func (w *window) caretRectOnScreen() NSRect {
	w.mu.Lock()
	face, scale, linespace := w.face, w.scale, w.linespace
	row, col := w.curRow, w.curCol
	w.mu.Unlock()
	if face == nil || scale < 1 {
		return NSRect{}
	}
	m := face.Metrics()
	local := caretRect(row, col, m.CellW, m.CellH+linespace, scale)

	inWindow := objc.Send[NSRect](w.view, selConvertRectToView, local, objc.ID(0))
	return objc.Send[NSRect](w.nsWindow, selConvertRectToScreen, inWindow)
}

// caretRect is the caret cell in the view's own coordinate space.
//
// The metrics are device pixels and an NSView works in logical points, so
// everything is divided by the scale: at backingScaleFactor 2 a 16-pixel cell
// is 8 points wide, and a popup positioned in pixels would sit twice as far
// across the window as the caret it belongs to. The view is flipped, so y grows
// downwards and the row needs no subtraction from the height.
func caretRect(row, col, cellW, rowH, scale int) NSRect {
	if scale < 1 {
		scale = 1
	}
	w := float64(cellW) / float64(scale)
	h := float64(rowH) / float64(scale)
	return NSRect{
		Origin: NSPoint{X: float64(col) * w, Y: float64(row) * h},
		Size:   NSSize{Width: w, Height: h},
	}
}

// eventChar returns the first UTF-16 unit of -charactersIgnoringModifiers.
func eventChar(ev objc.ID) (uint16, bool) {
	return firstUTF16(ev.Send(selCharactersIgnoringMods))
}

// nsStringRunes reads an NSString, or the string of an NSAttributedString,
// into runes.
//
// -characterAtIndex: in a loop rather than -UTF8String, for the reason
// firstUTF16 gives: reading a code unit at a time keeps this package free of
// any uintptr-to-unsafe.Pointer conversion, and an input method's output is a
// handful of characters, not a file.
//
// Surrogate pairs are decoded here because an input method really can produce
// one -- an emoji picker is an input method -- and half a surrogate is not a
// rune.
func nsStringRunes(s objc.ID) []rune {
	if s == 0 {
		return nil
	}
	if objc.Send[bool](s, selRespondsToSelector, selString) {
		s = s.Send(selString)
	}
	n := objc.Send[int](s, selLength)
	if n <= 0 {
		return nil
	}
	units := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		units = append(units, objc.Send[uint16](s, selCharacterAtIndex, i))
	}
	return utf16Runes(units)
}

// utf16Runes decodes UTF-16 code units, pairing surrogates and dropping an
// unpaired one.
//
// unicode/utf16.Decode would do this, and this is nine lines that do not
// allocate a second slice and do not turn an unpaired surrogate into U+FFFD,
// which would be a character the editor then had to insert.
func utf16Runes(units []uint16) []rune {
	out := make([]rune, 0, len(units))
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u >= 0xD800 && u < 0xDC00 && i+1 < len(units) &&
			units[i+1] >= 0xDC00 && units[i+1] < 0xE000:
			out = append(out, ((rune(u)-0xD800)<<10|(rune(units[i+1])-0xDC00))+0x10000)
			i++
		case u >= 0xD800 && u < 0xE000:
			// An unpaired surrogate is not a character. Dropping it loses
			// nothing a keyboard could have produced.
		default:
			out = append(out, rune(u))
		}
	}
	return out
}

// pushKeys queues a sequence of keys in order, which is what an Edit menu item
// delivers.
func (w *window) pushKeys(keys []key.Key) {
	for _, k := range keys {
		w.pushKey(k)
	}
}
