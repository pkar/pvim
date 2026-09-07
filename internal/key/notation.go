package key

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrNoLeader is returned when notation contains <Leader> or <LocalLeader> and
// the caller supplied no value for it. Vim leaves an unset mapleader as a
// backslash; pvim refuses instead, because a mapping that quietly binds the
// wrong key is worse than a message at startup.
var ErrNoLeader = errors.New("key: <Leader> used with no mapleader set")

// printNames maps a Special to the one spelling String uses. A Special missing
// from here has no notation and does not print.
var printNames = map[Special]string{
	KeyEsc:       "Esc",
	KeyCR:        "CR",
	KeyNL:        "NL",
	KeyTab:       "Tab",
	KeyBS:        "BS",
	KeyDel:       "Del",
	KeyNul:       "Nul",
	KeyUp:        "Up",
	KeyDown:      "Down",
	KeyLeft:      "Left",
	KeyRight:     "Right",
	KeyHome:      "Home",
	KeyEnd:       "End",
	KeyPageUp:    "PageUp",
	KeyPageDown:  "PageDown",
	KeyInsert:    "Insert",
	KeyHelp:      "Help",
	KeyUndo:      "Undo",
	KeyIgnore:    "Ignore",
	KeyPlug:      "Plug",
	KeyCmd:       "Cmd",
	KeyScriptCmd: "ScriptCmd",

	KeyF1:  "F1",
	KeyF2:  "F2",
	KeyF3:  "F3",
	KeyF4:  "F4",
	KeyF5:  "F5",
	KeyF6:  "F6",
	KeyF7:  "F7",
	KeyF8:  "F8",
	KeyF9:  "F9",
	KeyF10: "F10",
	KeyF11: "F11",
	KeyF12: "F12",

	KeyLeftMouse:        "LeftMouse",
	KeyLeftDrag:         "LeftDrag",
	KeyLeftRelease:      "LeftRelease",
	KeyMiddleMouse:      "MiddleMouse",
	KeyMiddleDrag:       "MiddleDrag",
	KeyMiddleRelease:    "MiddleRelease",
	KeyRightMouse:       "RightMouse",
	KeyRightDrag:        "RightDrag",
	KeyRightRelease:     "RightRelease",
	KeyX1Mouse:          "X1Mouse",
	KeyX1Drag:           "X1Drag",
	KeyX1Release:        "X1Release",
	KeyX2Mouse:          "X2Mouse",
	KeyX2Drag:           "X2Drag",
	KeyX2Release:        "X2Release",
	KeyScrollWheelUp:    "ScrollWheelUp",
	KeyScrollWheelDown:  "ScrollWheelDown",
	KeyScrollWheelLeft:  "ScrollWheelLeft",
	KeyScrollWheelRight: "ScrollWheelRight",
	KeyMouseMove:        "MouseMove",
}

// specialName is the printed spelling of s, or "" when s has none.
func specialName(s Special) string { return printNames[s] }

// parseNames is every spelling Parse accepts, lowercased. It is printNames plus
// vim's aliases: <Enter> and <Return> for <CR>, <Escape> for <Esc>, and the
// xterm duplicates <xF1>..<xF4> that vim keeps for terminals with two encodings
// of the same function key.
var parseNames = map[string]Special{}

// parseRunes is the set of <> names that stand for an ordinary character rather
// than a special key. Vim treats them exactly as the character: <Bar> is '|' and
// a mapping on it is a mapping on '|'.
var parseRunes = map[string]rune{
	"space":  ' ',
	"bar":    '|',
	"bslash": '\\',
	"lt":     '<',
}

