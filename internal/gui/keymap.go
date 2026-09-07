package gui

import "github.com/pkar/pvim/internal/key"

// The AppKit modifier bits, from NSEvent.h. They are spelled out here rather
// than in the darwin-only file so that the mapping below is testable on any
// machine: this is pure arithmetic over two integers and it has no business
// needing a window server to be checked.
const (
	modFlagCapsLock = 1 << 16
	modFlagShift    = 1 << 17
	modFlagControl  = 1 << 18
	modFlagOption   = 1 << 19
	modFlagCommand  = 1 << 20
	modFlagFunction = 1 << 23
)

// The Unicode private-use characters AppKit puts in -charactersIgnoringModifiers
// for keys that have no character of their own, from NSEvent.h's
// NSUpArrowFunctionKey block.
const (
	nsUpArrow    = 0xF700
	nsDownArrow  = 0xF701
	nsLeftArrow  = 0xF702
	nsRightArrow = 0xF703
	nsF1         = 0xF704
	nsF12        = 0xF70F
	nsInsert     = 0xF727
	nsDelete     = 0xF728
	nsHome       = 0xF729
	nsEnd        = 0xF72B
	nsPageUp     = 0xF72C
	nsPageDown   = 0xF72D

	// The whole private-use block AppKit reserves for function keys. Anything
	// in it that is not named above is a key this editor has no notation for,
	// and it is dropped rather than delivered as a garbage rune.
	nsFunctionKeyLo = 0xF700
	nsFunctionKeyHi = 0xF8FF
)

// keyFromNS turns one -keyDown: into a key.Key.
//
// ch is the first UTF-16 code unit of -charactersIgnoringModifiers and flags is
// -modifierFlags. "Ignoring modifiers" is AppKit's name for a lie: the string
// honours Shift and CapsLock and ignores only Control, Option and Command, so
// Shift-a arrives here as 'A' and Option-a as 'a', which is precisely the shape
// internal/key wants.
//
// The false return is a key with no notation, which the caller drops.
//
// # Shift
//
// ModShift is never set on a rune. It cannot be: AppKit has already folded
// Shift into the character and there is no way back from '!' to "Shift and 1".
// That costs the ability to map <S-Space>, and it is the same thing terminal vim
// cannot do without the CSI-u protocol, so a mapping that works in the GUI and
// not in the terminal is not a trap anybody falls into twice. Special keys are
// different: <S-Tab> and <Tab> really are two keys and the flag is the only way
// to tell them apart, so Shift is kept there.
func keyFromNS(ch rune, flags uint) (key.Key, bool) {
	var mod key.Mod
	if flags&modFlagControl != 0 {
		mod |= key.ModCtrl
	}
	if flags&modFlagOption != 0 {
		mod |= key.ModAlt
	}
	if flags&modFlagCommand != 0 {
		mod |= key.ModCmd
	}
	shift := flags&modFlagShift != 0

	if sp, ok := specialFromNS(ch); ok {
		if shift {
			mod |= key.ModShift
		}
		return key.Key{Special: sp, Mod: mod}, true
	}

	// Everything left in AppKit's private-use block is a key with no name in
	// internal/key: Home-with-a-numpad, the media keys, F13 upwards.
	if ch >= nsFunctionKeyLo && ch <= nsFunctionKeyHi {
		return key.Key{}, false
	}
	if ch == 0 {
		return key.Key{}, false
	}

	// key.Ctrl does vim's folding table, so Control-i is <Tab>, Control-[ is
	// <Esc> and Control-a is 'A' with the bit set. Doing it here rather than in
	// a table of our own is the difference between one definition of what
	// Control means and two that drift.
	if mod&key.ModCtrl != 0 {
		k := key.Ctrl(ch)
		k.Mod |= mod &^ key.ModCtrl
		return k, true
	}
	return key.Key{Rune: ch, Mod: mod}, true
}

// specialFromNS maps a character AppKit uses for a named key onto internal/key's
// name for it.
func specialFromNS(ch rune) (key.Special, bool) {
	switch ch {
	case 0x1b:
		return key.KeyEsc, true
	case '\r', 0x03: // 0x03 is the numeric keypad's Enter
		return key.KeyCR, true
	case '\t':
		return key.KeyTab, true
	case 0x19: // back-tab, what Shift-Tab produces
		return key.KeyTab, true
	case 0x7f: // AppKit's delete-backwards, vim's <BS>
		return key.KeyBS, true
	case nsDelete:
		return key.KeyDel, true
	case nsUpArrow:
		return key.KeyUp, true
	case nsDownArrow:
		return key.KeyDown, true
	case nsLeftArrow:
		return key.KeyLeft, true
	case nsRightArrow:
		return key.KeyRight, true
	case nsHome:
		return key.KeyHome, true
	case nsEnd:
		return key.KeyEnd, true
	case nsPageUp:
		return key.KeyPageUp, true
	case nsPageDown:
		return key.KeyPageDown, true
	case nsInsert:
		return key.KeyInsert, true
	}
	if ch >= nsF1 && ch <= nsF12 {
		return functionKeys[ch-nsF1], true
	}
	return key.KeyNone, false
}

