package x11

import (
	"unicode"

	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/key"
)

// The keyboard, from a keycode and a modifier mask to a key.Key.
//
// This file has no build tag and imports nothing from xgb, which is deliberate
// twice over. It is the part of the frontend most likely to be subtly wrong,
// because a keyboard is a table the server owns and the rules for reading it
// are prose in the protocol spec rather than a function anybody exports; and it
// is the part that can be tested exhaustively on a machine with no X server at
// all, because it is a pure function of two integers and a table of keysyms
// that a test can write out by hand. Everything x11_linux.go does is fetch
// that table and call in here.
//
// # Why the core protocol and not XKB
//
// github.com/jezek/xgb, which is the one dependency this package is allowed,
// has no XKB binding: its
// extension list is bigreq, composite, damage, dpms, dri2, ge, glx, randr,
// record, render, res, screensaver, shape, shm, sync, xcmisc, xevie, xf86dri,
// xf86vidmode, xfixes, xinerama, xprint, xselinux, xtest, xv and xvmc, and
// there is no xkb among them. Generating one from the XML with xgbgen is a
// week and a vendored generator, and the core protocol answers the question
// this editor actually asks.
//
// What is lost by staying on the core protocol is bounded and named. The core
// GetKeyboardMapping is XKB's compatibility view of itself: an XKB server
// projects its groups and levels down into the four-keysyms-per-keycode shape
// this file reads, which is why AltGr symbols turn up in the group 2 slots
// below. What does not survive the projection is anything past level 4, the
// distinction between a group switch and a level 3 shift, and per-group
// modifier redefinition. A layout that needs those is a layout this editor gets
// wrong, and the manual checks say which keys to press to find out.
//
// # The rules
//
// From the X11 protocol specification, section "Keyboard encoding", which is
// the only normative statement of any of this. Every rule below is written out
// as prose in that section rather than as an algorithm anywhere, so each one
// gets a comment here saying which sentence it came from.

// X11 modifier mask bits, from the protocol's SETofKEYMASK. The first three are
// fixed by the protocol; Mod1 through Mod5 mean whatever the modifier mapping
// says they mean, which is what resolveModifiers works out.
const (
	maskShift   = 1 << 0
	maskLock    = 1 << 1
	maskControl = 1 << 2
	maskMod1    = 1 << 3
	maskMod2    = 1 << 4
	maskMod3    = 1 << 5
	maskMod4    = 1 << 6
	maskMod5    = 1 << 7
)

// noSymbol is the keysym meaning "this slot is empty", from the protocol.
const noSymbol = 0

// The keysyms this file gives names to, from X11/keysymdef.h. There are
// thousands and this is the couple of dozen internal/key has a notation for
// plus the ones needed to work out what the modifier keys are called.
const (
	xkBackSpace = 0xff08
	xkTab       = 0xff09
	xkLinefeed  = 0xff0a
	xkReturn    = 0xff0d
	xkEscape    = 0xff1b
	xkDelete    = 0xffff

	xkHome     = 0xff50
	xkLeft     = 0xff51
	xkUp       = 0xff52
	xkRight    = 0xff53
	xkDown     = 0xff54
	xkPageUp   = 0xff55 // XK_Prior
	xkPageDown = 0xff56 // XK_Next
	xkEnd      = 0xff57
	xkInsert   = 0xff63

	xkF1  = 0xffbe
	xkF12 = 0xffc9

	// The keypad. XK_KP_Space through XK_KP_Equal is the contiguous block the
	// Num_Lock rule below is defined over.
	xkKPSpace    = 0xff80
	xkKPTab      = 0xff89
	xkKPEnter    = 0xff8d
	xkKPHome     = 0xff95
	xkKPLeft     = 0xff96
	xkKPUp       = 0xff97
	xkKPRight    = 0xff98
	xkKPDown     = 0xff99
	xkKPPageUp   = 0xff9a
	xkKPPageDown = 0xff9b
	xkKPEnd      = 0xff9c
	xkKPInsert   = 0xff9e
	xkKPDelete   = 0xff9f
	xkKPMultiply = 0xffaa
	xkKPAdd      = 0xffab
	xkKPSubtract = 0xffad
	xkKPDecimal  = 0xffae
	xkKPDivide   = 0xffaf
	xkKP0        = 0xffb0
	xkKP9        = 0xffb9
	xkKPEqual    = 0xffbd

	// ISO_Left_Tab is what a keyboard sends for Shift-Tab: a keysym of its
	// own rather than Tab with the Shift bit, which is the one place X11
	// disagrees with every other platform this editor runs on.
	xkISOLeftTab = 0xfe20

	// The modifier keysyms, which are never delivered as text and are only
	// here so resolveModifiers can recognise them in the modifier mapping.
	xkShiftL         = 0xffe1
	xkShiftR         = 0xffe2
	xkControlL       = 0xffe3
	xkControlR       = 0xffe4
	xkCapsLock       = 0xffe5
	xkShiftLock      = 0xffe6
	xkMetaL          = 0xffe7
	xkMetaR          = 0xffe8
	xkAltL           = 0xffe9
	xkAltR           = 0xffea
	xkSuperL         = 0xffeb
	xkSuperR         = 0xffec
	xkHyperL         = 0xffed
	xkHyperR         = 0xffee
	xkNumLock        = 0xff7f
	xkModeSwitch     = 0xff7e
	xkISOLevel3Shift = 0xfe03
)

