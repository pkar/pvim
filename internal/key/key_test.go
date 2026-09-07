package key

import (
	"strings"
	"testing"
)

// Every expectation in this file was read out of vim 9.2 at
// /opt/homebrew/bin/vim, either with eval("\<...>") and keytrans() or by
// feeding raw bytes to `vim -s` with a mapping in place. Where pvim's model
// differs from vim's byte encoding on purpose, the test says so.

func TestParseNotation(t *testing.T) {
	cases := []struct {
		notation string
		want     []Key
	}{
		// Bare characters, no notation at all.
		{"gg", []Key{{Rune: 'g'}, {Rune: 'g'}}},
		{"", nil},

		// Ctrl folds case away: eval("\<C-x>") and eval("\<C-X>") and
		// eval("\<C-S-x>") are all the byte 0x18.
		{"<C-x>", []Key{{Rune: 'X', Mod: ModCtrl}}},
		{"<C-X>", []Key{{Rune: 'X', Mod: ModCtrl}}},
		{"<C-S-x>", []Key{{Rune: 'X', Mod: ModCtrl}}},
		{"<c-x>", []Key{{Rune: 'X', Mod: ModCtrl}}},
		{"<C-a>", []Key{{Rune: 'A', Mod: ModCtrl}}},

		// The Ctrl keys vim canonicalises to a named key.
		{"<C-@>", []Key{{Special: KeyNul}}},
		{"<Nul>", []Key{{Special: KeyNul}}},
		{"<C-[>", []Key{{Special: KeyEsc}}},
		{"<C-i>", []Key{{Special: KeyTab}}},
		{"<C-m>", []Key{{Special: KeyCR}}},
		{"<C-j>", []Key{{Special: KeyNL}}},

		// <C-Space> is not <Nul> in vim 9.2: an <inoremap <C-Space>> does not
		// fire on a NUL byte and an <inoremap <Nul>> does.
		{"<C-Space>", []Key{{Rune: ' ', Mod: ModCtrl}}},

		// Ctrl over a character with a control code keeps the modifier and the
		// character; over one without, it keeps both too, and nothing folds.
		{"<C-]>", []Key{{Rune: ']', Mod: ModCtrl}}},
		{"<C-\\>", []Key{{Rune: '\\', Mod: ModCtrl}}},
		{"<C-Bslash>", []Key{{Rune: '\\', Mod: ModCtrl}}},
		{"<C-^>", []Key{{Rune: '^', Mod: ModCtrl}}},
		{"<C-_>", []Key{{Rune: '_', Mod: ModCtrl}}},
		{"<C-?>", []Key{{Rune: '?', Mod: ModCtrl}}},
		{"<C-1>", []Key{{Rune: '1', Mod: ModCtrl}}},
		{"<C-Bar>", []Key{{Rune: '|', Mod: ModCtrl}}},

		// Shift folds into the rune when there is an uppercase form and stays a
		// modifier when there is not.
		{"<S-a>", []Key{{Rune: 'A'}}},
		{"<S-A>", []Key{{Rune: 'A'}}},
		{"<S-1>", []Key{{Rune: '1', Mod: ModShift}}},
		{"<S-Space>", []Key{{Rune: ' ', Mod: ModShift}}},
		{"<S-Tab>", []Key{{Special: KeyTab, Mod: ModShift}}},

		// Alt and Meta are one bit; Cmd is its own.
		{"<M-x>", []Key{{Rune: 'x', Mod: ModAlt}}},
		{"<A-x>", []Key{{Rune: 'x', Mod: ModAlt}}},
		{"<D-x>", []Key{{Rune: 'x', Mod: ModCmd}}},
		{"<C-M-x>", []Key{{Rune: 'X', Mod: ModCtrl | ModAlt}}},
		{"<M-C-x>", []Key{{Rune: 'X', Mod: ModCtrl | ModAlt}}},
		{"<C-S-Right>", []Key{{Special: KeyRight, Mod: ModCtrl | ModShift}}},

		// The names for characters.
		{"<Space>", []Key{{Rune: ' '}}},
		{"<Bar>", []Key{{Rune: '|'}}},
		{"<Bslash>", []Key{{Rune: '\\'}}},
		{"<lt>", []Key{{Rune: '<'}}},
		{"<Char-97>", []Key{{Rune: 'a'}}},
		{"<Char-0x41>", []Key{{Rune: 'A'}}},

		// The named keys and their aliases.
		{"<CR>", []Key{{Special: KeyCR}}},
		{"<Enter>", []Key{{Special: KeyCR}}},
		{"<Return>", []Key{{Special: KeyCR}}},
		{"<NL>", []Key{{Special: KeyNL}}},
		{"<Esc>", []Key{{Special: KeyEsc}}},
		{"<Tab>", []Key{{Special: KeyTab}}},
		{"<BS>", []Key{{Special: KeyBS}}},
		{"<Del>", []Key{{Special: KeyDel}}},
		{"<Home>", []Key{{Special: KeyHome}}},
		{"<End>", []Key{{Special: KeyEnd}}},
		{"<PageUp>", []Key{{Special: KeyPageUp}}},
		{"<PageDown>", []Key{{Special: KeyPageDown}}},
		{"<Insert>", []Key{{Special: KeyInsert}}},
		{"<Help>", []Key{{Special: KeyHelp}}},
		{"<Undo>", []Key{{Special: KeyUndo}}},
		{"<Up>", []Key{{Special: KeyUp}}},
		{"<F5>", []Key{{Special: KeyF5}}},
		{"<f5>", []Key{{Special: KeyF5}}},
		{"<F12>", []Key{{Special: KeyF12}}},
		{"<S-F1>", []Key{{Special: KeyF1, Mod: ModShift}}},
		{"<Plug>", []Key{{Special: KeyPlug}}},

		// The mouse pseudo-keys, click counts included.
		{"<LeftMouse>", []Key{{Special: KeyLeftMouse}}},
		{"<ScrollWheelUp>", []Key{{Special: KeyScrollWheelUp}}},
		{"<2-LeftMouse>", []Key{{Special: KeyLeftMouse, Clicks: 2}}},
		{"<4-RightMouse>", []Key{{Special: KeyRightMouse, Clicks: 4}}},
		{"<C-LeftDrag>", []Key{{Special: KeyLeftDrag, Mod: ModCtrl}}},

		// Notation mixed with raw characters, which is every real mapping.
		{"<C-w>v", []Key{{Rune: 'W', Mod: ModCtrl}, {Rune: 'v'}}},
		{":NERDTreeToggle<CR>", append(runes(":NERDTreeToggle"), Key{Special: KeyCR})},
		{".<C-x><C-o>", []Key{{Rune: '.'}, {Rune: 'X', Mod: ModCtrl}, {Rune: 'O', Mod: ModCtrl}}},

		// Vim keeps an unrecognised <Foo> as the literal characters it is made
		// of: eval("\<SID>") is five characters and keytrans() prints <lt>SID>.
		// The map arguments go the same way, which is why ScanMapArgs exists.
		{"<SID>", runes("<SID>")},
		{"<silent>", runes("<silent>")},
		{"<nowait>", runes("<nowait>")},
		{"<buffer>", runes("<buffer>")},
		{"<expr>", runes("<expr>")},
		{"<unique>", runes("<unique>")},
		{"<special>", runes("<special>")},
		{"<script>", runes("<script>")},
		{"<Foo>", runes("<Foo>")},
		{"<C->", runes("<C->")},
		{"<>", runes("<>")},
		{"a<b", []Key{{Rune: 'a'}, {Rune: '<'}, {Rune: 'b'}}},

		// No backslash escaping inside a map, so this is a backslash and an Esc.
		{`\<Esc>`, []Key{{Rune: '\\'}, {Special: KeyEsc}}},
	}

	for _, c := range cases {
		got, err := Parse(c.notation, ",")
		if err != nil {
			t.Errorf("Parse(%q): %v", c.notation, err)
			continue
		}
		if !equal(got, c.want) {
			t.Errorf("Parse(%q) = %v, want %v", c.notation, got, c.want)
		}
	}
}

