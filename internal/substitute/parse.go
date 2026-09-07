package substitute

import (
	"errors"
	"strings"

	"github.com/pkar/pvim/internal/options"
)

// ErrLetterDelim is E146: a letter or a digit cannot be the delimiter of a
// pattern, because ":gx" would be indistinguishable from a command named "gx".
var ErrLetterDelim = errors.New("E146: Regular expressions can't be delimited by letters")

// PatternSource says which of vim's two remembered patterns an empty or absent
// one comes from. See State for what the two are.
type PatternSource int

const (
	// FromSubstitute is vim's RE_SUBST: the pattern of the last ":s",
	// whatever has been searched for since. ":s" with no delimiter and ":&"
	// use it.
	FromSubstitute PatternSource = iota
	// FromLast is vim's RE_LAST: whichever of the two slots was written most
	// recently. An empty pattern, ":~" and the "r" flag use it.
	FromLast
)

// Kind is which of the three spellings of a substitute is being parsed. They
// differ in one thing only: where an absent pattern comes from.
type Kind int

const (
	// KindSubstitute is ":s" and ":substitute".
	KindSubstitute Kind = iota
	// KindAmpersand is ":&" and, with the "&" flag, ":&&". Normal mode's "&"
	// is this with no range.
	KindAmpersand
	// KindTilde is ":~", which is ":&" reading the last used pattern instead
	// of the last substitute pattern.
	KindTilde
)

// Cmd is a parsed ":s" command line: everything after the command name and the
// range, resolved as far as it can be without a buffer.
type Cmd struct {
	// Pattern is the pattern as typed, still in vim's dialect and still
	// carrying its backslashes. Empty means "the remembered one", which is
	// not the same as HavePattern being false.
	Pattern string
	// HavePattern says a delimiter was present at all, which is the
	// difference between ":s//x/" and ":s". The first takes its pattern from
	// the remembered one and raises E35 when there is none; the second takes
	// both halves from the last ":s" and raises E33.
	HavePattern bool
	// Replacement is the replacement as typed, before "~" is expanded and
	// before Expand runs over it.
	Replacement string
	// HaveReplacement is HavePattern: a delimiter form always supplies a
	// replacement, an absent one being the empty string, and a repeat form
	// never does.
	HaveReplacement bool
	// Which is where an empty or absent pattern comes from.
	Which PatternSource
	Flags Flags
}

// Parse reads a ":s" command line.
//
// args is everything after the command name, so ":%s/a/b/g" arrives here as
// "/a/b/g". st supplies the flags a leading "&" inherits and o supplies
// 'gdefault'; both may be nil, which is the shape a table test wants.
//
// The first character decides the whole shape, and the test is vim's: for
// ":s", anything that is not a letter, a digit, a double quote or a bar is the
// delimiter, so "#", "," and even "&" all work. A digit is not, which is why
// ":s3" is the repeat form with a count of three and not a pattern delimited by
// 3; a double quote is not, because it starts an ex comment; a bar is not,
// because internal/ex has already cut the line there. A backslash is E10.
func Parse(kind Kind, args string, st *State, o *options.Options) (Cmd, error) {
	prev := st.prevFlags()
	gd := o != nil && o.G.GDefault

	c := Cmd{Which: FromSubstitute}
	if kind == KindTilde {
		c.Which = FromLast
	}

	s := strings.TrimLeft(args, " \t")

	// Only ":s" ever takes a pattern. ":&" and ":~" are repeats by
	// definition, so ":2&/a/Y/" is not a substitution with a new pattern, it
	// is E488 with "/a/Y/" in it. Measured, because the two commands read like
	// abbreviations of ":s" and are not.
	if kind != KindSubstitute || s == "" || isAlnum(s[0]) || s[0] == '"' || s[0] == '|' {
		// The repeat form, with or without flags on it: ":2sg", ":2s g",
		// ":2&g" and ":2&&". A state with no substitute in it fails here and
		// not at the flags, which is vim's order: ":scacXc" with no previous
		// ":s" is E33 and the same command after one is E488.
		if st == nil || !st.HaveReplacement {
			return c, ErrNoPrevSub
		}
		f, err := ParseFlags(s, prev, gd)
		c.Flags = f
		return c, err
	}
	if s[0] == '\\' {
		return c, ErrBackslashDelim
	}

	// The delimiter form. A delimiter form always resolves an empty pattern
	// from the last used one, whichever command spelled it.
	c.Which = FromLast
	delim := s[0]
	pat, rest, closed := split(s[1:], delim)
	c.Pattern, c.HavePattern = pat, true
	c.HaveReplacement = true
	if !closed {
		// ":%s/b" with nothing after it: the replacement is empty and there
		// are no flags.
		f, err := ParseFlags("", prev, gd)
		c.Flags = f
		return c, err
	}
	sub, tail, _ := split(rest, delim)
	c.Replacement = sub
	if strings.HasPrefix(sub, `\=`) {
		return c, ErrExpression
	}
	f, err := ParseFlags(strings.TrimLeft(tail, " \t"), prev, gd)
	c.Flags = f
	return c, err
}

