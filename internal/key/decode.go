package key

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Status is what a decode attempt concluded about the bytes it was given.
type Status int

const (
	// StatusEmpty means there were no bytes at all.
	StatusEmpty Status = iota
	// StatusKey means a whole key was decoded and n bytes were consumed.
	StatusKey
	// StatusNeedMore means the bytes so far are a prefix of a longer sequence.
	// The caller waits up to ttimeoutlen for more and then calls Flush, which
	// is how a lone Escape ends up being an Escape and not the start of an
	// arrow key.
	StatusNeedMore
)

// MouseEvent is where a mouse key's coordinates go, because a Key has to stay
// comparable and a pointer or a pair of ints on every keystroke is a pointer or
// a pair of ints on every keystroke.
//
// Col and Row are one-based, as the terminal reports them.
type MouseEvent struct {
	Col, Row int
}

// Decoder turns the bytes a terminal delivers into keys.
//
// It holds no timer. The timeout that decides whether a lone 0x1b is an Escape
// or the first byte of an arrow key is the caller's: Next returns
// StatusNeedMore, the caller waits ttimeoutlen (the vimrc asks for 10ms) for
// more bytes on the read, and calls Flush when nothing arrives. Modelling it
// this way is the difference between a test that runs in microseconds and a
// test that sleeps.
//
// A Decoder is not safe for concurrent use, which is fine: one terminal, one
// reader goroutine.
type Decoder struct {
	// AltIsEscape makes an Escape followed immediately by another key decode as
	// that key with ModAlt set. Vim does not do this and neither does pvim by
	// default, because it is what makes a bare Escape need a timeout at all;
	// verified by feeding "\x1bx" to vim with an <M-x> mapping in place and
	// watching it leave insert mode instead. Terminals configured with
	// metaSendsEscape need it turned on.
	AltIsEscape bool

	// Mouse carries the coordinates of the mouse key most recently returned by
	// Next or Flush. It means nothing after any other key.
	Mouse MouseEvent
}

// Next decodes the first key in b.
//
// It returns the key, the number of bytes it consumed, and a status. On
// StatusNeedMore and StatusEmpty nothing was consumed and the caller keeps the
// bytes.
func (d *Decoder) Next(b []byte) (Key, int, Status) {
	return d.decode(b, false)
}

// Flush is Next after the input timeout has expired: it never asks for more
// bytes. An incomplete escape sequence resolves to a bare Escape and the rest
// of the bytes are decoded on the next call, which is what a terminal that sent
// a real Escape and nothing else wants.
func (d *Decoder) Flush(b []byte) (Key, int, Status) {
	return d.decode(b, true)
}

// DecodeAll decodes every whole key in b and returns them with the number of
// bytes consumed, stopping at the first incomplete sequence. It is the
// convenient shape for tests and for a keystroke file, which has no timing at
// all and so can afford to treat a trailing partial sequence as leftovers.
func (d *Decoder) DecodeAll(b []byte) ([]Key, int) {
	var keys []Key
	used := 0
	for used < len(b) {
		k, n, st := d.Next(b[used:])
		if st != StatusKey {
			break
		}
		keys = append(keys, k)
		used += n
	}
	return keys, used
}

