package syntax

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/regex"
)

// A syntax pattern, compiled once when the syntax file is read.
//
// Three things happen between the text in the file and the *regex.Regexp:
//
// 1. A leading \%(...\)\@<= becomes \zs and a trailing \%(...\)\@= becomes
// \ze, because those two shapes are what a lookaround is used for in every
// syntax file vim ships and both mean exactly what \zs and \ze mean.
// 2. \zs and \ze are cut out and the pattern is wrapped in groups, so that
// RE2 -- which has neither -- reports the offsets vim would have. The
// magic level in force at each cut is restored inside its group, so a
// pattern that switches to \v halfway still means what it meant.
// 3. The result goes to internal/regex, which is the only door to regexp.
//
// What is left over is refused by name. See refuse.go for why that is the rule
// rather than an approximation.

// offset is one of the six:syn-pattern-offset values: a base, s or e, and a
// character delta.
type offset struct {
	set     bool
	fromEnd bool
	delta   int
}

// offsets is the whole:syn-pattern-offset set for one pattern.
//
// ms and me move the match itself, which is what contained items search inside
// and where scanning resumes. hs and he move only the highlight. rs and re move
// where a region's body begins and ends. lc is leading context: the first n
// characters the pattern matched are required but not part of the match.
type offsets struct {
	ms, me offset
	hs, he offset
	rs, re offset
	lc     int
}

// pattern is a compiled syntax pattern.
type pattern struct {
	vim string
	re  *regex.Regexp
	off offsets

	// startGroups and endGroups are the capture groups whose start and end are
	// the match's, after \zs and \ze were cut out, one per top-level
	// alternative that needed cutting. Whichever of them the match took part
	// in is the one that answers; empty means the whole match.
	startGroups []int
	endGroups   []int

	// bol is a pattern anchored at the start of the line, which can only ever
	// match at column 0.
	bol bool
	// bof and eof are \%^ and \%$: the first and last line of the buffer.
	bof bool
	eof bool

	// behind is a pattern that was written as a leading lookbehind. The
	// context in front of the reported match start is required, and it stands
	// before the column the search was asked to start at, so the line is
	// searched from its front and the answers before that column are skipped.
	// A \zs somebody wrote as a \zs is not this: vim starts its own search at
	// the column, and so does find.
	behind bool

	// tmpl is the vim text of a pattern that refers to a \z1 external match,
	// kept uncompiled because what it means depends on what the region's start
	// captured. cache holds one compiled pattern per distinct capture.
	tmpl  string
	cache map[string]*pattern

	// extGroup is the capture group a \z( made, for a region start pattern.
	extGroup int

	// neg is the trailing \@! assertion, checked at the end of every candidate
	// match rather than compiled into it.
	neg *pattern

	// multiline is a pattern that reaches across a line break. It is kept and
	// matched on one line, where it means what it meant on one line.
	multiline bool

	// full is a pattern with a ^ somewhere other than its front. Those cannot
	// be searched by slicing the line, because the slice would give the ^ a
	// line start that is not one, so they are matched over the whole line and
	// filtered by position instead.
	full bool
}

// prep is everything prepare works out about a pattern before it is compiled.
type prep struct {
	src         string
	startGroups []int
	endGroups   []int
	bol         bool
	bof         bool
	eof         bool
	full        bool
	behind      bool
	multiline   bool
	// neg is the inner pattern of a trailing \@!, or empty.
	neg string
}

// compilePattern turns one syntax pattern into a matcher, or refuses it.
func compilePattern(pat string, off offsets, at Refusal) (*pattern, error) {
	at.Detail = pat

	if hasExternalRef(pat) {
		// A \z1 means nothing until the region that owns it has started, so
		// the text is kept and compiled then. See external.go.
		return &pattern{vim: pat, tmpl: pat, off: off}, nil
	}
	pat, extGroup := stripExternalCapture(pat)

	pr, err := prepare(pat, at)
	if err != nil {
		return nil, err
	}

	re, err := regex.Compile(pr.src, regex.Options{})
	if err != nil {
		return nil, PatternError{Refusal: at, Err: err}
	}
	p := &pattern{
		vim: pat, re: re, off: off,
		startGroups: pr.startGroups, endGroups: pr.endGroups,
		bol: pr.bol, bof: pr.bof, eof: pr.eof, full: pr.full,
		behind: pr.behind, multiline: pr.multiline, extGroup: extGroup,
	}
	if extGroup > 0 && (len(pr.startGroups) > 0 || len(pr.endGroups) > 0) {
		// The wrapping that \zs and \ze need renumbers the captures, and the
		// external one would no longer be the group it was counted as.
		return nil, SplitError{Refusal: at}
	}
	if pr.neg != "" {
		n, err := compilePattern(pr.neg, offsets{}, at)
		if err != nil {
			return nil, err
		}
		p.neg = n
	}
	return p, nil
}

