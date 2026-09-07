// Package textobj finds the span iw, aw, i", ap and it name.
//
// A text object is not a motion. It has no direction and no start: it is a
// range around a position, chosen by count and by whether the user typed i or
// a, and it is only ever used with an operator or in visual mode. So it returns
// a range directly rather than a destination and a kind to resolve, and the
// only thing left for the caller to decide is what kind of span it is:
// ip and ap are linewise, and iw, aw, i", i(, i{, i< and it are charwise.
//
// The three that are worth writing tests for before writing code are i", which
// has its own rule about which quote is the opening one on a line with four of
// them, ip, which counts blank lines as a paragraph of their own, and it, which
// has to find a matching tag through nesting and through attributes with > in
// them.
//
// Every function in here is a port of the function of the same job in vim's
// textobject.c, and every one of them is checked against vim 9.2.0321 itself:
// testdata/vim92.tsv holds vim's answer for every object, at every count from
// one to three, at every cursor position of eight fixture files, and
// TestVimTable runs all 89,424 of them. Nothing here was written from memory
// of what vim does and left that way; where a rule below sounds too strange to
// be real, it is in the table.
//
// One rule belongs to the whole package rather than to any object, and it is
// in finish(): a charwise object that ends exclusively in column one, having
// started at or before the first non-blank of its own line, becomes linewise.
// That is what makes di{ over a brace block whose braces are each alone on
// their line delete the lines between them, and it fires for is, ap and aw as
// well. It does not fire in visual mode, and it does not fire for it and at.
package textobj

