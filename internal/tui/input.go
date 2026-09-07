package tui

import (
	"bytes"
	"time"

	"github.com/pkar/pvim/internal/key"
)

// input turns a terminal's byte stream into events.
//
// It holds no timer and no channel. The Escape ambiguity is resolved by the
// caller calling feed with flush set once the key-code timeout has passed,
// exactly as internal/key's Decoder asks: Next says "need more", the loop
// waits 'ttimeoutlen', and Flush turns the leftover into an Escape. Modelling
// it this way is the difference between a test that runs in microseconds and a
// test that sleeps ten milliseconds a case.
type input struct {
	dec key.Decoder

	// inPaste and paste hold a bracketed paste in progress. A paste arrives
	// in as many chunks as the terminal feels like, so the text accumulates
	// here until the closing marker and leaves as one PasteEvent.
	inPaste bool
	paste   []byte
}

// feed consumes as many whole events as b contains and returns them with the
// number of bytes it used. The unused tail is an incomplete sequence and
// belongs to the caller, who appends the next read to it and calls again.
//
// flush says the key-code timeout has expired: an incomplete escape sequence
// resolves to a bare Escape instead of waiting for the rest of an arrow key
// that is never coming.
func (in *input) feed(b []byte, flush bool) ([]Event, int) {
	var evs []Event
	used := 0

	for used < len(b) {
		rest := b[used:]

		if in.inPaste {
			text, n, done := scanPaste(rest)
			in.paste = append(in.paste, text...)
			used += n
			if !done {
				break // the rest is a partial end marker; wait for more
			}
			in.inPaste = false
			evs = append(evs, PasteEvent{Text: in.paste})
			in.paste = nil
			continue
		}

		if bytes.HasPrefix(rest, pasteStart) {
			in.inPaste = true
			used += len(pasteStart)
			continue
		}

		if bytes.HasPrefix(rest, csiPrivate) {
			n, complete := privateReport(rest)
			switch {
			case complete && n > 0:
				used += n
				continue
			case !complete && !flush:
				// The rest of the report has not arrived yet.
				return evs, used
			case !complete:
				// The key-code timeout says stop waiting, so it was an
				// Escape and then some ordinary characters. This is what
				// internal/key's Flush would conclude if it could see past
				// the "?"; it cannot, and eats all three bytes as an
				// <Ignore> instead, so the Escape is made here.
				evs = append(evs, KeyEvent{Key: key.Key{Special: key.KeyEsc}})
				used++
				continue
			}
			// A byte no report can hold. Hand it to the decoder.
		}

		// A proper prefix of the paste-start marker needs no special case:
		// every one of them is also an incomplete CSI, so the decoder says
		// StatusNeedMore and this loop waits, which is the same answer.
		k, n, st := in.dec.Next(rest)
		if st == key.StatusNeedMore && flush {
			k, n, st = in.dec.Flush(rest)
		}
		if st != key.StatusKey {
			break
		}
		used += n

		switch {
		case k.Special == key.KeyIgnore:
			// A cursor position report, a device attribute, a mode report:
			// consumed and dropped, never delivered as text.
		case k.IsMouse():
			if ev, ok := mouseEvent(k, in.dec.Mouse); ok {
				evs = append(evs, ev)
			}
		default:
			evs = append(evs, KeyEvent{Key: k})
		}
	}
	return evs, used
}

// privateReport measures a private-mode CSI sequence -- "\x1b[?" and then
// parameters up to a final byte -- and reports whether all of it has arrived.
//
// These are answers, not keys: a device attribute report, a DECRPM mode
// report, a cursor-style query response. internal/key's decoder stops at the
// "?" because it is looking for digits, hands back <Ignore> for the three
// bytes it read, and the parameters that follow arrive as typed text: feed it
// "\x1b[?62;1;6c" and the buffer gets "62;1;6c". This editor sends no query
// that asks for one of these, so nothing produces one; it is filtered
// here anyway because the day somebody adds a truecolor probe is not the day
// to find out that its answer types itself into a file.
//
// It returns 0, true for bytes that begin like one and then hold a byte no
// report can contain, which the caller hands to the decoder instead.
func privateReport(b []byte) (n int, complete bool) {
	for i := len(csiPrivate); i < len(b); i++ {
		switch c := b[i]; {
		case c >= 0x30 && c <= 0x3f: // parameter bytes: digits; : < = > ?
		case c >= 0x20 && c <= 0x2f: // intermediate bytes, the "$" of $y
		case c >= 0x40 && c <= 0x7e: // the final byte
			return i + 1, true
		default:
			// Not a report after all. Hand it to the decoder, which will make
			// an Escape of it when the timeout says to.
			return 0, true
		}
	}
	return 0, false
}

// scanPaste takes the next piece of a bracketed paste out of b.
//
// It returns the pasted text found, the bytes consumed and whether the closing
// marker was reached. When it was not, the returned count stops short of any
// suffix of b that could be the beginning of the marker, so that a paste split
// across two reads in the middle of "\x1b[201~" does not deliver half a marker
// as pasted text.
func scanPaste(b []byte) (text []byte, used int, done bool) {
	if i := bytes.Index(b, pasteEnd); i >= 0 {
		return b[:i], i + len(pasteEnd), true
	}
	hold := partialSuffix(b, pasteEnd)
	return b[:len(b)-hold], len(b) - hold, false
}

// partialSuffix returns the length of the longest suffix of b that is a proper
// prefix of marker.
func partialSuffix(b, marker []byte) int {
	max := len(marker) - 1
	if max > len(b) {
		max = len(b)
	}
	for n := max; n > 0; n-- {
		if bytes.Equal(b[len(b)-n:], marker[:n]) {
			return n
		}
	}
	return 0
}

// keyCodeTimeout is how long the reader waits for the rest of a key code
// before deciding a lone Escape was a lone Escape.
//
// The rule is vim's, out of the two tables in its own documentation rather
// than out of anybody's memory. From options.txt under 'ttimeout':
//
//	'timeout' 'ttimeout'		action
//	 off		off		do not time out
//	 on		on or off	time out on:mappings and key codes
//	 off		on		time out on key codes
//
// and under 'ttimeoutlen':
//
//	ttimeoutlen	mapping delay	 key code delay
//	 < 0		'timeoutlen'	 'timeoutlen'
//	 >= 0		'timeoutlen'	 'ttimeoutlen'
//
// The vimrc sets notimeout, ttimeout and ttimeoutlen=10 in its
// !has('gui_running') branch, which is the third row of the first table and
// the second of the second: mappings never time out, a key code times out
// after 10 milliseconds. The mapping half of that is the mode machine's
// business and not this package's; the key-code half is entirely this
// package's, and it is the only reason Options exists.
func (o Options) keyCodeTimeout() (time.Duration, bool) {
	if !o.Timeout && !o.TTimeout {
		return 0, false
	}
	ms := o.TTimeoutLen
	if ms < 0 {
		ms = o.TimeoutLen
	}
	if ms < 0 {
		return 0, false
	}
	return time.Duration(ms) * time.Millisecond, true
}
