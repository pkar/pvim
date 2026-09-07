package key

import (
	"strconv"
	"unicode/utf8"
)

// csiForm says how a special key is spelled as an escape sequence.
//
// A key has either a final letter ("\x1b[A" for Up, "\x1b[1;5A" when modified)
// or a tilde number ("\x1b[3~" for Del, "\x1b[3;5~" when modified). The two
// shapes are xterm's and every terminal worth supporting speaks them.
type csiForm struct {
	letter byte // final byte for the letter form, 0 when the key uses the tilde form
	num    int  // parameter for the tilde form, 0 when the key uses the letter form
}

var csiForms = map[Special]csiForm{
	KeyUp:       {letter: 'A'},
	KeyDown:     {letter: 'B'},
	KeyRight:    {letter: 'C'},
	KeyLeft:     {letter: 'D'},
	KeyHome:     {letter: 'H'},
	KeyEnd:      {letter: 'F'},
	KeyInsert:   {num: 2},
	KeyDel:      {num: 3},
	KeyPageUp:   {num: 5},
	KeyPageDown: {num: 6},
	KeyF1:       {letter: 'P'},
	KeyF2:       {letter: 'Q'},
	KeyF3:       {letter: 'R'},
	KeyF4:       {letter: 'S'},
	KeyF5:       {num: 15},
	KeyF6:       {num: 17},
	KeyF7:       {num: 18},
	KeyF8:       {num: 19},
	KeyF9:       {num: 20},
	KeyF10:      {num: 21},
	KeyF11:      {num: 23},
	KeyF12:      {num: 24},
}

// xtermMod is the ";n" parameter xterm puts in a modified sequence: one plus a
// bitmask of shift, alt, ctrl, super. Cmd rides in on the super bit, which is
// what the CSI-u protocol calls it and what a terminal that reports Cmd at all
// will send.
func xtermMod(m Mod) int {
	n := 1
	if m&ModShift != 0 {
		n += 1
	}
	if m&ModAlt != 0 {
		n += 2
	}
	if m&ModCtrl != 0 {
		n += 4
	}
	if m&ModCmd != 0 {
		n += 8
	}
	return n
}

// ToBytes renders the key as the bytes a terminal would deliver for it.
//
// This is the encoding vim itself reads from a `-s` keystroke script, checked
// by putting a mapping in place and feeding the bytes: <M-x> fires on the UTF-8
// of U+00F8, that is 'x' with the high bit set, and <C-x> on the single byte
// 0x18. Where vim's own byte encoding has no answer, because the key only
// exists on a modern terminal, the CSI-u form is used, which is what Ghostty
// and xterm send and what vim decodes.
//
// It returns nil for the keys that no terminal can deliver: <Plug>, <Cmd>,
// <ScriptCmd>, <Ignore> and every mouse pseudo-key, whose coordinates are not
// in a Key to begin with. <Help> and <Undo> are nil too: they are keys off a
// Sun and an Amiga keyboard, they reach pvim only from the GUI frontend, and
// picking an escape sequence for them would be inventing one.
func (k Key) ToBytes() []byte {
	if k.IsMouse() {
		return nil
	}

	if k.Special != KeyNone {
		return specialBytes(k)
	}

	// A rune with Cmd, or with a Ctrl that has no control code, can only be
	// said in CSI-u.
	if k.Mod&ModCmd != 0 {
		return csiU(int(k.Rune), k.Mod)
	}

	r := k.Rune
	if k.Mod&ModCtrl != 0 {
		c, ok := ctrlByte(r)
		if !ok {
			return csiU(int(r), k.Mod)
		}
		r = rune(c)
	}

	if k.Mod&ModAlt != 0 {
		// Vim's Alt is the high bit, not an Escape prefix: eval("\<M-x>") is
		// U+00F8 and a mapping on <M-x> fires on those two UTF-8 bytes. Above
		// U+007F there is no bit to set and CSI-u is the only encoding left.
		if r > 0x7f {
			return csiU(int(k.Rune), k.Mod)
		}
		r |= 0x80
	}

	buf := make([]byte, utf8.UTFMax)
	return buf[:utf8.EncodeRune(buf, r)]
}

// ctrlByte gives the control code for r, or false when there is none. The set
// is vim's: the letters, and @ [ \ ] ^ _ ?, with '?' the odd one out at 0x7f
// rather than 0x1f.
func ctrlByte(r rune) (byte, bool) {
	switch {
	case r == '?':
		return 0x7f, true
	case r == '@':
		return 0x00, true
	case r >= 'A' && r <= '_': // A-Z and [ \ ] ^ _
		return byte(r) & 0x1f, true
	case r >= 'a' && r <= 'z':
		return byte(r) & 0x1f, true
	}
	return 0, false
}

// specialBytes renders a named key.
func specialBytes(k Key) []byte {
	// The four keys with a C0 byte of their own. A modifier over one of them
	// has no C0 encoding, so it falls through to CSI-u below.
	if k.Mod == 0 {
		switch k.Special {
		case KeyNul:
			return []byte{0x00}
		case KeyEsc:
			return []byte{0x1b}
		case KeyCR:
			return []byte{0x0d}
		case KeyNL:
			return []byte{0x0a}
		case KeyTab:
			return []byte{0x09}
		case KeyBS:
			// 0x7f, not 0x08. On this machine the erase key sends 0x7f, and
			// feeding 0x7f to vim fires a mapping on <BS> while 0x08 fires one
			// on <C-h> and not on <BS>.
			return []byte{0x7f}
		}
	}
	if k.Special == KeyTab && k.Mod == ModShift {
		return []byte("\x1b[Z")
	}

	if f, ok := csiForms[k.Special]; ok {
		mod := xtermMod(k.Mod)
		switch {
		case f.letter != 0 && mod == 1:
			if k.Special >= KeyF1 && k.Special <= KeyF4 {
				return []byte{0x1b, 'O', f.letter}
			}
			return []byte{0x1b, '[', f.letter}
		case f.letter != 0:
			return []byte("\x1b[1;" + strconv.Itoa(mod) + string(rune(f.letter)))
		case mod == 1:
			return []byte("\x1b[" + strconv.Itoa(f.num) + "~")
		default:
			return []byte("\x1b[" + strconv.Itoa(f.num) + ";" + strconv.Itoa(mod) + "~")
		}
	}

	// A modified Esc, CR, NL, Tab, BS or Nul. CSI-u carries the base codepoint
	// plus the modifier, which is the only encoding that survives the trip.
	switch k.Special {
	case KeyNul:
		return csiU(0, k.Mod)
	case KeyEsc:
		return csiU(27, k.Mod)
	case KeyCR:
		return csiU(13, k.Mod)
	case KeyNL:
		return csiU(10, k.Mod)
	case KeyTab:
		return csiU(9, k.Mod)
	case KeyBS:
		return csiU(127, k.Mod)
	}
	return nil
}

// csiU renders the CSI-u form, "\x1b[<code>;<mod>u", which is how a modern
// terminal says a key combination that has no legacy encoding. Vim 9.2 decodes
// it, verified by feeding "\x1b[120;5u" and watching a <C-x> mapping fire.
func csiU(code int, m Mod) []byte {
	return []byte("\x1b[" + strconv.Itoa(code) + ";" + strconv.Itoa(xtermMod(m)) + "u")
}

// Bytes renders a whole sequence, which is what writes a keystroke file for the
// oracle.
func Bytes(keys []Key) []byte {
	var out []byte
	for _, k := range keys {
		out = append(out, k.ToBytes()...)
	}
	return out
}
