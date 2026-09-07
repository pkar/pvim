package mode

import (
	"strings"

	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/operator"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/search"
	"github.com/pkar/pvim/internal/textobj"
)

// Options is every setting the mode machine reads.
//
// One flat struct of typed fields, not a map of strings, because an option
// that parses and does nothing is the failure mode this whole layer has:
// `:set shiftround` with nothing reading it looks exactly like an editor that
// works. A field here has a reader somewhere below, and internal/options fills
// it from the vimrc rather than replacing it.
//
// The five methods below are the seam. Each package takes its own options
// struct so it can be tested alone, and this is the single place that maps one
// onto the other, so an option added is wired in one file.
type Options struct {
	// Indent and white space.
	TabStop     int
	ShiftWidth  int
	SoftTabStop int
	ExpandTab   bool
	ShiftRound  bool
	AutoIndent  bool
	SmartIndent bool

	// CinWords is 'cinwords', the keywords that open a block for 'smartindent'.
	// The vimrc sets it for python -- if, elif, else, for, while, try, except,
	// finally, def, class -- and it was parsed by internal/options and read by
	// internal/operator with nothing joining the two, so the python setting did
	// nothing at all.
	CinWords string
	// Backspace is 'backspace', the vimrc's "indent,eol,start": what insert
	// mode's backspace is allowed to delete over.
	Backspace string
	// TextWidth is what gq reflows to and what an insert-mode wrap uses.
	TextWidth int
	// JoinSpaces is 'joinspaces'.
	JoinSpaces bool

	// Searching.
	IgnoreCase bool
	SmartCase  bool
	WrapScan   bool
	NoMagic    bool
	// HlSearch and IncSearch are read by internal/screen, not here, and they
	// are in this struct because there is one option bag and not two.
	HlSearch  bool
	IncSearch bool

	// Motions and objects.
	IsKeyword   string
	WhichWrap   string
	StartOfLine bool
	ScrollOff   int
	MatchPairs  string
	Paragraphs  string
	Sections    string
	Selection   string
	// VirtualEdit is 'virtualedit', which this editor leaves empty and every
	// column function assumes is empty. It is here so the day it is set,
	// something fails loudly.
	VirtualEdit string
	// Wrap and Width are what gj, gk and g$ mean by a display line. Width is
	// the window's text width in cells and comes from the frontend.
	Wrap  bool
	Width int

	// Registers and messages.
	// Clipboard is 'clipboard', the vimrc's "unnamed,unnamedplus,autoselect".
	Clipboard string
	// FixEndOfLine is 'fixendofline', on by default: a file read without a
	// final line separator is written back with one. It is the option, not the
	// buffer's own record of what it read, which internal/text keeps as
	// NoEOL; the two together are vim's 'fixeol' and 'endofline'.
	FixEndOfLine bool
	// CompleteOpt is 'completeopt'. The vimrc sets "menu,menuone,noselect,
	// noinsert", and the two that matter here are the last two: with either of
	// them set the first CTRL-N shows a menu and puts nothing in the buffer,
	// and without them it inserts the first match. vim's own default is
	// "menu,preview", so the two oracle profiles exercise both halves.
	CompleteOpt string
	// Report is 'report': the change count above which a message is printed.
	Report int
	// ConcealLevel is 'conceallevel', which this editor never acts on -- with
	// no syntax highlighting nothing is ever concealed -- and which one thing
	// still reads: the mode message an insert says on the way out. With it
	// set, vim redraws the cursor line inside edit() and says "-- INSERT --"
	// a second time for an "o" whose insert went on to change the line count,
	// where with it clear the same keys say it once. See endInsert.
	ConcealLevel int
	// KeywordPrg is 'keywordprg', which K runs over the identifier under the
	// cursor. vim's default on this machine is "man".
	KeywordPrg string
}

