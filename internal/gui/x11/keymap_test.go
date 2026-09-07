package x11

import (
	"testing"

	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/key"
)

// The keyboard tests. Every one of them is a table the server would have sent,
// written out by hand, so that the rules in keymap.go are checked on a machine
// with no X server rather than asserted in a comment.
//
// The keycodes and keysym lists are the ones a stock XKB "us" layout produces
// on a PC105 keyboard: keycode 38 is a, keycode 10 is 1, keycode 79 is the
// keypad 7. They are here because a table taken from a real keyboard catches
// the case where the code is right about the protocol and wrong about what
// keyboards actually send, and made-up keycodes would not.

// usKeymap is a small slice of a US layout. min keycode 8, four keysyms each,
// which is the shape every XKB server projects its map down into.
func usKeymap() *Keymap {
	const min = 8
	// One row per keycode from 8 upwards; only the ones named below matter.
	syms := make([]uint32, 4*160)
	set := func(kc int, list ...uint32) {
		copy(syms[(kc-min)*4:(kc-min)*4+4], list)
	}
	set(10, '1', '!', 0, 0)
	set(24, 'q', 'Q', 0x40, 0x40) // q Q @ @, which is where AltGr lives
	set(38, 'a', 'A', 0, 0)       // a A
	set(39, 's', 0, 0, 0)         // one symbol only: the case pair is derived
	set(9, xkEscape, 0, 0, 0)     // Escape
	set(23, xkTab, xkISOLeftTab, 0, 0)
	set(36, xkReturn, 0, 0, 0)
	set(67, xkF1, 0, 0, 0)
	set(79, xkKPHome, 0xffb7, 0, 0) // keypad 7: KP_Home / KP_7
	set(50, xkShiftL, 0, 0, 0)
	set(64, xkAltL, 0, 0, 0)
	set(133, xkSuperL, 0, 0, 0)
	set(77, xkNumLock, 0, 0, 0)
	set(108, xkISOLevel3Shift, 0, 0, 0)
	set(20, 0x0100263a, 0, 0, 0) // a Unicode keysym: U+263A
	set(21, 0xe9, 0xc9, 0, 0)    // Latin-1 e-acute and its capital

	m := NewKeymap(min, 4, syms)
	// The modifier mapping a stock desktop has: two keycodes per modifier,
	// eight modifiers, in the protocol's fixed order.
	const perMod = 2
	mods := make([]byte, perMod*8)
	mods[0*perMod] = 50  // Shift
	mods[3*perMod] = 64  // Mod1 is Alt
	mods[4*perMod] = 77  // Mod2 is NumLock
	mods[6*perMod] = 133 // Mod4 is Super
	mods[7*perMod] = 108 // Mod5 is the level 3 shift
	m.SetModifiers(perMod, mods)
	return m
}

func TestKeymapResolvesModifiers(t *testing.T) {
	m := usKeymap()
	for _, tc := range []struct {
		name string
		got  uint16
		want uint16
	}{
		{"alt is Mod1", m.altMask, maskMod1},
		{"numlock is Mod2", m.numMask, maskMod2},
		{"super is Mod4", m.superMask, maskMod4},
		{"level3 is Mod5", m.level3Mask, maskMod5},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: mask is %#x, want %#x", tc.name, tc.got, tc.want)
		}
	}
}

