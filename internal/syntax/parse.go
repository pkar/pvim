package syntax

import (
	"strconv"
	"strings"
)

// The parser for the `:syntax` subcommand subset.
//
// Vim's own order is copied exactly, because it is load bearing: syn_cmd_match
// reads the options in front of the pattern, then the pattern, then the options
// behind it, and a syntax file relies on that when it writes
//
//	syn match goSpaceError display excludenl "\s\+$"
//	syn match goField /\.\w\+/hs=s+1
//
// The one rule that catches everybody: a pattern's delimiter is whatever
// non-blank character stands where the pattern begins, and " is allowed, so
// `syn match jsonEscape "\\u\x\{4}" contained` is a pattern and not a comment.
// A " is only a comment where an argument was expected and none was found.

// synOptions are the option words:syn keyword, :syn match and:syn region all
// share, in vim's spelling. A word that is not one of these ends the option
// run, which is how vim tells "contained" the option from "contained" the
// keyword: it cannot, and neither can this, and neither can anybody.
var synOptions = map[string]bool{
	"conceal": true, "concealends": true, "contained": true,
	"containedin": true, "nextgroup": true, "transparent": true,
	"skipwhite": true, "skipnl": true, "skipempty": true, "contains": true,
	"oneline": true, "fold": true, "display": true, "extend": true,
	"keepend": true, "excludenl": true, "cchar": true, "matchgroup": true,
}

// synCommand runs one `:syn ...` line.
func (s *Syntax) synCommand(arg string, at Refusal) {
	sub, rest := word(arg)
	switch fullName(sub, "keyword", "match", "region", "cluster", "case",
		"iskeyword", "include", "sync", "clear", "spell", "conceal",
		"enable", "on", "off", "reset", "list", "foldlevel", "manual") {
	case "keyword":
		s.synKeyword(rest, at)
	case "match":
		s.synMatch(rest, at)
	case "region":
		s.synRegion(rest, at)
	case "cluster":
		s.synCluster(rest, at)
	case "case":
		w, _ := word(rest)
		s.caseIgnore = strings.HasPrefix(w, "i")
	case "iskeyword":
		s.synIsKeyword(rest)
	case "include":
		s.synInclude(rest, at)
	case "sync":
		s.synSync(rest, at)
	case "clear":
		s.synClear(rest)
	case "spell":
		s.spellDefault, _ = word(rest)
	case "conceal", "enable", "on", "off", "reset", "manual", "foldlevel":
		// State a syntax file may set about the editor rather than about the
		// language. Nothing here draws from it.
	case "list":
		// `:syn list` prints; a syntax file that runs it is asking for output
		// this package does not have a message line for.
	default:
		at.Detail = "syn " + arg
		s.refuse(CommandError{Refusal: at})
	}
}

// synKeyword reads `syn keyword {group} [options] {keyword}...`.
func (s *Syntax) synKeyword(arg string, at Refusal) {
	group, rest := word(arg)
	if group == "" {
		return
	}
	at.Group = group

	// Vim collects every option on the line first and only then adds the
	// keywords, so an option behind the words applies to all of them:
	//
	//	syn keyword goTodo contained TODO FIXME XXX BUG
	//	syn keyword goImport import contained
	//
	// are both one contained group, and reading the line left to right and
	// adding as it goes makes the second one uncontained, which puts `import`
	// at the top level and takes goSingleDecl's place.
	proto := item{kind: kindKeyword, group: group, ignoreCase: s.caseIgnore, at: at}
	var words []string
	for {
		rest = s.options(rest, &proto, at)
		w, more := word(rest)
		if w == "" {
			break
		}
		rest = more
		words = append(words, expandKeyword(w)...)
	}
	for _, k := range words {
		it := proto
		it.word = k
		s.add(&it)
	}
}

// expandKeyword expands vim's `ab[breviate]` shorthand into every prefix it
// stands for. Nothing in go, json or python uses it; syntax/vim.vim is made of
// it.
func expandKeyword(w string) []string {
	open := strings.IndexByte(w, '[')
	if open < 0 || !strings.HasSuffix(w, "]") {
		return []string{w}
	}
	head, tail := w[:open], w[open+1:len(w)-1]
	out := []string{head}
	for i := range tail {
		out = append(out, head+tail[:i+1])
	}
	return out
}

