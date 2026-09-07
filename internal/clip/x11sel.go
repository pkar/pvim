package clip

import (
	"bytes"
	"strings"

	"github.com/pkar/pvim/internal/register"
)

// The X11 selection wire formats, with no X server in them.
//
// This file has no build tag and talks to no connection, which is the only
// reason any of it could be checked on the machine it was written on: there is
// no Linux box here, no X server and no vim on the other end of a selection, so
// the parts that can be tested are the parts that are pure functions over
// bytes, and those are the parts that decide whether a paste from another vim
// keeps its linewise-ness or arrives as one long line.
//
// # The targets, and which of them carries a motion
//
// A selection has a target, which is an atom naming the format the requestor
// wants. Everything agrees on UTF8_STRING and on the older STRING, and neither
// of them can say whether the text was yanked charwise, linewise or blockwise:
// they are text. So vim defines two of its own, exactly as MacVim defines
// VimPboardType on the pasteboard, and a yank between two vims keeps its type
// through them and loses it through the standard ones.
//
// # Where these layouts came from, which is not a measurement
//
// The darwin half of this package says of every rule in it that it was measured
// against /opt/homebrew/bin/vim through getregtype('*'). None of what follows
// was, and it must not be read as though it had been. The layouts here are
// taken from reading vim's clipboard code -- clip_x11_request_selection and
// clip_x11_convert_selection in src/clipboard.c, where vim_atom_t is a motion
// byte followed by the text and vimenc_atom_t is a motion byte, an encoding
// name, a NUL, and then the text -- and none of it has been checked against a
// running X server.
//
// That is why every decoder below refuses rather than guesses. A _VIMENC_TEXT
// whose encoding is not utf-8, a motion byte outside the three vim defines, a
// buffer with no NUL where the encoding should end: all of them come back
// false, and the caller falls through to UTF8_STRING, which is the format that
// cannot be wrong. The cost of that being wrong is a paste that loses its
// linewise-ness. The cost of guessing would be a paste that inserts an encoding
// name into the buffer.

// The vim-specific selection targets, by name. They are interned like any other
// atom; the names are what is fixed.
const (
	vimTextTarget    = "_VIM_TEXT"
	vimEncTextTarget = "_VIMENC_TEXT"
)

// vimEncoding is the encoding name written into a _VIMENC_TEXT and the only one
// read back out of one.
//
// The editor's 'encoding' is utf-8 and nothing here changes it, so
// this is a constant rather than an option. A _VIMENC_TEXT arriving in latin1
// from a vim with a different 'encoding' is refused by decodeVimEncText and
// falls back to UTF8_STRING, which the sending vim also offers: converting
// charsets is a job for a package that has a charset table, and this one has
// none.
const vimEncoding = "utf-8"

// maxEncodingName is how long an encoding name may be before the buffer stops
// looking like a _VIMENC_TEXT at all. vim's own names are all under a dozen
// bytes; the limit is here so that a hostile or corrupt property cannot make
// this scan a megabyte looking for a NUL that is really text.
const maxEncodingName = 32

// encodeVimText builds the _VIM_TEXT form: one motion byte, then the text.
func encodeVimText(v register.Value) []byte {
	b := make([]byte, 0, len(bytesFor(v))+1)
	b = append(b, byte(motionOf(v.Type)))
	return append(b, bytesFor(v)...)
}

// decodeVimText reads the _VIM_TEXT form.
//
// A motion byte that is none of vim's three is a refusal and not a fallback,
// which is the opposite of what valueOfMotion does with an unknown number on
// the pasteboard. The reason is that this is a length-prefixed-by-nothing
// format: if the first byte is not a motion then the buffer is not a _VIM_TEXT,
// and treating its first byte as text would put a stray control character at
// the head of every paste.
func decodeVimText(b []byte) (register.Value, bool) {
	if len(b) < 1 || !knownMotion(int(b[0])) {
		return register.Value{}, false
	}
	return valueOfMotion(b[1:], int(b[0])), true
}

// encodeVimEncText builds the _VIMENC_TEXT form: a motion byte, the encoding
// name, a NUL, then the text.
func encodeVimEncText(v register.Value) []byte {
	text := bytesFor(v)
	b := make([]byte, 0, len(text)+len(vimEncoding)+2)
	b = append(b, byte(motionOf(v.Type)))
	b = append(b, vimEncoding...)
	b = append(b, 0)
	return append(b, text...)
}

