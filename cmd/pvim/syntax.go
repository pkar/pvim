package main

import (
	"fmt"
	"strings"

	"github.com/pkar/pvim/internal/screen"
	"github.com/pkar/pvim/internal/syntax"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// Syntax highlighting: which file's rules a buffer gets, and what colour the
// groups in it come out.
//
// This was out of scope at first, on the grounds that "syntax on" is commented
// out in the vimrc and that nofrils is a colourscheme whose whole point is not
// having any. The first half of that stopped being true: the vimrc this editor
// is written for now has "syntax enable" uncommented. The second half is still exactly true and is worth knowing
// before anybody looks at a screen and decides this is broken. Read
// nofrils-dark.vim: it defines Comment as #6C6C6C grey, Todo as green on black,
// and String, Character, Statement, Type, Constant, Identifier, PreProc,
// Special, Number, Keyword, Function, Operator and Label with guifg=NONE
// guibg=NONE, which is Normal. So a correct highlighter on this config paints
// comments grey, TODO green, and everything else the colour it already was.
// That is the scheme working, not the highlighter failing. Point it at a
// colourscheme with real colours to see the difference; the tests do.
//
// tree-sitter is the obvious route if syntax is ever wanted, and it is not
// taken, because tree-sitter needs cgo and this binary has none. The route
// taken instead is in internal/syntax: vim's syntax files are `syn keyword`,
// `syn match` and `syn region` over vim regexes, internal/regex already
// translates those, and so pvim reads vim's own 773 syntax files rather than
// carrying per-language rules of its own.
//
// TODO: three lines elsewhere, and this file is inert without them.
//
// 1. internal/vimrc/parse.go line 187 has `{name: "syntax", min: 2, kind:
// kindInert}`. It wants to be a real statement, kindSyntax, with a
// `Syntax(Syntax) error` on the Sink and a `Syntax{Arg string, Pos}`
// carrying the word after ":syntax" -- on, off, enable, reset, clear,
// manual -- so that cmd/pvim can call syntaxes.command with it. Until then
// the vimrc's "syntax enable" parses, is dropped, and highlights nothing.
// 2. cmd/pvim/editor.go wants one field
//
// // syn is syntax highlighting: the rules per filetype, a highlighter
// // per buffer, and the group-to-HLID map the renderer draws through.
// syn *syntaxes
//
// built in newEditor after e.hl exists, `e.syn = newSyntaxes(e.hl,
// func(name string) bool { return e.hlSet[name] })`.
// 3. cmd/pvim/draw.go's frame() wants `f.Syntax = e.syn.forTabs(e.tabs)`, and
// cmd/pvim/filetype.go's setFileType wants `e.syn.attach(e.buf,
// e.opt.B.FileType)` after the FileType event, and every path that edits
// the buffer wants `e.syn.changed(e.buf, line)`. The last one can be
// `e.syn.changed(e.buf, 1)` to start with, which is correct and slow.

// syntaxes is the editor's syntax highlighting state.
//
// One loaded rule set per filetype, shared by every buffer of that type,
// because loading markdown means reading html, xml, javascript, css and about
// five thousand rules and doing it once per buffer would be felt. One
// highlighter per buffer, because the cache in it is the buffer's.
type syntaxes struct {
	// on is ":syntax on" or ":syntax enable". Off means every cell is Normal
	// and nothing is loaded.
	on bool

	// rt is where vim's syntax files are.
	rt syntax.Runtime

	// hl is the highlight table a group name is resolved against, and defined
	// says whether a `hi` line has given a group colours of its own. The two
	// together are what walks a `hi def link` chain: goComment links to
	// Comment, and the chain stops at the first name the colourscheme has an
	// opinion about.
	hl      *screen.Table
	defined func(group string) bool

	// loaded is one rule set per filetype, with a nil entry for a filetype
	// with no syntax file so that it is not looked for twice.
	loaded map[string]*syntax.Syntax

	// bufs is one highlighter per buffer.
	bufs map[*text.Buffer]*bufSyntax

	// errs is what could not be loaded, so ":syntax" can say so and a test can
	// assert it.
	errs []error
}

// bufSyntax is one buffer's highlighter and the group-to-highlight map for the
// rule set behind it.
type bufSyntax struct {
	h *syntax.Highlighter
	// ids maps a syntax group's index to a highlight id, resolved once when
	// the buffer is attached. The renderer asks per span and a map lookup per
	// span per frame is what this slice exists to avoid.
	ids []screen.HLID
	// filetype is what the rules were loaded for, so a re-detect that lands on
	// the same type keeps the cache.
	filetype string
}

// newSyntaxes returns the state, off.
//
// defined answers whether a group has been given colours by a `hi` line, which
// is the question screen.Table cannot be asked: its ID interns a name on first
// sight, so looking a group up to find out whether it exists creates it.
// cmd/pvim keeps the answer in editor.hlSet for ":hi default" and this wants
// the same one.
func newSyntaxes(hl *screen.Table, defined func(string) bool) *syntaxes {
	if defined == nil {
		defined = func(string) bool { return false }
	}
	return &syntaxes{
		rt:      syntax.DefaultRuntime(),
		hl:      hl,
		defined: defined,
		loaded:  map[string]*syntax.Syntax{},
		bufs:    map[*text.Buffer]*bufSyntax{},
	}
}

// command runs ":syntax {arg}".
//
// The five forms that mean anything here. vim's ":syntax on" loads
// $VIMRUNTIME/syntax/syntax.vim, which resets every highlight group to vim's
// defaults and then loads the filetype's file; ":syntax enable" does the same
// without the reset, so a colourscheme sourced before it survives. That
// difference is the whole reason the vimrc says enable and not on, and it is
// honoured here: "on" puts vim's defaults back into the table first.
func (s *syntaxes) command(arg string) error {
	switch word := strings.ToLower(strings.TrimSpace(arg)); word {
	case "on":
		screen.InitGroups(s.hl)
		s.enable()
	case "enable":
		s.enable()
	case "off":
		s.on = false
		s.bufs = map[*text.Buffer]*bufSyntax{}
	case "reset":
		screen.InitGroups(s.hl)
		s.reload()
	case "clear":
		// ":syntax clear" drops the rules for the current buffer and leaves
		// highlighting switched on, which is how a file is put back to plain
		// text without turning the feature off.
		s.bufs = map[*text.Buffer]*bufSyntax{}
	case "manual":
		// ":syntax manual" means "load the rules but do not follow FileType".
		// pvim has no separate 'syntax' option to set by hand, so this is
		// enable with a name nobody here can act on differently.
		s.enable()
	case "":
		return fmt.Errorf("E475: Invalid argument: syntax")
	default:
		return fmt.Errorf("E475: Invalid argument: syntax %s", word)
	}
	return nil
}

// enable turns highlighting on and re-attaches every buffer that had it.
func (s *syntaxes) enable() {
	s.on = true
	s.reload()
}

// reload throws the per-buffer state away, keeping which filetype each buffer
// had, so that the next frame builds it again against the table as it now is.
func (s *syntaxes) reload() {
	old := s.bufs
	s.bufs = map[*text.Buffer]*bufSyntax{}
	s.loaded = map[string]*syntax.Syntax{}
	s.errs = nil
	if !s.on {
		return
	}
	for b, bs := range old {
		s.attach(b, bs.filetype)
	}
}

// attach gives a buffer the rules for a filetype.
//
// A filetype with no syntax file is not an error and does not print: 767 of
// vim's 773 files are for languages nobody here edits, and a buffer with no
// rules draws in Normal exactly as vim draws it. What is recorded is a file
// that exists and would not load.
func (s *syntaxes) attach(b *text.Buffer, filetype string) {
	if b == nil {
		return
	}
	if !s.on || filetype == "" {
		delete(s.bufs, b)
		return
	}
	if bs, ok := s.bufs[b]; ok && bs.filetype == filetype {
		return
	}

	syn, ok := s.loaded[filetype]
	if !ok {
		var err error
		syn, err = syntax.Load(s.rt, filetype)
		if err != nil {
			if _, missing := err.(syntax.MissingError); !missing {
				s.errs = append(s.errs, err)
			}
			syn = nil
		}
		s.loaded[filetype] = syn
	}
	if syn == nil {
		delete(s.bufs, b)
		return
	}
	s.bufs[b] = &bufSyntax{
		h:        syntax.NewHighlighter(syn, b),
		ids:      s.resolveGroups(syn),
		filetype: filetype,
	}
}

// detach forgets a buffer, for a:bd.
func (s *syntaxes) detach(b *text.Buffer) { delete(s.bufs, b) }

// changed tells a buffer's highlighter that a line was edited.
func (s *syntaxes) changed(b *text.Buffer, line int) {
	if bs := s.bufs[b]; bs != nil {
		bs.h.Changed(line)
	}
}

// resolveGroups turns every group a rule set can produce into a highlight id.
//
// The walk is vim's: a group draws in its own colours when a `hi` line gave it
// any, and otherwise in the colours of whatever it is linked to, following the
// chain. go.vim says `hi def link goComment Comment`, nofrils-dark says
// `hi Comment guifg=#6C6C6C`, and so goComment is grey. A chain that ends at a
// group nobody has defined lands on Normal, which is the Table's own rule and
// is what most of nofrils leaves everything as.
func (s *syntaxes) resolveGroups(syn *syntax.Syntax) []screen.HLID {
	links := syn.Links()
	ids := make([]screen.HLID, syn.GroupCount())
	for i := range ids {
		name := syn.GroupName(i)
		seen := map[string]bool{}
		for !s.defined(name) && !seen[name] {
			seen[name] = true
			to, ok := links[name]
			if !ok {
				break
			}
			name = to
		}
		if !s.defined(name) {
			// The chain ended at a group nobody has given colours to, so there
			// is nothing to draw it in. That is Normal, and it is deliberately
			// not screen.Table.ID(name): ID interns a name on first sight and
			// would hand back a fresh id carrying Normal's appearance, which
			// draws the same and then overrides 'cursorline' and every other
			// base highlight for no reason at all.
			ids[i] = screen.Normal
			continue
		}
		ids[i] = s.hl.ID(name)
	}
	return ids
}

// sources is what a Frame wants: one SpanSource per window id, for the windows
// whose buffers have rules. Nil when there is nothing to draw, which is what
// screen.Frame reads as ":syntax off".
func (s *syntaxes) sources(windows map[int]*text.Buffer) map[int]screen.SpanSource {
	if !s.on || len(s.bufs) == 0 {
		return nil
	}
	out := map[int]screen.SpanSource{}
	for id, b := range windows {
		if bs := s.bufs[b]; bs != nil {
			out[id] = bs
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// forTabs is sources over the windows of a tab page, which is the shape
// cmd/pvim/draw.go's frame() has to hand: one line there rather than a map
// built at the call site.
func (s *syntaxes) forTabs(tabs *window.Tabs) map[int]screen.SpanSource {
	if !s.on || tabs == nil || tabs.Current() == nil {
		return nil
	}
	bufs := map[int]*text.Buffer{}
	for _, w := range tabs.Current().Windows() {
		bufs[w.ID] = w.Buf
	}
	return s.sources(bufs)
}

// SpansOn is screen.SpanSource: the highlighted runs on one buffer line, with
// the group already turned into a highlight id.
func (bs *bufSyntax) SpansOn(line int) []screen.SynSpan {
	spans := bs.h.SpansOn(line)
	if len(spans) == 0 {
		return nil
	}
	out := make([]screen.SynSpan, 0, len(spans))
	for _, sp := range spans {
		hl := screen.Normal
		if sp.Group >= 0 && sp.Group < len(bs.ids) {
			hl = bs.ids[sp.Group]
		}
		if hl == screen.Normal {
			// A group that resolved to Normal is not highlighting: leaving the
			// span out lets 'cursorline' and the rest of the base highlight
			// show through, which is what vim does with a group nobody gave a
			// colour. On this vimrc's colourscheme that is most of them.
			continue
		}
		out = append(out, screen.SynSpan{Start: sp.Start, End: sp.End, HL: hl})
	}
	return out
}

// refusals is everything the loaded rule sets would not run, named. It is what
// a ":syntax" with no argument should print and what the tests assert on.
func (s *syntaxes) refusals() []string {
	var out []string
	for ft, syn := range s.loaded {
		if syn == nil {
			continue
		}
		for _, r := range syn.Refusals {
			out = append(out, ft+": "+r.Error())
		}
	}
	for _, err := range s.errs {
		out = append(out, err.Error())
	}
	return out
}