// split cuts s at the first unescaped delimiter and reports whether it found
// one. The backslash stays in the returned text: internal/regex wants the
// pattern as the user typed it and Expand wants the replacement the same way,
// and an escaped delimiter is meaningful to both.
func split(s string, delim byte) (head, tail string, closed bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++ // whatever follows a backslash is not a delimiter
			continue
		}
		if s[i] == delim {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// isAlnum is ASCII only, on purpose: vim's ASCII_ISALNUM is what decides
// whether a byte can be a delimiter, and a multi-byte character can be one.
func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// Global is a parsed ":g" or ":v" command line.
type Global struct {
	// Pattern is the pattern as typed. Empty with HavePattern set is ":g//",
	// which uses the remembered one.
	Pattern     string
	HavePattern bool
	// Which is where an empty pattern comes from. ":g\/cmd" asks for the last
	// search pattern and ":g\&cmd" for the last substitute pattern, which are
	// the only two spellings that can tell them apart.
	Which PatternSource
	// Command is the ex command to run on each marked line. Empty is "p",
	// which is what ":g/x/" prints and what makes ":g/x/" a grep.
	Command string
	// Invert is ":v" and ":g!": act on the lines that do NOT match.
	Invert bool
}

// ParseGlobal reads a ":g" or ":v" command line. args is everything after the
// command name and any "!".
func ParseGlobal(args string, invert bool) (Global, error) {
	g := Global{Invert: invert, Which: FromLast}
	s := strings.TrimLeft(args, " \t")
	if s == "" {
		return g, ErrGlobalMissing
	}
	if s[0] == '\\' {
		// ":g\/cmd" and ":g\&cmd": no pattern of its own, take a remembered
		// one. Anything else after the backslash is E10.
		if len(s) < 2 || (s[1] != '/' && s[1] != '?' && s[1] != '&') {
			return g, ErrBackslashDelim
		}
		if s[1] == '&' {
			g.Which = FromSubstitute
		}
		g.HavePattern = true
		g.Command = s[2:]
		return g, nil
	}
	if isAlnum(s[0]) {
		return g, ErrLetterDelim
	}
	delim := s[0]
	pat, rest, _ := split(s[1:], delim)
	g.Pattern, g.HavePattern = pat, true
	g.Command = rest
	return g, nil
}

// Normal is a parsed ":normal".
type Normal struct {
	// Keys is the rest of the line, verbatim. Every byte of it is a
	// keystroke: a "|" does not end the command and a double quote does not
	// start a comment, which is why ":normal ia|ib" inserts "a|ib".
	Keys string
	// NoRemap is the "!" of ":normal!": run the keys with no mappings.
	NoRemap bool
}

// ParseNormal reads a ":normal" command line. args is everything after the
// command name and the "!".
//
// Leading whitespace is dropped and nothing else is: ":normal ix" inserts "x"
// and not " x", which is measured and is the one thing about this command that
// is worth a test of its own.
func ParseNormal(args string, bang bool) (Normal, error) {
	keys := strings.TrimLeft(args, " \t")
	if keys == "" {
		return Normal{}, ErrArgRequired
	}
	return Normal{Keys: keys, NoRemap: bang}, nil
}

// Terminator is what internal/ex feeds the mode machine when the keys of a
// ":normal" run out in the middle of a command.
//
// Vim does not append it to the argument. It arranges for the key reader to
// answer Escape once the typeahead a ":normal" pushed is exhausted, so ":normal
// d" ends with the operator abandoned and ":normal ihello" ends with insert
// mode left and the cursor back one, both of which are measured. The effect is
// the same as feeding Escape until the editor is in normal mode with nothing
// pending, which is what the ex layer does, and it has to be a loop rather than
// one key: ":normal 2d" needs the operator abandoned and the count with it.
const Terminator = 0x1b