// unicodeKeysym is the bit the protocol sets to spell a Unicode code point as a
// keysym: 0x01000000 | codepoint, defined for U+0100 upwards. Everything below
// that has a legacy keysym of its own and the servers use it.
const unicodeKeysym = 0x01000000

// Keymap is the server's keyboard, as much of it as this editor reads: the
// keysym table from GetKeyboardMapping and the four modifier masks that have to
// be discovered from GetModifierMapping because the protocol does not fix them.
//
// It is rebuilt from scratch on every MappingNotify. That is not an
// optimisation opportunity: a MappingNotify is somebody running setxkbmap, it
// happens seconds apart at the very most, and a keymap that is stale after one
// is an editor whose keys have quietly become someone else's layout.
//
// A zero Keymap answers no key at all rather than panicking, because the window
// is mapped before the first GetKeyboardMapping reply lands and a keystroke in
// that window is possible if unlikely.
type Keymap struct {
	// min is the server's min-keycode and per is keysyms-per-keycode, both
	// straight off the wire. syms is the flat table: the entry for keycode kc
	// starts at (kc-min)*per.
	min  byte
	per  int
	syms []uint32

	// The masks worked out from the modifier mapping. Zero means "this
	// keyboard has no such modifier", which is normal for level3 on a US
	// layout and for numLock on a laptop with no keypad.
	altMask    uint16
	superMask  uint16
	numMask    uint16
	level3Mask uint16
}

// NewKeymap builds a keymap from a GetKeyboardMapping reply.
//
// min is the setup's min-keycode, per is the reply's keysyms-per-keycode and
// syms is its flat keysym list. Nothing is copied: the caller has just decoded
// the reply and has no further use for it.
func NewKeymap(min byte, per int, syms []uint32) *Keymap {
	if per < 1 {
		per = 1
	}
	return &Keymap{min: min, per: per, syms: syms}
}

// SetModifiers works out which of Mod1 to Mod5 is Alt, which is Super, which is
// Num_Lock and which is the level 3 shift, from a GetModifierMapping reply.
//
// mods is the reply's keycode list and perMod its keycodes-per-modifier: eight
// rows of perMod keycodes, in the protocol's fixed order Shift, Lock, Control,
// Mod1, Mod2, Mod3, Mod4, Mod5. A row is padded with zero keycodes, which mean
// nothing and are skipped.
//
// The question this answers cannot be answered any other way. Mod1 is Alt on
// every desktop anybody has shipped in twenty years and Mod4 is Super on most
// of them, and hard-coding that is how an editor ends up with a dead Alt key on
// the one keyboard whose owner moved it. The keysym is the fact; the bit is a
// coincidence of how the mapping happened to be loaded.
func (m *Keymap) SetModifiers(perMod int, mods []byte) {
	m.altMask, m.superMask, m.numMask, m.level3Mask = 0, 0, 0, 0
	if perMod < 1 {
		return
	}
	for i, kc := range mods {
		if kc == 0 {
			continue
		}
		index := i / perMod
		if index < 3 || index > 7 {
			// Shift, Lock and Control are fixed by the protocol and anything
			// past Mod5 is a reply longer than the protocol allows.
			continue
		}
		bit := uint16(1) << uint(index)
		for _, ks := range m.symsFor(kc) {
			switch ks {
			case xkAltL, xkAltR, xkMetaL, xkMetaR:
				m.altMask |= bit
			case xkSuperL, xkSuperR, xkHyperL, xkHyperR:
				m.superMask |= bit
			case xkNumLock:
				m.numMask |= bit
			case xkISOLevel3Shift, xkModeSwitch:
				m.level3Mask |= bit
			}
		}
	}
}

