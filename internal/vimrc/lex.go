package vimrc

import "strings"

// logicalLine is one vimscript statement's worth of text: a physical line with
// every "\" continuation line that follows it joined on.
type logicalLine struct {
	text string
	pos  Pos
}

// logicalLines splits a script into statements.
//
// Vim's continuation rule is that a line whose first non-blank character is a
// backslash continues the previous line, with everything up to and including
// the backslash thrown away and NO space put in its place. That last detail is
// what makes the vimrc's dict literal
//
//	let g:ctrlp_custom_ignore = {
//	 \ 'dir': '...',
//	 \ 'file': '...'
//	 \ }
//
// join into one line with the spaces that are there and no others.
//
// A comment line -- first non-blank is a double quote -- and a blank line are
// dropped, and both end any continuation in progress, which is vim's behaviour
// in a legacy script: only a backslash continues.
func logicalLines(src []byte, file string) []logicalLine {
	var out []logicalLine
	cur := -1 // index into out of the line still open for continuation

	for n, raw := range strings.Split(string(src), "\n") {
		text := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimLeft(text, " \t")

		switch {
		case strings.HasPrefix(trimmed, "\\") && cur >= 0:
			out[cur].text += trimmed[1:]
			continue
		case trimmed == "", strings.HasPrefix(trimmed, "\""):
			// A comment or a blank line closes the previous statement and
			// contributes nothing of its own.
			cur = -1
			continue
		}

		out = append(out, logicalLine{text: text, pos: Pos{File: file, Line: n + 1}})
		cur = len(out) - 1
	}
	return out
}

// lineCount is how many physical lines a script has.
//
// A trailing newline ends the last line rather than starting an empty one,
// which is what makes vim's "E171: Missing :endif" land on line 3 of a
// two-line file whether or not that file ends in a newline. Measured
// on `if 1` / `set tw=3` written both ways.
func lineCount(src []byte) int {
	n := strings.Count(string(src), "\n")
	if len(src) > 0 && src[len(src)-1] != '\n' {
		n++
	}
	return n
}

// separateNextCmd cuts s where vim's separate_nextcmd() does, and reports
// whether what follows is another command.
//
// It is vim's function and not an approximation of it, because the two things
// it does are both things a vimrc trips over. It has NO idea what a string is:
// it walks bytes looking for a "|", a newline, or -- only when comments is
// true -- a double quote, and the one escape it honours is a single backslash
// immediately in front of whichever of those it found, which it deletes and
// then walks past. So
//
//	set ef=%f\|%l | set ts=3
//
// sets 'errorformat' to "%f|%l" and 'tabstop' to 3, both measured.
//
// comments is vim's EX_NOTRLCOM flag inverted. It is false for the map family,
// which is why `nnoremap <F7> za " Spacebar to unfold` maps the comment and
// all, and true for :set, :augroup, :highlight and the rest, which is why
//
//	set ts=8 " a " | set sw=8
//
// leaves 'shiftwidth' alone: the first double quote ends the command and the
// bar after it is comment text. A quote never introduces a next command, so
// found is false there.
//
// The caller decides whether to ask at all. :autocmd, :command, :normal,
// :function and the :unmap family have no EX_TRLBAR and take the rest of the
// line bars and quotes and all, which is why the vimrc's
//
//	au BufWritePost .vimrc,... so $MYVIMRC | if has('gui_running') && ... | endif
//
// registers ONE autocmd whose command contains three bars, and why those bars
// are split later, when the autocmd fires and its command is run as a command
// line. Measured with :execute('autocmd BufWritePost').
func separateNextCmd(s string, comments bool) (head, rest string, found bool) {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x16 {
			// CTRL-V protects the byte after it, which is vim's EX_USECTRLV
			// and the only way to put a bare "|" in a mapping without <Bar>.
			b = append(b, c)
			if i+1 < len(s) {
				i++
				b = append(b, s[i])
			}
			continue
		}
		if c != '|' && c != '\n' && !(comments && c == '"') {
			b = append(b, c)
			continue
		}
		if len(b) > 0 && b[len(b)-1] == '\\' {
			// One backslash comes off and the character stays. Vim does not
			// re-examine what is now in front of it, so `\\|` ends up as
			// `\|` and still does not split. Measured.
			b[len(b)-1] = c
			continue
		}
		if c == '"' {
			return string(b), "", false
		}
		return string(b), s[i+1:], true
	}
	return string(b), "", false
}

// splitBar cuts s at the first "|" that separates two commands, and reports
// whether it found one.
//
// It is the stand-in for an expression parser, for the two places that need
// one and do not have one: the arguments of :call, which this package reads a
// function name out of and otherwise drops, and any command line inside an
// :if branch that is not running, where nothing is evaluated and the :endif
// behind two bars still has to be found. Vim decides both by parsing the
// expression and stopping; knowing where a quoted string ends is the cheapest
// thing that gets the same answer on every line in scope.
//
// It is NOT what vim does to a command it is about to run. That is
// separateNextCmd above.
func splitBar(s string) (head, rest string, found bool) {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			// A single-quoted vim string escapes a quote by doubling it and
			// has no backslash escapes at all, which is why the ctrlp ignore
			// patterns can hold backslashes untouched.
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				quote = 0
			}
		case quote == '"':
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
				quote = 0
			}
		case c == '\\':
			i++
		case c == '\'' || c == '"':
			quote = c
		case c == '|':
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// stripComment removes a trailing double-quote comment from a command's
// arguments.
//
// Vim's rule for the commands that allow one is that a double quote starts a
// comment when it is not preceded by a backslash, which is how
//
//	set noerrorbells visualbell t_vb= " Disable ALL bells"
//
// on line 76 sets three options and not four. It is deliberately not used for
// :let and :if, whose arguments are expressions and whose comments are found
// by the expression parser stopping: `let x = "/tmp/log"` has a double quote
// in exactly the place this function would cut.
func stripComment(s string) string {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
				return strings.TrimRight(s[:i], " \t")
			}
		}
	}
	return strings.TrimRight(s, " \t")
}

// splitWord takes the first white-space-delimited word off s and returns it
// with the remainder, leading white space removed.
func splitWord(s string) (word, rest string) {
	s = strings.TrimLeft(s, " \t")
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimLeft(s[i:], " \t")
}

// splitLHS takes a mapping's left-hand side off s.
//
// It ends at the first white space, and there is no escaping to worry about:
// a mapping whose left-hand side contains a space is written <Space>, because
// :map has no way to say a literal one.
func splitLHS(s string) (lhs, rhs string) { return splitWord(s) }