// functionKeys is F1 through F12 written out rather than derived by adding to
// key.KeyF1. internal/key's constant block is append-only and its comment says
// so, which means the twelve names are contiguous and a table is the only
// spelling that stays right if that ever stops being true.
var functionKeys = [12]key.Special{
	key.KeyF1, key.KeyF2, key.KeyF3, key.KeyF4,
	key.KeyF5, key.KeyF6, key.KeyF7, key.KeyF8,
	key.KeyF9, key.KeyF10, key.KeyF11, key.KeyF12,
}

// modFromNS is the modifier set for an event that carries no character: a mouse
// click, a wheel notch.
func modFromNS(flags uint) key.Mod {
	var mod key.Mod
	if flags&modFlagShift != 0 {
		mod |= key.ModShift
	}
	if flags&modFlagControl != 0 {
		mod |= key.ModCtrl
	}
	if flags&modFlagOption != 0 {
		mod |= key.ModAlt
	}
	if flags&modFlagCommand != 0 {
		mod |= key.ModCmd
	}
	return mod
}

// menuItem is one item of the menu bar: what it is called, the selector it
// sends, and the character that is its Command key equivalent.
//
// The table lives in this file, which has no build tag, because two things
// read it and they must not disagree: menu_darwin.go builds the menu from it,
// and menuOwnsKeyEquivalent below decides which keys the view has to refuse so
// that the menu can have them. A key in one and not the other is either a menu
// item that never fires or a keystroke that beeps, and neither shows up in a
// test that only knows about one of the two.
type menuItem struct {
	title    string
	selector string
	key      rune
}

// appMenuItems is the application menu. Quit is targeted at pvim's own window
// delegate rather than at NSApp's own quit action, which would kill the
// process where it stands and lose a modified buffer that 'confirm' was about
// to prompt for on the command line.
var appMenuItems = []menuItem{
	{title: "Quit pvim", selector: "pvimQuit:", key: 'q'},
}

// editMenuItems is the Edit menu. It exists so that macOS delivers these four
// keys at all; what each one types is menukeys.go.
var editMenuItems = []menuItem{
	{title: "Cut", selector: "cut:", key: 'x'},
	{title: "Copy", selector: "copy:", key: 'c'},
	{title: "Paste", selector: "paste:", key: 'v'},
	{title: "Select All", selector: "selectAll:", key: 'a'},
}

// menuKeyEquivalents is every Command key the menu bar has claimed.
var menuKeyEquivalents = collectMenuKeys()

// collectMenuKeys collects the key equivalents out of the two menus.
func collectMenuKeys() []rune {
	var out []rune
	for _, items := range [][]menuItem{appMenuItems, editMenuItems} {
		for _, it := range items {
			out = append(out, it.key)
		}
	}
	return out
}

// menuOwnsKeyEquivalent reports whether the menu bar has claimed this
// Cmd-modified key, in which case the view must not.
//
// The order AppKit resolves a key equivalent in is the trap. NSApplication
// hands a Command-modified key to the key window first, which walks its view
// hierarchy calling performKeyEquivalent:, and only if every view returns NO
// does it offer the event to the main menu. So a view that claims everything
// Cmd-modified silently disables the entire menu bar: Cmd-Q stops quitting and
// the four Edit items never fire, which is exactly the routing the menu was
// built to get.
//
// The keys here are the keys the menu defines, out of the one table both
// halves read. A key held with another modifier is not one of them -- a menu item's equivalent is
// Command and the character alone -- so Cmd-Shift-A is delivered to the editor
// while Cmd-A goes to Select All.
func menuOwnsKeyEquivalent(ch rune, flags uint) bool {
	if flags&modFlagCommand == 0 {
		return false
	}
	if flags&(modFlagControl|modFlagOption|modFlagShift) != 0 {
		return false
	}
	for _, k := range menuKeyEquivalents {
		if ch == k {
			return true
		}
	}
	return false
}

// imeCandidate reports whether a key should be offered to the input method
// before it is turned into a key.Key.
//
// Only text goes to the input method, and "text" here means: no Command and no
// Control, because a Cmd or Ctrl chord is a command and interpretKeyEvents:
// would turn it into a doCommandBySelector: this editor would then have to map
// back; nothing in AppKit's function-key block, which is the arrows and the F
// keys; and nothing that internal/key already has a name for, which is Escape,
// Tab, Return, Backspace and Delete. Those five are the keys an input method
// would want while composing and the keys vim cannot afford to lose, and full
// marked-text composition is out of scope precisely so that this choice can be
// made in vim's favour.
//
// Option is deliberately not excluded. Option-e is the dead-key acute on a US
// layout and the whole reason the input method is in the path at all.
func imeCandidate(ch rune, flags uint) bool {
	if flags&(modFlagCommand|modFlagControl) != 0 {
		return false
	}
	if ch == 0 || ch < 0x20 {
		return false
	}
	if ch >= nsFunctionKeyLo && ch <= nsFunctionKeyHi {
		return false
	}
	if _, ok := specialFromNS(ch); ok {
		return false
	}
	return true
}