// symsFor returns the keysym slot list for one keycode, or nil for a keycode
// the server never told us about.
func (m *Keymap) symsFor(kc byte) []uint32 {
	if m == nil || m.per < 1 || kc < m.min {
		return nil
	}
	start := (int(kc) - int(m.min)) * m.per
	if start < 0 || start+m.per > len(m.syms) {
		return nil
	}
	return m.syms[start : start+m.per]
}

// Key turns one KeyPress into a key.Key. The false return is a keystroke with
// no notation in this editor, which the caller drops.
//
// state is the event's modifier mask, which is the state BEFORE this key was
// pressed. That is the protocol's rule and it is the right one: a KeyPress of
// Shift itself reports no Shift, and this drops modifier keysyms anyway.
func (m *Keymap) Key(keycode byte, state uint16) (key.Key, bool) {
	ks, ok := m.Keysym(keycode, state)
	if !ok {
		return key.Key{}, false
	}
	// Shift comes out of the modifier set and goes in as a flag. Everything
	// else in Mod is a modifier the editor sees; Shift on a rune is already in
	// the character, and keyFromKeysym puts it back only for a special key.
	return keyFromKeysym(ks, m.Mod(state)&^key.ModShift, state&maskShift != 0)
}

// Keysym applies the protocol's group, shift and lock rules to a keycode and
// returns the keysym the server says it means under state.
//
// It is exported separately from Key so that a test can check the table
// reading, which is the part with prose behind it, without also going through
// the internal/key mapping, which is the part with a table behind it.
func (m *Keymap) Keysym(keycode byte, state uint16) (uint32, bool) {
	list := m.symsFor(keycode)
	if list == nil {
		return 0, false
	}

	// "The first four elements of the list are split into two groups of
	// keysyms. Group 1 contains the first and second keysyms, group 2 the
	// third and fourth." The level 3 shift selects group 2, which is where an
	// XKB server projects its AltGr level down to. A group 2 that is empty
	// means the key has no AltGr symbol and the press is the group 1 one,
	// which is what a bare AltGr-b does on a US layout.
	k0, k1 := lookupPair(list, 0)
	if m.level3Mask != 0 && state&m.level3Mask != 0 && m.per >= 3 {
		if a, b := lookupPair(list, 2); a != noSymbol || b != noSymbol {
			k0, k1 = a, b
		}
	}

	// "If the second element of the pair is NoSymbol, then... if the first
	// element is an alphabetic keysym for which both lowercase and uppercase
	// forms are defined, the pair is the lowercase and the uppercase form."
	if k1 == noSymbol {
		if lo, up, cased := caseOf(k0); cased {
			k0, k1 = lo, up
		}
	}

	shift := state&maskShift != 0
	lock := state&maskLock != 0
	num := m.numMask != 0 && state&m.numMask != 0

	// "If the Num_Lock modifier is on and the second keysym is a keypad
	// keysym: if the Shift modifier is on, the first keysym is used, otherwise
	// the second." Note which way round that is: Num_Lock inverts Shift for
	// the keypad and does nothing for any other key, which is why a keypad 7
	// with Num_Lock on and Shift held is Home.
	if num && isKeypad(k1) {
		if shift {
			return k0, k0 != noSymbol
		}
		return k1, k1 != noSymbol
	}

	// "Otherwise, the first element is used when Shift and Lock are both off.
	// When Lock is on and Shift is off, the first element is used, converted
	// to uppercase. When Shift is on and Lock is off, the second element is
	// used. When both are on, the second element is used, converted to
	// lowercase."
	//
	// The two conversions are only defined when Lock is CapsLock. The
	// protocol's other option, ShiftLock, converts differently and no desktop
	// ships it; a keyboard whose Lock is a ShiftLock behaves here as though it
	// were a CapsLock, which is a difference nobody can produce without
	// editing a modifier map by hand.
	var ks uint32
	switch {
	case !shift && !lock:
		ks = k0
	case !shift && lock:
		ks = upperOf(k0)
	case shift && !lock:
		ks = k1
	default:
		ks = lowerOf(k1)
	}
	return ks, ks != noSymbol
}

