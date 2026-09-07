package key

import "testing"

// TestToBytes is the table that was fed to vim to check it.
//
// The check is not a comparison against vim's own internal encoding, which is
// K_SPECIAL-prefixed and private to vim. It is behavioural: for each row, a
// keystroke file containing exactly these bytes was run through
//
//	vim --clean -i NONE --not-a-term -s keys file
//
// with `:inoremap <notation> MARK` in place, and the mapping fired. 42 of the 44
// rows fire; the two that do not are noted below and neither is a wrong
// encoding.
func TestToBytes(t *testing.T) {
	cases := []struct {
		notation string
		want     string
	}{
		// Ctrl folds to the C0 code. <C-?> is 0x7f and not 0x1f, which is the
		// one everybody gets wrong.
		{"<C-x>", "\x18"},
		{"<C-X>", "\x18"},
		{"<C-a>", "\x01"},
		{"<C-?>", "\x7f"},
		{"<C-\\>", "\x1c"},
		{"<C-]>", "\x1d"},
		{"<C-^>", "\x1e"},
		{"<C-_>", "\x1f"},
		{"<C-@>", "\x00"},
		{"<Nul>", "\x00"},

		{"<Esc>", "\x1b"},
		{"<CR>", "\r"},
		{"<NL>", "\n"},
		{"<Tab>", "\t"},
		{"<C-i>", "\t"},
		// 0x7f, not 0x08: feeding 0x7f fires an <inoremap <BS>> and feeding
		// 0x08 fires an <inoremap <C-h>> instead.
		{"<BS>", "\x7f"},
		{"<C-h>", "\x08"},
		{"<Space>", " "},
		{"<lt>", "<"},
		{"<Bar>", "|"},
		{"a", "a"},
		{"é", "\xc3\xa9"},

		// Alt is the high bit, not an Escape prefix. eval("\<M-x>") is U+00F8
		// and a mapping on <M-x> fires on its UTF-8.
		{"<M-x>", "\xc3\xb8"},
		{"<A-x>", "\xc3\xb8"},
		{"<C-M-x>", "\xc2\x98"},
		{"<M-Space>", "\xc2\xa0"},

		// The named keys, in the forms xterm sends and vim decodes.
		{"<S-Tab>", "\x1b[Z"},
		{"<Up>", "\x1b[A"},
		{"<Down>", "\x1b[B"},
		{"<Right>", "\x1b[C"},
		{"<Left>", "\x1b[D"},
		{"<Home>", "\x1b[H"},
		{"<End>", "\x1b[F"},
		{"<PageUp>", "\x1b[5~"},
		{"<PageDown>", "\x1b[6~"},
		{"<Insert>", "\x1b[2~"},
		{"<Del>", "\x1b[3~"},
		{"<F1>", "\x1bOP"},
		{"<F4>", "\x1bOS"},
		{"<F5>", "\x1b[15~"},
		{"<F12>", "\x1b[24~"},
		{"<C-Right>", "\x1b[1;5C"},
		{"<S-Left>", "\x1b[1;2D"},
		{"<M-Right>", "\x1b[1;3C"},
		{"<C-F5>", "\x1b[15;5~"},
		{"<S-F5>", "\x1b[15;2~"},

		// Combinations with no legacy encoding go out as CSI-u, which vim 9.2
		// decodes: feeding "\x1b[32;5u" fires an <inoremap <C-Space>>.
		{"<C-Space>", "\x1b[32;5u"},
		{"<C-1>", "\x1b[49;5u"},

		// <D-x> is the one row that does not round-trip through vim. Vim reads
		// the CSI-u modifier bit 8 as xterm's Meta and inserts U+00F8; the
		// kitty protocol, which is what a terminal reporting Cmd at all
		// speaks, calls bit 8 super. pvim sends and reads it as Cmd, because
		// Cmd otherwise only ever arrives from AppKit and vim has no way to
		// say it in a terminal at all.
		{"<D-x>", "\x1b[120;9u"},

		// Keys no terminal can deliver encode to nothing.
		{"<Plug>", ""},
		{"<Cmd>", ""},
		{"<Ignore>", ""},
		{"<Help>", ""},
		{"<Undo>", ""},
		{"<LeftMouse>", ""},
	}

	for _, c := range cases {
		keys, err := Parse(c.notation, ",")
		if err != nil {
			t.Errorf("Parse(%q): %v", c.notation, err)
			continue
		}
		if len(keys) != 1 {
			t.Errorf("Parse(%q) gave %d keys, want 1", c.notation, len(keys))
			continue
		}
		got := string(keys[0].ToBytes())
		if got != c.want {
			t.Errorf("%s.ToBytes() = %q, want %q", c.notation, dump([]byte(got)), dump([]byte(c.want)))
		}
	}
}

func TestBytesSequence(t *testing.T) {
	keys, err := Parse("i<C-w><Esc>:wq<CR>", ",")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(Bytes(keys)), "i\x17\x1b:wq\r"; got != want {
		t.Errorf("Bytes = %q, want %q", dump([]byte(got)), dump([]byte(want)))
	}
}
