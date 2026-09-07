package search

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/text"
)

// Word is what * and # found to search for.
type Word struct {
	// Pattern is the search pattern: the word with vim's magic characters
	// escaped, wrapped in \< and \> when the command was * or # rather than
	// g* or g#.
	Pattern string
	// Start is the first byte of the word. Vim moves the cursor there before
	// it searches -- "put cursor at start of word, makes search skip the
	// word" -- and leaves it there when the search then fails, so * on the
	// last abc in a file with 'nowrapscan' ends on the a and not where it
	// began.
	Start text.Pos
}

// WordAt builds the pattern * and # search for: the keyword under or after the
// cursor, wrapped in \< and \> when whole is true.
//
// whole is false for g* and g#, which match the word anywhere inside a longer
// one.
//
// Vim looks twice. First for a keyword character, scanning forward along the
// line from the cursor and then back to the start of the run it lands in, so
// the cursor anywhere in or before a word finds that word. If the line holds
// no keyword character at all it looks again for any run of non-blanks, which
// is how * on a line of punctuation searches for that punctuation, escaped and
// with no \< \> around it because there is no word boundary to ask for. With
// neither, E348.
func WordAt(b *text.Buffer, at text.Pos, whole bool, opt Options) (Word, error) {
	line := b.Line(at.Line)
	if at.Col > len(line) {
		at.Col = len(line)
	}
	kw := parseIsKeyword(opt.IsKeyword)

	for pass := 0; pass < 2; pass++ {
		// What the forward scan is looking for. The first pass wants a
		// keyword character and steps over white space and punctuation alike;
		// the second takes the first non-blank whatever it is. Vim's
		// find_ident_at_pos runs the same two passes over mb_get_class, where
		// white space is 0, punctuation is 1 and a keyword character is 2.
		wanted := func(r rune) bool { return kw.has(r) }
		if pass == 1 {
			// Vim's VIM_ISWHITE, which is a space or a tab and not the rest
			// of what Unicode calls whitespace.
			wanted = func(r rune) bool { return r != ' ' && r != '\t' }
		}

		col := at.Col
		for col < len(line) {
			r, n := utf8.DecodeRune(line[col:])
			if wanted(r) {
				break
			}
			col += n
		}
		if col >= len(line) {
			continue // nothing on the rest of the line; try the wider net
		}

		// The run is expanded by the CLASS of the character that was found
		// and not by what the scan was looking for. That is the whole of the
		// difference between vim and a search that widens to "non-blank":
		// with the cursor on the ")" at the end of "does not end)", vim's
		// second pass stops on the ")", sees punctuation, and searches for
		// ")" alone -- where expanding over every non-blank gives "end)".
		//
		// Class and not a yes-or-no, because above Latin-1 vim has more than
		// two: "*" over the CJK in "日本語のテキスト" searches for \<日本語\>
		// and stops where the ideographs give way to Hiragana, and a run
		// widened by "is this a keyword character" swallows the whole line.
		// A fuzz script over the wide-rune corpus file found it.
		found, _ := utf8.DecodeRune(line[col:])
		want := kw.class(found)
		isIn := func(r rune) bool { return kw.class(r) == want }

		start := col
		for start > 0 {
			r, n := utf8.DecodeLastRune(line[:start])
			if !isIn(r) {
				break
			}
			start -= n
		}
		end := col
		for end < len(line) {
			r, n := utf8.DecodeRune(line[end:])
			if !isIn(r) {
				break
			}
			end += n
		}

		word := line[start:end]
		var p strings.Builder
		first, _ := utf8.DecodeRune(word)
		last, _ := utf8.DecodeLastRune(word)
		if whole && kw.has(first) {
			p.WriteString(`\<`)
		}
		p.WriteString(escapeWord(string(word), opt))
		if whole && kw.has(last) {
			p.WriteString(`\>`)
		}
		return Word{Pattern: p.String(), Start: text.Pos{Line: at.Line, Col: start}}, nil
	}
	return Word{}, NoStringError{}
}