// DefaultOptions is vim's own defaults, before any vimrc has had a say. The
// oracle's vanilla profile is exactly this, which is why it is worth having as
// a value rather than a zero struct.
func DefaultOptions() Options {
	return Options{
		TabStop:      8,
		ShiftWidth:   8,
		SoftTabStop:  0,
		Backspace:    "indent,eol,start",
		WrapScan:     true,
		IsKeyword:    "@,48-57,_,192-255",
		WhichWrap:    "b,s",
		StartOfLine:  true,
		FixEndOfLine: true,
		MatchPairs:   "(:),{:},[:]",
		Paragraphs:   "IPLPPPQPP TPHPLIPpLpItpplpipbp",
		Sections:     "SHNHH HUnhsh",
		Selection:    "inclusive",
		Wrap:         true,
		Report:       2,
		CompleteOpt:  "menu,preview",
		// On, against the documented default of off. ":echo &joinspaces"
		// through "vim --clean" on this machine answers 1, and J after a line
		// ending in a full stop puts two spaces in, so that is what this is.
		JoinSpaces: true,
		KeywordPrg: "man",
	}
}

// Motion is the view internal/motion takes.
func (o Options) Motion() motion.Options {
	return motion.Options{
		TabStop:     o.TabStop,
		IsKeyword:   o.IsKeyword,
		WhichWrap:   o.WhichWrap,
		StartOfLine: o.StartOfLine,
		ScrollOff:   o.ScrollOff,
		Paragraphs:  o.Paragraphs,
		Sections:    o.Sections,
		MatchPairs:  o.MatchPairs,
		Wrap:        o.Wrap,
		Width:       o.Width,
	}
}

// TextObj is the view internal/textobj takes.
func (o Options) TextObj() textobj.Options {
	return textobj.Options{
		IsKeyword:  o.IsKeyword,
		TabStop:    o.TabStop,
		MatchPairs: o.MatchPairs,
		Paragraphs: o.Paragraphs,
		Sections:   o.Sections,
		Selection:  o.Selection,
	}
}

// Operator is the view internal/operator takes.
func (o Options) Operator() operator.Options {
	return operator.Options{
		ShiftWidth:  o.ShiftWidth,
		TabStop:     o.TabStop,
		SoftTabStop: o.SoftTabStop,
		ExpandTab:   o.ExpandTab,
		ShiftRound:  o.ShiftRound,
		AutoIndent:  o.AutoIndent,
		SmartIndent: o.SmartIndent,
		CinWords:    splitCinWords(o.CinWords),
		JoinSpaces:  o.JoinSpaces,
		TextWidth:   o.TextWidth,
		Report:      o.Report,
	}
}

// Search is the view internal/search takes. LastSubstitute is left empty here
// and filled in by the ex layer, which is the only thing that knows what the
// last :s replaced.
func (o Options) Search() search.Options {
	return search.Options{
		IgnoreCase: o.IgnoreCase,
		SmartCase:  o.SmartCase,
		NoMagic:    o.NoMagic,
		WrapScan:   o.WrapScan,
		IsKeyword:  o.IsKeyword,
	}
}

// Register is the view internal/register takes: which register the unnamed one
// really is, read out of 'clipboard'.
func (o Options) Register() register.Options {
	return register.Options{
		Unnamed:     hasFlag(o.Clipboard, "unnamed"),
		UnnamedPlus: hasFlag(o.Clipboard, "unnamedplus"),
	}
}

// hasFlag reports whether a comma-separated option value contains a flag.
//
// Split and compare, not strings.Contains, because "unnamedplus" contains
// "unnamed": an editor that reads 'clipboard' with Contains puts every yank on
// the wrong pasteboard on a machine where the two selections differ. macOS is
// not that machine, which is exactly why it would never be noticed here.
func hasFlag(value, flag string) bool {
	for _, item := range strings.Split(value, ",") {
		if item == flag {
			return true
		}
	}
	return false
}

// splitCinWords turns 'cinwords' into the slice internal/operator wants. An
// empty option is no keywords rather than one empty keyword, which would match
// every line.
func splitCinWords(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
