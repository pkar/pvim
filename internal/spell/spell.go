// Package spell is the wordlist half of vim's spell checking: which words in a
// line are not in a dictionary, and the two files a person adds words to.
//
// It is deliberately not vim's spell checker. Vim compiles a hunspell
// dictionary and its affix rules into a .spl file, and that machinery, along
// with the "z=" suggestion list, is out of scope here. What is here is
// smaller: /usr/share/dict/words, 235,976 words on this machine, plus the
// words "zg" and "zw" put in ~/.cache/vim/spell.add, and an undercurl under
// everything else.
//
// # The dictionary is base forms only, and that decides the checker
//
// /usr/share/dict/words on macOS is a symlink to web2, Webster's Second
// unabridged, and it holds no inflections at all:
//
//	word in web2 words NOT in web2
//	file in web2 files NOT in web2
//	jump in web2 jumped NOT in web2
//	editor in web2 editors NOT in web2
//
// A checker that looked up the word as typed would underline the plural of
// every noun in the file, which is not a spell checker, it is a bug with a red
// line under it. So a word that is not in the dictionary is looked up again
// with an English suffix taken off -- the plural, the past tense, the -ing,
// the -ly, the comparative -- and it is good if any of those stems is in the
// dictionary. See stems for the table and what each row was checked against.
//
// The cost is stated rather than hidden: a misspelling that happens to be a
// dictionary word plus a stripped suffix is not caught. "runned" is bad and is
// caught; "flys" is bad and is NOT, because "fly" is in the dictionary and "s"
// comes off. A real affix file says which stems take which affix and this does
// not have one.
//
// # Case
//
// Vim's rule, and this one: a dictionary entry that is all lowercase matches
// the word lowercase, Capitalised or ALLCAPS, and an entry with a capital in
// it matches only that spelling and its ALLCAPS form. So "the", "The" and
// "THE" are all good, "Kurds" and "KURDS" are good and "kurds" is bad.
//
// # What is not here
//
// - Suggestions. "z=" is out of scope and is not implemented.
// - 'spelllang' beyond the fact that the option exists: there is one
// dictionary on this machine and it is English. A ":setlocal spelllang=de"
// gets the English one and no warning, which is a difference worth knowing
// about and not worth a second dictionary nobody has.
// - Vim's spell regions, the CAPS flag on sentence starts ('spelloptions'),
// compound words, and "spellcapcheck": a lowercase word starting a
// sentence is not flagged here, where vim draws SpellCap under it.
// - Words with a digit or an underscore in them are not checked at all,
// which is what keeps a spell-checked README from underlining every
// identifier in its code blocks.
package spell

import (
	"bufio"
	"os"
	"strings"
	"unicode"
)

// Checker answers whether a word is spelled right.
//
// It holds the dictionary and the word lists on top of it, in the order they
// are consulted: a word marked wrong by "zw" in a later list beats the
// dictionary, which is what makes "zw" able to reject a word that is in it.
type Checker struct {
	lists []*List
}

// New returns a checker over the lists given, in order.
func New(lists ...*List) *Checker {
	c := &Checker{}
	for _, l := range lists {
		if l != nil {
			c.lists = append(c.lists, l)
		}
	}
	return c
}

// Add puts another list on top of the ones already there.
func (c *Checker) Add(l *List) {
	if l != nil {
		c.lists = append(c.lists, l)
	}
}

// Bad reports whether the word is spelled wrong.
//
// The lists are consulted from the top down, so a word "zw" rejected is bad
// even when the dictionary holds it, and a word "zg" added is good even when
// it does not.
func (c *Checker) Bad(word string) bool {
	if word == "" {
		return false
	}
	for i := len(c.lists) - 1; i >= 0; i-- {
		switch c.lists[i].lookup(word) {
		case verdictGood:
			return false
		case verdictBad:
			return true
		}
	}
	// Not found as typed anywhere. Try the stems, which is what makes a
	// dictionary of base forms usable, and only against the good words: a
	// "zw" rejection is of the word as typed and vim does not extend it to
	// every inflection either.
	for _, s := range stems(word) {
		for i := len(c.lists) - 1; i >= 0; i-- {
			if c.lists[i].lookup(s) == verdictGood {
				return false
			}
		}
	}
	return true
}

