package motion

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// H, M and L are the only motions in this package that need to know what is on
// the screen, and they are the only ones this package refuses to guess at: the
// Window comes in on the Context, filled in by whichever frontend is drawing.
//
// Testing them against vim needs the same trick. Each case is two probes: one
// that runs the setup keys and reports where the cursor is and which lines the
// window is showing, and one that runs the setup keys and then the motion.
// This package is handed the first probe's window and asked for the second
// probe's answer, which is exactly the arrangement the editor will be in.

// tall is a file with more lines than any window, numbered so that a failure
// message says which line it landed on.
func tall() string {
	var b strings.Builder
	for i := 1; i <= 120; i++ {
		if i%7 == 0 {
			// An indented line every so often, because H, M and L all end
			// with beginline(BL_WHITE) and the column is part of the answer.
			fmt.Fprintf(&b, "\t  line %d\n", i)
			continue
		}
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

// TestWindowMotionsAgainstVim runs H, M and L from several scroll positions,
// with 'scrolloff' at its default and at the vimrc's 3.
func TestWindowMotionsAgainstVim(t *testing.T) {
	haveVim(t)
	content := tall()
	b := text.Read([]byte(content))

	setups := []string{"1G", "G", "60G", "60Gzt", "60Gzz", "60Gzb", "115Gzt", "3Gzb"}
	motions := []string{"H", "M", "L", "2H", "2L", "3H", "3L", "20H", "20L", "2M"}

	for _, so := range []int{0, 3} {
		t.Run(fmt.Sprintf("scrolloff=%d", so), func(t *testing.T) {
			var probes []vimProbe
			for _, s := range setups {
				probes = append(probes, vimProbe{From: text.Pos{Line: 1}, Keys: s})
				for _, m := range motions {
					probes = append(probes, vimProbe{From: text.Pos{Line: 1}, Keys: s + m})
				}
			}
			answers := runVim(t, content, []string{fmt.Sprintf("set scrolloff=%d", so)}, probes, false)

			opt := DefaultOptions()
			opt.ScrollOff = so
			i := 0
			for _, s := range setups {
				start := answers[i]
				i++
				for _, mk := range motions {
					want := answers[i]
					i++
					ctx := &Context{
						Curswant: dispCol(b, start.Pos, opt.TabStop),
						Window:   start.Win,
					}
					opt.Width = start.Width
					got := runKeys(t, b, ctx, start.Pos, mk, opt, false, 0)
					if got.Pos != want.Pos {
						t.Errorf("%s then %s (window %d-%d, cursor %d): pvim %d:%d, vim %d:%d",
							s, mk, start.Win.Top, start.Win.Bottom, start.Pos.Line,
							got.Pos.Line, got.Pos.Col, want.Pos.Line, want.Pos.Col)
					}
				}
			}
		})
	}
}

// TestWindowMotionsNeedAWindow: with no window at all, which is every headless
// run, H M and L fail rather than inventing a line. The alternative is a
// motion that quietly means "line 1" in the oracle and something else in the
// editor.
func TestWindowMotionsNeedAWindow(t *testing.T) {
	b := text.Read([]byte("one\ntwo\nthree\n"))
	for _, keys := range []string{"H", "M", "L"} {
		m, _ := ByKeys(keys)
		res := m.Do(Request{Buf: b, Ctx: &Context{}, From: text.Pos{Line: 2}, Opt: DefaultOptions()})
		if res.Ok {
			t.Errorf("%s with no window gave %v; it has to fail", keys, res.To)
		}
	}
}
