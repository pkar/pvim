// Package key is the keystroke vocabulary: one struct for a key, vim's
// angle-bracket notation in and out of it, and the raw terminal bytes on
// either side. It imports nothing else in this module so that both frontends
// and every mode can speak it.
//
// Every rule in here that could have been guessed was instead checked against
// vim 9.2 at /opt/homebrew/bin/vim, either through eval("\<...>") and
// keytrans() or by feeding raw bytes to `vim -s` with a mapping in place. The
// interesting ones are noted where they are implemented, because the case and
// folding rules are the classic source of a mapping that binds the wrong key.
package key

import (
	"strconv"
	"strings"
	"unicode"
)

// Mod is a bitmask of the modifiers held down with a key.
//
// A modifier only appears here when vim does not fold it into the rune. Vim
// folds Shift into the rune when the rune has an uppercase form (<S-a> is 'A'
// and carries no Shift bit) and leaves it as a bit when it does not (<S-Space>,
// <S-1>, <S-Tab>). It folds Ctrl into the rune only to canonicalise the seven
// characters that have a distinct name (<C-[> is <Esc>, <C-i> is <Tab>, <C-@>
// is <Nul>); every other Ctrl key keeps the bit and an uppercased rune, so
// <C-x> and <C-X> are one key. Alt and Cmd never fold.
type Mod uint8

// The modifier bits. ModCmd exists only on the AppKit frontend; a terminal
// never sees it unless it speaks the CSI-u protocol.
const (
	ModShift Mod = 1 << iota
	ModCtrl
	ModAlt
	ModCmd
)

// Special names a key that has no rune of its own. KeyNone means the Key
// carries a rune instead, which is the common case.
type Special uint16

// The special keys. This list is closed: a key that is not here arrives as a
// rune or is dropped by the frontend, and adding one means adding its notation
// to the tables in notation.go in the same commit.
//
// The order is append-only. New names go at the bottom even where they would
// read better next to a relative, because a stale build of a neighbouring
// package comparing against a shifted constant is a bug with no symptom.
const (
	KeyNone Special = iota
	KeyEsc
	KeyCR // <CR>, <Enter> and <Return>, all the same key
	KeyTab
	KeyBS
	KeyDel
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyInsert
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
	KeyMouse // a mouse event with no button identity; Parse never produces one

	KeyNL  // <NL>, the newline vim reaches through <C-j>
	KeyNul // <Nul>, which is <C-@> and the byte 0x00
	KeyHelp
	KeyUndo
	KeyIgnore    // <Ignore>: consumed, never acted on
	KeyPlug      // <Plug>: the prefix no terminal can send
	KeyCmd       // <Cmd>: runs an ex command without leaving the mode
	KeyScriptCmd // <ScriptCmd>: <Cmd> with a script context

	KeyLeftMouse
	KeyLeftDrag
	KeyLeftRelease
	KeyMiddleMouse
	KeyMiddleDrag
	KeyMiddleRelease
	KeyRightMouse
	KeyRightDrag
	KeyRightRelease
	KeyX1Mouse
	KeyX1Drag
	KeyX1Release
	KeyX2Mouse
	KeyX2Drag
	KeyX2Release
	KeyScrollWheelUp
	KeyScrollWheelDown
	KeyScrollWheelLeft
	KeyScrollWheelRight
	KeyMouseMove
)

// Key is one keystroke.
//
// Exactly one of Rune and Special carries the identity: when Special is KeyNone
// the key is Rune, otherwise Rune is zero and Special names it. Space is a rune
// and not a Special, even though it prints as <Space>, because every mapping and
// text object treats it as an ordinary character.
//
// Key is comparable on purpose. Mapping tables, the dot register and the macro
// recorder all key on it directly, so it holds no pointer and no slice, and the
// mouse coordinates that would need one live on the Decoder instead.
type Key struct {
	Rune    rune
	Special Special
	Mod     Mod

	// Clicks is the click count on a mouse press: 0 or 1 for a single click,
	// 2, 3 and 4 for <2-LeftMouse> and friends. Zero on every other key.
	Clicks uint8
}

// IsRune reports whether the key carries a rune rather than a named key.
func (k Key) IsRune() bool { return k.Special == KeyNone }

// IsMouse reports whether the key is one of the mouse pseudo-keys, including
// the wheel and the bare motion event.
func (k Key) IsMouse() bool {
	return k.Special == KeyMouse || (k.Special >= KeyLeftMouse && k.Special <= KeyMouseMove)
}

// Rune builds a key from a plain character with no modifiers.
func Rune(r rune) Key { return Key{Rune: r} }

// Ctrl builds the Ctrl-modified form of r, canonicalised the way vim does it:
// Ctrl of 'i' is Tab, of '[' is Escape, of '@' is Nul, and of any letter is the
// uppercase letter with ModCtrl set.
func Ctrl(r rune) Key { return canon(Key{Rune: r, Mod: ModCtrl}) }