func init() {
	for s, name := range printNames {
		parseNames[strings.ToLower(name)] = s
	}
	for alias, s := range map[string]Special{
		"enter":     KeyCR,
		"return":    KeyCR,
		"escape":    KeyEsc,
		"backspace": KeyBS,
		"delete":    KeyDel,
		"lf":        KeyNL,
		"newline":   KeyNL,
		"linefeed":  KeyNL,
		"xf1":       KeyF1,
		"xf2":       KeyF2,
		"xf3":       KeyF3,
		"xf4":       KeyF4,
		"xup":       KeyUp,
		"xdown":     KeyDown,
		"xleft":     KeyLeft,
		"xright":    KeyRight,
		"xhome":     KeyHome,
		"xend":      KeyEnd,
	} {
		parseNames[alias] = s
	}
}

// Parse turns vim's notation into the keys it stands for: "<C-w>v", "gg",
// "<leader>," and the contents of a map command's left or right hand side, one
// Key per keystroke.
//
// leader is the current value of mapleader, substituted wherever <Leader>
// appears; <LocalLeader> uses the same value unless the caller has a separate
// one, in which case ParseLeader takes both. Everything Parse does not recognise
// between angle brackets is returned as its literal characters, which is what
// vim does: eval("\<SID>") is the five characters "<SID>", and so are <silent>,
// <buffer> and every other map argument, because those are consumed by the :map
// command and never reach the key parser. ScanMapArgs is how a caller strips
// them first.
//
// There is no backslash escaping here. Inside a:map command vim does not
// process "\<Esc>"; the backslash is a literal backslash and <Esc> is the key.
// Parse does the same.
func Parse(notation, leader string) ([]Key, error) {
	return ParseLeader(notation, leader, leader)
}

// ParseLeader is Parse with a separate value for <LocalLeader>, which the vimrc
// package needs because maplocalleader is its own variable.
func ParseLeader(notation, leader, localLeader string) ([]Key, error) {
	return parse(notation, leader, localLeader, 0)
}

// maxLeaderDepth stops a mapleader that itself contains <Leader> from recursing
// forever. Vim has the same guard; the value only has to be small.
const maxLeaderDepth = 8

func parse(notation, leader, localLeader string, depth int) ([]Key, error) {
	if depth > maxLeaderDepth {
		return nil, errors.New("key: <Leader> expands into itself")
	}

	var keys []Key
	for i := 0; i < len(notation); {
		if notation[i] != '<' {
			r, size := utf8.DecodeRuneInString(notation[i:])
			keys = append(keys, Key{Rune: r})
			i += size
			continue
		}

		end := strings.IndexByte(notation[i:], '>')
		if end < 0 {
			// No closing bracket at all: the rest is literal, same as vim.
			keys = append(keys, Key{Rune: '<'})
			i++
			continue
		}
		inner := notation[i+1 : i+end]

		switch strings.ToLower(inner) {
		case "leader", "localleader":
			text := leader
			if strings.EqualFold(inner, "localleader") {
				text = localLeader
			}
			if text == "" {
				return nil, fmt.Errorf("%w: <%s>", ErrNoLeader, inner)
			}
			sub, err := parse(text, leader, localLeader, depth+1)
			if err != nil {
				return nil, err
			}
			keys = append(keys, sub...)
			i += end + 1
			continue
		}

		k, ok := parseAngle(inner)
		if !ok {
			// Vim keeps an unrecognised <Foo> as the literal characters, which
			// is how <SID> and <silent> survive to whoever does understand them.
			keys = append(keys, Key{Rune: '<'})
			i++
			continue
		}
		keys = append(keys, k)
		i += end + 1
	}
	return keys, nil
}