// prepare does the rewrites and reports the flags the matcher needs.
func prepare(pat string, at Refusal) (prep, error) {
	var pr prep

	pat, pr.neg = cutNegative(pat)
	var err error
	pat, pr.behind, err = rewriteLookaround(pat, at)
	if err != nil {
		return pr, err
	}

	toks := scan(pat, magicOn)
	for _, t := range toks {
		switch t.kind {
		case tokNewline, tokAnyOf:
			// A pattern that reaches across a line break is compiled and kept
			// rather than refused. internal/regex turns \n into \n and \_s
			// into [\s\n], and a matcher handed one line at a time never sees
			// a newline, so the pattern means on one line exactly what it
			// meant on one line before: \_s degrades to \s, and an atom that
			// insists on a \n never matches at all. Both failures are a colour
			// that is missing, which is what a rule nobody wrote looks like,
			// and neither is a colour that is wrong. The item is kept and the
			// pattern is marked, and Syntax.MultiLine lists them.
			pr.multiline = true
		case tokLookahead, tokNegLook, tokLookbehind, tokNegBehind, tokAtomic:
			return pr, LookaroundError{Refusal: at}
		case tokBOF:
			pr.bof = true
		case tokEOF:
			pr.eof = true
		}
	}
	for i, t := range toks {
		if t.kind != tokBOL || !anchoring(toks, i) {
			continue
		}
		if firstAtom(toks, i) {
			pr.bol = true
		} else {
			pr.full = true
		}
	}
	if pr.bof {
		// \%^ is the start of the buffer, which is line 1 column 0 and nowhere
		// else; RE2 gets \A, which is the start of what it was handed.
		pr.bol = true
	}

	pr.src, pr.startGroups, pr.endGroups, err = split(pat, toks, at)
	return pr, err
}

// cutNegative takes a trailing \@! off a pattern and returns the two halves.
//
// RE2 has no negative lookahead and never will, but a trailing one is not
// really a regexp question: it is "match this, then check that the next thing
// is not that", and the second half is a second search anchored where the first
// one ended. That is two calls into internal/regex and no new engine, and it is
// what json.vim's `\(true\|false\)\(\_s\+\ze"\)\@!` needs to exist at all.
//
// Only a trailing one. A \@! in the middle would have to constrain a match
// that has not finished being found, which is the thing a backtracking engine
// is for, and not to write one.
func cutNegative(pat string) (string, string) {
	toks := scan(pat, magicOn)
	if len(toks) < 2 {
		return pat, ""
	}
	last := toks[len(toks)-1]
	if last.kind != tokNegLook || last.end != len(pat) {
		return pat, ""
	}
	cut := atomStart(toks, len(toks)-1, last.start)
	if cut < 0 {
		return pat, ""
	}
	inner := pat[cut:last.start]
	level := magicOn
	if len(toks) >= 2 {
		level = toks[len(toks)-2].level
	}
	// A group around the assertion is unwrapped, so that the two halves of
	// `\%(a\|b\)\@!` do not become a group inside a group for nothing.
	if prev := toks[len(toks)-2]; prev.kind == tokGroupClose && prev.depth == 0 && prev.end == last.start {
		for j := len(toks) - 3; j >= 0; j-- {
			if (toks[j].kind == tokGroupOpen || toks[j].kind == tokNCGroupOpen) && toks[j].depth == 0 {
				inner = pat[toks[j].end:prev.start]
				level = toks[j].level
				break
			}
		}
	}
	// The assertion only ever asks whether something matches, so a \zs or a
	// \ze inside it moves nothing and is dropped rather than split on.
	inner = strings.ReplaceAll(inner, `\zs`, "")
	inner = strings.ReplaceAll(inner, `\ze`, "")
	return pat[:cut], `\m\%(` + level.directive() + inner + `\m\)`
}