// synMatch reads `syn match {group} [options] {pattern} [options]`.
func (s *Syntax) synMatch(arg string, at Refusal) {
	group, rest := word(arg)
	if group == "" {
		return
	}
	at.Group = group

	it := &item{kind: kindMatch, group: group, at: at}
	rest = s.options(rest, it, at)

	pat, off, more, err := s.readPattern(rest, at)
	if err != nil {
		s.refuse(err)
		return
	}
	s.options(more, it, at)

	p, err := s.compile(pat, off, at)
	if err != nil {
		s.refuse(err)
		return
	}
	it.pat = p
	s.add(it)
}

// synRegion reads `syn region {group} [options] start=... skip=... end=...`,
// in any order and with any number of each.
func (s *Syntax) synRegion(arg string, at Refusal) {
	group, rest := word(arg)
	if group == "" {
		return
	}
	at.Group = group

	it := &item{kind: kindRegion, group: group, at: at}
	failed := false
	for {
		rest = s.options(rest, it, at)
		rest = strings.TrimLeft(rest, " \t")
		which := ""
		for _, n := range []string{"start=", "skip=", "end="} {
			if strings.HasPrefix(rest, n) {
				which = n
				break
			}
		}
		if which == "" {
			break
		}
		pat, off, more, err := s.readPattern(rest[len(which):], at)
		if err != nil {
			s.refuse(err)
			failed = true
			break
		}
		rest = more
		p, err := s.compile(pat, off, at)
		if err != nil {
			s.refuse(err)
			failed = true
			continue
		}
		switch which {
		case "start=":
			it.starts = append(it.starts, p)
		case "skip=":
			it.skips = append(it.skips, p)
		case "end=":
			it.ends = append(it.ends, p)
		}
	}
	if failed || len(it.starts) == 0 || len(it.ends) == 0 {
		// A region missing either half cannot be run at all, and half a region
		// is the one thing worse than none: the start would match and nothing
		// would ever close it.
		if !failed {
			at.Detail = "region with no " + map[bool]string{true: "start=", false: "end="}[len(it.starts) == 0]
			s.refuse(CommandError{Refusal: at})
		}
		return
	}
	s.add(it)
}

// compile builds a pattern under the syntax file's current `syn case`.
func (s *Syntax) compile(pat string, off offsets, at Refusal) (*pattern, error) {
	if s.caseIgnore {
		pat = `\c` + pat
	}
	p, err := compilePattern(pat, off, at)
	if err == nil && p.multiline {
		// Kept and run, and noted: the pattern reaches across a line break and
		// the matcher is per line, so \_s means \s here and an atom that
		// insists on a \n never matches. See the package doc.
		note := at
		note.Detail = pat
		s.MultiLine = append(s.MultiLine, note)
	}
	return p, err
}

// synCluster reads `syn cluster {name} [contains=...] [add=...] [remove=...]`.
func (s *Syntax) synCluster(arg string, at Refusal) {
	name, rest := word(arg)
	if name == "" {
		return
	}
	c := s.cl(name)
	for {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return
		}
		var list string
		var remove bool
		switch {
		case strings.HasPrefix(rest, "contains="):
			// A second contains= replaces the first, which is vim.
			c.names, c.subs = map[string]bool{}, map[string]bool{}
			list, rest = readIDList(rest[len("contains="):])
		case strings.HasPrefix(rest, "add="):
			list, rest = readIDList(rest[len("add="):])
		case strings.HasPrefix(rest, "remove="):
			remove = true
			list, rest = readIDList(rest[len("remove="):])
		default:
			at.Detail = "syn cluster " + arg
			s.refuse(CommandError{Refusal: at})
			return
		}
		for _, n := range strings.Split(list, ",") {
			if n == "" {
				continue
			}
			switch {
			case remove && strings.HasPrefix(n, "@"):
				delete(c.subs, n[1:])
			case remove:
				delete(c.names, n)
			case strings.HasPrefix(n, "@"):
				c.subs[n[1:]] = true
			default:
				c.names[n] = true
			}
		}
	}
}