// canon folds modifiers into the rune wherever vim does, so that two spellings
// of one key compare equal.
//
// The folding table was read out of vim rather than out of its source: <C-x>
// and <C-X> and <C-S-x> all evaluate to the byte 0x18, <S-a> to 'A', <S-1> to a
// Shift modifier over '1', and <C-1> to a Ctrl modifier over '1'. Ctrl folds
// for the letters and for @ [ \ ] ^ _ ? and for nothing else.
func canon(k Key) Key {
	if k.Special != KeyNone {
		return k
	}

	// Shift over a letter is that letter uppercased, and the bit goes away:
	// <S-a>, <S-A> and <S-S-a> all evaluate to 'A'. Over anything with no case
	// it stays a modifier, which is why <S-Space> and <S-1> are their own keys.
	//
	// Cmd suppresses the fold. eval("\<D-S-a>") keeps a Shift bit over a
	// lowercase 'a' while eval("\<M-S-a>") folds to 'A', which reads like an
	// accident of vim's internals and is copied anyway, because a mapping table
	// that disagrees with vim about which key <D-S-a> is would be wrong on the
	// one frontend that can deliver it.
	if k.Mod&ModShift != 0 && k.Mod&ModCmd == 0 {
		if up := unicode.ToUpper(k.Rune); up != k.Rune || unicode.IsUpper(k.Rune) {
			k.Rune = up
			k.Mod &^= ModShift
		}
	}

	if k.Mod&ModCtrl == 0 {
		return k
	}

	up := unicode.ToUpper(k.Rune)
	switch {
	case up == '@':
		// <C-@> is the NUL key, and vim prints it as <Nul>.
		return Key{Special: KeyNul, Mod: k.Mod &^ (ModCtrl | ModShift)}
	case up == '[':
		return Key{Special: KeyEsc, Mod: k.Mod &^ (ModCtrl | ModShift)}
	case up == 'I':
		return Key{Special: KeyTab, Mod: k.Mod &^ (ModCtrl | ModShift)}
	case up == 'M':
		return Key{Special: KeyCR, Mod: k.Mod &^ (ModCtrl | ModShift)}
	case up == 'J':
		return Key{Special: KeyNL, Mod: k.Mod &^ (ModCtrl | ModShift)}
	case up >= 'A' && up <= 'Z', up == '\\', up == ']', up == '^', up == '_', up == '?':
		// A real control code exists for these, so Shift has nowhere to go and
		// vim drops it: <C-S-a> is 0x01, exactly as <C-a> is.
		k.Rune = up
		k.Mod &^= ModShift
		return k
	}
	return k
}

// String prints one key in the notation Parse reads, so that `:map` output and
// a keys file round-trip.
//
// The spellings follow vim's keytrans(): Ctrl keys print with an uppercase
// letter, a bare '<' prints as <lt>, and '|' and '\' print as themselves rather
// than as <Bar> and <Bslash>. Parse accepts every alternative spelling, so the
// round trip holds in both directions without this having to pick the prettiest
// name.
func (k Key) String() string {
	var b strings.Builder
	prefix := k.modPrefix()

	if k.Special != KeyNone {
		name := specialName(k.Special)
		if name == "" {
			return ""
		}
		b.WriteString("<")
		b.WriteString(prefix)
		b.WriteString(name)
		b.WriteString(">")
		return b.String()
	}

	if prefix == "" {
		switch {
		case k.Rune == ' ':
			return "<Space>"
		case k.Rune == '<':
			return "<lt>"
		case k.Rune < 0x20 || k.Rune == 0x7f:
			// A raw control rune with no modifier bit is not something Parse
			// produces, but a frontend can hand one over and it has to print as
			// something that reads back the same.
			return "<Char-0x" + strconv.FormatInt(int64(k.Rune), 16) + ">"
		}
		return string(k.Rune)
	}

	b.WriteString("<")
	b.WriteString(prefix)
	switch k.Rune {
	case ' ':
		b.WriteString("Space")
	case '<':
		b.WriteString("lt")
	case '|':
		b.WriteString("Bar")
	case '\\':
		b.WriteString("Bslash")
	default:
		b.WriteRune(k.Rune)
	}
	b.WriteString(">")
	return b.String()
}

// modPrefix renders the modifier bits and the click count in the order vim
// prints them, which is click count first and then M, C, S, D. Verified with
// keytrans(): <M-C-S-Left> and <S-D-a> and <2-LeftMouse>.
func (k Key) modPrefix() string {
	var b strings.Builder
	if k.Clicks >= 2 && k.Clicks <= 4 {
		b.WriteByte('0' + k.Clicks)
		b.WriteByte('-')
	}
	if k.Mod&ModAlt != 0 {
		b.WriteString("M-")
	}
	if k.Mod&ModCtrl != 0 {
		b.WriteString("C-")
	}
	if k.Mod&ModShift != 0 {
		b.WriteString("S-")
	}
	if k.Mod&ModCmd != 0 {
		b.WriteString("D-")
	}
	return b.String()
}

// Format prints a sequence of keys as one notation string.
func Format(keys []Key) string {
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k.String())
	}
	return b.String()
}
