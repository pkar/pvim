package operator

import (
	"bytes"

	"github.com/pkar/pvim/internal/text"
)

// DefaultTextWidth is what gq reflows to when 'textwidth' is zero.
//
// vim's comp_textwidth() answers the window's width minus the number, fold and
// sign columns, capped at 79. This package has no window -- internal/operator
// is under internal/screen and has to run in a test with no display -- so a
// caller with a narrower window than 79 columns has to resolve 'textwidth'
// itself and pass the answer in Options.TextWidth.
//
// Measured both ways: with the terminal at 120 columns and tw=0, gq wraps a
// 246-character line at 78, and the next word would have taken it to 81, so
// the limit is 79. With the terminal at 60 the same line wraps at 58 and 59.
// The oracle runs vim on a 120-column pty, so 79 is the number every case sees.
const DefaultTextWidth = 79

// applyFormat is gq and gw: reflow the lines to 'textwidth'.
//
// What it does, all of it run through vim first:
//
// - Paragraphs are separated by lines that hold nothing but white space, and
// those lines are left exactly as they are.
// - The first line of a paragraph keeps its own indent, rebuilt: vim writes
// every formatted line's indent back through set_indent, so four spaces of
// indent under the vimrc's ts=2 and 'noexpandtab' come back as two tabs,
// and one line that needed no reflow at all is still rewritten. Measured
// with "gqq" over " abcdef" at ts=2, ts=4 and ts=2 'expandtab', which
// answer two tabs, one tab and four spaces. The lines after the first get
// that same indent when 'autoindent' is on and NO indent when it is off,
// which is why gq with vim's defaults left-aligns everything under an
// indented first line.
// - Words are joined with one space, or two after a word ending in '.', '!'
// or '?' when 'joinspaces' is on, which under --clean it is.
// - A line is broken before the word that would take it past 'textwidth'; a
// line of exactly 'textwidth' cells is allowed.
//
// gq leaves the cursor on the first non-blank of the last line it formatted.
// gw puts it back on the character it was on, which after a reflow is a
// different line and column; see keepCursor.
func applyFormat(r Request) (Result, error) {
	first, last := r.Span.Lines()
	if first < 1 || last > r.Buf.LineCount() || first > last {
		return Result{}, ErrNoRange
	}
	width := r.Opt.TextWidth
	if width <= 0 {
		width = DefaultTextWidth
	}

	before := make([][]byte, 0, last-first+1)
	for n := first; n <= last; n++ {
		before = append(before, r.Buf.Line(n))
	}
	after, report := formatRegion(before, width, r.Opt)

	// vim's did_change is set by the work format_lines did and not by the
	// text coming out different: a paragraph of two lines is joined and split
	// again, which is a change even when the result is the same bytes. One
	// line on its own with nothing to do is not. Measured: "gqG" over two
	// lines that reflow to the same two reports the change, "gqq" over one
	// line that fits reports nothing.
	changed := !sameLines(before, after) || rejoined(before)
	if changed {
		r.Buf.Replace(text.Range{
			Start: text.Pos{Line: first, Col: 0},
			End:   text.Pos{Line: last, Col: len(r.Buf.Line(last))},
		}, bytes.Join(after, []byte("\n")))
	}

	// What vim reports is the LAST change format_lines made, and formatRegion
	// works it out paragraph by paragraph because a range can hold more than
	// one and only the last one that changed anything is on the changelist.
	// Nothing at all when the reflow came out the same as it went in: vim
	// saves undo before it looks, so "gqW" over a paragraph already the right
	// shape numbers an undo header and leaves getchangelist() empty, and
	// reporting it anyway put an entry there and was eighteen of the fuzz
	// differences.
	if changed && report.Line > 0 {
		r.Buf.ChangedAt(text.Pos{Line: first + report.Line - 1, Col: report.Col})
	}

	grew := len(after) - len(before)
	end := first + len(after) - 1
	if r.Span.EndAdjusted && end < r.Buf.LineCount() {
		// vim's op_format: "If the cursor was moved one line back (e.g. with
		// Q}) go to the next line, so . will do the next lines."
		end++
	}
	cursor := text.Pos{Line: end, Col: beginLine(r.Buf.Line(end))}
	if r.Op == OpFormatKeep {
		cursor = keepCursor(r.At, first, before, after)
		cursor = clampNormal(r.Buf, cursor)
	}
	return Result{Cursor: cursor, Message: lineMessage(grew, r.Opt.Report), Keep: grew != 0}, nil
}

// rejoined reports whether the region holds a paragraph of more than one line,
// which is the work vim always counts as a change however the reflow comes
// out.
func rejoined(lines [][]byte) bool {
	run := 0
	for _, line := range lines {
		if whiteOnly(line) {
			run = 0
			continue
		}
		if run++; run > 1 {
			return true
		}
	}
	return false
}