// decodeVimEncText reads the _VIMENC_TEXT form, refusing anything it is not
// certain of.
//
// Three refusals, and each of them is a paste that would otherwise be wrong
// rather than merely untyped: a motion byte vim does not define, no NUL within
// maxEncodingName bytes, and an encoding that is not utf-8. The last is the one
// that will actually happen, on a box where somebody's other vim runs with
// 'encoding' set to latin1, and the fallback for it is the UTF8_STRING that
// same vim also offers.
func decodeVimEncText(b []byte) (register.Value, bool) {
	if len(b) < 2 || !knownMotion(int(b[0])) {
		return register.Value{}, false
	}
	rest := b[1:]
	limit := len(rest)
	if limit > maxEncodingName+1 {
		limit = maxEncodingName + 1
	}
	nul := bytes.IndexByte(rest[:limit], 0)
	if nul < 1 {
		return register.Value{}, false
	}
	if !strings.EqualFold(string(rest[:nul]), vimEncoding) {
		return register.Value{}, false
	}
	return valueOfMotion(rest[nul+1:], int(b[0])), true
}

// knownMotion reports whether n is one of vim's three motion types. MAUTO, which
// vim spells 255, is deliberately not one: it means "work it out from the text"
// and a buffer whose first byte is 255 is far more likely to be a UTF-8
// continuation byte in a plain-text selection than a vim motion.
func knownMotion(n int) bool {
	return n == motionChar || n == motionLine || n == motionBlock
}

// chunkSizes splits a transfer of total bytes into pieces of at most max.
//
// This is the INCR transfer, which is what the selection protocol does instead
// of having a length field: a selection too large for one property is offered
// as the atom INCR with the total size in it, and then handed over one property
// write at a time, the receiver deleting the property to ask for the next
// piece, until a zero-length write says it is over.
//
// A zero total gives one zero-length piece, which is the terminator on its own
// and is what an empty selection under INCR looks like. A max of zero or less
// gives nothing, which the caller reports rather than sending forever.
func chunkSizes(total, max int) []int {
	if max <= 0 {
		return nil
	}
	if total <= 0 {
		return []int{0}
	}
	var out []int
	for left := total; left > 0; left -= max {
		if left < max {
			out = append(out, left)
			break
		}
		out = append(out, max)
	}
	// The terminator. A transfer whose last piece is a full-sized one is
	// indistinguishable from one that has more to come, so every transfer ends
	// with an explicit empty write, including the ones that did not need it.
	return append(out, 0)
}

// latin1ToUTF8 decodes bytes from the STRING target, which the ICCCM defines as
// Latin-1 and not as "whatever the sender had".
//
// Every byte is a code point, which is what makes Latin-1 the one encoding with
// no table: Unicode's first 256 code points are Latin-1 by construction. An
// application that puts UTF-8 on a STRING target is doing something the ICCCM
// does not allow and its e-acute arrives here as two characters, which is
// visible and fixable rather than silent; UTF8_STRING is tried first for
// exactly that reason and STRING is the fallback for applications too old to
// offer it.
func latin1ToUTF8(b []byte) []byte {
	ascii := true
	for _, c := range b {
		if c >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return b
	}
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = utf8Append(out, rune(c))
	}
	return out
}

// utf8ToLatin1 encodes text for the STRING target, and says whether it fits.
//
// It refuses rather than substituting. A selection with a character Latin-1
// cannot hold is a selection this editor does not offer on the STRING target at
// all, so an application that asks for STRING gets a refusal and asks for
// something else, rather than being handed a filename with a question mark
// where a character used to be.
func utf8ToLatin1(b []byte) ([]byte, bool) {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		r, size := decodeRune(b[i:])
		if r < 0 || r > 0xff {
			return nil, false
		}
		out = append(out, byte(r))
		i += size
	}
	return out, true
}

// utf8Append and decodeRune are unicode/utf8 by another name, written out so
// that the two functions above read as one thing each rather than as a call
// into a package whose error behaviour a reader would have to go and check.
func utf8Append(dst []byte, r rune) []byte {
	if r < 0x80 {
		return append(dst, byte(r))
	}
	return append(dst, byte(0xc0|r>>6), byte(0x80|r&0x3f))
}

// decodeRune returns the first rune in b and how many bytes it took, or -1 for
// a byte sequence that is not valid UTF-8. It handles the two lengths that can
// possibly encode a code point under 0x100 and reports everything longer as a
// rune out of Latin-1's range, which is what the caller does with it anyway.
func decodeRune(b []byte) (rune, int) {
	if len(b) == 0 {
		return -1, 0
	}
	switch {
	case b[0] < 0x80:
		return rune(b[0]), 1
	case b[0]&0xe0 == 0xc0:
		if len(b) < 2 || b[1]&0xc0 != 0x80 {
			return -1, 1
		}
		r := rune(b[0]&0x1f)<<6 | rune(b[1]&0x3f)
		if r < 0x80 {
			return -1, 2 // overlong, which is not valid UTF-8
		}
		return r, 2
	default:
		// Three bytes or more, so a code point of at least 0x800: out of
		// Latin-1's range whatever it is, and the caller only asks whether it
		// fits.
		return 0x10000, 1
	}
}