// TestKeymapModifiersAreNotAssumed is the test that says why SetModifiers
// exists. A keyboard whose Alt is on Mod4 and whose Super is on Mod1 is legal,
// somebody has one, and hard-coding Mod1 would give them a dead Alt key.
func TestKeymapModifiersAreNotAssumed(t *testing.T) {
	const min, per = 8, 2
	syms := make([]uint32, per*160)
	syms[(64-min)*per] = xkSuperL
	syms[(133-min)*per] = xkAltL
	m := NewKeymap(min, per, syms)

	mods := make([]byte, 8)
	mods[3] = 64  // Mod1 holds Super here
	mods[6] = 133 // Mod4 holds Alt
	m.SetModifiers(1, mods)

	if m.altMask != maskMod4 {
		t.Errorf("alt mask is %#x, want Mod4 %#x", m.altMask, maskMod4)
	}
	if m.superMask != maskMod1 {
		t.Errorf("super mask is %#x, want Mod1 %#x", m.superMask, maskMod1)
	}
	if got, _ := m.Key(0, 0); got != (key.Key{}) {
		t.Errorf("an unmapped keycode gave %v, want nothing", got)
	}
}

func TestKeymapKeys(t *testing.T) {
	m := usKeymap()
	for _, tc := range []struct {
		name  string
		kc    byte
		state uint16
		want  key.Key
		drop  bool
	}{
		{name: "plain letter", kc: 38, want: key.Rune('a')},
		{name: "shift gives the second element", kc: 38, state: maskShift, want: key.Rune('A')},
		{name: "capslock uppercases the first", kc: 38, state: maskLock, want: key.Rune('A')},
		{name: "capslock and shift lowercase the second", kc: 38, state: maskShift | maskLock, want: key.Rune('a')},
		{name: "a one-symbol list derives its case pair", kc: 39, state: maskShift, want: key.Rune('S')},
		{name: "capslock does nothing to a digit", kc: 10, state: maskLock, want: key.Rune('1')},
		{name: "shift on a digit", kc: 10, state: maskShift, want: key.Rune('!')},
		{name: "control folds through internal/key", kc: 38, state: maskControl, want: key.Ctrl('a')},
		{name: "alt is a modifier and not a character", kc: 38, state: maskMod1, want: key.Key{Rune: 'a', Mod: key.ModAlt}},
		{name: "super arrives as ModCmd", kc: 38, state: maskMod4, want: key.Key{Rune: 'a', Mod: key.ModCmd}},
		{name: "escape", kc: 9, want: key.Key{Special: key.KeyEsc}},
		{name: "return", kc: 36, want: key.Key{Special: key.KeyCR}},
		{name: "tab", kc: 23, want: key.Key{Special: key.KeyTab}},
		{name: "shift tab is ISO_Left_Tab", kc: 23, state: maskShift, want: key.Key{Special: key.KeyTab, Mod: key.ModShift}},
		{name: "f1", kc: 67, want: key.Key{Special: key.KeyF1}},
		{name: "a unicode keysym", kc: 20, want: key.Rune('☺')},
		{name: "latin-1 stays latin-1", kc: 21, want: key.Rune('é')},
		{name: "latin-1 uppercases", kc: 21, state: maskShift, want: key.Rune('É')},
		{name: "a modifier key types nothing", kc: 50, drop: true},
		{name: "an empty slot types nothing", kc: 100, drop: true},

		// The keypad, which is the one place Num_Lock inverts Shift.
		{name: "keypad with numlock off is navigation", kc: 79, want: key.Key{Special: key.KeyHome}},
		{name: "keypad with numlock on is a digit", kc: 79, state: maskMod2, want: key.Rune('7')},
		{name: "keypad with numlock and shift is navigation again", kc: 79, state: maskMod2 | maskShift,
			want: key.Key{Special: key.KeyHome, Mod: key.ModShift}},

		// AltGr, which an XKB server projects into the group 2 slots.
		{name: "level3 selects group two", kc: 24, state: maskMod5, want: key.Rune('@')},
		{name: "level3 on a key with no group two falls back", kc: 38, state: maskMod5, want: key.Rune('a')},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := m.Key(tc.kc, tc.state)
			if tc.drop {
				if ok {
					t.Fatalf("got %v, want the key dropped", got)
				}
				return
			}
			if !ok {
				t.Fatalf("the key was dropped, want %v", tc.want)
			}
			if got != tc.want {
				t.Errorf("got %v (%+v), want %v (%+v)", got, got, tc.want, tc.want)
			}
		})
	}
}

