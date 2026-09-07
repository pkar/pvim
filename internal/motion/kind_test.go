package motion

import (
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// A motion's kind cannot be checked by looking at the cursor: dw and de leave
// it in the same place and take a different number of characters. So this file
// checks the kinds the only way there is, by running an operator and comparing
// what is left of the buffer against vim's.
//
// The operator is d, and the four lines of it are here in the test rather than
// in the package because internal/operator owns the real one. What the test
// exercises is this package's answer -- the kind the motion reported, and the
// two adjustments in :help exclusive -- with the smallest possible delete
// wrapped round it.

// applyDelete deletes the span a motion produced and returns the lines left.
func applyDelete(b *text.Buffer, start, end text.Pos, kind Kind, noAdjust bool) []string {
	if end.Compare(start) < 0 {
		start, end = end, start
	}
	if !noAdjust {
		start, end, kind = AdjustExclusive(b, start, end, kind)
	}
	kind = blankLineRule(b, start, end, kind)

	switch kind {
	case KindLine:
		b.DeleteLines(start.Line, end.Line)
	default:
		stop := end
		if kind == KindCharInclusive {
			line := b.Line(end.Line)
			stop.Col = end.Col + charLen(line, end.Col)
			if end.Col >= len(line) {
				stop.Col = end.Col
			}
		}
		if start.Compare(stop) < 0 {
			b.Delete(text.Range{Start: start, End: stop})
		}
	}
	var out []string
	for i := 1; i <= b.LineCount(); i++ {
		out = append(out, string(b.Line(i)))
	}
	return out
}

// blankLineRule is op_delete's own adjustment, quoted from vim's source as
// "imitate the strange Vi behaviour": a charwise delete that spans more than
// one line, ends at or after the last non-blank of its last line and starts at
// or before the first non-blank of its first, is done linewise instead.
//
// It belongs to internal/operator and not to this package -- it is a rule
// about d, not about any motion, and c and y do not have it -- and it is here
// because without it this test blames the motions for a blank line that the
// operator is supposed to take away. When internal/operator's op_delete grows
// this rule, this copy is what it has to agree with.
func blankLineRule(b *text.Buffer, start, end text.Pos, kind Kind) Kind {
	if kind == KindLine || kind == KindBlock || end.Line <= start.Line {
		return kind
	}
	line := b.Line(end.Line)
	i := end.Col
	if i < len(line) && kind == KindCharInclusive {
		i++
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i < len(line) || !inIndent(b.Line(start.Line), start.Col) {
		return kind
	}
	return KindLine
}

// opKeys is the motions run under d. Everything whose kind the table states,
// plus the ones whose kind is decided at run time.
var opKeys = []string{
	"w", "W", "2w", "3w", "e", "E", "2e", "b", "B", "2b", "ge", "gE",
	"$", "0", "^", "g_", "h", "l", "3l", "3h", "|", "5|",
	"}", "{", "2}", ")", "(", "j", "k", "2j", "G", "gg", "2G",
	"%", "fo", "to", "Fo", "To", "]]", "[[", "][", "[]",
	// The two that cross a line boundary out of the box, because 'whichwrap'
	// is "b,s": these are where d joins two lines and where the exclusive
	// adjustment has to be turned off.
	"<BS>", "<Space>", "2<BS>", "2<Space>", "3<Space>", "<CR>",
}

// TestDeleteAgainstVim runs d{motion} from every position of most of the
// corpus and compares the buffer with vim's.
//
// This is the test that would catch an inclusive motion marked exclusive, a
// missing exclusive adjustment, or a w that joins two lines when it should
// stop at the end of one.
func TestDeleteAgainstVim(t *testing.T) {
	haveVim(t)
	for _, c := range corpus {
		t.Run(c.name, func(t *testing.T) {
			base := text.Read([]byte(c.text))
			var probes []vimProbe
			var starts []text.Pos
			var keys []string
			for _, from := range positions(base) {
				for _, k := range opKeys {
					probes = append(probes, vimProbe{From: from, Keys: "d" + vimKeys(k), Undo: true})
					starts = append(starts, from)
					keys = append(keys, k)
				}
			}
			answers := runVim(t, c.text, nil, probes, true)

			bad := 0
			for i, want := range answers {
				opt := DefaultOptions()
				opt.Width = want.Width
				b := text.Read([]byte(c.text))
				ctx := &Context{Curswant: dispCol(b, starts[i], opt.TabStop)}
				got := runKeys(t, b, ctx, starts[i], keys[i], opt, true, 'd')
				var lines []string
				if !got.Ok {
					for i := 1; i <= b.LineCount(); i++ {
						lines = append(lines, string(b.Line(i)))
					}
				} else {
					lines = applyDelete(b, starts[i], got.Pos, got.Kind, got.NoAdjust)
				}
				if strings.Join(lines, "\n") != strings.Join(want.Lines, "\n") {
					bad++
					if bad <= 10 {
						t.Errorf("%s %d:%d d%s: pvim %q, vim %q",
							c.name, starts[i].Line, starts[i].Col, keys[i],
							strings.Join(lines, "↵"), strings.Join(want.Lines, "↵"))
					}
				}
			}
			if bad > 0 {
				t.Errorf("%s: %d of %d deletes disagree with vim", c.name, bad, len(answers))
			}
		})
	}
}
