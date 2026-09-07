package textobj

import (
	"strconv"
	"strings"
	"unicode"
)

// keywords is a parsed 'iskeyword', the option that decides which characters
// iw and aw treat as one word. The default is "@,48-57,_,192-255": letters,
// digits, underscore and the top half of latin-1.
//
// It is a table and not a predicate because iw asks it once per character over
// a whole word and the parse is not free.
// It is parsed once per object rather than cached in a package variable: the
// parse is twenty characters and one text object is thousands, and a package
// variable would be shared state in a package that otherwise has none.
type keywords struct {
	ascii [256]bool
	spec  string
}

// parseKeywords reads 'iskeyword'.
//
// The syntax is vim's: a comma-separated list where an item is a single
// character, a decimal character code, a range written with '-', or "@" for
// every alphabetic character, and a leading '^' removes instead of adding. A
// literal comma is the item ",", and "^," removes it, which is why the split
// below is by hand rather than strings.Split.
func parseKeywords(spec string) *keywords {
	if spec == "" {
		spec = DefaultOptions().IsKeyword
	}
	k := &keywords{spec: spec}
	for _, item := range splitKeywordSpec(spec) {
		add := true
		if strings.HasPrefix(item, "^") && len(item) > 1 {
			add = false
			item = item[1:]
		}
		if item == "@" {
			// Every alphabetic character, and in the order it was
			// written: "@,^a" is vim's way of saying letters except a.
			for c := 0; c < 256; c++ {
				if unicode.IsLetter(rune(c)) {
					k.ascii[c] = add
				}
			}
			continue
		}
		lo, hi, ok := keywordRange(item)
		if !ok {
			continue
		}
		for c := lo; c <= hi && c < 256; c++ {
			k.ascii[c] = add
		}
	}
	return k
}

// splitKeywordSpec splits on commas, keeping a comma that is itself an item.
// "@,48-57,_,192-255" has a "," item in the middle of it, which is how the
// option spells the comma character.
func splitKeywordSpec(spec string) []string {
	var items []string
	for i := 0; i < len(spec); {
		if spec[i] == ',' {
			i++
			continue
		}
		// A ',' or "^," item is the comma character itself: it is the only
		// item that may start with a comma.
		if spec[i] == '^' && i+1 < len(spec) && spec[i+1] == ',' {
			items = append(items, "^,")
			i += 2
			continue
		}
		j := i
		for j < len(spec) && spec[j] != ',' {
			j++
		}
		item := spec[i:j]
		// "48-," and "a-," style ranges ending in a comma keep the comma.
		if strings.HasSuffix(item, "-") && j < len(spec) {
			item += ","
			j++
		}
		items = append(items, item)
		i = j
	}
	return items
}

// keywordRange turns one item into the byte range it covers.
func keywordRange(item string) (lo, hi int, ok bool) {
	if item == "" {
		return 0, 0, false
	}
	if i := strings.IndexByte(item, '-'); i > 0 && i < len(item)-1 {
		lo, ok1 := keywordChar(item[:i])
		hi, ok2 := keywordChar(item[i+1:])
		if ok1 && ok2 && lo <= hi {
			return lo, hi, true
		}
		return 0, 0, false
	}
	c, ok := keywordChar(item)
	return c, c, ok
}

// keywordChar reads one end of a range: a decimal number or a single
// character.
func keywordChar(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n, n >= 0 && n < 256
	}
	if len(s) == 1 {
		return int(s[0]), true
	}
	return 0, false
}

// is reports whether r is a keyword character, vim's vim_iswordc().
//
// Above U+00FF vim asks utf_class(), which answers 2 for letters and digits in
// every script; unicode.IsLetter and friends are the same answer with a table
// the standard library keeps up to date.
func (k *keywords) is(r rune) bool {
	if r < 256 {
		return k.ascii[r]
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}
