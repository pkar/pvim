package motion

import (
	"fmt"
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// The sweep: every motion, from every character position of every corpus file,
// against the vim on this machine. It is one test rather than one per motion
// because the interesting cases are not the ones anybody would think to write
// down -- w on the blank line before a line of tabs, ge out of a line whose
// last character is wide, ) at the end of a file with no newline -- and the
// only way to get them is to try every position and let vim say.

// corpus is the eight files everything in this package is checked against.
// Each one is here for a shape that has broken a motion: a tab in column one,
// a line of nothing but blanks, a wide rune the cursor cannot land inside, an
// empty line in the middle and at the end, a last line with no newline after
// it, and a line longer than the window so that gj and g$ have somewhere to
// wrap.
var corpus = []struct {
	name string
	text string
}{
	{"plain", "hello world\nfoo bar baz\n\nlast_word here\n"},
	{"blanks", "   leading\ntrailing   \n   \n\nx\n"},
	{"tabs", "\tone\ttwo\n\t\tdeep\nno_tab\n \tmixed \t\n"},
	{"punct", "a,b.c;d\n(x)[y]{z}\n!!!???\n--- ---\n"},
	{"wide", "漢字 kanji\nおはよう world\ncafé naïve\n漢a漢b\n"},
	{"code", "package main\n\nfunc one() {\n\tif x {\n\t\treturn\n\t}\n}\n\nfunc two() {\n}\n"},
	{"prose", "One sentence. Two! Three?  Four.\n\nA para \"with quotes.\" And more.\nEnd\n"},
	{"noeol", "first line\nsecond\nno newline at all"},
	// The nroff half of the paragraph and section rules, which nobody has
	// typed on purpose since 1985 and which { } [[ and ]] still consult: a
	// macro from 'paragraphs' or 'sections' written as .XX in column one, and
	// a form feed.
	{"nroff", ".SH Section\ntext here\n.IP item\nmore text\n\fafter the feed\nend\n"},
}

// wrapped is the corpus for the display-line motions, which need a line longer
// than the window before g0, g$, gj and gk mean anything at all. It is kept
// out of the sweep above because every position of a 200-column line times
// every motion is three minutes of vim.
const wrapped = "word word word word word word word word word word word word word word word word word word word word\n" +
	"short\n" +
	"\ttabbed line that also runs past the edge of an eighty column window somewhere around here\n" +
	"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n" +
	"last\n"

// positions returns every place the cursor can be in a buffer: the first byte
// of every character of every line, plus column zero of an empty one.
func positions(b *text.Buffer) []text.Pos {
	var out []text.Pos
	for lnum := 1; lnum <= b.LineCount(); lnum++ {
		line := b.Line(lnum)
		if len(line) == 0 {
			out = append(out, text.Pos{Line: lnum, Col: 0})
			continue
		}
		for col := 0; col < len(line); col += charLen(line, col) {
			out = append(out, text.Pos{Line: lnum, Col: col})
		}
	}
	return out
}

// sweepKeys is what gets typed from every position. Two-key sequences are here
// as well as single motions because a count and a repeat are where the state
// between motions shows up.
var sweepKeys = []string{
	"h", "l", "3h", "3l", "0", "^", "$", "2$", "g_", "5|", "1|", "20|",
	"w", "W", "b", "B", "e", "E", "ge", "gE", "2w", "3w", "2b", "2e", "5w", "5b",
	"j", "k", "3j", "3k", "+", "-", "_", "3_", "G", "gg", "3G", "2gg",
	"{", "}", "2}", "2{", "(", ")", "2)", "2(",
	"[[", "]]", "[]", "][", "%", "50%", "100%", "1%",
	"fo", "Fo", "to", "To", "fx", "tx", "f ", "t ", "F ", "T ",
	"fo;", "fo,", "to;", "to,", "to;;", "2fo", "2to", "to2;",
	"$j", "$k", "$jj", "$3j", "3|j", "3|jj",
	"go", "5go", "30go",
	"[(", "[{", "])", "]}",
}

// TestSweepAgainstVim is the gate for this package.
func TestSweepAgainstVim(t *testing.T) {
	haveVim(t)
	for _, c := range corpus {
		t.Run(c.name, func(t *testing.T) {
			b := text.Read([]byte(c.text))
			var probes []vimProbe
			var starts []text.Pos
			var keys []string
			for _, from := range positions(b) {
				for _, k := range sweepKeys {
					probes = append(probes, vimProbe{From: from, Keys: k})
					starts = append(starts, from)
					keys = append(keys, k)
				}
			}
			answers := runVim(t, c.text, nil, probes, false)

			bad := 0
			for i, want := range answers {
				opt := DefaultOptions()
				opt.Width = want.Width
				ctx := &Context{Curswant: dispCol(b, starts[i], opt.TabStop)}
				got := runKeys(t, b, ctx, starts[i], keys[i], opt, false, 0)
				// A motion that fails beeps and leaves the cursor where the
				// motions before it in the sequence put it, which is what
				// runKeys returns, so there is nothing to undo here.
				gotPos, gotWant := got.Pos, ctx.Curswant
				if gotPos != want.Pos || gotWant != want.Curswant {
					bad++
					if bad <= 12 {
						t.Errorf("%s %d:%d %q: pvim %d:%d curswant=%s, vim %d:%d curswant=%s",
							c.name, starts[i].Line, starts[i].Col, keys[i],
							gotPos.Line, gotPos.Col, curswantString(gotWant),
							want.Pos.Line, want.Pos.Col, curswantString(want.Curswant))
					}
				}
			}
			if bad > 0 {
				t.Errorf("%s: %d of %d probes disagree with vim", c.name, bad, len(answers))
			}
		})
	}
}

// TestWrappedAgainstVim is the display-line family, which is the only part of
// this package that has to know how wide the window is. Every third position
// of a file whose lines wrap two and three times over.
func TestWrappedAgainstVim(t *testing.T) {
	haveVim(t)
	keys := []string{
		"gj", "gk", "2gj", "2gk", "3gj", "3gk", "g0", "g^", "g$", "2g$",
		"g$gj", "g$gk", "gjgj", "gkgk", "j", "k", "$gj", "$gk", "0", "$",
	}
	b := text.Read([]byte(wrapped))
	var probes []vimProbe
	var starts []text.Pos
	var typed []string
	for i, from := range positions(b) {
		if i%3 != 0 {
			continue
		}
		for _, k := range keys {
			probes = append(probes, vimProbe{From: from, Keys: k})
			starts = append(starts, from)
			typed = append(typed, k)
		}
	}
	answers := runVim(t, wrapped, nil, probes, false)

	bad := 0
	for i, want := range answers {
		opt := DefaultOptions()
		opt.Width = want.Width
		ctx := &Context{Curswant: dispCol(b, starts[i], opt.TabStop)}
		got := runKeys(t, b, ctx, starts[i], typed[i], opt, false, 0)
		if got.Pos != want.Pos || ctx.Curswant != want.Curswant {
			bad++
			if bad <= 12 {
				t.Errorf("wrapped %d:%d %q: pvim %d:%d curswant=%s, vim %d:%d curswant=%s",
					starts[i].Line, starts[i].Col, typed[i],
					got.Pos.Line, got.Pos.Col, curswantString(ctx.Curswant),
					want.Pos.Line, want.Pos.Col, curswantString(want.Curswant))
			}
		}
	}
	if bad > 0 {
		t.Errorf("wrapped: %d of %d probes disagree with vim", bad, len(answers))
	}
}

// curswantString prints a wanted column, naming the two that are not columns.
func curswantString(v int) string {
	switch v {
	case CurswantEOL:
		return "eol"
	case CurswantKeep:
		return "keep"
	}
	return fmt.Sprintf("%d", v)
}
