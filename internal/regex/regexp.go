package regex

import "regexp"

// Options are the editor settings a pattern's meaning depends on.
//
// They are passed in rather than read from a global because a pattern is
// compiled once and used for as long as 'hlsearch' keeps it alive, and an
// option that changed underneath a compiled pattern would change what the
// screen highlights without recompiling anything.
type Options struct {
	// IgnoreCase and SmartCase are the two options of those names. \c and \C in
	// the pattern override both.
	IgnoreCase bool
	SmartCase  bool

	// NoMagic is the 'magic' option turned off, which starts the pattern at \M
	// instead of \m. Vim has shipped with 'magic' on since before this was a
	// question and nothing sets it off, but a pattern compiled with it on when
	// it is off means something else, so it is here rather than assumed.
	NoMagic bool

	// LastSubstitute is what `~` expands to: the replacement text of the last
	// :s. It is inserted literally, exactly as vim inserts it.
	LastSubstitute string
}

// magic gives the level a pattern starts at.
func (o Options) magic() Magic {
	if o.NoMagic {
		return NoMagic
	}
	return MagicOn
}

// Regexp is a compiled vim pattern.
//
// It deliberately does not embed *regexp.Regexp. Everything the editor needs is
// byte offsets into a line, and exposing the standard library's whole surface
// would let a caller reach ReplaceAllString, whose $1 syntax is not vim's \1
// and which would put a second dialect in the tree.
type Regexp struct {
	vim        string
	src        string
	ignoreCase bool
	re         *regexp.Regexp
}

// Compile translates a vim pattern to Go regexp source and compiles it.
//
// An error is either a Refused, meaning valid vim that pvim will not run, a
// SyntaxError, meaning a pattern nobody would run, or a CompileError, meaning
// the source was fine vim and RE2 still would not take it.
func Compile(pattern string, opt Options) (*Regexp, error) {
	src, ic, err := translate(pattern, opt)
	if err != nil {
		return nil, err
	}
	re, err := regexp.Compile(src)
	if err != nil {
		return nil, CompileError{Pattern: pattern, Source: src, Err: err}
	}
	return &Regexp{vim: pattern, src: src, ignoreCase: ic, re: re}, nil
}

// MustCompile is Compile for patterns written in this repository, where a
// failure is a bug and not a user's typo.
func MustCompile(pattern string, opt Options) *Regexp {
	re, err := Compile(pattern, opt)
	if err != nil {
		panic("regex: " + err.Error())
	}
	return re
}

// Translate returns the Go regexp source a vim pattern turns into, without
// compiling it. It exists for the tests and for the day someone has to see why
// a pattern matched what it did.
func Translate(pattern string, opt Options) (string, error) {
	src, _, err := translate(pattern, opt)
	return src, err
}

// String gives back the vim pattern, so that a Regexp prints as the thing the
// user typed.
func (r *Regexp) String() string { return r.vim }

// Source gives the Go regexp source. This is the only place the translation is
// visible, and it is what an E-code message and a bug report both want.
func (r *Regexp) Source() string { return r.src }

// IgnoreCase reports how the case flags, 'ignorecase' and 'smartcase' came out
// for this pattern. The search prompt shows it and :s needs it.
func (r *Regexp) IgnoreCase() bool { return r.ignoreCase }

// NumSubexp is the number of \( \) groups.
func (r *Regexp) NumSubexp() int { return r.re.NumSubexp() }

// Match reports whether b contains a match.
func (r *Regexp) Match(b []byte) bool { return r.re.Match(b) }

// MatchString reports whether s contains a match.
func (r *Regexp) MatchString(s string) bool { return r.re.MatchString(s) }

// FindIndex gives the byte offsets of the leftmost match, or nil.
func (r *Regexp) FindIndex(b []byte) []int { return r.re.FindIndex(b) }

// FindStringIndex gives the byte offsets of the leftmost match, or nil.
func (r *Regexp) FindStringIndex(s string) []int { return r.re.FindStringIndex(s) }

// FindSubmatchIndex gives the byte offsets of the leftmost match and of every
// group in it, which is what :s needs to build a replacement.
func (r *Regexp) FindSubmatchIndex(b []byte) []int { return r.re.FindSubmatchIndex(b) }

// FindStringSubmatchIndex is FindSubmatchIndex over a string.
func (r *Regexp) FindStringSubmatchIndex(s string) []int { return r.re.FindStringSubmatchIndex(s) }

// FindAllIndex gives every non-overlapping match, at most n of them, or all of
// them when n is negative. This is what 'hlsearch' walks a line with.
func (r *Regexp) FindAllIndex(b []byte, n int) [][]int { return r.re.FindAllIndex(b, n) }

// FindAllStringIndex is FindAllIndex over a string.
func (r *Regexp) FindAllStringIndex(s string, n int) [][]int { return r.re.FindAllStringIndex(s, n) }