func (d *Decoder) decode(b []byte, flush bool) (Key, int, Status) {
	if len(b) == 0 {
		return Key{}, 0, StatusEmpty
	}
	if b[0] != 0x1b {
		return d.decodePlain(b)
	}

	if len(b) == 1 {
		if flush {
			return Key{Special: KeyEsc}, 1, StatusKey
		}
		return Key{}, 0, StatusNeedMore
	}

	switch b[1] {
	case '[':
		k, n, st := d.decodeCSI(b)
		if st == StatusNeedMore && flush {
			return Key{Special: KeyEsc}, 1, StatusKey
		}
		return k, n, st
	case 'O':
		// SS3: the application-cursor and F1-F4 forms.
		if len(b) < 3 {
			if flush {
				return Key{Special: KeyEsc}, 1, StatusKey
			}
			return Key{}, 0, StatusNeedMore
		}
		if s, ok := ss3Keys[b[2]]; ok {
			return Key{Special: s}, 3, StatusKey
		}
		if k, ok := ss3Keypad[b[2]]; ok {
			return k, 3, StatusKey
		}
		return Key{Special: KeyEsc}, 1, StatusKey
	case 0x1b:
		// Two escapes in a row: the first is a real one whatever the second
		// turns out to begin.
		return Key{Special: KeyEsc}, 1, StatusKey
	}

	if d.AltIsEscape {
		k, n, st := d.decodePlain(b[1:])
		if st != StatusKey {
			if flush {
				return Key{Special: KeyEsc}, 1, StatusKey
			}
			return Key{}, 0, st
		}
		return canon(Key{Rune: k.Rune, Special: k.Special, Mod: k.Mod | ModAlt}), n + 1, StatusKey
	}
	return Key{Special: KeyEsc}, 1, StatusKey
}

// ss3Keys is the SS3 table: "\x1bO" then one letter. Vim decodes all of these,
// checked by feeding "\x1bOA" and "\x1bOP" with mappings in place.
var ss3Keys = map[byte]Special{
	'A': KeyUp, 'B': KeyDown, 'C': KeyRight, 'D': KeyLeft,
	'H': KeyHome, 'F': KeyEnd,
	'P': KeyF1, 'Q': KeyF2, 'R': KeyF3, 'S': KeyF4,
}

// ss3Keypad is the rest of the SS3 table: the numeric keypad in xterm's
// application mode, which vim's builtin xterm termcap carries as K_K0 to K_K9,
// K_KPLUS and the four other operators.
//
// They decode to the character each one prints rather than to a special key of
// their own, because that is what they do everywhere this editor can see: an
// unmapped K_K3 is a "3", so it is a count in normal mode and a digit in
// insert mode, and nothing here can map them. Measured key by key against vim
// 9.2.0321 by typing each sequence in insert mode: "\x1bOs" puts a 3 in the
// buffer and "\x1bOk" a "+".
//
// The three bytes matter as much as the character. Without this table
// "\x1bOs" decoded as a bare Escape and left "Os" behind it, so a generated
// script that happened to put an O after an Escape opened a line in pvim and
// did nothing in vim -- two of the twenty-two differences the third fuzz round
// found. A letter that is NOT in either table is still Escape and two more
// keys, which is vim as well: "\x1bOl" opens a line and types an "l" in both.
var ss3Keypad = map[byte]Key{
	'j': Rune('*'), 'k': Rune('+'), 'm': Rune('-'), 'n': Rune('.'), 'o': Rune('/'),
	'p': Rune('0'), 'q': Rune('1'), 'r': Rune('2'), 's': Rune('3'), 't': Rune('4'),
	'u': Rune('5'), 'v': Rune('6'), 'w': Rune('7'), 'x': Rune('8'), 'y': Rune('9'),
	// The keypad Enter, which vim's K_KENTER behaves as a carriage return
	// everywhere: in insert mode it splits the line.
	'M': {Special: KeyCR},
}

