package syntax

import (
	"math/bits"
	"sort"
	"strings"
)

// The item model: what a syntax file's `syn keyword`, `syn match` and
// `syn region` lines become.
//
// One item per rule, in definition order, because definition order is a rule of
// the language: ":help:syn-priority" says that when two items match at the
// same position the one defined last wins, and every syntax file vim ships is
// written to that. A `syn keyword` line with eight words on it is eight items
// here rather than one with a list, so that priority, `contains` and
// `nextgroup` are one mechanism and not two.

// kind is what sort of rule an item is.
type kind int

const (
	kindKeyword kind = iota
	kindMatch
	kindRegion
)

// contains is a parsed `contains=` or `nextgroup=` list, before the group
// names in it have been resolved to items.
type contains struct {
	all      bool // contains=ALL
	allBut   bool // contains=ALLBUT,...
	top      bool // contains=TOP or TOP,...
	onlyCont bool // contains=CONTAINED
	names    []string
	set      bool
}

// item is one syntax rule.
type item struct {
	id    int
	group string
	kind  kind

	// word is the keyword, for kindKeyword.
	word       string
	ignoreCase bool

	// pat is the pattern, for kindMatch.
	pat *pattern

	// starts, skips and ends are a region's three pattern lists. Vim allows
	// several of each on one line and tries them all.
	starts, skips, ends []*pattern
	matchGroup          string

	containedFlag bool
	transparent   bool
	oneline       bool
	keepend       bool
	extend        bool
	display       bool
	excludenl     bool
	fold          bool
	conceal       bool
	concealends   bool

	contains    contains
	containedin contains
	nextGroup   contains
	skipWhite   bool
	skipNl      bool
	skipEmpty   bool

	// groupID is group's index in the Syntax group table.
	groupID int

	at Refusal
}

// cluster is a `syn cluster` name and the group names in it. Clusters are
// resolved once, after the whole file has been read, because a syntax file may
// add to a cluster after the item that contains it was defined and vim
// resolves at match time.
type cluster struct {
	names map[string]bool
	// subs are the clusters this one includes, kept separate so that a cycle
	// terminates rather than recurses.
	subs map[string]bool
}

// bitset is a set of item ids. It is a bitset and not a map because the
// matcher asks "is item 47 allowed here" once per item per column, and a 2,000
// item syntax file would otherwise be 2,000 map lookups a character.
type bitset []uint64

func newBitset(n int) bitset { return make(bitset, (n+63)/64) }

func (b bitset) set(i int) {
	if i/64 < len(b) {
		b[i/64] |= 1 << uint(i%64)
	}
}

func (b bitset) has(i int) bool {
	if i < 0 || i/64 >= len(b) {
		return false
	}
	return b[i/64]&(1<<uint(i%64)) != 0
}

// each calls fn with every id in the set, ascending, and allocates nothing.
// The matcher runs it once per item per column, which is why it hands the ids
// over one at a time rather than returning a slice of them.
func (b bitset) each(fn func(int)) {
	for w, word := range b {
		for word != 0 {
			fn(w*64 + bits.TrailingZeros64(word))
			word &= word - 1
		}
	}
}

// Syntax is one filetype's rules, ready to highlight with.
type Syntax struct {
	// Filetype is what this was loaded for.
	Filetype string

	items    []*item
	clusters map[string]*cluster
	links    map[string]string

	groupIndex map[string]int
	groupList  []string

	// keywords maps a keyword to the items that define it, so the matcher can
	// look one up by the word under the column rather than by trying every
	// keyword item in turn. The key is the keyword folded to lower case when
	// any keyword item is case-insensitive, which is what makes `syn case
	// ignore` cost nothing at match time.
	keywords     map[string][]int
	anyIgnoreCas bool

	// top is the set of items allowed where nothing encloses them, and inside
	// is the set allowed inside each item.
	top    bitset
	inside []bitset
	next   []bitset

	// iskeyword is the extra characters `syn iskeyword` added to the default
	// set. Empty means vim's own 'iskeyword' default.
	extraKeyword map[rune]bool

	// syncMinLines, syncMaxLines and syncFromStart are `syn sync`. They are
	// recorded and reported and they change nothing, because this package
	// keeps a cached end-of-line state for every line and recomputes from the
	// first changed one, which is `fromstart` and is strictly better than any
	// of them. See the package doc.
	syncMinLines  int
	syncMaxLines  int
	syncFromStart bool

	// MultiLine is every pattern that reaches across a line break. They are
	// compiled and run rather than refused, because on one line they mean what
	// they meant on one line; what they cost is the matches that would have
	// crossed the break, which is a colour that is missing. Listed here so
	// that "why is a setext heading not highlighted" has an answer with a line
	// number on it.
	MultiLine []Refusal

	// Refusals is everything in the syntax file this package would not run,
	// each one named. A group that appears here is a group whose text is left
	// in the colour of whatever encloses it.
	Refusals []Refused

	// spellDefault is `syn spell default|toplevel|notoplevel`, accepted and
	// inert: pvim's spell checking is later work and is not syntax-driven.
	spellDefault string

	// caseIgnore is `syn case ignore`, which is a mode the file switches on
	// and off as it goes rather than a property of the file, so every item
	// carries the value that was in force when it was defined.
	caseIgnore bool

	// includeDepth stops `syn include` and `runtime!` recursing when two
	// syntax files include each other, which html and javascript very nearly
	// do.
	includeDepth int

	// contained is set while a `syn include` is being read, because every item
	// an included file defines is contained whether it says so or not.
	forceContained bool
	// intoCluster is the cluster a `syn include` puts its items in.
	intoCluster string

	// pending is how the parser reaches the file reader without item.go
	// knowing what a file is: the runner installs it when it starts a file
	// and `syn include` calls it.
	pending func(cluster, file string, at Refusal)

	finished bool
}