// verdict is what one list says about a word.
type verdict int

const (
	verdictUnknown verdict = iota
	verdictGood
	verdictBad
)

// List is one word list: the system dictionary, a spellfile, or the internal
// list "zG" and "zW" write to.
//
// It carries both halves because a ".add" file does: a plain line is a word to
// accept and a line ending in "/!" is one to reject, which is what "zw" writes
// and what makes the file able to say "not this one" as well as "this one".
type List struct {
	// Name is the file the list came from, which is what the "Word 'x' added
	// to y" message names. Empty for a list built in memory.
	Name string
	// good holds the entries that are all lowercase, keyed by themselves, and
	// mixed holds the entries with a capital in them, keyed as written.
	good  map[string]bool
	mixed map[string]bool
	// bad holds the words a "/!" line rejects, keyed by their lowercase form,
	// because vim's "zw" rejects the word whatever case it is typed in.
	bad map[string]bool
}

// NewList returns an empty list with the given name.
func NewList(name string) *List {
	return &List{
		Name:  name,
		good:  map[string]bool{},
		mixed: map[string]bool{},
		bad:   map[string]bool{},
	}
}

// Len is how many words the list holds, which is what a test counts and what
// tells a dictionary that failed to load from one that is empty.
func (l *List) Len() int { return len(l.good) + len(l.mixed) }

// Read reads a word list file.
//
// One word per line, which is the format of both /usr/share/dict/words and
// vim's ".add" files. A line ending in "/!" is a word to reject, which is what
// "zw" writes; a line starting with "#" is one vim's "zug" commented out; a
// line with a "/" and any other flags after it is read as the word before the
// slash, because vim's own affix flags mean nothing here.
//
// A file that is not there is not an error: an empty spell.add is the normal
// state of a machine where nobody has pressed "zg" yet, and the caller would
// only have to turn the error back into an empty list.
func Read(path string) (*List, error) {
	l := NewList(path)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		l.AddLine(sc.Text())
	}
	if err := sc.Err(); err != nil {
		return l, err
	}
	return l, nil
}

// AddLine adds one line of a word list file.
func (l *List) AddLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "/") {
		return
	}
	word, flags, _ := strings.Cut(line, "/")
	word = strings.TrimSpace(word)
	if word == "" {
		return
	}
	if strings.Contains(flags, "!") {
		l.AddBad(word)
		return
	}
	l.AddGood(word)
}

// AddGood adds a word the list accepts: one line of the dictionary, or a "zg".
func (l *List) AddGood(word string) {
	if word == "" {
		return
	}
	if word == strings.ToLower(word) {
		l.good[word] = true
		return
	}
	l.mixed[word] = true
}

// AddBad adds a word the list rejects: a "zw".
func (l *List) AddBad(word string) {
	if word != "" {
		l.bad[strings.ToLower(word)] = true
	}
}

// lookup is what this list says about a word, with vim's case rules applied.
func (l *List) lookup(word string) verdict {
	lower := strings.ToLower(word)
	if l.bad[lower] {
		return verdictBad
	}
	if l.mixed[word] {
		return verdictGood
	}
	if l.good[lower] && matchesCase(word, lower) {
		return verdictGood
	}
	// ALLCAPS is allowed for every entry however the entry is spelled, so a
	// dictionary holding "Kurds" accepts "KURDS".
	if isUpper(word) && (l.good[lower] || l.mixed[title(lower)]) {
		return verdictGood
	}
	return verdictUnknown
}