// anchoring reports whether a ^ token is vim's start-of-line anchor rather
// than a literal caret. Below very magic a ^ is only magic at the front of the
// pattern or straight after \(, \%( or \|.
func anchoring(toks []token, i int) bool {
	if toks[i].level == veryMagic {
		return true
	}
	for j := i - 1; j >= 0; j-- {
		switch toks[j].kind {
		case tokMagic:
			continue
		case tokGroupOpen, tokNCGroupOpen, tokAlternate:
			return true
		default:
			return false
		}
	}
	return true
}

// firstAtom reports whether the token at i is the first thing in the pattern,
// magic directives aside, which is the only place a ^ can be and still let the
// line be searched by slicing it.
func firstAtom(toks []token, i int) bool {
	for j := 0; j < i; j++ {
		if toks[j].kind != tokMagic {
			return false
		}
	}
	return toks[i].start == 0 || (i > 0 && toks[i-1].end == toks[i].start)
}

// rewriteLookaround turns the two lookaround shapes that mean \zs and \ze into
// \zs and \ze.
//
//	\%(A\)\@<=B is \%(A\)\zsB the context must be there, and is not the match
//	A\%(B\)\@= is A\zeB the same at the other end
//
// Only a lookbehind at the very front and a lookahead at the very back are
// rewritten, because those are the only two positions where the assertion and
// the marker mean the same thing. Anything else is refused in prepare.
func rewriteLookaround(pat string, at Refusal) (string, bool, error) {
	lookbehind := false
	for _, br := range topBranches(pat) {
		text := pat[br.from:br.to]
		out, behind := rewriteBranch(text, br.level)
		if out == text {
			continue
		}
		lookbehind = lookbehind || behind
		return rewriteRest(pat[:br.from]+out+pat[br.to:], at, lookbehind)
	}
	return pat, lookbehind, nil
}

// rewriteRest runs the rewrite again over the pattern a rewrite just produced,
// because one branch changing shifts every offset behind it.
func rewriteRest(pat string, at Refusal, behind bool) (string, bool, error) {
	out, more, err := rewriteLookaround(pat, at)
	return out, behind || more, err
}

// branchOf is one top-level alternative and the magic level it starts at.
type branchOf struct {
	from, to int
	level    magic
}

// topBranches splits a pattern at its top-level \| separators.
func topBranches(pat string) []branchOf {
	toks := scan(pat, magicOn)
	var out []branchOf
	from, level := 0, magicOn
	for _, t := range toks {
		if t.kind != tokAlternate || t.depth != 0 {
			continue
		}
		out = append(out, branchOf{from, t.start, level})
		from, level = t.end, t.level
	}
	return append(out, branchOf{from, len(pat), level})
}

// rewriteBranch turns a leading \@<= and a trailing \@= in one alternative
// into \zs and \ze.
//
//	\%(A\)\@<=B is \%(A\)\zsB the context must be there, and is not the match
//	A\%(B\)\@= is A\zeB the same at the other end
//
// Only at the two ends, because those are the only positions where the
// assertion and the marker mean the same thing. A \@<= in the middle is
// refused by name in prepare.
func rewriteBranch(text string, level magic) (string, bool) {
	toks := scan(text, level)
	if len(toks) == 0 {
		return text, false
	}
	head := zeroWidthPrefix(text, level)
	for i, t := range toks {
		if t.kind != tokLookbehind || i == 0 {
			continue
		}
		if atomStart(toks, i, t.start) != head {
			continue
		}
		return text[:t.start] + `\zs` + text[t.end:], true
	}
	if last := toks[len(toks)-1]; last.kind == tokLookahead && last.end == len(text) {
		if cut := atomStart(toks, len(toks)-1, last.start); cut >= 0 {
			return text[:cut] + `\ze` + text[cut:last.start], false
		}
	}
	return text, false
}

