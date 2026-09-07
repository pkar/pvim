package motion

import (
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// Every option in motion.Options changes an answer, and an option that parses
// and does nothing is the failure mode a table of settings invites. So each
// one gets a case here: the same keys run through vim with the :set line and
// through this package with the field, over every position of a buffer chosen
// to make the option matter.

func TestOptionsAgainstVim(t *testing.T) {
	haveVim(t)
	cases := []struct {
		name    string
		set     string
		opt     func(*Options)
		content string
		keys    []string
	}{
		{
			// The option that decides where a word ends. With the default, a
			// hyphen is punctuation and w stops on it; with it in
			// 'iskeyword', foo-bar is one word.
			name:    "iskeyword",
			set:     "set iskeyword+=-",
			opt:     func(o *Options) { o.IsKeyword = "@,48-57,_,192-255,-" },
			content: "foo-bar baz-qux\nx-y z\n",
			keys:    []string{"w", "W", "b", "e", "ge", "2w", "2b", "3w"},
		},
		{
			// Display columns. | counts them, and j and k aim at them.
			name:    "tabstop",
			set:     "set tabstop=4",
			opt:     func(o *Options) { o.TabStop = 4 },
			content: "\tone\n\t\ttwo\nplain line\n\tlast\n",
			keys:    []string{"5|", "9|", "j", "k", "$j", "3|j", "g0", "g$", "0"},
		},
		{
			// h, l, <BS> and <Space> crossing a line boundary, which is the
			// whole of what 'whichwrap' does and is off for h and l by
			// default.
			name:    "whichwrap",
			set:     "set whichwrap=b,s,h,l,<,>",
			opt:     func(o *Options) { o.WhichWrap = "b,s,h,l,<,>" },
			content: "ab\ncd\n\nef\n",
			keys:    []string{"h", "l", "3h", "3l", "<BS>", "<Space>", "2<BS>", "2<Space>"},
		},
		{
			// With 'nostartofline' the jumps keep the column instead of going
			// to the first non-blank.
			name:    "nostartofline",
			set:     "set nostartofline",
			opt:     func(o *Options) { o.StartOfLine = false },
			content: "first line here\n    indented line\n\ttabbed line\nlast\n",
			keys:    []string{"G", "gg", "2G", "3G", "50%", "100%", "$G", "$gg", "3|G"},
		},
		{
			// % knows only about 'matchpairs'.
			name:    "matchpairs",
			set:     "set matchpairs=(:),{:},[:],<:>",
			opt:     func(o *Options) { o.MatchPairs = "(:),{:},[:],<:>" },
			content: "a<b>c\nif (x) {y}\n<<nested <inner> here>>\n",
			keys:    []string{"%", "2%"},
		},
		{
			// 'nowrap' turns the whole display-line family back into the
			// buffer-line one.
			name:    "nowrap",
			set:     "set nowrap",
			opt:     func(o *Options) { o.Wrap = false },
			content: wrapped,
			keys:    []string{"gj", "gk", "g0", "g$", "g^", "2gj"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := text.Read([]byte(c.content))
			var probes []vimProbe
			var starts []text.Pos
			var typed []string
			for i, from := range positions(b) {
				if c.name == "nowrap" && i%5 != 0 {
					continue // the wrapped corpus is 400 positions of x
				}
				for _, k := range c.keys {
					probes = append(probes, vimProbe{From: from, Keys: vimKeys(k)})
					starts = append(starts, from)
					typed = append(typed, k)
				}
			}
			answers := runVim(t, c.content, []string{c.set}, probes, false)

			bad := 0
			for i, want := range answers {
				opt := DefaultOptions()
				c.opt(&opt)
				opt.Width = want.Width
				ctx := &Context{Curswant: dispCol(b, starts[i], opt.TabStop), Window: want.Win}
				got := runKeys(t, b, ctx, starts[i], typed[i], opt, false, 0)
				if got.Pos != want.Pos || ctx.Curswant != want.Curswant {
					bad++
					if bad <= 8 {
						t.Errorf("%s %d:%d %q: pvim %d:%d curswant=%s, vim %d:%d curswant=%s",
							c.name, starts[i].Line, starts[i].Col, typed[i],
							got.Pos.Line, got.Pos.Col, curswantString(ctx.Curswant),
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