// synIsKeyword reads `syn iskeyword {list}` and adds the extra characters to
// the default keyword set. `syn iskeyword clear` puts it back.
func (s *Syntax) synIsKeyword(arg string) {
	list, _ := word(arg)
	if list == "" || list == "clear" {
		s.extraKeyword = map[rune]bool{}
		return
	}
	for _, part := range strings.Split(list, ",") {
		if part == "@" {
			// The alphabetic class, which the default already has.
			continue
		}
		// A range, but only when both ends are numbers. A lone "-" is the
		// character itself, and vim's own default set ends in one often
		// enough that reading it as half a range loses it.
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, err1 := strconv.Atoi(lo)
			b, err2 := strconv.Atoi(hi)
			if err1 == nil && err2 == nil {
				for r := a; r <= b && r-a < 4096; r++ {
					s.extraKeyword[rune(r)] = true
				}
				continue
			}
		}
		if n, err := strconv.Atoi(part); err == nil {
			s.extraKeyword[rune(n)] = true
			continue
		}
		for _, r := range part {
			s.extraKeyword[r] = true
		}
	}
}

// synSync reads `syn sync ...`.
//
// minlines, maxlines and fromstart are recorded; everything else is refused by
// name. None of the three changes what this package does, and the package doc
// says why: the state at the end of every line is cached from line one, which
// is fromstart and is never wrong, where minlines is a guess that is right
// most of the time.
func (s *Syntax) synSync(arg string, at Refusal) {
	rest := arg
	for {
		var w string
		w, rest = word(rest)
		if w == "" {
			return
		}
		switch {
		case w == "fromstart":
			s.syncFromStart = true
		case strings.HasPrefix(w, "minlines="):
			s.syncMinLines, _ = strconv.Atoi(w[len("minlines="):])
		case strings.HasPrefix(w, "maxlines="):
			s.syncMaxLines, _ = strconv.Atoi(w[len("maxlines="):])
		case strings.HasPrefix(w, "linebreaks="), w == "clear":
			// Accepted and inert for the same reason as above.
		case w == "ccomment":
			// "ccomment [group]" says a C comment can be synced on. The group
			// name behind it is one more word to step over.
			if g, more := word(rest); g != "" && !strings.Contains(g, "=") {
				rest = more
			}
		default:
			at.Detail = "syn sync " + w
			s.refuse(CommandError{Refusal: at})
			// Keep reading: minlines= behind a form this does not implement is
			// still worth having, and every one of these only changes where a
			// recompute starts.
		}
	}
}

// synClear reads `syn clear [group|@cluster]...`, which a syntax file uses to
// drop rules another one defined.
func (s *Syntax) synClear(arg string) {
	if strings.TrimSpace(arg) == "" {
		s.items = nil
		s.clusters = map[string]*cluster{}
		return
	}
	drop := map[string]bool{}
	for {
		var w string
		w, arg = word(arg)
		if w == "" {
			break
		}
		if strings.HasPrefix(w, "@") {
			delete(s.clusters, w[1:])
			continue
		}
		drop[w] = true
	}
	var kept []*item
	for _, it := range s.items {
		if drop[it.group] {
			continue
		}
		it.id = len(kept)
		kept = append(kept, it)
	}
	s.items = kept
}

// options reads the run of option words at the front of rest and returns what
// is left. The first word that is not an option ends the run, which is vim.
func (s *Syntax) options(rest string, it *item, at Refusal) string {
	for {
		rest = strings.TrimLeft(rest, " \t")
		name, _, _ := strings.Cut(rest, "=")
		name, _ = word(name)
		if !synOptions[name] {
			return rest
		}
		var val string
		after := rest[len(name):]
		if strings.HasPrefix(after, "=") {
			if name == "contains" || name == "containedin" || name == "nextgroup" {
				val, after = readIDList(after[1:])
			} else {
				val, after = word(after[1:])
			}
		} else if name == "containedin" || name == "nextgroup" || name == "contains" ||
			name == "cchar" || name == "matchgroup" {
			// An option that wants a value and has none is not that option.
			return rest
		}
		rest = after

		switch name {
		case "contained":
			it.containedFlag = true
		case "transparent":
			it.transparent = true
		case "oneline":
			it.oneline = true
		case "keepend":
			it.keepend = true
		case "extend":
			it.extend = true
		case "display":
			it.display = true
		case "excludenl":
			it.excludenl = true
		case "fold":
			it.fold = true
		case "conceal":
			it.conceal = true
		case "concealends":
			it.concealends = true
		case "skipwhite":
			it.skipWhite = true
		case "skipnl":
			it.skipNl = true
		case "skipempty":
			it.skipEmpty = true
		case "cchar":
			// The character 'conceallevel' 2 shows in place of the text. The
			// vimrc sets conceallevel=2, and conceal under "do
			// not build", so this is read and dropped rather than silently
			// hiding text.
		case "matchgroup":
			if val == "NONE" {
				val = ""
			}
			it.matchGroup = val
		case "contains":
			it.contains = parseContains(val)
		case "containedin":
			it.containedin = parseContains(val)
		case "nextgroup":
			it.nextGroup = parseContains(val)
		}
	}
}