// zeroWidthPrefix is how far into a pattern the directives that match nothing
// reach: \c, \C, the four magic levels, \Z and \%#=1.
//
// It exists because `syn case ignore` puts a \c on the front of every pattern
// in the file, and a lookbehind that was at the front of the pattern is then
// two bytes in. markdown turns case folding on at line 62 and writes every one
// of its italic and bold rules under it.
func zeroWidthPrefix(text string, level magic) int {
	i := 0
	for i+1 < len(text) && text[i] == '\\' {
		switch text[i+1] {
		case 'c', 'C', 'v', 'm', 'M', 'V', 'Z':
			i += 2
		case '%':
			if strings.HasPrefix(text[i:], `\%#=`) && i+5 < len(text) {
				i += 6
				continue
			}
			return i
		default:
			return i
		}
	}
	return i
}

// atomStart returns the byte where the atom in front of the token at index i
// begins, which is where a \zs or a \ze has to be inserted to stand in for a
// lookaround.
//
// Three shapes, and they cover every lookaround in the shipped runtime: a
// group, which runs back to its opening bracket; a token the scanner already
// measured, such as a [] collection or an escaped character, which is its own
// range; and a bare character, which is one rune.
func atomStart(toks []token, i, before int) int {
	if i > 0 {
		prev := toks[i-1]
		if prev.kind == tokGroupClose && prev.depth == 0 && prev.end == before {
			for j := i - 2; j >= 0; j-- {
				if (toks[j].kind == tokGroupOpen || toks[j].kind == tokNCGroupOpen) && toks[j].depth == 0 {
					return toks[j].start
				}
			}
			return -1
		}
		if prev.end == before && prev.depth == 0 && prev.kind == tokOther {
			return prev.start
		}
		if prev.end < before && prev.depth == 0 {
			return before - 1
		}
	}
	if before > 0 {
		return before - 1
	}
	return -1
}

// split cuts the pattern at \zs and \ze and wraps the pieces in groups, so
// that RE2 can report the offsets those two markers move.
//
// The pattern must have no top-level alternation and the markers must stand at
// depth zero, because "everything before the \zs" is only a thing when the
// pattern is one concat. Anything else is a SplitError rather than a guess.
func split(pat string, toks []token, at Refusal) (string, []int, []int, error) {
	// Every top-level alternative is cut on its own. `\S\@<=\*\|^$` is
	// markdown's end pattern for italic, and after the lookbehind becomes a
	// \zs it is one branch that needs cutting beside one that does not, so a
	// pattern is not one concat and cannot be treated as one. Each branch gets
	// its own group, and whichever group the match participated in is the one
	// that carries the offsets.
	var bounds []int
	for _, t := range toks {
		if t.kind == tokAlternate && t.depth == 0 {
			bounds = append(bounds, t.start, t.end)
		}
	}
	any := false
	for _, t := range toks {
		if t.kind == tokMatchStart || t.kind == tokMatchEnd {
			any = true
		}
	}
	if !any {
		return pat, nil, nil, nil
	}

	type branch struct {
		from, to int
		level    magic
	}
	var branches []branch
	from, level := 0, magicOn
	for i := 0; i < len(bounds); i += 2 {
		branches = append(branches, branch{from, bounds[i], level})
		for _, t := range toks {
			if t.start == bounds[i] {
				level = t.level
			}
		}
		from = bounds[i+1]
	}
	branches = append(branches, branch{from, len(pat), level})

	var out strings.Builder
	var startGroups, endGroups []int
	caps := 0
	for bi, br := range branches {
		if bi > 0 {
			out.WriteString(`\m\|`)
		}
		text := pat[br.from:br.to]
		sub := scan(text, br.level)

		zsIdx, zeIdx := -1, -1
		for i, t := range sub {
			switch t.kind {
			case tokMatchStart:
				if t.depth == 0 {
					zsIdx = i
				}
			case tokMatchEnd:
				if t.depth == 0 && zeIdx < 0 {
					zeIdx = i
				}
			}
		}
		for _, t := range sub {
			if (t.kind == tokMatchStart || t.kind == tokMatchEnd) && t.depth != 0 {
				return "", nil, nil, SplitError{Refusal: at}
			}
		}
		if zsIdx >= 0 && zeIdx >= 0 && zeIdx < zsIdx {
			return "", nil, nil, SplitError{Refusal: at}
		}
		if zsIdx < 0 && zeIdx < 0 {
			out.WriteString(`\m\%(`)
			out.WriteString(br.level.directive())
			out.WriteString(text)
			out.WriteString(`\m\)`)
			for _, t := range sub {
				if t.kind == tokGroupOpen {
					caps++
				}
			}
			continue
		}

		// Drop every \zs and \ze, cutting the branch at the two that count:
		// the last \zs and the first \ze. A leading lookbehind rewritten into
		// a \zs can land in front of one the file already had, which is
		// json.vim's `\:\@<=[[:blank:]\r\n]*\zs\.\d\+`, and there the
		// later one is the match start and the earlier one only the context
		// marker it came from.
		var parts []string
		levels := []magic{br.level}
		var b strings.Builder
		copied := 0
		for i, t := range sub {
			if t.kind != tokMatchStart && t.kind != tokMatchEnd {
				continue
			}
			b.WriteString(text[copied:t.start])
			copied = t.end
			if i == zsIdx || i == zeIdx {
				parts = append(parts, b.String())
				b.Reset()
				levels = append(levels, t.level)
			}
		}
		b.WriteString(text[copied:])
		parts = append(parts, b.String())

		carries := 0
		if zsIdx >= 0 {
			carries = 1
		}
		group := 0
		for i, piece := range parts {
			if i == carries {
				out.WriteString(`\m\(`)
				caps++
				group = caps
			} else {
				out.WriteString(`\m\%(`)
			}
			out.WriteString(levels[i].directive())
			out.WriteString(piece)
			out.WriteString(`\m\)`)
			if i == carries {
				continue
			}
			for _, t := range scan(piece, levels[i]) {
				if t.kind == tokGroupOpen {
					caps++
				}
			}
		}
		if zsIdx >= 0 {
			startGroups = append(startGroups, group)
		}
		if zeIdx >= 0 {
			endGroups = append(endGroups, group)
		}
	}
	return out.String(), startGroups, endGroups, nil
}