func TestParseLeader(t *testing.T) {
	// The vimrc sets mapleader to a comma, so <leader>, is the "," that opens
	// the tree and that is the mapping this has to get right.
	got, err := Parse("<leader>,", ",")
	if err != nil {
		t.Fatal(err)
	}
	if want := runes(",,"); !equal(got, want) {
		t.Errorf("<leader>, with mapleader=, = %v, want %v", got, want)
	}

	got, err = Parse("<Leader>x", "<Space>")
	if err != nil {
		t.Fatal(err)
	}
	if want := []Key{{Rune: ' '}, {Rune: 'x'}}; !equal(got, want) {
		t.Errorf("a mapleader that is itself notation = %v, want %v", got, want)
	}

	if _, err := Parse("<leader>x", ""); err == nil {
		t.Error("<leader> with no mapleader should be an error, not a silent backslash")
	}

	got, err = ParseLeader("<LocalLeader>x", ",", ";")
	if err != nil {
		t.Fatal(err)
	}
	if want := runes(";x"); !equal(got, want) {
		t.Errorf("<LocalLeader> = %v, want %v", got, want)
	}
}

func TestString(t *testing.T) {
	cases := []struct {
		key  Key
		want string
	}{
		// Vim's keytrans() spellings.
		{Key{Rune: 'X', Mod: ModCtrl}, "<C-X>"},
		{Key{Rune: 'A', Mod: ModCtrl}, "<C-A>"},
		{Key{Special: KeyNul}, "<Nul>"},
		{Key{Special: KeyEsc}, "<Esc>"},
		{Key{Special: KeyCR}, "<CR>"},
		{Key{Special: KeyTab, Mod: ModShift}, "<S-Tab>"},
		{Key{Rune: ' '}, "<Space>"},
		{Key{Rune: '<'}, "<lt>"},
		{Key{Rune: '|'}, "|"},
		{Key{Rune: '\\'}, `\`},
		{Key{Rune: 'a'}, "a"},
		{Key{Rune: '1', Mod: ModShift}, "<S-1>"},
		{Key{Rune: 'x', Mod: ModAlt}, "<M-x>"},
		{Key{Rune: 'x', Mod: ModCmd}, "<D-x>"},
		// The modifier order is vim's, checked with keytrans(): M, then C, then
		// S, then D, with the click count ahead of all of them.
		{Key{Special: KeyLeft, Mod: ModCtrl | ModAlt | ModShift}, "<M-C-S-Left>"},
		{Key{Rune: 'a', Mod: ModShift | ModCmd}, "<S-D-a>"},
		{Key{Special: KeyLeftMouse, Clicks: 2}, "<2-LeftMouse>"},
		{Key{Rune: ' ', Mod: ModCtrl}, "<C-Space>"},
		{Key{Rune: '|', Mod: ModCtrl}, "<C-Bar>"},
		{Key{Rune: '\\', Mod: ModCtrl}, "<C-Bslash>"},
		{Key{Rune: '?', Mod: ModCtrl}, "<C-?>"},
		// A raw control rune with no modifier is not something Parse makes, but
		// it still has to print as something that reads back the same.
		{Key{Rune: 0x18}, "<Char-0x18>"},
	}

	for _, c := range cases {
		if got := c.key.String(); got != c.want {
			t.Errorf("Key%+v.String() = %q, want %q", c.key, got, c.want)
		}
	}
}

// TestRoundTrip is the property that keeps :map output usable as :map input:
// printing a parsed sequence and parsing it again gives the same keys.
func TestRoundTrip(t *testing.T) {
	inputs := []string{
		"gg", "<C-w>v", "<C-x><C-o>", "<leader>,", ":wq<CR>",
		"<Esc><Tab><BS><Del><Nul><NL><Space><lt><Bar><Bslash>",
		"<Up><Down><Left><Right><Home><End><PageUp><PageDown><Insert>",
		"<F1><F5><F12><S-F1><C-F5><M-F12>",
		"<C-a><C-z><C-@><C-[><C-]><C-\\><C-^><C-_><C-?>",
		"<S-a><S-1><S-Space><S-Tab><C-S-1>",
		"<M-x><A-x><D-x><C-M-x><C-S-Right><M-C-S-Left><S-D-a>",
		"<LeftMouse><2-LeftMouse><3-RightMouse><ScrollWheelDown><MouseMove>",
		"<Plug><Cmd><ScriptCmd><Ignore><Help><Undo>",
		"hello world, 42! <C-v>u00e9 é 日本",
		"<Foo><SID><silent>",
		"vnoremap <C-c> \"+y",
		"<line1>,<line2>!jq --sort-keys '.'",
	}

	for _, in := range inputs {
		first, err := Parse(in, ",")
		if err != nil {
			t.Errorf("Parse(%q): %v", in, err)
			continue
		}
		printed := Format(first)
		second, err := Parse(printed, ",")
		if err != nil {
			t.Errorf("Parse(Format(Parse(%q))) = %q: %v", in, printed, err)
			continue
		}
		if !equal(first, second) {
			t.Errorf("round trip of %q through %q changed the keys:\n got %v\nwant %v", in, printed, second, first)
		}
		// And once more, because a printer that is not idempotent will drift.
		if again := Format(second); again != printed {
			t.Errorf("Format is not idempotent for %q: %q then %q", in, printed, again)
		}
	}
}

func TestScanMapArgs(t *testing.T) {
	cases := []struct {
		in   string
		want MapArgs
		rest string
	}{
		{"<leader>x :foo<CR>", MapArgs{}, "<leader>x :foo<CR>"},
		{"<silent> <leader>x", MapArgs{Silent: true}, "<leader>x"},
		{"<buffer> . .<C-x><C-o>", MapArgs{Buffer: true}, ". .<C-x><C-o>"},
		{"<silent><buffer><nowait>gg", MapArgs{Silent: true, Buffer: true, NoWait: true}, "gg"},
		{"<expr> <unique> <special> <script> x", MapArgs{Expr: true, Unique: true, Special: true, Script: true}, "x"},
		// A key notation is not a map argument and stops the scan.
		{"<C-w>v", MapArgs{}, "<C-w>v"},
		{"<silent> <C-p>", MapArgs{Silent: true}, "<C-p>"},
	}

	for _, c := range cases {
		got, rest := ScanMapArgs(c.in)
		if got != c.want || rest != c.rest {
			t.Errorf("ScanMapArgs(%q) = %+v, %q; want %+v, %q", c.in, got, rest, c.want, c.rest)
		}
	}
}

func TestCtrlHelper(t *testing.T) {
	if got, want := Ctrl('x'), (Key{Rune: 'X', Mod: ModCtrl}); got != want {
		t.Errorf("Ctrl('x') = %+v, want %+v", got, want)
	}
	if got, want := Ctrl('i'), (Key{Special: KeyTab}); got != want {
		t.Errorf("Ctrl('i') = %+v, want %+v", got, want)
	}
	if got, want := Ctrl('@'), (Key{Special: KeyNul}); got != want {
		t.Errorf("Ctrl('@') = %+v, want %+v", got, want)
	}
	if !Rune('a').IsRune() || Rune('a').IsMouse() {
		t.Error("a plain rune should be a rune and not a mouse event")
	}
	if !(Key{Special: KeyScrollWheelUp}).IsMouse() {
		t.Error("<ScrollWheelUp> should count as a mouse event")
	}
}

// runes is the expectation for a stretch of notation with nothing special in it.
func runes(s string) []Key {
	var keys []Key
	for _, r := range s {
		keys = append(keys, Key{Rune: r})
	}
	return keys
}

func equal(a, b []Key) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// dump is the failure message for a byte slice, because %q on a slice of bytes
// full of escapes is unreadable.
func dump(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			sb.WriteByte(c)
			continue
		}
		const hex = "0123456789abcdef"
		sb.WriteString("\\x")
		sb.WriteByte(hex[c>>4])
		sb.WriteByte(hex[c&0xf])
	}
	return sb.String()
}