// TestKeymapNeverSetsShiftOnARune pins the rule internal/gui states and this
// package copies: Shift is folded into the character and never also delivered
// as a modifier, because there is no way back from '!' to "Shift and 1".
func TestKeymapNeverSetsShiftOnARune(t *testing.T) {
	m := usKeymap()
	for _, kc := range []byte{10, 38, 39, 21} {
		for _, state := range []uint16{maskShift, maskShift | maskLock, maskShift | maskMod1} {
			k, ok := m.Key(kc, state)
			if ok && k.IsRune() && k.Mod&key.ModShift != 0 {
				t.Errorf("keycode %d with state %#x gave %v, which carries ModShift on a rune", kc, state, k)
			}
		}
	}
}

// TestZeroKeymapAnswersNothing keeps a keystroke arriving before the first
// GetKeyboardMapping reply from being a crash.
func TestZeroKeymapAnswersNothing(t *testing.T) {
	var m *Keymap
	if k, ok := m.Key(38, 0); ok {
		t.Errorf("a nil keymap gave %v, want nothing", k)
	}
	if got := m.Mod(maskShift | maskControl); got != key.ModShift|key.ModCtrl {
		t.Errorf("a nil keymap's Mod gave %v, want the fixed bits", got)
	}
	if _, ok := (&Keymap{}).Keysym(38, 0); ok {
		t.Error("a zero keymap answered a keysym")
	}
}

// TestModIncludesShift is the difference between what a click carries and what
// a keystroke carries.
func TestModIncludesShift(t *testing.T) {
	m := usKeymap()
	got := m.Mod(maskShift | maskControl | maskMod1 | maskMod4)
	want := key.ModShift | key.ModCtrl | key.ModAlt | key.ModCmd
	if got != want {
		t.Errorf("Mod gave %v, want %v", got, want)
	}
}

func TestButtons(t *testing.T) {
	for _, tc := range []struct {
		n    byte
		want gui.MouseButton
		ok   bool
	}{
		{1, gui.MouseLeft, true},
		{2, gui.MouseMiddle, true},
		{3, gui.MouseRight, true},
		{4, gui.MouseNone, false},
		{8, gui.MouseNone, false},
	} {
		got, ok := buttonOf(tc.n)
		if got != tc.want || ok != tc.ok {
			t.Errorf("buttonOf(%d) = %v, %v; want %v, %v", tc.n, got, ok, tc.want, tc.ok)
		}
	}
}

func TestWheel(t *testing.T) {
	for _, tc := range []struct {
		n    byte
		want gui.MouseAction
		ok   bool
	}{
		{4, gui.MouseWheelUp, true},
		{5, gui.MouseWheelDown, true},
		{6, gui.MouseWheelLeft, true},
		{7, gui.MouseWheelRight, true},
		{1, 0, false},
		{8, 0, false},
	} {
		got, ok := wheelOf(tc.n)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("wheelOf(%d) = %v, %v; want %v, %v", tc.n, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRuneOfKeysym(t *testing.T) {
	for _, tc := range []struct {
		ks   uint32
		want rune
		ok   bool
	}{
		{'a', 'a', true},
		{0x20, ' ', true},
		{0x7e, '~', true},
		{0xe9, 'é', true},
		{0x01000263, 'ɣ', true},
		{0x0100263a, '☺', true},
		{unicodeKeysym | 0x10ffff, '\U0010ffff', true},
		{unicodeKeysym | 0x110000, 0, false},
		{xkF1, 0, false},
		{xkShiftL, 0, false},
		{0x01b1, 0, false}, // Latin-2 aogonek: the documented gap
	} {
		got, ok := runeOf(tc.ks)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("runeOf(%#x) = %q, %v; want %q, %v", tc.ks, got, ok, tc.want, tc.ok)
		}
	}
}