// Mod is the modifier set for an event that carries no keysym of its own: a
// mouse click, a wheel notch.
//
// Shift is in it, which is the difference between this and what Key does with a
// rune. A click knows nothing that folded Shift into it.
func (m *Keymap) Mod(state uint16) key.Mod {
	var mod key.Mod
	if state&maskShift != 0 {
		mod |= key.ModShift
	}
	if state&maskControl != 0 {
		mod |= key.ModCtrl
	}
	alt := uint16(maskMod1)
	if m != nil && m.altMask != 0 {
		alt = m.altMask
	}
	if state&alt != 0 {
		mod |= key.ModAlt
	}
	if m != nil && m.superMask != 0 && state&m.superMask != 0 {
		// internal/key has no name for Super, and vim on X11 has none either:
		// <D-...> is a MacVim notation. Mapping it onto ModCmd is what lets a
		// Super chord reach the editor with its identity intact rather than
		// arriving as the bare key, which is the failure that looks like a
		// stuck modifier. On any desktop whose window manager has grabbed
		// Super, none of this is reachable at all.
		mod |= key.ModCmd
	}
	return mod
}

// lookupPair reads two keysym slots out of a keycode's list, treating a list
// too short to hold them as empty.
//
// The protocol's padding rule, which is what makes a one-element list behave
// like a two-element one, is in Keysym above rather than here: a NoSymbol
// second element is what a short list produces and what the caller then applies
// the case rule to.
func lookupPair(list []uint32, at int) (uint32, uint32) {
	var a, b uint32
	if at < len(list) {
		a = list[at]
	}
	if at+1 < len(list) {
		b = list[at+1]
	}
	return a, b
}

// isKeypad reports whether a keysym is one of the keypad block the Num_Lock
// rule is defined over: XK_KP_Space through XK_KP_Equal.
func isKeypad(ks uint32) bool { return ks >= xkKPSpace && ks <= xkKPEqual }

// caseOf returns the lowercase and uppercase forms of an alphabetic keysym.
//
// The protocol defines the pairs for Latin-1, Latin-2, Latin-3, Latin-4, Greek
// and Cyrillic, in XConvertCase, which is nine hundred lines of table in Xlib.
// This does Latin-1 and the Unicode keysyms and no more, which is: every key on
// a US, UK, German, French, Spanish, Italian or Nordic layout, plus anything an
// XKB server chooses to deliver as 0x01000000|codepoint.
//
// What that leaves out is the legacy keysyms for Latin-2 through Latin-4,
// Greek and Cyrillic -- Polish aogonek, Greek alpha, Cyrillic a -- which arrive
// as keysyms in the 0x100 to 0x6ff range that this file has no table for. They
// are dropped by runeOf below rather than mis-cased here, so those layouts type
// nothing rather than typing the wrong thing. That is a real gap and it is in
// the manual checks with the test for it.
func caseOf(ks uint32) (lower, upper uint32, cased bool) {
	r, ok := runeOf(ks)
	if !ok {
		return ks, ks, false
	}
	lo, up := unicode.ToLower(r), unicode.ToUpper(r)
	if lo == up {
		return ks, ks, false
	}
	return keysymOf(lo), keysymOf(up), true
}

// upperOf and lowerOf are the CapsLock conversions. A keysym with no case is
// returned unchanged, which is what makes CapsLock do nothing to a digit.
func upperOf(ks uint32) uint32 {
	if _, up, cased := caseOf(ks); cased {
		return up
	}
	return ks
}

func lowerOf(ks uint32) uint32 {
	if lo, _, cased := caseOf(ks); cased {
		return lo
	}
	return ks
}

