package screen

// RGB is a 24-bit colour. There is no palette and no terminal colour number:
// the highlight table is built from guifg/guibg in the colourscheme and the
// terminal frontend writes 38;2;r;g;b, so one representation covers both.
type RGB struct {
	R, G, B uint8
}

// Attr is a bitmask of the non-colour parts of a highlight.
type Attr uint16

// The attribute bits. These are the ones vim's `gui=` accepts that anything in
// this editor can draw; `gui=NONE` is the zero value.
const (
	AttrBold Attr = 1 << iota
	AttrItalic
	AttrUnderline
	AttrUndercurl
	AttrReverse
	AttrStrikethrough
	AttrStandout
)

// Highlight is one fully resolved appearance: what a cell looks like, with
// nothing left to inherit.
//
// Resolution happens when the colourscheme is parsed, not when a cell is drawn.
// A `hi` line that omits guifg, or says NONE, or links to another group, has
// been folded into concrete colours against Normal by the time it lands in the
// table, so a frontend never has to know what a link is.
type Highlight struct {
	FG   RGB
	BG   RGB
	SP   RGB // the undercurl and underline colour
	Attr Attr
}

// HLID indexes the highlight table. Zero is always Normal, so a zeroed Grid is
// a screenful of ordinary text rather than a crash.
type HLID int

// Normal is the highlight every cell starts with.
const Normal HLID = 0

// NormalName is the group name Normal is interned under. Every other group
// resolves against it, which is why it cannot be reassigned to another id.
const NormalName = "Normal"

// DefaultNormal is nofrils-dark's Normal, read out of
// dotfiles/home/.vim/pack/*/start/nofrils/colors/nofrils-dark.vim:
//
//	hi Normal term=NONE cterm=NONE ctermfg=255 ctermbg=235 gui=NONE guifg=#eeeeee guibg=#262626
//
// It is here so the editor has something to render before internal/vimrc
// exists to parse that file. When it does, it overwrites this entry and this
// var stops being reachable from anything but a test.
var DefaultNormal = Highlight{
	FG: RGB{0xee, 0xee, 0xee},
	BG: RGB{0x26, 0x26, 0x26},
	SP: RGB{0xee, 0xee, 0xee},
}

// Table maps highlight ids to appearances and group names to ids.
//
// Two lookups, one structure, because the two sides run at different rates:
// the vimrc parser asks for an id by name once per `hi` line at startup, and a
// frontend asks for an appearance by id once per cell per frame. The names live
// in a map and the appearances in a slice for exactly that reason.
//
// A group that is named before it is defined gets an id and Normal's
// appearance, so `hi link Foo Bar` in either order resolves and a group nobody
// ever defined renders as ordinary text instead of as black on black.
type Table struct {
	hl  []Highlight
	ids map[string]HLID
}

// NewTable returns a table holding only Normal, in nofrils-dark's colours.
func NewTable() *Table {
	t := &Table{
		hl:  []Highlight{DefaultNormal},
		ids: map[string]HLID{NormalName: Normal},
	}
	return t
}

// ID returns the id for a group name, interning it on first sight. Names are
// case-sensitive, as vim's are: `hi normal` and `hi Normal` are two groups in
// vim too, and the second is the one that means anything.
func (t *Table) ID(name string) HLID {
	if id, ok := t.ids[name]; ok {
		return id
	}
	id := HLID(len(t.hl))
	t.hl = append(t.hl, t.hl[Normal])
	t.ids[name] = id
	return id
}

// Set defines a group by name and returns its id.
func (t *Table) Set(name string, h Highlight) HLID {
	id := t.ID(name)
	t.hl[id] = h
	return id
}

// Look returns the appearance for id, falling back to Normal for an id the
// table does not have. A frontend must never index the table directly: a
// colourscheme reload can shrink it under a frame already in flight.
func (t *Table) Look(id HLID) Highlight {
	if t == nil {
		return DefaultNormal
	}
	if int(id) < 0 || int(id) >= len(t.hl) {
		return t.hl[Normal]
	}
	return t.hl[id]
}

// Len is the number of ids in the table, which is always at least one.
func (t *Table) Len() int {
	if t == nil {
		return 0
	}
	return len(t.hl)
}

// Names returns the interned group names in id order. It is for tests and for
// the dump helper's legend; nothing in the render path needs it.
func (t *Table) Names() []string {
	if t == nil {
		return nil
	}
	out := make([]string, len(t.hl))
	for name, id := range t.ids {
		out[id] = name
	}
	return out
}