// parseContains reads a contains=, containedin= or nextgroup= value.
func parseContains(val string) contains {
	c := contains{set: true}
	for _, n := range strings.Split(val, ",") {
		switch n {
		case "":
		case "ALL":
			c.all = true
		case "ALLBUT":
			c.allBut = true
		case "TOP":
			c.top = true
		case "CONTAINED":
			c.onlyCont = true
		default:
			c.names = append(c.names, n)
		}
	}
	return c
}

// readPattern reads a delimited pattern and the offsets behind it, and returns
// what is left of the line.
func (s *Syntax) readPattern(arg string, at Refusal) (string, offsets, string, error) {
	arg = strings.TrimLeft(arg, " \t")
	if arg == "" {
		at.Detail = "missing pattern"
		return "", offsets{}, "", CommandError{Refusal: at}
	}
	// Where the pattern ends is vim's skip_regexp() and not "the next
	// delimiter": a backslash escapes the character behind it, and a []
	// collection swallows everything up to its ], delimiter included. Without
	// the second rule
	//
	//	syn match jsonEscape "\\["\\/bfnrt]" contained
	//
	// reads as the two characters `\\[`, which matches every backslash in the
	// file and leaves the rest of the line as options nobody recognises.
	delim := arg[0]
	end := -1
	level := magicOn
	for i := 1; i < len(arg); i++ {
		switch {
		case arg[i] == delim:
			end = i
		case arg[i] == '\\' && i+1 < len(arg):
			switch arg[i+1] {
			case 'v':
				level = veryMagic
			case 'm':
				level = magicOn
			case 'M':
				level = noMagic
			case 'V':
				level = veryNoMagic
			}
			i++
		case arg[i] == '[' && level <= magicOn:
			i = scanCollection(arg, i) - 1
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		at.Detail = arg
		return "", offsets{}, "", CommandError{Refusal: at}
	}
	pat := arg[1:end]
	rest := arg[end+1:]

	var off offsets
	rest, err := parseOffsets(rest, &off, at)
	if err != nil {
		return "", offsets{}, "", err
	}
	return pat, off, rest, nil
}

// readIDList reads a contains=, containedin=, nextgroup= or cluster list and
// returns it with the whitespace squeezed out, plus what is left of the line.
//
// Vim's get_id_list, and the two skipwhite calls in it are the whole point:
// white space is allowed after the "=" and on either side of a comma, and the
// list ends at the first blank that is not followed by one. Syntax files rely
// on it every time they write a long list over continuation lines --
//
//	syn cluster afterIdentifier contains=
//	 \ typescriptDotNotation,
//	 \ typescriptFuncCallArg
//
// joins to "contains= typescriptDotNotation, typescriptFuncCallArg", and a
// reader that stopped at the first blank would take an empty list and lose
// every rule behind the cluster. typescript.vim is 2,100 items behind that one
// rule.
func readIDList(s string) (string, string) {
	var b strings.Builder
	i := 0
	for {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		start := i
		for i < len(s) && s[i] != ' ' && s[i] != '\t' && s[i] != ',' {
			i++
		}
		b.WriteString(s[start:i])
		j := i
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		if j >= len(s) || s[j] != ',' {
			return b.String(), s[i:]
		}
		b.WriteByte(',')
		i = j + 1
	}
}

// word takes the first blank-separated word off a string and returns it with
// the rest.
func word(s string) (string, string) {
	s = strings.TrimLeft(s, " \t")
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i:]
}

// fullName resolves an abbreviated subcommand against a list of full ones, the
// way vim does: `syn ma` is `syn match` because no other subcommand starts
// that way, and `syn c` is ambiguous and is nothing.
func fullName(abbrev string, full ...string) string {
	if abbrev == "" {
		return ""
	}
	hit := ""
	for _, f := range full {
		if f == abbrev {
			return f
		}
		if strings.HasPrefix(f, abbrev) {
			if hit != "" {
				return abbrev // ambiguous: let the caller refuse it by name
			}
			hit = f
		}
	}
	if hit == "" {
		return abbrev
	}
	return hit
}