// matchesCase reports whether word is a spelling an all-lowercase dictionary
// entry allows: itself, Capitalised, or ALLCAPS.
func matchesCase(word, lower string) bool {
	return word == lower || word == title(lower) || word == strings.ToUpper(lower)
}

// title capitalises the first rune and leaves the rest alone, which is vim's
// "Capitalised" and not strings.Title's word-by-word rule.
func title(s string) string {
	for i, r := range s {
		return string(unicode.ToUpper(r)) + s[i+len(string(r)):]
	}
	return s
}

// isUpper reports whether every cased rune in the word is uppercase, and there
// is at least one.
func isUpper(s string) bool {
	any := false
	for _, r := range s {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsUpper(r) {
			any = true
		}
	}
	return any
}

// stems is the affix table: the base forms a word might be an inflection of.
//
// Each row was checked against /usr/share/dict/words on this machine, which is
// the only dictionary this editor has. The word on the left is what a person
// types and the one on the right is what is actually in the file:
//
//	words -> word files -> file (plural, -s)
//	boxes -> box watches -> watch (plural, -es)
//	flies -> fly cities -> city (plural, -ies)
//	jumped -> jump used -> use (past, -ed and -d)
//	stopped -> stop planned -> plan (past, doubled)
//	jumping -> jump using -> use (-ing, and -ing with -e)
//	running -> run sitting -> sit (-ing, doubled)
//	quickly -> quick (-ly)
//	quicker -> quick quickest -> quick (-er, -est)
//	editor's -> editor (possessive)
//	don't -> don (contraction)
//
// The lookup is case-folded through the same rules as any other, so "Files"
// stems to "file" and is good.
func stems(word string) []string {
	w := strings.ToLower(word)
	var out []string
	add := func(s string) {
		if len(s) >= 2 {
			out = append(out, s)
		}
	}

	// A possessive or a contraction: everything from the apostrophe on comes
	// off. The dictionary holds no apostrophes at all, so this is the only
	// way "editor's" or "don't" can be good.
	if i := strings.IndexAny(w, "'’"); i > 0 {
		add(w[:i])
	}

	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		add(w[:len(w)-3] + "y")
		add(w[:len(w)-2])
	case strings.HasSuffix(w, "es") && len(w) > 3:
		add(w[:len(w)-2])
		add(w[:len(w)-1])
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && len(w) > 2:
		add(w[:len(w)-1])
	}
	switch {
	case strings.HasSuffix(w, "ing") && len(w) > 4:
		add(w[:len(w)-3])
		add(w[:len(w)-3] + "e")
		add(undouble(w[:len(w)-3]))
	case strings.HasSuffix(w, "ed") && len(w) > 3:
		add(w[:len(w)-2])
		add(w[:len(w)-1])
		add(undouble(w[:len(w)-2]))
	}
	switch {
	case strings.HasSuffix(w, "ly") && len(w) > 3:
		add(w[:len(w)-2])
		add(w[:len(w)-2] + "le")
	case strings.HasSuffix(w, "est") && len(w) > 4:
		add(w[:len(w)-3])
		add(w[:len(w)-2])
		add(undouble(w[:len(w)-3]))
	case strings.HasSuffix(w, "er") && len(w) > 3:
		add(w[:len(w)-2])
		add(w[:len(w)-1])
		add(undouble(w[:len(w)-2]))
	}
	return out
}

// undouble takes a doubled final consonant off: the "pp" of "stopped" and the
// "nn" of "running", which English doubles before a suffix that starts with a
// vowel.
func undouble(s string) string {
	if n := len(s); n >= 3 && s[n-1] == s[n-2] && !isVowel(s[n-1]) {
		return s[:n-1]
	}
	return s
}

// isVowel is the ASCII vowels, which is all undouble needs: a doubled vowel is
// not the doubling English does before a suffix.
func isVowel(c byte) bool {
	switch c {
	case 'a', 'e', 'i', 'o', 'u', 'y':
		return true
	}
	return false
}