// escapeChars are the characters vim puts a backslash in front of when it
// builds the pattern for * and #, from nv_ident().
//
// Both commands escape the same set, including the / that would end a forward
// search and not the ? that would end a backward one: * on a line reading "??"
// puts ?? in @/ unescaped, checked against vim 9.2. The nomagic set is vim's
// and is not checked against anything, because nothing in this editor turns
// 'magic' off.
const (
	escapeChars        = `/.*~[^$\`
	escapeCharsNoMagic = `/^$\`
)

func escapeWord(word string, opt Options) string {
	set := escapeChars
	if opt.NoMagic {
		set = escapeCharsNoMagic
	}
	var b strings.Builder
	for i := 0; i < len(word); i++ {
		if strings.IndexByte(set, word[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(word[i])
	}
	return b.String()
}

// keywordSet is 'iskeyword' resolved to a decision per byte value, which is as
// far as vim's own table goes: init_chartab() refuses an entry above 255 and
// everything above that is decided by character class instead.
type keywordSet [256]bool

// has answers whether a character is a keyword character.
//
// Below 256 the option decides. Above it, vim asks utf_class() and takes
// anything of class 2 or higher, which is letters, digits, marks and the CJK
// and Hangul blocks, and leaves out blanks and punctuation. Approximated here
// by Unicode's own categories, which agrees with vim for every case that was
// run through it -- 日本 in, U+00A9 out -- and is not a transcription of vim's
// table, which is 60 hand-maintained ranges. The day one of them matters, it
// is one range in this function.
// class is vim's mb_get_class(): 0 for a blank, 2 for a keyword character, 1
// for anything else below Latin-1, and above it the script class out of
// text.UTFClass, so that a run of CJK ideographs and the Hiragana beside it
// are two runs and not one.
func (k *keywordSet) class(r rune) int {
	if r == ' ' || r == '\t' || r == 0 {
		return text.ClassBlank
	}
	if r < 0x100 {
		if k.has(r) {
			return text.ClassWord
		}
		return text.ClassPunct
	}
	return text.UTFClass(r)
}

func (k *keywordSet) has(r rune) bool {
	if r < 0 {
		return false
	}
	if r < 256 {
		return k[r]
	}
	return !unicode.IsSpace(r) && !unicode.IsPunct(r) &&
		!unicode.IsSymbol(r) && !unicode.IsControl(r)
}

// defaultIsKeyword is what --clean vim reports for 'iskeyword' and what the
// vimrc leaves it at.
const defaultIsKeyword = "@,48-57,_,192-255"

// parseIsKeyword reads vim's 'iskeyword' syntax: comma-separated items, each a
// character, a decimal code point, or a range of either with a dash, and a
// leading ^ taking the item back out again. The bare @ means "every letter",
// which vim decides by asking whether the character has a case, so the two
// ordinal indicators ª and º are out and the micro sign µ is in.
//
// A malformed item is skipped rather than failing the whole option, because
// the option came from a:set that has already been accepted and a search is
// not the place to report E474.
func parseIsKeyword(spec string) keywordSet {
	if spec == "" {
		spec = defaultIsKeyword
	}
	var k keywordSet
	for _, item := range splitIsKeyword(spec) {
		remove := false
		if strings.HasPrefix(item, "^") && len(item) > 1 {
			remove, item = true, item[1:]
		}
		lo, hi, alpha, ok := parseIsKeywordItem(item)
		if !ok {
			continue
		}
		for c := lo; c <= hi; c++ {
			if alpha && !isCased(rune(c)) {
				continue
			}
			k[c] = !remove
		}
	}
	return k
}

// splitIsKeyword splits on commas, with the one case vim has to special-case:
// a comma is itself a legal item, written as a bare "," which produces an
// empty field on each side of it.
func splitIsKeyword(spec string) []string {
	fields := strings.Split(spec, ",")
	out := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		// "a,b" and a trailing "," are the comma character itself.
		if f == "" && i+1 < len(fields) && fields[i+1] == "" {
			out = append(out, ",")
			i++
			continue
		}
		if f == "" && i == len(fields)-1 {
			out = append(out, ",")
			continue
		}
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// parseIsKeywordItem reads one item and gives the byte range it covers, with
// alpha set for the @ form that means "the letters in this range".
func parseIsKeywordItem(item string) (lo, hi int, alpha, ok bool) {
	c, rest, ok := isKeywordChar(item)
	if !ok {
		return 0, 0, false, false
	}
	c2 := -1
	if strings.HasPrefix(rest, "-") && len(rest) > 1 {
		c2, rest, ok = isKeywordChar(rest[1:])
		if !ok {
			return 0, 0, false, false
		}
	}
	if rest != "" || c <= 0 || c > 255 || c2 > 255 || (c2 != -1 && c2 < c) {
		return 0, 0, false, false
	}
	if c2 == -1 {
		if c == '@' {
			return 1, 255, true, true
		}
		c2 = c
	}
	return c, c2, false, true
}

// isKeywordChar reads a decimal number or a single character off the front.
func isKeywordChar(s string) (int, string, bool) {
	if s == "" {
		return 0, "", false
	}
	if isDigit(s[0]) {
		n := 0
		i := 0
		for i < len(s) && isDigit(s[i]) {
			n = n*10 + int(s[i]-'0')
			if n > 1<<20 {
				return 0, "", false
			}
			i++
		}
		return n, s[i:], true
	}
	r, n := utf8.DecodeRuneInString(s)
	return int(r), s[n:], true
}

// isCased is vim's isalpha() for a Latin-1 code point under 'encoding=utf-8':
// a character with an upper or lower case, which takes in é, µ and ß and
// leaves out ª, º, × and ÷.
func isCased(r rune) bool { return unicode.IsUpper(r) || unicode.IsLower(r) }
