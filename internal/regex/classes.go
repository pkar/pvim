package regex

// class is one of vim's backslash-letter character classes. body is the inside
// of a Go character class; negated says the vim atom means everything the body
// does not.
//
// Storing \S as the negation of \s rather than as its own body is what makes
// the \_ forms fall out for free: \_s is \s plus the line break, and \_S is \S
// plus the line break, which is the same body with the newline moving from one
// side of the ^ to the other.
type class struct {
	body    string
	negated bool
}

// expr renders the class as Go source. eol is true for the \_ forms, which
// include the line break.
//
// The newline is excluded from every negated class by default because vim's
// classes never match a line break unless asked, and Go's [^ \t] does. Get that
// backwards and \S\+ swallows the rest of the buffer the first time a pattern
// is run over more than one line.
func (c class) expr(eol bool) string {
	switch {
	case c.negated && eol:
		return "[^" + c.body + "]"
	case c.negated:
		return "[^" + c.body + `\n]`
	case eol:
		return "[" + c.body + `\n]`
	default:
		return "[" + c.body + "]"
	}
}

// The character classes, keyed by the letter that follows the backslash.
//
// Every body here was read off vim 9.2 rather than out of the help: each atom
// was run against every character from 0x20 to 0x7e plus a spread of Latin-1,
// CJK, symbol and emoji code points, and the body is the set that came back.
// The help says \i is "identifier character (see 'isident')" and leaves the
// reader to work out that the default 'isident' of "@,48-57,_,192-255" means
// 0xb5 is in and 0xaa is out, which is the sort of thing only the binary knows.
//
// 'isident', 'iskeyword', 'isfname' and 'isprint' are frozen at their defaults.
// The vimrc never sets any of them, and threading four option strings into
// pattern compilation to support a setting nobody has is the wrong trade.
var classes = map[byte]class{
	's': {body: ` \t`},
	'S': {body: ` \t`, negated: true},
	'd': {body: `0-9`},
	'D': {body: `0-9`, negated: true},
	'x': {body: `0-9A-Fa-f`},
	'X': {body: `0-9A-Fa-f`, negated: true},
	'o': {body: `0-7`},
	'O': {body: `0-7`, negated: true},
	'w': {body: `0-9A-Za-z_`},
	'W': {body: `0-9A-Za-z_`, negated: true},
	'h': {body: `A-Za-z_`},
	'H': {body: `A-Za-z_`, negated: true},
	'a': {body: `A-Za-z`},
	'A': {body: `A-Za-z`, negated: true},
	'l': {body: `a-z`},
	'L': {body: `a-z`, negated: true},
	'u': {body: `A-Z`},
	'U': {body: `A-Z`, negated: true},

	// The four option-driven classes. \I, \K, \F and \P are vim's "same but
	// without the digits", which is a subset and not a complement, so they get
	// their own bodies rather than a negated flag.
	'i': {body: identBody},
	'I': {body: identNoDigits},
	'k': {body: keywordBody},
	'K': {body: keywordNoDigits},
	'f': {body: fnameBody},
	'F': {body: fnameNoDigits},
	'p': {body: printBody},
	'P': {body: printNoDigits},
}

// The two option-driven bodies that are still rules. 'iskeyword' and 'isprint'
// are lists of code points instead, and they live in classtab.go with the sweep
// that produced them.
//
// 0xb5 is MICRO SIGN and is in 'isident' because the "@" entry means isalpha()
// and vim decides that for a Latin-1 code point by asking whether it is upper
// or lower case; 0xaa and 0xba are ordinal indicators, letters in Unicode but
// neither case, and vim leaves them out. That one code point is the whole
// difference between this body and a guess.
const (
	identBody     = `0-9A-Za-z_\x{00b5}\x{00c0}-\x{00ff}`
	identNoDigits = `A-Za-z_\x{00b5}\x{00c0}-\x{00ff}`

	// 'isfname' defaults to "@,48-57,/,.,-,_,+,#,$,%,~,=" and vim treats
	// every code point from 0xa0 up as a filename character regardless.
	fnameBody     = `#$%+,\-./0-9=A-Za-z_~\x{00a0}-\x{10ffff}`
	fnameNoDigits = `#$%+,\-./=A-Za-z_~\x{00a0}-\x{10ffff}`
)

// posixClasses maps vim's [:name:] forms to the inside of a Go character class.
//
// Go carries most of them under the same name, and those are passed through
// unchanged so that the emitted source still reads like the pattern that was
// typed. The five vim invented and the three whose meaning differs are
// expanded:
//
// - [:print:] in Go is ASCII 0x20-0x7e; in vim it is 'isprint', which takes
// almost everything from 0xa0 up, so a printable-character search would
// silently stop matching accented text.
// - [:lower:] and [:upper:] in Go are a-z and A-Z; vim asks whether the
// character has a case counterpart, which takes in é, the titlecase
// letters and the small roman numerals, and leaves out the letters that
// have no counterpart to have.
//
// The list of names is vim's class_names, exactly: a name outside it is not an
// error but an ordinary run of characters, which collection.go handles.
var posixClasses = map[string]string{
	"alnum":  `[:alnum:]`,
	"alpha":  `[:alpha:]`,
	"blank":  `[:blank:]`,
	"cntrl":  `[:cntrl:]`,
	"digit":  `[:digit:]`,
	"graph":  `[:graph:]`,
	"punct":  `[:punct:]`,
	"space":  `[:space:]`,
	"xdigit": `[:xdigit:]`,

	"lower": lowerBody,
	"upper": upperBody,
	"print": printBody,

	"return":    `\r`,
	"tab":       `\t`,
	"escape":    `\x1b`,
	"backspace": `\x08`,
	"ident":     identBody,
	"keyword":   keywordBody,
	"fname":     fnameBody,
}