// keysymOf spells a rune back as a keysym: Latin-1 as itself, because keysyms
// 0x20 to 0xff are their own code points, and everything else in the Unicode
// form. It is the inverse of runeOf over the range runeOf accepts, which is
// what makes the case conversions above round-trip.
func keysymOf(r rune) uint32 {
	if r < 0x100 {
		return uint32(r)
	}
	return unicodeKeysym | uint32(r)
}

// runeOf returns the character a keysym stands for, if it stands for one.
//
// Two ranges and no table. Latin-1 keysyms are their own code points, which is
// the protocol's own statement and not a coincidence: keysyms 0x20 to 0xff were
// defined as Latin-1 and Unicode's first 256 code points are Latin-1. Unicode
// keysyms are 0x01000000 | codepoint. Everything else -- the function keys, the
// modifier keys, the legacy non-Latin-1 alphabets -- is not a character and
// comes back false.
func runeOf(ks uint32) (rune, bool) {
	switch {
	case ks >= 0x20 && ks <= 0x7e:
		return rune(ks), true
	case ks >= 0xa0 && ks <= 0xff:
		return rune(ks), true
	case ks >= unicodeKeysym+0x100 && ks <= unicodeKeysym+0x10ffff:
		return rune(ks &^ unicodeKeysym), true
	case ks >= unicodeKeysym+0x20 && ks < unicodeKeysym+0x100:
		// A server that spells a Latin-1 character the Unicode way anyway.
		// Nothing forbids it and at least one keyboard driver does it.
		return rune(ks &^ unicodeKeysym), true
	}
	return 0, false
}

// specials is the keysyms internal/key has a name for. Function keys are not in
// it because they are contiguous and handled by arithmetic below; keypad
// digits are not in it because they are characters.
var specials = map[uint32]key.Special{
	xkBackSpace: key.KeyBS,
	xkTab:       key.KeyTab,
	xkLinefeed:  key.KeyNL,
	xkReturn:    key.KeyCR,
	xkEscape:    key.KeyEsc,
	xkDelete:    key.KeyDel,

	xkHome:     key.KeyHome,
	xkLeft:     key.KeyLeft,
	xkUp:       key.KeyUp,
	xkRight:    key.KeyRight,
	xkDown:     key.KeyDown,
	xkPageUp:   key.KeyPageUp,
	xkPageDown: key.KeyPageDown,
	xkEnd:      key.KeyEnd,
	xkInsert:   key.KeyInsert,

	// The keypad's navigation half, which is what those keys send with
	// Num_Lock off. They are the same keys to vim: the vimrc has no mapping
	// that can tell a keypad Home from the other one, and neither can a
	// terminal.
	xkKPEnter:    key.KeyCR,
	xkKPTab:      key.KeyTab,
	xkKPHome:     key.KeyHome,
	xkKPLeft:     key.KeyLeft,
	xkKPUp:       key.KeyUp,
	xkKPRight:    key.KeyRight,
	xkKPDown:     key.KeyDown,
	xkKPPageUp:   key.KeyPageUp,
	xkKPPageDown: key.KeyPageDown,
	xkKPEnd:      key.KeyEnd,
	xkKPInsert:   key.KeyInsert,
	xkKPDelete:   key.KeyDel,
}

// keypadRunes is the keypad's numeric half: the characters those keys type with
// Num_Lock on, which have keysyms of their own rather than being the ordinary
// digits.
var keypadRunes = map[uint32]rune{
	xkKPSpace:    ' ',
	xkKPMultiply: '*',
	xkKPAdd:      '+',
	xkKPSubtract: '-',
	xkKPDecimal:  '.',
	xkKPDivide:   '/',
	xkKPEqual:    '=',
}

// functionKeys is F1 through F12 written out rather than derived by adding to
// key.KeyF1, for the reason internal/gui's copy gives: internal/key's constant
// block is append-only and the twelve names being contiguous is true and
// not guaranteed.
var functionKeys = [12]key.Special{
	key.KeyF1, key.KeyF2, key.KeyF3, key.KeyF4,
	key.KeyF5, key.KeyF6, key.KeyF7, key.KeyF8,
	key.KeyF9, key.KeyF10, key.KeyF11, key.KeyF12,
}

