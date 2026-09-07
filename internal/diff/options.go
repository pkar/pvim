package diff

import "strings"

// Options is the part of 'diffopt' that changes what this package computes.
//
// The vimrc sets
//
//	set diffopt=vertical,filler,iwhite
//
// which is the whole of what has to work; the rest is parsed because a
// 'diffopt' word that is silently dropped is worse than one that is refused,
// and because ":set diffopt" is already in internal/options' table with every
// word vim allows.
type Options struct {
	// Filler is "filler": draw the filler lines. It changes nothing this
	// package computes -- Pair.Filler answers the same either way -- and is
	// carried so that a frontend has one place to read 'diffopt' from.
	Filler bool
	// Vertical is "vertical": ":diffsplit" and ":Gdiff" split vertically.
	Vertical bool
	// IWhite is "iwhite": a run of white space equals any other run, and
	// white space at the end of a line is ignored. This is diff(1)'s -b.
	IWhite bool
	// IWhiteAll is "iwhiteall": all white space is ignored, diff(1)'s -w.
	IWhiteAll bool
	// IWhiteEol is "iwhiteeol": white space at the end of a line is ignored
	// and nothing else is.
	IWhiteEol bool
	// ICase is "icase": upper and lower case compare equal.
	ICase bool
	// IBlank is "iblank", parsed and NOT applied. It is not a normalisation
	// of a line the way the other four are: xdiff implements it as a pass
	// over the finished script that drops hunks whose every line is blank,
	// and an approximation of that would put fillers where vim puts none.
	// The vimrc does not set it. Applying it is a day's work with the vim
	// oracle already written, and until someone wants it this field is here
	// so that ":set diffopt+=iblank" is a word this package has heard of
	// rather than one it drops on the floor.
	IBlank bool
}

// ParseOptions reads a 'diffopt' string.
//
// Words this package does not act on -- "internal", "closeoff", "context:4",
// "algorithm:patience" and the rest -- are skipped rather than refused,
// because internal/options already checks that a word is one vim allows and a
// second opinion here would be a second table to keep in step.
func ParseOptions(s string) Options {
	var o Options
	for _, w := range strings.Split(s, ",") {
		switch strings.TrimSpace(w) {
		case "filler":
			o.Filler = true
		case "vertical":
			o.Vertical = true
		case "iwhite":
			o.IWhite = true
		case "iwhiteall":
			o.IWhiteAll = true
		case "iwhiteeol":
			o.IWhiteEol = true
		case "icase":
			o.ICase = true
		case "iblank":
			o.IBlank = true
		}
	}
	return o
}

// blank is vim's VIM_ISWHITE: a space or a tab, and nothing else. A form feed
// is not white space to vim's differ and neither is a carriage return, which
// matters for a file with CRLF line endings read as bytes.
func blank(c byte) bool { return c == ' ' || c == '\t' }

// normalize is the key a line is compared by: the line itself, unless a
// 'diffopt' flag says two different lines are the same.
//
// Measured against vim, in this order of precedence, with iwhiteall winning
// over iwhite the way xdiff's flags do:
//
//	"x y" and "x y" equal under iwhite, different without it
//	"x " and "x" equal under iwhite and under iwhiteeol
//	" x" and "x" DIFFERENT under iwhite, equal under iwhiteall
//
// The third is the one that surprises: iwhite ignores a change in the AMOUNT
// of white space, and a run of two spaces against a run of none is not a
// change in an amount, it is a run that is not there.
func (o Options) normalize(line []byte) string {
	s := line
	switch {
	case o.IWhiteAll:
		b := make([]byte, 0, len(s))
		for _, c := range s {
			if !blank(c) {
				b = append(b, c)
			}
		}
		s = b
	case o.IWhite:
		b := make([]byte, 0, len(s))
		for i := 0; i < len(s); i++ {
			if !blank(s[i]) {
				b = append(b, s[i])
				continue
			}
			b = append(b, ' ')
			for i+1 < len(s) && blank(s[i+1]) {
				i++
			}
		}
		s = trimBlank(b)
	case o.IWhiteEol:
		s = trimBlank(s)
	}
	if o.ICase {
		b := make([]byte, len(s))
		for i, c := range s {
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			b[i] = c
		}
		s = b
	}
	return string(s)
}

// trimBlank drops white space at the end of a line.
func trimBlank(s []byte) []byte {
	for len(s) > 0 && blank(s[len(s)-1]) {
		s = s[:len(s)-1]
	}
	return s
}
