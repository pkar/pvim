package vimrc

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/screen"
)

// colorNames is vim's colour name table, read out of the installed vim 9.2.0321
// with
//
//	:echo v:colornames[name]
//
// for each of them, and not out of X11's rgb.txt, because they differ: X11's
// "lightred" is #ffbbbb and vim's is #ff8b8b, and three of these were wrong
// when they were written from memory.
//
// Only the names the four nofrils files use have to be here -- black, white
// and green -- but the sixteen vim starts with are cheap and stop the first
// colourscheme that says "red" from being an E254.
var colorNames = map[string]screen.RGB{
	"black":        {R: 0x00, G: 0x00, B: 0x00},
	"darkblue":     {R: 0x00, G: 0x00, B: 0x8b},
	"darkgreen":    {R: 0x00, G: 0x64, B: 0x00},
	"darkcyan":     {R: 0x00, G: 0x8b, B: 0x8b},
	"darkred":      {R: 0x8b, G: 0x00, B: 0x00},
	"darkmagenta":  {R: 0x8b, G: 0x00, B: 0x8b},
	"brown":        {R: 0xa5, G: 0x2a, B: 0x2a},
	"darkyellow":   {R: 0x8b, G: 0x8b, B: 0x00},
	"lightgray":    {R: 0xd3, G: 0xd3, B: 0xd3},
	"lightgrey":    {R: 0xd3, G: 0xd3, B: 0xd3},
	"gray":         {R: 0xbe, G: 0xbe, B: 0xbe},
	"grey":         {R: 0xbe, G: 0xbe, B: 0xbe},
	"darkgray":     {R: 0xa9, G: 0xa9, B: 0xa9},
	"darkgrey":     {R: 0xa9, G: 0xa9, B: 0xa9},
	"blue":         {R: 0x00, G: 0x00, B: 0xff},
	"lightblue":    {R: 0xad, G: 0xd8, B: 0xe6},
	"green":        {R: 0x00, G: 0xff, B: 0x00},
	"lightgreen":   {R: 0x90, G: 0xee, B: 0x90},
	"cyan":         {R: 0x00, G: 0xff, B: 0xff},
	"lightcyan":    {R: 0xe0, G: 0xff, B: 0xff},
	"red":          {R: 0xff, G: 0x00, B: 0x00},
	"lightred":     {R: 0xff, G: 0x8b, B: 0x8b},
	"magenta":      {R: 0xff, G: 0x00, B: 0xff},
	"lightmagenta": {R: 0xff, G: 0x8b, B: 0xff},
	"yellow":       {R: 0xff, G: 0xff, B: 0x00},
	"lightyellow":  {R: 0xff, G: 0xff, B: 0xe0},
	"white":        {R: 0xff, G: 0xff, B: 0xff},
}

// attrNames is what "gui=" accepts, restricted to the attributes something in
// this editor can draw. "inverse" is vim's spelling of "reverse" and both
// appear in the wild.
var attrNames = map[string]screen.Attr{
	"bold":          screen.AttrBold,
	"italic":        screen.AttrItalic,
	"underline":     screen.AttrUnderline,
	"undercurl":     screen.AttrUndercurl,
	"reverse":       screen.AttrReverse,
	"inverse":       screen.AttrReverse,
	"strikethrough": screen.AttrStrikethrough,
	"standout":      screen.AttrStandout,
}

// ToHighlight turns a parsed "hi" statement into a screen.Highlight, resolved
// against Normal.
//
// It is here rather than in internal/screen because the parsing of
// "guifg=#bbbbbb" and "gui=bold,underline" is vimscript's business, and
// internal/screen should not learn a colour syntax to be handed a colour.
//
// Resolution is the reason normal is a parameter. screen.Highlight is "what a
// cell looks like, with nothing left to inherit", so the four ways a hi line
// declines to name a colour all end here: an omitted guifg, "NONE", "fg" and
// "bg". The first two mean Normal's, and the last two mean Normal's foreground
// and background by name, which is how nofrils writes
// "hi LineNr guifg=#808080 guibg=bg".
//
// cterm* and term= are parsed and dropped. The terminal frontend writes
// 24-bit colour from guifg, so a colour number for a 256-colour terminal is a
// value nothing in this editor reads, and keeping it would be keeping a field
// to be wrong about.
func ToHighlight(h Highlight, normal screen.Highlight) (screen.Highlight, error) {
	out := screen.Highlight{FG: normal.FG, BG: normal.BG, SP: normal.FG}

	fg, err := resolveColor(h.Args["guifg"], normal, out.FG)
	if err != nil {
		return out, err
	}
	out.FG = fg

	bg, err := resolveColor(h.Args["guibg"], normal, out.BG)
	if err != nil {
		return out, err
	}
	out.BG = bg

	// guisp is the undercurl and underline colour. Vim leaves it unset almost
	// everywhere and then draws the curl in the group's own foreground, so
	// that -- and not Normal's -- is the fallback here. Its own default
	// highlights are where an explicit one shows up: ":hi SpellBad" in a
	// --clean vim prints guisp=Red.
	sp, err := resolveColor(h.Args["guisp"], normal, out.FG)
	if err != nil {
		return out, err
	}
	out.SP = sp

	attr, err := resolveAttr(h.Args["gui"])
	if err != nil {
		return out, err
	}
	out.Attr = attr
	return out, nil
}

// resolveColor turns one guifg/guibg/guisp value into a colour, with fallback
// as the answer for "not said".
func resolveColor(value string, normal screen.Highlight, fallback screen.RGB) (screen.RGB, error) {
	switch {
	case value == "", strings.EqualFold(value, "NONE"):
		return fallback, nil
	case strings.EqualFold(value, "fg"), strings.EqualFold(value, "foreground"):
		return normal.FG, nil
	case strings.EqualFold(value, "bg"), strings.EqualFold(value, "background"):
		return normal.BG, nil
	case strings.HasPrefix(value, "#"):
		return parseHex(value)
	}
	if rgb, ok := colorNames[strings.ToLower(value)]; ok {
		return rgb, nil
	}
	return fallback, fmt.Errorf("E254: Cannot allocate color %s", value)
}

// parseHex reads "#rrggbb". Vim also takes "#rgb"; nothing in the four files
// writes one and a three-digit form silently doubling its nibbles is the kind
// of guess this package is here not to make.
func parseHex(s string) (screen.RGB, error) {
	if len(s) != 7 {
		return screen.RGB{}, fmt.Errorf("E254: Cannot allocate color %s", s)
	}
	n, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return screen.RGB{}, fmt.Errorf("E254: Cannot allocate color %s", s)
	}
	return screen.RGB{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n)}, nil
}

// resolveAttr reads a "gui=" list.
func resolveAttr(value string) (screen.Attr, error) {
	if value == "" || strings.EqualFold(value, "NONE") {
		return 0, nil
	}
	var out screen.Attr
	for _, name := range strings.Split(value, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || name == "none" {
			continue
		}
		bit, ok := attrNames[name]
		if !ok {
			return out, fmt.Errorf("E418: Illegal value: %s", name)
		}
		out |= bit
	}
	return out, nil
}
