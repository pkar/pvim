package substitute

import "strings"

// Flags is the letters after the last delimiter of a ":s", resolved against
// whatever they started from.
//
// The letters toggle rather than set, which is vim and which nobody expects:
// ":s/a/b/gg" is not global and ":s/a/b/ggg" is. So a Flags value is the
// answer and not the question, and ParseFlags is where a base and a string of
// letters become one.
type Flags struct {
	// All is the "g" flag: every match on a line and not only the first. It
	// starts at 'gdefault', which inverts what a bare ":s" means.
	All bool
	// Confirm is "c": prompt before each substitution.
	Confirm bool
	// CountOnly is "n": count the matches, substitute nothing, and print "N
	// matches on M lines". It is the one flag "&" never inherits, because vim
	// resets it on every command.
	CountOnly bool
	// KeepErrors is the absence of "e": a pattern that matches nothing raises
	// E486. It starts true, and each "e" toggles it.
	KeepErrors bool
	// IgnoreCase is "i" and MatchCase is "I": override 'ignorecase' AND
	// 'smartcase' for this substitution only. They are not toggles; the last
	// one given wins.
	IgnoreCase, MatchCase bool
	// Print is "p", List is "l" and Number is "#": print the last changed line
	// afterwards, as text, in 'list' form, or with its line number. "l" and
	// "#" both imply "p".
	Print, List, Number bool
	// Repeat is "&": start from the flags of the previous ":s" instead of from
	// the defaults. It only counts as the FIRST flag; ":s/a/b/g&" is E488.
	Repeat bool
	// Reverse is "r": take an empty pattern from whichever pattern was used
	// last rather than from the last substitute pattern, which is what makes
	// ":&r" and ":~" different from ":&".
	Reverse bool
	// Count is the trailing count. It does not shrink the range, it moves it:
	// the range becomes Count lines starting at its own last line, so
	// ":%s/a/b/ 3" on a six-line file is lines 6 to 8.
	Count int
}

// DefaultFlags is where the letters start when "&" was not the first of them.
// Only two are not zero: "g" starts at 'gdefault', and errors are on until an
// "e" turns them off.
func DefaultFlags(gdefault bool) Flags {
	return Flags{All: gdefault, KeepErrors: true}
}

// InitialFlags is what the first "&" of a session inherits, before any ":s"
// has run and left flags of its own behind.
//
// It is vim's static subflags at its initialiser, which is everything off
// except do_error, and it is deliberately NOT DefaultFlags: vim's "&" skips
// the block that reads 'gdefault', so ":set gdefault" and then ":s/o/X/&" as
// the first substitute of the session replaces one "o" and not three.
// Measured, both halves: the same command without the "&" replaces three, and
// ":s/zzz/b/&" with nothing before it prints E486 rather than nothing.
func InitialFlags() Flags {
	return Flags{KeepErrors: true}
}

// ParseFlags reads the flag letters and the optional trailing count off the
// tail of a ":s" command.
//
// prev is the flags of the last ":s", which the leading "&" asks for, and
// gdefault is the option, which is where everything else starts. With no ":s"
// behind it, prev is InitialFlags and not the zero value: the difference is
// E486, which a zero KeepErrors turns off. What is left over is E488 and its
// text, because vim prints the offending characters and a habit keyed to
// "E488" wants the same code from the same input.
//
// The order is fixed: an optional "&", then letters, then whitespace, then a
// count. A letter after the count is E488 ("...:1s/a/X/3g"), and so is an "&"
// that is not first.
func ParseFlags(s string, prev Flags, gdefault bool) (Flags, error) {
	f := DefaultFlags(gdefault)
	i := 0
	if i < len(s) && s[i] == '&' {
		// "&" keeps the previous flags, except "n", which vim resets on every
		// command whatever else it does.
		f = prev
		f.CountOnly = false
		f.Count = 0
		f.Repeat = true
		i++
	}

	for ; i < len(s); i++ {
		switch s[i] {
		case 'g':
			f.All = !f.All
		case 'c':
			f.Confirm = !f.Confirm
		case 'n':
			f.CountOnly = true
		case 'e':
			f.KeepErrors = !f.KeepErrors
		case 'r':
			f.Reverse = true
		case 'p':
			f.Print = true
		case '#':
			f.Print, f.Number = true, true
		case 'l':
			f.Print, f.List = true, true
		case 'i':
			f.IgnoreCase, f.MatchCase = true, false
		case 'I':
			f.IgnoreCase, f.MatchCase = false, true
		default:
			goto count
		}
	}

count:
	rest := strings.TrimLeft(s[i:], " \t")
	j := 0
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		j++
	}
	if j > 0 {
		n := 0
		for _, c := range rest[:j] {
			n = n*10 + int(c-'0')
		}
		f.Count = n
	}
	if left := strings.TrimRight(rest[j:], " \t"); left != "" {
		return f, TrailingError{Rest: left}
	}
	return f, nil
}
