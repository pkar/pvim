package screen

// The highlight groups the renderer draws with, and vim's own defaults for
// them under 'background' dark.
//
// Every colour here was read out of vim 9.2.0321 rather than out of a
// colourscheme file, by asking
//
//	synIDattr(synIDtrans(hlID("Search")), "bg#", "gui")
//
// under `vim --clean` with `set background=dark termguicolors`, which is the
// only way to get vim's compiled-in colour names as numbers without shipping a
// copy of X11's rgb.txt. The vimrc loads nofrils-dark over the top of most of
// them, and these are what is on screen until it does.
//
// A group with no colour of its own inherits Normal's, which is why every
// default below is folded against the table's Normal at the time InitGroups
// runs. Reload the colourscheme, reload these.

// The group names, spelled as vim spells them so a `hi` line in a
// colourscheme finds the same entry.
const (
	GroupNonText      = "NonText"
	GroupEndOfBuffer  = "EndOfBuffer"
	GroupLineNr       = "LineNr"
	GroupCursorLineNr = "CursorLineNr"
	GroupCursorLine   = "CursorLine"
	GroupSearch       = "Search"
	GroupIncSearch    = "IncSearch"
	GroupCurSearch    = "CurSearch"
	GroupFolded       = "Folded"
	// The four diff-mode groups. nofrils-dark defines all four, so on this
	// owner's colourscheme they are green, olive, maroon and navy foregrounds
	// with no background, which is what makes a diff readable there at all.
	GroupDiffAdd      = "DiffAdd"
	GroupDiffChange   = "DiffChange"
	GroupDiffDelete   = "DiffDelete"
	GroupDiffText     = "DiffText"
	GroupFoldColumn   = "FoldColumn"
	GroupStatusLine   = "StatusLine"
	GroupStatusLineNC = "StatusLineNC"
	GroupVertSplit    = "VertSplit"
	GroupTabLine      = "TabLine"
	GroupTabLineSel   = "TabLineSel"
	GroupTabLineFill  = "TabLineFill"
	GroupPmenu        = "Pmenu"
	GroupPmenuSel     = "PmenuSel"
	GroupPmenuSbar    = "PmenuSbar"
	GroupPmenuThumb   = "PmenuThumb"
	GroupErrorMsg     = "ErrorMsg"
	GroupWarningMsg   = "WarningMsg"
	GroupModeMsg      = "ModeMsg"
	GroupMoreMsg      = "MoreMsg"
	GroupQuestion     = "Question"
	GroupTitle        = "Title"
	GroupMatchParen   = "MatchParen"
	GroupVisual       = "Visual"
	GroupSpecialKey   = "SpecialKey"
	GroupDirectory    = "Directory"
	GroupConceal      = "Conceal"
	GroupSpellBad     = "SpellBad"
)

// Groups holds the ids of the groups the renderer uses, looked up once when
// the table is built rather than by name on every cell.
type Groups struct {
	NonText      HLID
	EndOfBuffer  HLID
	LineNr       HLID
	CursorLineNr HLID
	CursorLine   HLID
	Search       HLID
	IncSearch    HLID
	CurSearch    HLID
	Folded       HLID
	// DiffAdd is a line only this side has, DiffChange a line both sides have
	// differently, DiffDelete a FILLER row standing where the other side has a
	// line this one does not, and DiffText the columns inside a DiffChange
	// line that actually differ.
	DiffAdd      HLID
	DiffChange   HLID
	DiffDelete   HLID
	DiffText     HLID
	FoldColumn   HLID
	StatusLine   HLID
	StatusLineNC HLID
	VertSplit    HLID
	TabLine      HLID
	TabLineSel   HLID
	TabLineFill  HLID
	Pmenu        HLID
	PmenuSel     HLID
	PmenuSbar    HLID
	PmenuThumb   HLID
	ErrorMsg     HLID
	WarningMsg   HLID
	ModeMsg      HLID
	MoreMsg      HLID
	Question     HLID
	Title        HLID
	MatchParen   HLID
	Visual       HLID
	SpecialKey   HLID
	Directory    HLID
	Conceal      HLID
	SpellBad     HLID
}

// The colours vim's compiled-in dark defaults name, as RGB. Vim's names come
// from X11's rgb.txt and these were read back out of vim itself, so a name
// this file spells wrong would have shown up as the wrong number above.
var (
	colBlack     = RGB{0x00, 0x00, 0x00}
	colWhite     = RGB{0xff, 0xff, 0xff}
	colRed       = RGB{0xff, 0x00, 0x00}
	colGreen     = RGB{0x00, 0xff, 0x00}
	colBlue      = RGB{0x00, 0x00, 0xff}
	colYellow    = RGB{0xff, 0xff, 0x00}
	colMagenta   = RGB{0xff, 0x00, 0xff}
	colCyan      = RGB{0x00, 0xff, 0xff}
	colGrey      = RGB{0xbe, 0xbe, 0xbe}
	colDarkGrey  = RGB{0xa9, 0xa9, 0xa9}
	colLightGrey = RGB{0xd3, 0xd3, 0xd3}
	colGrey40    = RGB{0x66, 0x66, 0x66}
	colSeaGreen  = RGB{0x2e, 0x8b, 0x57}
	colDarkCyan  = RGB{0x00, 0x8b, 0x8b}
	colVisualBG  = RGB{0x57, 0x57, 0x57}
)

// def is one row of vim's default highlight table: a group, the colours it
// sets, and the attributes. A nil colour means "leave Normal's".
type def struct {
	name string
	fg   *RGB
	bg   *RGB
	sp   *RGB
	attr Attr
}