// keyFromKeysym turns a resolved keysym plus the modifiers held into a key.Key.
//
// shift is passed separately from mod because the two are used differently, and
// this is the one rule in the file taken from internal/gui rather than from the
// X protocol. ModShift is never set on a rune: the keysym lookup has already
// folded Shift into the character and there is no way back from '!' to "Shift
// and 1". That costs <S-Space>, which terminal vim cannot map either without
// the CSI-u protocol, so a mapping that works in one frontend and not the other
// is not a trap anybody falls into twice. On a special key Shift is kept,
// because <S-Tab> and <Tab> really are two keys.
func keyFromKeysym(ks uint32, mod key.Mod, shift bool) (key.Key, bool) {
	// Shift-Tab is a keysym of its own on X11 and the Shift bit that produced
	// it is in the state as well, so this is the one place both halves are
	// already present and the notation is <S-Tab> either way.
	if ks == xkISOLeftTab {
		return key.Key{Special: key.KeyTab, Mod: mod | key.ModShift}, true
	}
	if sp, ok := specials[ks]; ok {
		if shift {
			mod |= key.ModShift
		}
		return key.Key{Special: sp, Mod: mod}, true
	}
	if ks >= xkF1 && ks <= xkF12 {
		if shift {
			mod |= key.ModShift
		}
		return key.Key{Special: functionKeys[ks-xkF1], Mod: mod}, true
	}

	r, ok := keypadRunes[ks]
	if !ok {
		if ks >= xkKP0 && ks <= xkKP9 {
			r, ok = rune('0'+ks-xkKP0), true
		}
	}
	if !ok {
		r, ok = runeOf(ks)
	}
	if !ok {
		// A modifier key, a media key, a legacy keysym from an alphabet this
		// file has no table for. Dropped rather than delivered as a garbage
		// rune, which is what internal/gui does with AppKit's private-use
		// block for the same reason.
		return key.Key{}, false
	}

	if mod&key.ModCtrl != 0 {
		// key.Ctrl does vim's folding table, so Control-i is <Tab>, Control-[
		// is <Esc> and Control-a is 'A' with the bit set. Doing it here rather
		// than in a table of our own is the difference between one definition
		// of what Control means and two that drift.
		k := key.Ctrl(r)
		k.Mod |= mod &^ key.ModCtrl
		return k, true
	}
	return key.Key{Rune: r, Mod: mod}, true
}

// The X11 button numbers, from the core protocol. One to three are the buttons;
// four through seven are what a wheel looks like to a server that has no wheel
// in its protocol, which is a press and a release of a button that does not
// exist.
const (
	btnLeft       = 1
	btnMiddle     = 2
	btnRight      = 3
	btnWheelUp    = 4
	btnWheelDown  = 5
	btnWheelLeft  = 6
	btnWheelRight = 7
)

// buttonOf maps an X button number onto the frontend vocabulary's button, and
// says whether this editor has one at all.
//
// Buttons 8 and 9, the side buttons, come back false. internal/gui's MouseButton
// has three names and internal/key's mouse keys have X1 and X2, and the gap
// between the two is a frontend seam this package may not widen on its own.
func buttonOf(n byte) (gui.MouseButton, bool) {
	switch n {
	case btnLeft:
		return gui.MouseLeft, true
	case btnMiddle:
		return gui.MouseMiddle, true
	case btnRight:
		return gui.MouseRight, true
	}
	return gui.MouseNone, false
}

// wheelOf maps an X button number onto a wheel action.
//
// The four directions are the standard assignment every X toolkit and every
// server agrees on, and it is an assignment rather than a protocol fact: the
// core protocol knows nothing about wheels and says only that a pointer may
// have up to five buttons. Shift-wheel is not swapped here the way it is on
// macOS, because an X server reports a horizontal wheel as buttons 6 and 7 in
// its own right and the modifier stays the editor's to read.
func wheelOf(n byte) (gui.MouseAction, bool) {
	switch n {
	case btnWheelUp:
		return gui.MouseWheelUp, true
	case btnWheelDown:
		return gui.MouseWheelDown, true
	case btnWheelLeft:
		return gui.MouseWheelLeft, true
	case btnWheelRight:
		return gui.MouseWheelRight, true
	}
	return 0, false
}