// newSyntax returns an empty rule set.
func newSyntax(filetype string) *Syntax {
	return &Syntax{
		Filetype:     filetype,
		clusters:     map[string]*cluster{},
		links:        map[string]string{},
		keywords:     map[string][]int{},
		extraKeyword: map[rune]bool{},
		groupIndex:   map[string]int{},
	}
}

// refuse records one thing this package will not do.
func (s *Syntax) refuse(err error) {
	if r, ok := err.(Refused); ok {
		s.Refusals = append(s.Refusals, r)
	}
}

// add appends an item.
//
// The two things it does beyond appending are what makes `syn include` work:
// every item an included file defines is contained even when it does not say
// so, and every one of them joins the cluster the include named.
func (s *Syntax) add(it *item) {
	if s.forceContained {
		it.containedFlag = true
	}
	it.id = len(s.items)
	s.items = append(s.items, it)
	if s.intoCluster != "" {
		s.cl(s.intoCluster).names[it.group] = true
	}
}

// Groups returns every syntax group the file defines, sorted. It is for the
// tests and for a person asking what a file can produce.
func (s *Syntax) Groups() []string {
	seen := map[string]bool{}
	for _, it := range s.items {
		seen[it.group] = true
	}
	out := make([]string, 0, len(seen))
	for g := range seen {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// Links returns every `hi link` and `hi def link` the file declared, one step
// each.
//
// One step and not the whole chain on purpose: whoever resolves a group has to
// stop at the first name their highlight table has an opinion about, and only
// they know which those are. cmd/pvim/syntax.go is the caller and does exactly
// that. A cycle is the caller's to notice, and it does.
func (s *Syntax) Links() map[string]string {
	out := make(map[string]string, len(s.links))
	for k, v := range s.links {
		out[k] = v
	}
	return out
}

// cluster returns a cluster by name, making it when it is new. A syntax file
// may name a cluster before it defines it.
func (s *Syntax) cl(name string) *cluster {
	c, ok := s.clusters[name]
	if !ok {
		c = &cluster{names: map[string]bool{}, subs: map[string]bool{}}
		s.clusters[name] = c
	}
	return c
}

// expand fills seen with every group name a `contains=` entry stands for,
// following @clusters.
func (s *Syntax) expand(name string, into map[string]bool, seenCl map[string]bool) {
	if !strings.HasPrefix(name, "@") {
		into[name] = true
		return
	}
	key := name[1:]
	if seenCl[key] {
		return
	}
	seenCl[key] = true
	c, ok := s.clusters[key]
	if !ok {
		return
	}
	for n := range c.names {
		s.expand(n, into, seenCl)
	}
	for sub := range c.subs {
		s.expand("@"+sub, into, seenCl)
	}
}

// finish resolves clusters, contains, containedin and nextgroup into item sets.
// It runs once, after the whole file and everything it included have been read,
// because a cluster may grow after an item that names it was defined.
func (s *Syntax) finish() {
	if s.finished {
		return
	}
	s.finished = true

	n := len(s.items)
	byGroup := map[string][]int{}
	for _, it := range s.items {
		byGroup[it.group] = append(byGroup[it.group], it.id)
		it.groupID = s.groupID(it.group)
		if it.matchGroup != "" {
			s.groupID(it.matchGroup)
		}
	}

	s.top = newBitset(n)
	for _, it := range s.items {
		if !it.containedFlag {
			s.top.set(it.id)
		}
	}

	s.inside = make([]bitset, n)
	s.next = make([]bitset, n)
	for _, it := range s.items {
		s.inside[it.id] = s.resolve(it.contains, byGroup, n)
		s.next[it.id] = s.resolve(it.nextGroup, byGroup, n)
	}

	// containedin puts an item into somebody else's contains list, which is
	// the only way a syntax file can extend a group it did not define.
	for _, it := range s.items {
		if !it.containedin.set {
			continue
		}
		names := map[string]bool{}
		for _, nm := range it.containedin.names {
			s.expand(nm, names, map[string]bool{})
		}
		for g := range names {
			for _, host := range byGroup[g] {
				s.inside[host].set(it.id)
			}
		}
	}

	// The keyword index. Vim looks a keyword up by the word under the cursor,
	// and so does this.
	for _, it := range s.items {
		if it.kind != kindKeyword {
			continue
		}
		if it.ignoreCase {
			s.anyIgnoreCas = true
		}
		s.keywords[it.word] = append(s.keywords[it.word], it.id)
	}
	if s.anyIgnoreCas {
		folded := map[string][]int{}
		for w, ids := range s.keywords {
			k := strings.ToLower(w)
			folded[k] = append(folded[k], ids...)
		}
		for _, ids := range folded {
			sort.Ints(ids)
		}
		s.keywords = folded
	}
}

// resolve turns one contains= spec into the set of items it stands for.
//
// The five keywords, from:help:syn-contains, and each is the whole answer
// rather than a modifier on the list beside it:
//
//	ALL every item there is
//	ALLBUT,x,y every item except the ones named
//	TOP,x,y every item that is not `contained`, except the ones named
//	CONTAINED every item that is `contained`, except the ones named
//	x,y,@c exactly the ones named, with @clusters expanded
//
// A group name that nothing defines contributes nothing and is not an error,
// which is what lets go.vim write `contains=goGenerate,@goCommentGroup` with
// goGenerate switched off behind a `g:go_highlight_generate_tags` nobody set.
func (s *Syntax) resolve(c contains, byGroup map[string][]int, n int) bitset {
	b := newBitset(n)
	if !c.set {
		return b
	}

	named := map[string]bool{}
	for _, nm := range c.names {
		s.expand(nm, named, map[string]bool{})
	}

	switch {
	case c.all:
		for i := 0; i < n; i++ {
			b.set(i)
		}
	case c.allBut:
		for _, it := range s.items {
			if !named[it.group] {
				b.set(it.id)
			}
		}
	case c.top:
		for _, it := range s.items {
			if !it.containedFlag && !named[it.group] {
				b.set(it.id)
			}
		}
	case c.onlyCont:
		for _, it := range s.items {
			if it.containedFlag && !named[it.group] {
				b.set(it.id)
			}
		}
	default:
		for g := range named {
			for _, id := range byGroup[g] {
				b.set(id)
			}
		}
	}
	return b
}

// The group table. Every span carries an index into it rather than a name,
// because the renderer resolves a group to a highlight id once per group and
// then once per span looks it up in a slice, where a name would be a map
// lookup per span per frame.

// groupID interns a group name and returns its index.
func (s *Syntax) groupID(name string) int {
	if id, ok := s.groupIndex[name]; ok {
		return id
	}
	if s.groupIndex == nil {
		s.groupIndex = map[string]int{}
	}
	id := len(s.groupList)
	s.groupIndex[name] = id
	s.groupList = append(s.groupList, name)
	return id
}

// GroupCount is how many distinct group names the file mentions.
func (s *Syntax) GroupCount() int { return len(s.groupList) }

// GroupName is the name behind a Span's Group, which is what vim's
// synIDattr(synID(...), "name") prints for the same position.
func (s *Syntax) GroupName(id int) string {
	if id < 0 || id >= len(s.groupList) {
		return ""
	}
	return s.groupList[id]
}

// isKeyword reports whether a rune counts as part of a keyword.
//
// Vim's 'iskeyword' default is "@,48-57,_,192-255": the alphabetic class,
// digits, underscore, and every character from 192 up. utf_class then calls
// everything above 255 that it has no punctuation or space entry for a word
// character too, which is what internal/regex's \k table already says, and
// what this follows: a letter or a digit anywhere in Unicode, an underscore,
// and whatever `syn iskeyword` added.
func (s *Syntax) isKeyword(r rune) bool {
	if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return true
	}
	if s.extraKeyword[r] {
		return true
	}
	if r < 0xc0 {
		return false
	}
	return isLetterOrDigit(r)
}

// isLetterOrDigit is unicode.IsLetter || unicode.IsDigit for the ranges above
// Latin-1, kept here so that this package's imports stay what they are.
func isLetterOrDigit(r rune) bool {
	switch {
	case r >= 0xc0 && r <= 0xff:
		return r != 0xd7 && r != 0xf7
	case r >= 0x100 && r < 0x2000:
		return true
	case r >= 0x2070 && r < 0x2e00:
		return true
	case r >= 0x3040 && r < 0xd800:
		return true
	case r >= 0xf900 && r < 0xfff0:
		return true
	case r >= 0x10000:
		return true
	}
	return false
}