// decodePlain handles a byte that is not the start of an escape sequence.
//
// The C0 codes canonicalise the way vim does: 0x09 is <Tab> and not <C-i>, 0x0d
// is <CR>, 0x0a is <NL>, 0x00 is <Nul>. Checked by mapping both spellings and
// feeding the byte; <Tab> and <C-i> both fire on 0x09, <BS> fires on 0x7f and
// not on 0x08, and <C-h> fires on 0x08.
func (d *Decoder) decodePlain(b []byte) (Key, int, Status) {
	if len(b) == 0 {
		return Key{}, 0, StatusEmpty
	}
	c := b[0]
	switch {
	case c == 0x00:
		return Key{Special: KeyNul}, 1, StatusKey
	case c == 0x09:
		return Key{Special: KeyTab}, 1, StatusKey
	case c == 0x0a:
		return Key{Special: KeyNL}, 1, StatusKey
	case c == 0x0d:
		return Key{Special: KeyCR}, 1, StatusKey
	case c == 0x7f:
		return Key{Special: KeyBS}, 1, StatusKey
	case c < 0x20:
		// 0x01-0x1a are the letters, 0x1c-0x1f are \ ] ^ _. 0x1b never reaches
		// here.
		if c <= 0x1a {
			return Key{Rune: rune('A' + c - 1), Mod: ModCtrl}, 1, StatusKey
		}
		return Key{Rune: rune('@' + c), Mod: ModCtrl}, 1, StatusKey
	case c < utf8.RuneSelf:
		return Key{Rune: rune(c)}, 1, StatusKey
	}

	r, size := utf8.DecodeRune(b)
	if r == utf8.RuneError && size <= 1 {
		if !utf8.FullRune(b) {
			return Key{}, 0, StatusNeedMore
		}
		// A byte that cannot start a rune. Hand it back as a latin1 character
		// rather than dropping it, so a paste of mis-encoded text still lands.
		return Key{Rune: rune(c)}, 1, StatusKey
	}
	return Key{Rune: r}, size, StatusKey
}

// decodeCSI parses "\x1b[" and everything after it up to the final byte.
func (d *Decoder) decodeCSI(b []byte) (Key, int, Status) {
	i := 2
	sgrMouse := false
	if i < len(b) && b[i] == '<' {
		sgrMouse = true
		i++
	}
	start := i
	for i < len(b) && (b[i] >= '0' && b[i] <= '9' || b[i] == ';') {
		i++
	}
	if i >= len(b) {
		return Key{}, 0, StatusNeedMore
	}
	params := parseParams(string(b[start:i]))
	final := b[i]
	n := i + 1

	if sgrMouse {
		if final != 'M' && final != 'm' {
			return Key{Special: KeyIgnore}, n, StatusKey
		}
		k, ok := d.mouseKey(params, final == 'M')
		if !ok {
			return Key{Special: KeyIgnore}, n, StatusKey
		}
		return k, n, StatusKey
	}

	param := func(i int) int {
		if i < len(params) {
			return params[i]
		}
		return 0
	}

	switch final {
	case 'u':
		// CSI-u: the codepoint and the modifier, the encoding a terminal uses
		// for combinations the legacy tables cannot say. Vim 9.2 decodes it.
		return canon(applyXtermMod(Key{Rune: rune(param(0))}, param(1))), n, StatusKey
	case 'Z':
		return Key{Special: KeyTab, Mod: ModShift}, n, StatusKey
	case '~':
		if param(0) == 27 {
			// modifyOtherKeys form two: "\x1b[27;<mod>;<code>~".
			return canon(applyXtermMod(Key{Rune: rune(param(2))}, param(1))), n, StatusKey
		}
		s, ok := tildeKeys[param(0)]
		if !ok {
			return Key{Special: KeyIgnore}, n, StatusKey
		}
		return applyXtermMod(Key{Special: s}, param(1)), n, StatusKey
	}

	if s, ok := csiLetterKeys[final]; ok {
		return applyXtermMod(Key{Special: s}, param(1)), n, StatusKey
	}
	// Something well formed that pvim has no key for: a cursor position report,
	// a device attribute, a mode report. It is consumed and reported as
	// <Ignore>, which is vim's name for a key that is swallowed, so that it can
	// never reach the editor as loose text.
	return Key{Special: KeyIgnore}, n, StatusKey
}

// tildeKeys is the "\x1b[<n>~" table. 1 and 7 are Home and 4 and 8 are End on
// the terminals that do not send "\x1b[H" and "\x1b[F"; vim's own table for
// this terminal has only the letter forms, so feeding "\x1b[1~" to vim does
// nothing while pvim accepts it, which is a superset and not a difference the
// oracle can see.
var tildeKeys = map[int]Special{
	1: KeyHome, 2: KeyInsert, 3: KeyDel, 4: KeyEnd,
	5: KeyPageUp, 6: KeyPageDown, 7: KeyHome, 8: KeyEnd,
	11: KeyF1, 12: KeyF2, 13: KeyF3, 14: KeyF4, 15: KeyF5,
	17: KeyF6, 18: KeyF7, 19: KeyF8, 20: KeyF9, 21: KeyF10,
	23: KeyF11, 24: KeyF12,
}

