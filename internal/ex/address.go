package ex

import (
	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/search"
	"github.com/pkar/pvim/internal/text"
)

// Turning a parsed Range into two line numbers.
//
// This is the half that needs the editor: a mark, a search pattern, the
// cursor and the length of the buffer. Everything the parser does needs none
// of them, which is why the two halves are separate functions and why the
// parser has a table test with no Context in it.

// rangeCurrent is the default range of a command that acts on one line when
// none was given: ":d", ":j", ":s".
//
// Given is 1 and not 0 because Resolve reads it: one address means the range
// is that line twice, which is what a command acting on the current line
// wants.
var rangeCurrent = Range{Given: 1, From: Addr{Kind: AddrCurrent}, To: Addr{Kind: AddrCurrent}}

// rangeFile is the default of a command that acts on the whole buffer when
// none was given: ":w", ":g", ":sort".
//
// Given is 2, and it has to be: a default with two different addresses in it
// is only two addresses if Resolve is told so, and one that says 1 collapses
// to its second address alone. That made ":g/x/d" a one-line command whose
// line was the last in the file, which is the shape of bug an unwired command
// hides -- ":g" was the first RangeFile command to get a handler.
var rangeFile = Range{Given: 2, From: Addr{Kind: AddrLine, Line: 1}, To: Addr{Kind: AddrLast}}

// Resolve turns a Range into two buffer line numbers, 1-based and inclusive.
//
// def is the range a command uses when none was given. Nothing is clamped and
// nothing is bumped: a line number past the end of the buffer is E16, a
// backwards range is E493 for the caller to prompt about, and a line 0 comes
// back as 0 so that the commands with vim's EX_ZEROR flag can use it and the
// rest can turn it into 1. Doing either here would make ":0put" and ":0d"
// indistinguishable, and they are not.
//
// The semicolon is where this stops being arithmetic. ";" moves the cursor to
// the first address before the second is resolved, so "/a/;/b/" finds the
// first b after the first a while "/a/,/b/" finds the first b in the file. The
// move is real in vim -- the cursor stays there afterwards even when the
// command fails -- and it is real here: ctx.moveToKeepCol is called, not a
// local variable.
func Resolve(r Range, ctx *Context, def Range) (first, last int, err error) {
	if ctx == nil || ctx.Ed == nil {
		return 0, 0, ErrInvalidRange
	}
	if r.Given == 0 {
		// The default keeps its own Given: it is what says whether the
		// default is one address or two, and forcing it to zero here threw
		// the first address of every two-address default away.
		r = def
	}

	cur := ctx.cursorLine()
	if r.Given < 2 {
		last, err = resolveAddr(r.To, ctx, cur)
		if err != nil {
			return 0, 0, err
		}
		return last, last, ctx.checkLine(last)
	}

	// Left to right, which matters for one thing only and matters for it on
	// every range with two searches in it: each "/pat/" leaves its pattern in
	// "/ as it is resolved, so ":/beta/,/delta/d" ends with "delta" there and
	// an "n" after it looks for delta. Resolving the second address first
	// left "beta". vim's get_address is a single left-to-right walk and this
	// is that walk.
	//
	// The addresses in front of the surviving pair are part of that walk: they
	// decide nothing about which lines the command acts on and their
	// semicolons still move the cursor, which is why ":3;.,.+1d" deletes lines
	// 3 and 4.
	for _, e := range r.Earlier {
		n, err := resolveAddr(e.Addr, ctx, cur)
		if err != nil {
			return 0, 0, err
		}
		if !e.Semicolon {
			continue
		}
		if err := ctx.checkLine(n); err != nil {
			return 0, 0, err
		}
		ctx.moveToKeepCol(n)
		cur = n
	}

	first, err = resolveAddr(r.From, ctx, cur)
	if err != nil {
		return 0, 0, err
	}
	if r.Semicolon {
		if err := ctx.checkLine(first); err != nil {
			return 0, 0, err
		}
		ctx.moveToKeepCol(first)
		cur = first
	}
	last, err = resolveAddr(r.To, ctx, cur)
	if err != nil {
		return 0, 0, err
	}
	if err := ctx.checkLine(first); err != nil {
		return 0, 0, err
	}
	if err := ctx.checkLine(last); err != nil {
		return 0, 0, err
	}
	if first > last {
		return first, last, ErrBackwardsRange
	}
	return first, last, nil
}

// checkLine is vim's range check: a line number below zero or past the end of
// the buffer is E16.
//
// Zero is allowed here and rejected or bumped later, by the command, because
// vim's EX_ZEROR flag is per command: ":0put" puts above the first line and
// ":0d" deletes it. Measured, both.
func (c *Context) checkLine(n int) error {
	if n < 0 || n > c.buffer().LineCount() {
		return ErrInvalidRange
	}
	return nil
}

// resolveAddr turns one address into a line number, with its offsets applied.
//
// cur is what "." means here, which is the cursor line for the first address
// of a range and the first address for the second one after a ";".
func resolveAddr(a Addr, c *Context, cur int) (int, error) {
	b := c.buffer()
	n := 0
	switch a.Kind {
	case AddrNone, AddrCurrent:
		n = cur
	case AddrLine:
		n = a.Line
	case AddrLast:
		n = b.LineCount()
	case AddrAll:
		// AddrAll never reaches here: the parser expands "%" into a pair.
		// Answering with the whole buffer rather than zero keeps a hand-built
		// Range from silently meaning line 0.
		n = b.LineCount()
	case AddrMark:
		p, err := c.markPos(a.Mark)
		if err != nil {
			return 0, err
		}
		n = p.Line
	case AddrSearchFwd, AddrSearchBack, AddrNextMatch, AddrPrevMatch, AddrLastSubst:
		m, err := c.searchAddr(a, cur)
		if err != nil {
			return 0, err
		}
		n = m
	}
	return n + a.Offset, nil
}