// find returns the first match of the pattern in line at or after byte offset
// from, with \zs and \ze already applied.
func (p *pattern) find(line string, from int) (start, end int, ok bool) {
	for guard := 0; guard < 256; guard++ {
		s, e, ok := p.find1(line, from)
		if !ok || p.neg == nil {
			return s, e, ok
		}
		if ns, _, nok := p.neg.find(line, e); !nok || ns != e {
			return s, e, true
		}
		if p.bol {
			return 0, 0, false
		}
		from = s + 1
		if from > len(line) {
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// find1 is find without the trailing assertion.
func (p *pattern) find1(line string, from int) (start, end int, ok bool) {
	if p.bol && from > 0 {
		return 0, 0, false
	}
	if p.full && len(p.startGroups) == 0 && len(p.endGroups) == 0 {
		// A ^ that is not at the front of the pattern cannot be searched for
		// by slicing the line, because the slice would give it a line start
		// that is not one. FindAllStringIndex is handed the whole line and
		// gets it right; it has no submatch form in internal/regex, so a
		// pattern that also needs \zs takes the slice below and the ^ in it can
		// match at a column that is not 0. Where that shows: markdown's
		// `end="\S\@<=\*\|^$"` closes an emphasis run at the end of a line
		// whose rest is empty, where vim would carry it to the next line. A
		// region that ends early rather than one that never ends.
		for _, m := range p.re.FindAllStringIndex(line, -1) {
			if m[0] >= from {
				return m[0], m[1], true
			}
		}
		return 0, 0, false
	}
	if from > len(line) {
		return 0, 0, false
	}
	if p.behind {
		return p.findBehind(line, from)
	}
	sub := line[from:]
	if len(p.startGroups) == 0 && len(p.endGroups) == 0 {
		m := p.re.FindStringIndex(sub)
		if m == nil {
			return 0, 0, false
		}
		return from + m[0], from + m[1], true
	}
	m := p.re.FindStringSubmatchIndex(sub)
	if m == nil {
		return 0, 0, false
	}
	s, e := p.bounds(m)
	return from + s, from + e, true
}

// bounds picks the match's reported start and end out of a submatch list: the
// whole match, unless one of the groups a \zs or a \ze made took part.
func (p *pattern) bounds(m []int) (int, int) {
	s, e := m[0], m[1]
	for _, g := range p.startGroups {
		if 2*g+1 < len(m) && m[2*g] >= 0 {
			s = m[2*g]
			break
		}
	}
	for _, g := range p.endGroups {
		if 2*g+1 < len(m) && m[2*g+1] >= 0 {
			e = m[2*g+1]
			break
		}
	}
	return s, e
}

// findBehind searches a pattern whose front is required context, from the
// start of the line, and returns the first match whose reported start is at or
// after from.
func (p *pattern) findBehind(line string, from int) (int, int, bool) {
	at := 0
	for at <= len(line) {
		m := p.re.FindStringSubmatchIndex(line[at:])
		if m == nil {
			return 0, 0, false
		}
		s, e := p.bounds(m)
		if at+s >= from {
			return at + s, at + e, true
		}
		if p.bol {
			return 0, 0, false
		}
		step := m[0] + 1
		if step <= 0 {
			step = 1
		}
		at += step
	}
	return 0, 0, false
}

// apply resolves one offset against a match, in characters, clamped to the
// line. Vim counts offsets in characters and not in bytes, which shows up the
// first time me=e-1 lands on a multibyte rune.
func apply(o offset, line string, s, e int) int {
	if !o.set {
		return -1
	}
	base := s
	if o.fromEnd {
		base = e
	}
	return step(line, base, o.delta)
}

// step moves n characters from a byte offset, forwards or backwards.
func step(line string, at, n int) int {
	for ; n > 0 && at < len(line); n-- {
		_, w := utf8.DecodeRuneInString(line[at:])
		at += w
	}
	for ; n < 0 && at > 0; n++ {
		_, w := utf8.DecodeLastRuneInString(line[:at])
		at -= w
	}
	if at < 0 {
		at = 0
	}
	if at > len(line) {
		at = len(line)
	}
	return at
}

// parseOffsets reads the offset suffix that follows a syntax pattern, the
// "ms=s+1,he=e-2" half of `syn match x /pat/ms=s+1,he=e-2`.
//
// It returns the rest of the argument line, which is where the options after
// the pattern begin.
func parseOffsets(s string, off *offsets, at Refusal) (string, error) {
	for {
		name := ""
		for _, n := range []string{"ms", "me", "hs", "he", "rs", "re", "lc"} {
			if strings.HasPrefix(s, n+"=") {
				name = n
				break
			}
		}
		if name == "" {
			return s, nil
		}
		rest := s[3:]
		if name == "lc" {
			n, used := number(rest)
			if used == 0 {
				return s, OptionError{Refusal: Refusal{File: at.File, Line: at.Line, Group: at.Group, Detail: s}}
			}
			off.lc = n
			s = rest[used:]
		} else {
			var o offset
			if len(rest) == 0 || (rest[0] != 's' && rest[0] != 'e') {
				return s, OptionError{Refusal: Refusal{File: at.File, Line: at.Line, Group: at.Group, Detail: s}}
			}
			o.set = true
			o.fromEnd = rest[0] == 'e'
			rest = rest[1:]
			if len(rest) > 0 && (rest[0] == '+' || rest[0] == '-') {
				sign := 1
				if rest[0] == '-' {
					sign = -1
				}
				n, used := number(rest[1:])
				o.delta = sign * n
				rest = rest[1+used:]
			}
			switch name {
			case "ms":
				off.ms = o
			case "me":
				off.me = o
			case "hs":
				off.hs = o
			case "he":
				off.he = o
			case "rs":
				off.rs = o
			case "re":
				off.re = o
			}
			s = rest
		}
		if strings.HasPrefix(s, ",") {
			s = s[1:]
			continue
		}
		return s, nil
	}
}

// number reads a run of digits and reports how many bytes it used.
func number(s string) (int, int) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, 0
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, 0
	}
	return n, i
}