// csiLetterKeys is the "\x1b[<params><letter>" table.
//
// 'R' is deliberately absent even though "\x1b[1;5R" would be <C-F3>: bare
// "\x1b[<row>;<col>R" is the cursor position report every terminal sends when
// asked, and swallowing a position report as a function key is a worse bug than
// losing <C-F3>, which "\x1b[13;5~" also says.
var csiLetterKeys = map[byte]Special{
	'A': KeyUp, 'B': KeyDown, 'C': KeyRight, 'D': KeyLeft,
	'H': KeyHome, 'F': KeyEnd,
	'P': KeyF1, 'Q': KeyF2, 'S': KeyF4,
}

// applyXtermMod folds the ";n" modifier parameter onto a key. Zero and one both
// mean no modifier.
func applyXtermMod(k Key, n int) Key {
	if n <= 1 {
		return k
	}
	n--
	if n&1 != 0 {
		k.Mod |= ModShift
	}
	if n&2 != 0 {
		k.Mod |= ModAlt
	}
	if n&4 != 0 {
		k.Mod |= ModCtrl
	}
	if n&8 != 0 {
		k.Mod |= ModCmd
	}
	return k
}

// parseParams splits the numeric parameters of a CSI sequence. An omitted
// parameter reads as zero, which is what every caller here wants.
func parseParams(s string) []int {
	if s == "" {
		return nil
	}
	fields := strings.Split(s, ";")
	out := make([]int, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			n = 0
		}
		out[i] = n
	}
	return out
}

// mouseKey turns an SGR mouse report into a pseudo-key and records where it
// happened.
//
// The button byte is xterm's: the low two bits pick the button, 0x04 0x08 0x10
// are shift, alt and ctrl, 0x20 says the pointer was moving, 0x40 says a wheel
// and 0x80 says one of the two extra buttons. A final 'm' is a release.
func (d *Decoder) mouseKey(params []int, press bool) (Key, bool) {
	if len(params) < 3 {
		return Key{}, false
	}
	code := params[0]
	d.Mouse = MouseEvent{Col: params[1], Row: params[2]}

	var mod Mod
	if code&0x04 != 0 {
		mod |= ModShift
	}
	if code&0x08 != 0 {
		mod |= ModAlt
	}
	if code&0x10 != 0 {
		mod |= ModCtrl
	}
	drag := code&0x20 != 0
	low := code & 0x03

	switch {
	case code&0x40 != 0:
		wheel := []Special{KeyScrollWheelUp, KeyScrollWheelDown, KeyScrollWheelLeft, KeyScrollWheelRight}
		return Key{Special: wheel[low], Mod: mod}, true
	case code&0x80 != 0:
		set := [][3]Special{
			{KeyX1Mouse, KeyX1Drag, KeyX1Release},
			{KeyX2Mouse, KeyX2Drag, KeyX2Release},
		}
		if low > 1 {
			return Key{}, false
		}
		return Key{Special: set[low][phase(press, drag)], Mod: mod}, true
	case low == 3:
		if drag {
			return Key{Special: KeyMouseMove, Mod: mod}, true
		}
		// A release with no button identity, which is what a terminal without
		// SGR reporting sends. pvim treats it as a left release, the only one
		// any binding cares about.
		return Key{Special: KeyLeftRelease, Mod: mod}, true
	default:
		set := [][3]Special{
			{KeyLeftMouse, KeyLeftDrag, KeyLeftRelease},
			{KeyMiddleMouse, KeyMiddleDrag, KeyMiddleRelease},
			{KeyRightMouse, KeyRightDrag, KeyRightRelease},
		}
		return Key{Special: set[low][phase(press, drag)], Mod: mod}, true
	}
}

// phase indexes the press, drag and release triple.
func phase(press, drag bool) int {
	switch {
	case !press:
		return 2
	case drag:
		return 1
	}
	return 0
}
