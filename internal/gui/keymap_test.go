package gui

import (
	"testing"

	"github.com/pkar/pvim/internal/key"
)

// TestKeyFromNS covers the mapping from an AppKit key event to a key.Key.
//
// The expectations are what MacVim delivers for the same physical keys, which
// is what the vimrc's mappings are written against. The Control cases in
// particular are vim's folding table and not AppKit's: Control-i really is
// <Tab> to vim, and a frontend that delivered 'i' with a Control bit would
// break every <C-i> jump-forward in the editor.
func TestKeyFromNS(t *testing.T) {
	tests := []struct {
		name  string
		ch    rune
		flags uint
		want  key.Key
		ok    bool
	}{
		{"plain letter", 'a', 0, key.Key{Rune: 'a'}, true},
		{"shifted letter arrives already uppercase", 'A', modFlagShift, key.Key{Rune: 'A'}, true},
		{"shifted digit arrives as its symbol", '!', modFlagShift, key.Key{Rune: '!'}, true},
		{"space", ' ', 0, key.Key{Rune: ' '}, true},

		{"control-w", 'w', modFlagControl, key.Ctrl('w'), true},
		{"control-i is tab", 'i', modFlagControl, key.Key{Special: key.KeyTab}, true},
		{"control-bracket is escape", '[', modFlagControl, key.Key{Special: key.KeyEsc}, true},
		{"control-shift-a is control-a", 'A', modFlagControl | modFlagShift, key.Ctrl('a'), true},

		{"option-a keeps the base rune", 'a', modFlagOption, key.Key{Rune: 'a', Mod: key.ModAlt}, true},
		{"command-p", 'p', modFlagCommand, key.Key{Rune: 'p', Mod: key.ModCmd}, true},
		{"caps lock is not a modifier", 'A', modFlagCapsLock, key.Key{Rune: 'A'}, true},

		{"escape", 0x1b, 0, key.Key{Special: key.KeyEsc}, true},
		{"return", '\r', 0, key.Key{Special: key.KeyCR}, true},
		{"keypad enter is return", 0x03, 0, key.Key{Special: key.KeyCR}, true},
		{"tab", '\t', 0, key.Key{Special: key.KeyTab}, true},
		{"back-tab is shift-tab", 0x19, modFlagShift, key.Key{Special: key.KeyTab, Mod: key.ModShift}, true},
		{"delete key is backspace", 0x7f, 0, key.Key{Special: key.KeyBS}, true},
		{"forward delete", nsDelete, modFlagFunction, key.Key{Special: key.KeyDel}, true},

		{"up", nsUpArrow, modFlagFunction, key.Key{Special: key.KeyUp}, true},
		{"down", nsDownArrow, modFlagFunction, key.Key{Special: key.KeyDown}, true},
		{"left", nsLeftArrow, modFlagFunction, key.Key{Special: key.KeyLeft}, true},
		{"right", nsRightArrow, modFlagFunction, key.Key{Special: key.KeyRight}, true},
		{"page up", nsPageUp, modFlagFunction, key.Key{Special: key.KeyPageUp}, true},
		{"home", nsHome, modFlagFunction, key.Key{Special: key.KeyHome}, true},
		{"f1", nsF1, modFlagFunction, key.Key{Special: key.KeyF1}, true},
		{"f12", nsF12, modFlagFunction, key.Key{Special: key.KeyF12}, true},
		{"shift-f1", nsF1, modFlagFunction | modFlagShift, key.Key{Special: key.KeyF1, Mod: key.ModShift}, true},

		{"f13 has no notation", 0xF710, modFlagFunction, key.Key{}, false},
		{"an unnamed function key", 0xF7A0, modFlagFunction, key.Key{}, false},
		{"an empty string", 0, 0, key.Key{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := keyFromNS(tt.ch, tt.flags)
			if ok != tt.ok {
				t.Fatalf("keyFromNS(%#x, %#x) ok = %v, want %v", tt.ch, tt.flags, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("keyFromNS(%#x, %#x) = %+v (%s), want %+v (%s)",
					tt.ch, tt.flags, got, got.String(), tt.want, tt.want.String())
			}
		})
	}
}

// TestShiftIsNeverOnARune pins the one deliberate loss in the mapping, so that
// it is a decision on the record rather than a bug somebody finds.
// AppKit folds Shift into the character before this package sees it, and there
// is no way back.
func TestShiftIsNeverOnARune(t *testing.T) {
	for _, ch := range []rune{'A', '!', ' ', '_'} {
		k, ok := keyFromNS(ch, modFlagShift)
		if !ok {
			t.Fatalf("keyFromNS(%q) refused", ch)
		}
		if k.Mod&key.ModShift != 0 {
			t.Errorf("keyFromNS(%q, shift) = %+v; a rune key must not carry ModShift", ch, k)
		}
	}
}

// TestModFromNS covers the modifier set delivered with a mouse event, where
// Shift does survive because there is no character for it to hide in.
func TestModFromNS(t *testing.T) {
	got := modFromNS(modFlagShift | modFlagControl | modFlagOption | modFlagCommand | modFlagCapsLock)
	want := key.ModShift | key.ModCtrl | key.ModAlt | key.ModCmd
	if got != want {
		t.Errorf("modFromNS = %b, want %b", got, want)
	}
	if modFromNS(0) != 0 {
		t.Errorf("modFromNS(0) = %b, want 0", modFromNS(0))
	}
}

// TestMenuOwnsKeyEquivalent is the routing that keeps the menu bar alive.
//
// AppKit offers a Command-modified key to the key window's views before it
// offers it to the menu, so every key this answers true for is a key the view
// must refuse, and every key it answers false for is one the editor gets
// instead of a beep. Getting it wrong in one direction disables Cmd-Q; in the
// other it turns Cmd-P into a system beep.
func TestMenuOwnsKeyEquivalent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ch    rune
		flags uint
		want  bool
	}{
		{"cmd-q is the Quit item", 'q', modFlagCommand, true},
		{"cmd-c is the Copy item", 'c', modFlagCommand, true},
		{"cmd-v is the Paste item", 'v', modFlagCommand, true},
		{"cmd-x is the Cut item", 'x', modFlagCommand, true},
		{"cmd-a is Select All", 'a', modFlagCommand, true},
		{"cmd-p is nobody's menu item", 'p', modFlagCommand, false},
		{"a bare c is not a key equivalent at all", 'c', 0, false},
		{"cmd-shift-c is not the Copy item", 'C', modFlagCommand | modFlagShift, false},
		{"cmd-ctrl-c is not the Copy item", 'c', modFlagCommand | modFlagControl, false},
		{"cmd-alt-c is not the Copy item", 'c', modFlagCommand | modFlagOption, false},
	} {
		if got := menuOwnsKeyEquivalent(tc.ch, tc.flags); got != tc.want {
			t.Errorf("%s: menuOwnsKeyEquivalent(%q, %#x) = %v, want %v", tc.name, tc.ch, tc.flags, got, tc.want)
		}
	}
}