// markPos returns the position of a mark named in an address.
//
// "'<" and "'>" are the visual selection, which the mode machine owns and the
// buffer does not, so they are asked for separately. Everything else is a
// buffer mark; A-Z file marks need more than one buffer and are still a TODO
// in internal/text.
func (c *Context) markPos(name byte) (text.Pos, error) {
	switch name {
	case '<', '>':
		start, end, ok := c.Ed.LastVisual()
		if !ok {
			return text.Pos{}, ErrMarkNotSet
		}
		if name == '<' {
			return start, nil
		}
		return end, nil
	}
	p, ok := c.buffer().Mark(name)
	if !ok {
		return text.Pos{}, ErrMarkNotSet
	}
	return p, nil
}

// searchAddr runs the search forms of an address: "/pat/", "?pat?", "\/",
// "\?" and "\&".
//
// The search starts from the line after the current one going forwards and the
// line before it going backwards, which is vim's rule and the reason
// ":/foo/d" on a line that itself matches foo deletes the next one.
//
// An absent pattern is where vim's two remembered patterns stop being
// interchangeable. "\/" and "\?" are spats[RE_SEARCH] specifically, which only
// a search writes, and "\&" is spats[RE_SUBST], which only a ":s" writes. An
// "n" after a ":s" repeats the substitute pattern -- that is RE_LAST and it is
// what the mode machine's one slot holds -- and a "\/" after the same ":s" is
// E35 when nothing has been searched for. Measured both ways, including the
// case that tells the slots apart rather than merely tells set from unset:
// "/xzz" then ":s/z/Z/" then ":\/=" answers E486 naming xzz.
func (c *Context) searchAddr(a Addr, cur int) (int, error) {
	dir := search.Forward
	if a.Kind == AddrSearchBack || a.Kind == AddrPrevMatch {
		dir = search.Backward
	}

	pattern := a.Pattern
	switch {
	case pattern != "":
		// A pattern written out is do_search, which writes RE_SEARCH.
		c.searchPat = pattern
	case a.Kind == AddrLastSubst:
		pattern = c.subState().SubPattern
	default:
		pattern = c.searchSlot()
	}
	if pattern == "" {
		return 0, ErrNoPrevRegexp
	}

	// The pattern becomes the last search, exactly as a "/" typed in normal
	// mode does: ":/beta/,/delta/d" leaves @/ holding "delta", and an "n"
	// after it looks for delta. vim's do_search is the same function for both
	// and sets it unconditionally, which is why an address that then fails to
	// match still leaves the pattern behind.
	c.publishPattern(pattern)

	opt := c.searchOptions()
	re, err := search.Compile(pattern, opt)
	if err != nil {
		return 0, err
	}
	// Column zero of the cursor line: Find never returns the match the cursor
	// is already on, so starting at the line's first byte finds a match on
	// the next line going forward and on this one going back only if it is
	// before column zero, which nothing is.
	from := text.Pos{Line: cur, Col: 0}
	if dir == search.Forward {
		from = text.Pos{Line: cur, Col: len(c.buffer().Line(cur))}
	}
	hit, err := search.Find(c.buffer(), search.Query{Re: re, From: from, Dir: dir, Count: 1}, opt)
	// The wrap warning is a KEPT message. vim's give_warning calls
	// set_keep_msg unconditionally, so ":redir" catches "search hit BOTTOM,
	// continuing at TOP" twice, once when it is printed and once on the redraw
	// after the command. A plain "/" typed in normal mode already goes through
	// sayKeep; this is the same warning out of the same searchit and it has to
	// go through it too.
	if hit.Wrapped {
		c.sayKeep(search.WrapMessage(dir))
	}
	if err != nil {
		return 0, withName(ErrPatternNotFound, pattern)
	}
	return hit.Match.Start.Line, nil
}

// searchSlot is vim's spats[RE_SEARCH]: the pattern the last "/", "?", "*" or
// "#" looked for, which a ":s" does not write.
//
// Two places it can be. The ex layer records the addresses it searched with
// itself, in Context.searchPat; a "/" typed in normal mode lands in the mode
// machine's one slot, and the way to tell that apart from a ":s" having landed
// there is the memory noteSearch already keeps. Anything in the mode machine
// that this layer did not put there is a search.
func (c *Context) searchSlot() string {
	if c.Ed != nil {
		if p := c.Ed.Search().Pattern; p != "" && p != c.searchSeen {
			return p
		}
	}
	return c.searchPat
}

// searchOptions is the option state as internal/search wants it. The same
// three options decide what a pattern means everywhere in the editor, and this
// is the ex layer's copy of the mapping internal/substitute makes for ":s".
func (c *Context) searchOptions() search.Options {
	o := c.Opt
	if o == nil {
		d := options.Defaults()
		o = &d
	}
	return search.Options{
		IgnoreCase: o.G.IgnoreCase,
		SmartCase:  o.G.SmartCase,
		NoMagic:    !o.G.Magic,
		WrapScan:   o.G.WrapScan,
		IsKeyword:  o.B.IsKeyword,
	}
}