// defaults is `:hi <group>` under vim --clean with background=dark, one row
// per group, in the order `:highlight` lists them for a group that has not
// been touched.
var defaults = []def{
	{GroupNonText, &colBlue, nil, nil, AttrBold},
	{GroupEndOfBuffer, &colBlue, nil, nil, AttrBold}, // links to NonText
	{GroupLineNr, &colYellow, nil, nil, 0},
	{GroupCursorLineNr, &colYellow, nil, nil, AttrBold},
	{GroupCursorLine, nil, &colGrey40, nil, 0},
	{GroupSearch, &colBlack, &colYellow, nil, 0},
	{GroupIncSearch, nil, nil, nil, AttrReverse},
	{GroupCurSearch, &colBlack, &colYellow, nil, 0}, // links to Search
	{GroupFolded, &colCyan, &colDarkGrey, nil, 0},
	{GroupFoldColumn, &colCyan, &colGrey, nil, 0},
	{GroupStatusLine, nil, nil, nil, AttrBold | AttrReverse},
	{GroupStatusLineNC, nil, nil, nil, AttrReverse},
	{GroupVertSplit, nil, nil, nil, AttrReverse},
	{GroupTabLine, nil, &colDarkGrey, nil, AttrUnderline},
	{GroupTabLineSel, nil, nil, nil, AttrBold},
	{GroupTabLineFill, nil, nil, nil, AttrReverse},
	{GroupPmenu, nil, &colMagenta, nil, 0},
	{GroupPmenuSel, nil, &colDarkGrey, nil, 0},
	{GroupPmenuSbar, nil, &colGrey, nil, 0},
	{GroupPmenuThumb, nil, &colWhite, nil, 0},
	{GroupErrorMsg, &colWhite, &colRed, nil, 0},
	{GroupWarningMsg, &colRed, nil, nil, 0},
	{GroupModeMsg, nil, nil, nil, AttrBold},
	{GroupMoreMsg, &colSeaGreen, nil, nil, AttrBold},
	{GroupQuestion, &colGreen, nil, nil, AttrBold},
	{GroupTitle, &colMagenta, nil, nil, AttrBold},
	{GroupMatchParen, nil, &colDarkCyan, nil, 0},
	{GroupVisual, &colLightGrey, &colVisualBG, nil, 0},
	{GroupSpecialKey, &colCyan, nil, nil, AttrBold},
	{GroupDirectory, &colCyan, nil, nil, AttrBold},
	{GroupConceal, &colLightGrey, &colDarkGrey, nil, 0},
	{GroupSpellBad, nil, nil, &colRed, AttrUndercurl},
}

// InitGroups defines vim's dark defaults in t and returns their ids.
//
// It is safe to call on a table a colourscheme has already written to: a group
// the scheme defined is redefined here, which is why internal/vimrc calls this
// first and sources the scheme second, in that order.
func InitGroups(t *Table) Groups {
	normal := t.Look(Normal)
	for _, d := range defaults {
		h := Highlight{FG: normal.FG, BG: normal.BG, SP: normal.FG, Attr: d.attr}
		if d.fg != nil {
			h.FG = *d.fg
		}
		if d.bg != nil {
			h.BG = *d.bg
		}
		if d.sp != nil {
			h.SP = *d.sp
		}
		t.Set(d.name, h)
	}
	return LookupGroups(t)
}

// LookupGroups interns every group the renderer uses and returns their ids,
// without changing any appearance. A group nobody has defined gets Normal's
// colours, which is the Table's own rule.
func LookupGroups(t *Table) Groups {
	return Groups{
		NonText:      t.ID(GroupNonText),
		EndOfBuffer:  t.ID(GroupEndOfBuffer),
		LineNr:       t.ID(GroupLineNr),
		CursorLineNr: t.ID(GroupCursorLineNr),
		CursorLine:   t.ID(GroupCursorLine),
		Search:       t.ID(GroupSearch),
		IncSearch:    t.ID(GroupIncSearch),
		CurSearch:    t.ID(GroupCurSearch),
		Folded:       t.ID(GroupFolded),
		DiffAdd:      t.ID(GroupDiffAdd),
		DiffChange:   t.ID(GroupDiffChange),
		DiffDelete:   t.ID(GroupDiffDelete),
		DiffText:     t.ID(GroupDiffText),
		FoldColumn:   t.ID(GroupFoldColumn),
		StatusLine:   t.ID(GroupStatusLine),
		StatusLineNC: t.ID(GroupStatusLineNC),
		VertSplit:    t.ID(GroupVertSplit),
		TabLine:      t.ID(GroupTabLine),
		TabLineSel:   t.ID(GroupTabLineSel),
		TabLineFill:  t.ID(GroupTabLineFill),
		Pmenu:        t.ID(GroupPmenu),
		PmenuSel:     t.ID(GroupPmenuSel),
		PmenuSbar:    t.ID(GroupPmenuSbar),
		PmenuThumb:   t.ID(GroupPmenuThumb),
		ErrorMsg:     t.ID(GroupErrorMsg),
		WarningMsg:   t.ID(GroupWarningMsg),
		ModeMsg:      t.ID(GroupModeMsg),
		MoreMsg:      t.ID(GroupMoreMsg),
		Question:     t.ID(GroupQuestion),
		Title:        t.ID(GroupTitle),
		MatchParen:   t.ID(GroupMatchParen),
		Visual:       t.ID(GroupVisual),
		SpecialKey:   t.ID(GroupSpecialKey),
		Directory:    t.ID(GroupDirectory),
		Conceal:      t.ID(GroupConceal),
		SpellBad:     t.ID(GroupSpellBad),
	}
}