import (
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// Options are the settings a text object's answer depends on.
type Options struct {
	// IsKeyword is 'iskeyword', which decides where iw and aw end.
	IsKeyword string
	// TabStop is 'tabstop', for the objects that measure display columns.
	TabStop int
	// MatchPairs is 'matchpairs'. i( and friends do not read it -- their pairs
	// are fixed by the key that was typed -- but i% would, if it ever exists.
	MatchPairs string
	// Paragraphs and Sections are the nroff macro lists ip, ap, i[ and a[
	// consult on top of their blank-line rules.
	Paragraphs string
	Sections   string
	// Selection is 'selection': "inclusive", "exclusive" or "old". It changes
	// where a visual selection's end sits and therefore what an object
	// extended from one covers.
	Selection string
	// QuoteEscape is 'quoteescape', the characters that hide a quote inside a
	// string from i" and a". Empty means vim's default of a backslash, which
	// is what it is until internal/options grows a field for it.
	QuoteEscape string
}

// DefaultOptions is vim's own defaults for the settings above.
func DefaultOptions() Options {
	return Options{
		IsKeyword:   "@,48-57,_,192-255",
		TabStop:     8,
		MatchPairs:  "(:),{:},[:]",
		Paragraphs:  "IPLPPPQPP TPHPLIPpLpItpplpipbp",
		Sections:    "SHNHH HUnhsh",
		Selection:   "inclusive",
		QuoteEscape: "\\",
	}
}

// Request is one text object, asked for.
type Request struct {
	// Buf is the buffer. A text object never changes it.
	Buf *text.Buffer
	// At is the cursor, which is the position the object is found around.
	At text.Pos
	// Count is the count typed, already multiplied out. Zero means none, and
	// every object treats that as one.
	Count int
	// Inner is true for i and false for a. It is not a cosmetic difference:
	// aw takes the trailing whitespace, ap takes the blank lines after the
	// paragraph, and a" takes the quotes and the white space after them.
	Inner bool
	// Arg is the character that named the object for the ones that need it:
	// the quote for i" i' i`, the bracket for i( i[ i{ i<, and zero
	// otherwise. It is separate from the Object's own Key because ib and iB
	// are aliases for i( and i{ and arrive here as the pair they mean.
	//
	// Nothing reads it: every entry in the table already knows its own pair
	// or quote, so ByKey('b') answers exactly as ByKey('(') does whatever is
	// in here. It stays in the request because a message about a failed
	// object wants to name the key the person typed.
	Arg byte
	// Sel is the visual selection the object is being asked for inside, and
	// HasSel says whether there is one. A selection of one character, which
	// is what "v" alone gives, produces the same object an operator would get
	// with one difference: the rule that turns a charwise object ending in
	// column one into a linewise one does not run, so vi{ over a brace block
	// selects the text between the braces where di{ takes the lines.
	//
	// Extending an existing selection -- vi(vi( growing outwards, viwiw
	// taking the next word -- is NOT implemented: an object asked for inside
	// a selection wider than one character answers as if the cursor were
	// alone, so the second vi( repeats the first instead of growing. Every
	// branch of that in vim is per object and none of it is verified against
	// vim yet, and a wrong answer here is a wrong selection the person can
	// see, not a wrong edit.
	Sel    text.Range
	HasSel bool
	// Opt is the settings above.
	Opt Options
}

// Count1 is the count with vim's default of one filled in.
func (r Request) Count1() int {
	if r.Count < 1 {
		return 1
	}
	return r.Count
}

// Result is the span the object covers.
//
// The two types read Range differently, and this is the one thing a caller has
// to get right:
//
// - TypeChar: Range is half-open in bytes, Start included and End not, the
// way text.Range always is. An empty Range is a real answer and not a
// failure -- iw on an empty line gives one, d does nothing with it and c
// opens insert mode there.
// - TypeLine: only the line numbers mean anything and both ends are
// INCLUDED. Columns are zero. That is the shape operator.Span uses for a
// linewise span, so a linewise object hands straight through.
//
// An object is never exclusive or inclusive here: vim's rules about that have
// already been applied, including the one that turns a charwise object ending
// in column one into a linewise one, and this is the span they produced.
type Result struct {
	// Range is the text: half-open for TypeChar, inclusive line numbers for
	// TypeLine. See above.
	Range text.Range
	// Type is charwise for iw, aw, i", i( and it, and linewise for ip and ap.
	// It is register.Type and not a fourth enum because it is the same
	// question a register answers and the same one an operator asks.
	Type register.Type
	// Ok is false when there is no such object here: i( with no unmatched
	// bracket around the cursor, it outside any tag. vim beeps and the
	// operator is abandoned.
	Ok bool
	// Moved says a FAILED object still left the cursor somewhere new, and
	// Range.Start is where. Only iw, aw, iW and aW set it: vim's
	// current_word() walks the cursor forward as it counts objects and
	// returns FAIL without putting it back, where current_block(),
	// current_quote() and current_sent() all restore it. Measured on
	// "gamma": "2daW" changes nothing, beeps, and leaves the cursor on the
	// last "a" where it started on the first "g".
	Moved bool
	// EndAdjusted says the column-one rule above fired, whichever half of it
	// the object came out of. It is vim's oap->end_adjusted and exactly one
	// thing reads it: "gq" puts the cursor one line further on when it is set,
	// so that a following "." formats the next paragraph rather than this one
	// again. Measured, "gqas" on a buffer whose first line is empty leaves the
	// cursor on line 2.
	EndAdjusted bool
}

// Lines is the inclusive range of lines a linewise result covers.
func (r Result) Lines() (first, last int) {
	return r.Range.Start.Line, r.Range.End.Line
}

// Func finds one text object.
type Func func(r Request) Result

// Object is one entry in the table.
type Object struct {
	// Key is the character after i or a: 'w', 'W', 's', 'p', '(', '"', 't'
	// and the rest. Objects are always one character, which is why this is a
	// byte and not a notation string the way a motion's is.
	Key byte
	// Name is what the object is called in a message and a test failure.
	Name string
	// Type is charwise or linewise. ip and ap are the linewise ones and
	// everything else is charwise; no text object is blockwise.
	Type register.Type
	// Find runs it.
	Find Func
}

// newScan puts a cursor on the request's buffer at the request's position,
// with 'iskeyword' parsed.
func newScan(r Request) *scan {
	esc := r.Opt.QuoteEscape
	if esc == "" {
		esc = DefaultOptions().QuoteEscape
	}
	s := &scan{
		b:           r.Buf,
		visual:      r.HasSel && r.Opt.Selection != "old",
		kw:          parseKeywords(r.Opt.IsKeyword),
		quoteEscape: esc,
		paragraphs:  r.Opt.Paragraphs,
		sections:    r.Opt.Sections,
	}
	s.at(r.Buf.Clamp(r.At))
	return s
}

// word is iw, aw, iW and aW. big folds punctuation into words, which is the
// whole of the difference between w and W.
func word(big bool) Func {
	return func(r Request) Result {
		s := newScan(r)
		res := finish(s, currentWord(s, r.Count1(), !r.Inner, big))
		if !res.Ok {
			// vim's current_word leaves the cursor where the count ran out.
			// See Result.Moved.
			res.Range.Start = s.p
			res.Moved = s.p != r.Buf.Clamp(r.At)
		}
		return res
	}
}

// sentence is is and as.
func sentence(r Request) Result {
	s := newScan(r)
	return finish(s, currentSent(s, r.Count1(), !r.Inner))
}

// paragraph is ip and ap, the only linewise objects there are.
func paragraph(r Request) Result {
	s := newScan(r)
	return currentPar(s, r.Count1(), !r.Inner)
}

// block is i( a( i{ a{ i[ a[ i< a< and the b and B spellings of the first two.
// open and closer are the pair; the object's own key decides them, so ib and
// i( are the same function with the same arguments and not a lookup.
func block(open, closer byte) Func {
	return func(r Request) Result {
		s := newScan(r)
		return finish(s, currentBlock(s, r.Count1(), !r.Inner, open, closer))
	}
}

// quoted is i" a" i' a' i` and a`.
func quoted(quote byte) Func {
	return func(r Request) Result {
		s := newScan(r)
		return finish(s, currentQuote(s, r.Count1(), !r.Inner, quote))
	}
}

// tagBlock is it and at.
func tagBlock(r Request) Result {
	s := newScan(r)
	return finish(s, currentTagBlock(s, r.Count1(), !r.Inner))
}

// table is every text object, keyed by the character after i or a.
var table = map[byte]Object{}

// add puts an object in the table, panicking on a duplicate key.
func add(o Object) {
	if _, dup := table[o.Key]; dup {
		panic("textobj: two objects bound to " + string(rune(o.Key)))
	}
	table[o.Key] = o
}

func init() {
	for _, o := range []Object{
		{Key: 'w', Name: "word", Type: register.TypeChar, Find: word(false)},
		{Key: 'W', Name: "WORD", Type: register.TypeChar, Find: word(true)},
		{Key: 's', Name: "sentence", Type: register.TypeChar, Find: sentence},
		// The only two linewise objects there are.
		{Key: 'p', Name: "paragraph", Type: register.TypeLine, Find: paragraph},
		// Brackets. b and B are aliases vim documents for ( and {, and they
		// are separate entries rather than a lookup indirection so that a
		// message can say which key was typed.
		{Key: '(', Name: "parens", Type: register.TypeChar, Find: block('(', ')')},
		{Key: ')', Name: "parens", Type: register.TypeChar, Find: block('(', ')')},
		{Key: 'b', Name: "parens", Type: register.TypeChar, Find: block('(', ')')},
		{Key: '{', Name: "braces", Type: register.TypeChar, Find: block('{', '}')},
		{Key: '}', Name: "braces", Type: register.TypeChar, Find: block('{', '}')},
		{Key: 'B', Name: "braces", Type: register.TypeChar, Find: block('{', '}')},
		{Key: '[', Name: "brackets", Type: register.TypeChar, Find: block('[', ']')},
		{Key: ']', Name: "brackets", Type: register.TypeChar, Find: block('[', ']')},
		{Key: '<', Name: "angles", Type: register.TypeChar, Find: block('<', '>')},
		{Key: '>', Name: "angles", Type: register.TypeChar, Find: block('<', '>')},
		// Quotes. These are the ones with the rule that surprises people: the
		// object is found by counting quotes from the start of the line, so
		// the cursor being between two of them is not enough to say which pair
		// it is in, and a" takes the white space after the closing quote and
		// only takes the white space before it when there is none after.
		{Key: '"', Name: "double quotes", Type: register.TypeChar, Find: quoted('"')},
		{Key: '\'', Name: "single quotes", Type: register.TypeChar, Find: quoted('\'')},
		{Key: '`', Name: "backticks", Type: register.TypeChar, Find: quoted('`')},
		// Tags. it and at, which the vimrc has no HTML in but
		// names explicitly.
		{Key: 't', Name: "tag block", Type: register.TypeChar, Find: tagBlock},
	} {
		add(o)
	}
}

// ByKey returns the object bound to the character after i or a.
func ByKey(c byte) (Object, bool) {
	o, ok := table[c]
	return o, ok
}

// All returns every object, for a test that wants to walk them. The order is
// not defined.
func All() []Object {
	out := make([]Object, 0, len(table))
	for _, o := range table {
		out = append(out, o)
	}
	return out
}
