package syntax

import "strings"

// Vim's external matches, \z( and \z1, which exist for exactly one job and are
// the only reason python's strings can be highlighted at all.
//
//	syn region pythonString start=+[uU]\=\z(['"]\)+ skip=+\\\\\|\\\z1+ end="\z1"
//
// The start pattern captures the quote that opened the string and the end
// pattern insists on the same one, so that a ' does not close a "…". RE2 has no
// backreference and internal/regex refuses \z outright, but nothing here needs
// a backreference: the captured text is known by the time the end pattern is
// searched for, so the end pattern is compiled with the quote substituted in,
// once per distinct quote, and cached. Two regexps and no new engine.
//
// The substitution is \V, very nomagic, where the only special character left
// is the backslash, followed by \m to put the rest of the pattern back where it
// was.

// hasExternalRef reports whether a pattern refers to a capture the start
// pattern made.
func hasExternalRef(pat string) bool {
	for _, t := range scan(pat, magicOn) {
		if t.kind == tokZ && t.end-t.start == 3 && pat[t.start+2] != '(' {
			return true
		}
	}
	return false
}

// stripExternalCapture turns \z( into an ordinary group and reports which
// capture number it became, or 0 when there was none.
func stripExternalCapture(pat string) (string, int) {
	toks := scan(pat, magicOn)
	group, caps := 0, 0
	var b strings.Builder
	copied := 0
	for _, t := range toks {
		switch {
		case t.kind == tokGroupOpen:
			caps++
		case t.kind == tokZ && t.end-t.start == 3 && pat[t.start+2] == '(':
			b.WriteString(pat[copied:t.start])
			b.WriteString(`\(`)
			copied = t.end
			caps++
			if group == 0 {
				group = caps
			}
		}
	}
	if group == 0 {
		return pat, 0
	}
	b.WriteString(pat[copied:])
	return b.String(), group
}

// substituteExternal puts the captured text where every \z1 stood.
//
// Only \z1 is substituted, because only one \z( is honoured above, and a
// pattern reaching for \z2 is left with the atom internal/regex will refuse by
// name rather than quietly given the wrong capture.
func substituteExternal(pat, text string) string {
	var b strings.Builder
	copied := 0
	for _, t := range scan(pat, magicOn) {
		if t.kind != tokZ || t.end-t.start != 3 || pat[t.start+2] != '1' {
			continue
		}
		b.WriteString(pat[copied:t.start])
		b.WriteString(`\V`)
		b.WriteString(strings.ReplaceAll(text, `\`, `\\`))
		b.WriteString(t.level.directive())
		copied = t.end
	}
	b.WriteString(pat[copied:])
	return b.String()
}

// resolve compiles a template pattern against the text the region's start
// captured, caching the result: a file full of strings has two distinct
// quotes in it and not two thousand.
func (p *pattern) resolve(ext string) *pattern {
	if p.tmpl == "" {
		return p
	}
	if q, ok := p.cache[ext]; ok {
		return q
	}
	q, err := compilePattern(substituteExternal(p.tmpl, ext), p.off, Refusal{})
	if err != nil {
		q = nil
	}
	if p.cache == nil {
		p.cache = map[string]*pattern{}
	}
	p.cache[ext] = q
	return q
}

// findExt is find with the text the external capture took, for a region start.
func (p *pattern) findExt(line string, from int) (int, int, string, bool) {
	if p.extGroup == 0 {
		s, e, ok := p.find(line, from)
		return s, e, "", ok
	}
	if p.bol && from > 0 || from > len(line) {
		return 0, 0, "", false
	}
	m := p.re.FindStringSubmatchIndex(line[from:])
	if m == nil {
		return 0, 0, "", false
	}
	ext := ""
	if g := p.extGroup; 2*g+1 < len(m) && m[2*g] >= 0 {
		ext = line[from+m[2*g] : from+m[2*g+1]]
	}
	return from + m[0], from + m[1], ext, true
}
