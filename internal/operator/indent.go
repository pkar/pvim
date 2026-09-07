package operator

import (
	"strings"

	"github.com/pkar/pvim/internal/text"
)

// applyIndent is =, and it is the one operator in this package that does not
// match vim, on purpose and with the difference written down.
//
// What vim does: with 'equalprg' empty, no 'indentexpr' and 'lisp' off, = runs
// get_c_indent(), vim's C indenter, whatever the file is. Measured on
// "alpha / (4 spaces)beta / (8 spaces)gamma / delta": = G strips every indent
// to zero, and on "foo { / bar / baz; / } / qux" it produces the C layout
// "foo { / (4)bar / (8)baz; / } / qux" with sw=4. That is fifteen hundred lines
// of C with 'cinoptions' behind it and no filetype in this editor to want it.
//
// What this does instead, which is what: 'autoindent'-style.
// Each line takes the indent of the last non-blank line above it, plus what
// 'smartindent' adds -- a shiftwidth after a line ending in '{' or starting
// with one of 'cinwords', a shiftwidth back for a line starting with '}'. That
// is the whole indent engine the vimrc asks for, since the file sets
// smartindent and cinwords for python and nothing else.
//
// The cursor and the two messages ARE vim's, because those are cheap and the
// oracle diffs them: = G over five lines prints "4 lines to indent... " and
// then "5 lines indented ", both with the trailing space vim puts there.
func applyIndent(r Request) (Result, error) {
	first, last := r.Span.Lines()
	if first < 1 || last > r.Buf.LineCount() || first > last {
		return Result{}, ErrNoRange
	}
	ts := r.Opt.tabStop()
	sw := r.Opt.shiftWidth()

	// vim's op_reindent walks the lines, remembers the first and last it
	// actually moved, and then reports ONE changed_lines() at the first of
	// them. Reporting each line as it goes leaves the changelist on the last
	// line instead: measured, "=j" over two indented lines puts vim's entry
	// on line 1.
	firstChanged := 0
	prev := previousNonBlank(r.Buf, first)
	for n := first; n <= last; n++ {
		line := r.Buf.Line(n)
		if whiteOnly(line) {
			continue
		}
		want := 0
		if prev != nil {
			want = indentWidth(prev, ts)
			if r.Opt.SmartIndent {
				body := prev[indentEnd(prev):]
				if len(body) > 0 && body[len(body)-1] == '{' {
					want += sw
				} else if hasCinWord(body, r.Opt.CinWords) {
					want += sw
				}
			}
		}
		if r.Opt.SmartIndent {
			body := line[indentEnd(line):]
			if len(body) > 0 && body[0] == '}' {
				want -= sw
			}
		}
		if want < 0 {
			want = 0
		}
		if want != indentWidth(line, ts) {
			r.Buf.SetLine(n, setIndent(line, want, ts, r.Opt.ExpandTab))
			if firstChanged == 0 {
				firstChanged = n
			}
		}
		prev = r.Buf.Line(n)
	}
	if firstChanged != 0 {
		r.Buf.ChangedAt(text.Pos{Line: firstChanged})
	}

	n := last - first + 1
	var msg strings.Builder
	if n > r.Opt.Report {
		if n-1 > 1 {
			msg.WriteString(plural(n-1, "line to indent... ", "lines to indent... "))
			msg.WriteString("\n")
		}
		msg.WriteString(plural(n, "line indented ", "lines indented "))
	}
	return Result{
		Cursor:  text.Pos{Line: first, Col: beginLine(r.Buf.Line(first))},
		Message: msg.String(),
	}, nil
}

// previousNonBlank returns the last line above n that holds something other
// than white space, or nil when there is none.
func previousNonBlank(b *text.Buffer, n int) []byte {
	for i := n - 1; i >= 1; i-- {
		if line := b.Line(i); !whiteOnly(line) {
			return line
		}
	}
	return nil
}

// hasCinWord reports whether the line starts with one of 'cinwords' followed by
// something that is not a keyword character, which is what makes "iffy" not an
// "if".
func hasCinWord(body []byte, words []string) bool {
	for _, w := range words {
		if w == "" || len(body) < len(w) {
			continue
		}
		if string(body[:len(w)]) != w {
			continue
		}
		if len(body) == len(w) {
			return true
		}
		if c := body[len(w)]; !isWordByte(c) {
			return true
		}
	}
	return false
}

// isWordByte is the default 'iskeyword' for the purpose above: letters, digits
// and underscore. The real option lives in internal/motion and = does not need
// its subtleties to tell "if" from "iffy".
func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}