// TestMenuOwnsExactlyWhatTheMenuDefines ties the routing table to the menu
// itself. The two are written in different files and the failure mode of them
// disagreeing is silent: a menu item that never fires, or a key that beeps.
func TestMenuOwnsExactlyWhatTheMenuDefines(t *testing.T) {
	for _, ch := range menuKeyEquivalents {
		if !menuOwnsKeyEquivalent(ch, modFlagCommand) {
			t.Errorf("the menu defines Cmd-%c and the view would swallow it", ch)
		}
	}
	for _, ch := range []rune{'p', 'w', 's', 'z', 'n', 't'} {
		if menuOwnsKeyEquivalent(ch, modFlagCommand) {
			t.Errorf("the view refuses Cmd-%c, which no menu item claims, so it beeps", ch)
		}
	}
}

// TestIMECandidate is which keys go to the input method.
//
// The rule it is protecting: vim's five control keys and every chord must
// reach internal/key unchanged, and everything that could be a character must
// go through the input method first or the dead keys do not work.
func TestIMECandidate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ch    rune
		flags uint
		want  bool
	}{
		{"a plain letter is text", 'a', 0, true},
		{"a digit is text", '7', 0, true},
		{"shift is folded into the character and stays text", 'A', modFlagShift, true},
		{"option is the dead-key modifier and must reach the input method", 'e', modFlagOption, true},
		{"a control chord is a command", 'a', modFlagControl, false},
		{"a command chord is a command", 'c', modFlagCommand, false},
		{"escape is vim's", 0x1b, 0, false},
		{"return is vim's", '\r', 0, false},
		{"tab is vim's", '\t', 0, false},
		{"backspace is vim's", 0x7f, 0, false},
		{"an arrow is not text", nsUpArrow, 0, false},
		{"F1 is not text", nsF1, 0, false},
		{"a nul character is nothing at all", 0, 0, false},
	} {
		if got := imeCandidate(tc.ch, tc.flags); got != tc.want {
			t.Errorf("%s: imeCandidate(%q, %#x) = %v, want %v", tc.name, tc.ch, tc.flags, got, tc.want)
		}
	}
}
