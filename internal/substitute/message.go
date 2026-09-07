package substitute

import (
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
)

// countMessage renders the line vim prints after a ":s".
//
// Two shapes, both with singular forms, both measured:
//
//	4 substitutions on 4 lines
//	1 substitution on 1 line
//	4 matches on 4 lines the "n" flag
//	1 match on 1 line
//
// 'report' gates the first shape and not the second: ":set report=99" still
// prints the count from an "n" flag, and ":set report=4" swallows four
// substitutions. The comparison is against the number of SUBSTITUTIONS and not
// the number of lines, which is why ":set report=2" prints "6 substitutions on
// 2 lines" -- six beats two even though two does not.
func countMessage(subs, lines int, countOnly bool, report int) string {
	if subs == 0 {
		return ""
	}
	noun := "substitution"
	if countOnly {
		noun = "match"
	} else if subs <= report {
		return ""
	}
	if subs != 1 {
		noun += "s"
		if countOnly {
			noun = "matches"
		}
	}
	line := "line"
	if lines != 1 {
		line = "lines"
	}
	return strconv.Itoa(subs) + " " + noun + " on " + strconv.Itoa(lines) + " " + line
}

// printLine renders the line the "p", "l" and "#" flags print after the
// message: the last line that changed, and only that one.
//
// Three forms, and they combine, which is why this is one function and not
// three. Measured on a line reading "\tX2 " with 'tabstop' at 8:
//
//	p " X2 " the tab expanded to the next tabstop
//	l "^IX2 $" 'list' form, with the end of the line marked
//	# " 2 ..." the line number, right aligned, then a space
//	#l " 2 ^IX2 $" both
func printLine(b *text.Buffer, lnum int, f Flags, o *options.Options) string {
	var sb strings.Builder
	if f.Number {
		n := strconv.Itoa(lnum)
		for i := len(n); i < numberWidth(b, o); i++ {
			sb.WriteByte(' ')
		}
		sb.WriteString(n)
		sb.WriteByte(' ')
	}
	line := b.Line(lnum)
	if f.List {
		sb.WriteString(listForm(line))
		return sb.String()
	}
	sb.WriteString(printForm(line, tabStop(o)))
	return sb.String()
}

// listForm is 'list' rendering: a tab is "^I", any other control character is
// "^" and the letter it is, and the end of the line is "$".
func listForm(line []byte) string {
	var sb strings.Builder
	for _, c := range line {
		switch {
		case c == '\t':
			sb.WriteString("^I")
		case c < 0x20:
			sb.WriteByte('^')
			sb.WriteByte(c + '@')
		case c == 0x7f:
			sb.WriteString("^?")
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('$')
	return sb.String()
}

// printForm is what ":p" shows: a tab reaches the next multiple of 'tabstop'
// as spaces and every other control character is "^" and its letter.
//
// An empty line prints as one space, which is vim putting something under the
// cursor rather than leaving it on the NUL, and which shows up as the
// difference between " 1 " and " 1 " the first time a "#" flag empties a
// line. Measured.
func printForm(line []byte, ts int) string {
	if len(line) == 0 {
		return " "
	}
	var sb strings.Builder
	col := 0
	for _, c := range line {
		switch {
		case c == '\t':
			n := ts - col%ts
			sb.WriteString(strings.Repeat(" ", n))
			col += n
		case c < 0x20:
			sb.WriteByte('^')
			sb.WriteByte(c + '@')
			col += 2
		case c == 0x7f:
			sb.WriteString("^?")
			col += 2
		default:
			sb.WriteByte(c)
			col++
		}
	}
	return sb.String()
}

// numberWidth is vim's number_width: as many columns as the last line number
// needs, and never fewer than 'numberwidth' minus the space that follows it.
// With the default of 4 that is 3, which is why a six-line file numbers its
// lines " 3" and a 149-line file numbers one of them "120".
func numberWidth(b *text.Buffer, o *options.Options) int {
	w := 3
	if o != nil && o.W.NumberWidth > 0 {
		w = o.W.NumberWidth - 1
	}
	if n := len(strconv.Itoa(b.LineCount())); n > w {
		w = n
	}
	return w
}

// tabStop is 'tabstop', never zero.
func tabStop(o *options.Options) int {
	if o == nil || o.B.TabStop < 1 {
		return 8
	}
	return o.B.TabStop
}