// parseAngle resolves the text between angle brackets to one key.
//
// It reads the click count and the modifier prefixes off the front, then the
// base, which is a special name, a <Char-N> escape, a named character or a
// single rune. Anything left over means this was not a key notation at all.
func parseAngle(inner string) (Key, bool) {
	if inner == "" {
		return Key{}, false
	}

	var mod Mod
	var clicks uint8
	rest := inner

	// A click count only ever comes first, and only 2, 3 and 4 exist.
	if len(rest) >= 2 && rest[1] == '-' && rest[0] >= '2' && rest[0] <= '4' {
		clicks = rest[0] - '0'
		rest = rest[2:]
	}

	for len(rest) >= 2 && rest[1] == '-' {
		var bit Mod
		switch rest[0] {
		case 'S', 's':
			bit = ModShift
		case 'C', 'c':
			bit = ModCtrl
		case 'M', 'm', 'A', 'a':
			// Vim treats <M-x> and <A-x> as the same key, and so does everything
			// downstream, so there is one bit for both.
			bit = ModAlt
		case 'D', 'd':
			bit = ModCmd
		default:
			bit = 0
		}
		if bit == 0 {
			break
		}
		// "<C->" is not a key, and neither is a modifier with nothing after it.
		if len(rest) == 2 {
			return Key{}, false
		}
		mod |= bit
		rest = rest[2:]
	}

	if rest == "" {
		return Key{}, false
	}

	lower := strings.ToLower(rest)

	if s, ok := parseNames[lower]; ok {
		return canon(Key{Special: s, Mod: mod, Clicks: clicks}), true
	}
	if r, ok := parseRunes[lower]; ok {
		return canon(Key{Rune: r, Mod: mod, Clicks: clicks}), true
	}
	if strings.HasPrefix(lower, "char-") {
		r, ok := parseCharCode(rest[len("char-"):])
		if !ok {
			return Key{}, false
		}
		return canon(Key{Rune: r, Mod: mod, Clicks: clicks}), true
	}

	r, size := utf8.DecodeRuneInString(rest)
	if size != len(rest) || r == utf8.RuneError {
		return Key{}, false
	}
	return canon(Key{Rune: r, Mod: mod, Clicks: clicks}), true
}

// parseCharCode reads the number in <Char-97> and <Char-0x41>, which vim
// accepts in decimal, hex and octal.
func parseCharCode(s string) (rune, bool) {
	n, err := strconv.ParseInt(s, 0, 32)
	if err != nil || n < 0 || n > unicode.MaxRune {
		return 0, false
	}
	return rune(n), true
}

// MapArgs are the <> flags a:map command takes before its left hand side.
//
// They are not keys. Vim's key notation parser returns them as literal text
// (eval("\<silent>") is eight characters), because :map strips them first, and
// pvim keeps that split: whoever parses the ex command calls ScanMapArgs and
// hands what is left to Parse.
type MapArgs struct {
	Buffer  bool // <buffer>: the mapping is local to the current buffer
	NoWait  bool // <nowait>: do not wait for a longer mapping to match
	Silent  bool // <silent>: do not echo the command it runs
	Special bool // <special>: allow special keys in the rhs even with a <> in it
	Script  bool // <script>: remap only mappings defined in this script
	Expr    bool // <expr>: the rhs is an expression, which pvim has no evaluator for
	Unique  bool // <unique>: fail if the mapping already exists
}

// ScanMapArgs strips the leading <buffer>, <nowait>, <silent>, <special>,
// <script>, <expr> and <unique> arguments from a:map command's arguments and
// returns them along with the rest of the line, leading whitespace removed.
//
// Order does not matter and repeats are allowed, which is what vim does. It
// stops at the first token that is not one of them, so "<silent> <leader>x" gets
// Silent set and "<leader>x" back.
func ScanMapArgs(s string) (MapArgs, string) {
	var args MapArgs
	rest := strings.TrimLeft(s, " \t")

	for strings.HasPrefix(rest, "<") {
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			break
		}
		var target *bool
		switch strings.ToLower(rest[1:end]) {
		case "buffer":
			target = &args.Buffer
		case "nowait":
			target = &args.NoWait
		case "silent":
			target = &args.Silent
		case "special":
			target = &args.Special
		case "script":
			target = &args.Script
		case "expr":
			target = &args.Expr
		case "unique":
			target = &args.Unique
		default:
			return args, rest
		}
		*target = true
		rest = strings.TrimLeft(rest[end+1:], " \t")
	}
	return args, rest
}