// formatRegion reflows lines and returns the new ones, with the position of the
// last change it made relative to the region: line 1 is the region's first
// line, and a zero line means it changed nothing. It is a pure function of its
// arguments so that a test can state a paragraph and the width and read the
// answer without a buffer at all.
//
// The report is per paragraph, and the last paragraph that changed anything
// wins, because that is the order vim's format_lines makes the changes in and
// the changelist keeps only the last position of an undo block. Measured on
// "package main", a blank line, "import (" and an indented "n++": 2gqas from
// the third line reports line 4, the line after the paragraph it joined, and
// not line 2, which is what counting from the start of the whole range gives.
//
// vim formats a paragraph in three steps -- join it into one line, write the
// indent back through set_indent, break it again -- and the last of the three
// that actually changed a byte is what gets reported. So a paragraph broken
// into more than one line reports the LAST break, at the end of the second to
// last line. One that came out as a single line reports its own first line when
// set_indent rewrote the indent, which under the vimrc's ts=2 'noexpandtab' is
// any indent of two columns or more: "gqq" over four spaces reports line 1, and
// the same keys at ts=8, where the indent comes back as the four spaces it
// already was, report line 2. And with the indent unchanged it reports the join,
// on the line after the paragraph's first, the same place "J" reports.
func formatRegion(lines [][]byte, width int, opt Options) ([][]byte, text.Pos) {
	out := make([][]byte, 0, len(lines))
	var report text.Pos
	for i := 0; i < len(lines); {
		if whiteOnly(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < len(lines) && !whiteOnly(lines[j]) {
			j++
		}
		at := len(out)
		para := formatParagraph(lines[i:j], width, opt)
		out = append(out, para...)
		reindented := !bytes.Equal(lines[i][:indentEnd(lines[i])], para[0][:indentEnd(para[0])])
		switch {
		case len(para) >= 2:
			report = text.Pos{Line: at + len(para) - 1, Col: len(para[len(para)-2])}
		case reindented:
			report = text.Pos{Line: at + 1}
		case j-i >= 2:
			report = text.Pos{Line: at + 2}
		}
		i = j
	}
	return out, report
}

// formatParagraph reflows one paragraph, which is one or more lines with no
// blank among them.
func formatParagraph(lines [][]byte, width int, opt Options) [][]byte {
	ts := opt.tabStop()
	head := makeIndent(text.DisplayWidth(lines[0][:indentEnd(lines[0])], ts), ts, opt.ExpandTab)
	cont := []byte(nil)
	if opt.AutoIndent {
		cont = head
	}

	// vim reflows in two steps and this is the same two: join the paragraph
	// into one line with do_join's spacing, then break the long line at a
	// blank. Rebuilding it out of bytes.Fields instead was the shortcut that
	// made "gq" collapse every run of spaces inside a line -- "cp: turns"
	// came back as "cp: turns" -- and lose do_join's rules, which are what
	// puts NO space in front of a ")". Both measured against vim.
	body, _, _ := joinWithSpaces(lines, true, opt)
	body = body[indentEnd(body):]
	if len(body) == 0 {
		return lines
	}

	var out [][]byte
	cur := append([]byte(nil), head...)
	empty := true
	for i := 0; i < len(body); {
		// One gap and the word after it, both taken as they stand.
		gap := i
		for i < len(body) && blank(body[i]) {
			i++
		}
		word := i
		for i < len(body) && !blank(body[i]) {
			i++
		}
		if word == i {
			// Trailing white space, which the join cannot produce and a
			// hand-built paragraph can. It belongs to the line it is on.
			cur = append(cur, body[gap:]...)
			break
		}
		sep := body[gap:word]
		if empty {
			sep = nil
		}
		if !empty && text.DisplayWidth(cur, ts)+text.DisplayWidth(sep, ts)+
			text.DisplayWidth(body[word:i], ts) > width {
			out = append(out, cur)
			cur = append([]byte(nil), cont...)
			sep = nil
		}
		cur = append(cur, sep...)
		cur = append(cur, body[word:i]...)
		empty = false
	}
	return append(out, cur)
}

// keepCursor is gw's promise that the cursor comes back to the same character.
//
// A reflow only moves white space around, so the sequence of non-blank
// characters through the region is unchanged and counting them is an exact map
// from the old text to the new one. vim gets the same answer a different way,
// by adjusting a saved cursor as if it were a mark, which is approximate at a
// line that was split; this is not, and where the two disagree it is on the
// blank the cursor was sitting on rather than on a character.
func keepCursor(at text.Pos, first int, before, after [][]byte) text.Pos {
	if at.Line < first || at.Line >= first+len(before) {
		return at
	}
	want := 0
	for i := 0; i < at.Line-first; i++ {
		want += countNonBlank(before[i], len(before[i]))
	}
	want += countNonBlank(before[at.Line-first], at.Col)

	seen := 0
	for i, line := range after {
		for col := 0; col <= len(line); col++ {
			if col == len(line) {
				break
			}
			if blank(line[col]) {
				continue
			}
			if seen == want {
				return text.Pos{Line: first + i, Col: col}
			}
			seen++
		}
	}
	// Past the last non-blank: the end of the last line, which is where a
	// cursor sitting on trailing white space ends up.
	last := len(after) - 1
	return text.Pos{Line: first + last, Col: len(after[last])}
}

// countNonBlank counts the characters before col that are not a space or a tab.
func countNonBlank(line []byte, col int) int {
	if col > len(line) {
		col = len(line)
	}
	n := 0
	for i := range col {
		if !blank(line[i]) {
			n++
		}
	}
	return n
}

// sameLines reports whether two line slices hold the same bytes, so that a gq
// that changed nothing does not open an undo step.
func sameLines(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
