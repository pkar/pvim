package key

import "testing"

// TestDecodeSequences covers the escape sequences a real terminal sends.
//
// Every sequence here was also fed to vim 9.2 through a keystroke file with a
// mapping in place, and vim decoded all of them, including the modifyOtherKeys
// and CSI-u forms for <C-x>. The two exceptions are noted on their rows.
func TestDecodeSequences(t *testing.T) {
	cases := []struct {
		in   string
		want Key
	}{
		// Plain characters and the C0 codes, canonicalised the way vim does.
		{"a", Key{Rune: 'a'}},
		{"é", Key{Rune: 'é'}},
		{"\x00", Key{Special: KeyNul}},
		{"\x01", Key{Rune: 'A', Mod: ModCtrl}},
		{"\x08", Key{Rune: 'H', Mod: ModCtrl}},
		{"\x09", Key{Special: KeyTab}},
		{"\x0a", Key{Special: KeyNL}},
		{"\x0d", Key{Special: KeyCR}},
		{"\x17", Key{Rune: 'W', Mod: ModCtrl}},
		{"\x1a", Key{Rune: 'Z', Mod: ModCtrl}},
		{"\x1c", Key{Rune: '\\', Mod: ModCtrl}},
		{"\x1d", Key{Rune: ']', Mod: ModCtrl}},
		{"\x1e", Key{Rune: '^', Mod: ModCtrl}},
		{"\x1f", Key{Rune: '_', Mod: ModCtrl}},
		// 0x7f is the erase key on this machine, and vim treats it as <BS>.
		{"\x7f", Key{Special: KeyBS}},

		// Arrows, both the normal and the application-cursor form.
		{"\x1b[A", Key{Special: KeyUp}},
		{"\x1b[B", Key{Special: KeyDown}},
		{"\x1b[C", Key{Special: KeyRight}},
		{"\x1b[D", Key{Special: KeyLeft}},
		{"\x1bOA", Key{Special: KeyUp}},
		{"\x1bOD", Key{Special: KeyLeft}},

		// Home and End, both spellings. Vim's own table for this terminal has
		// only the letter forms, so "\x1b[1~" is a superset pvim accepts for
		// the terminals that send it.
		{"\x1b[H", Key{Special: KeyHome}},
		{"\x1b[F", Key{Special: KeyEnd}},
		{"\x1b[1~", Key{Special: KeyHome}},
		{"\x1b[4~", Key{Special: KeyEnd}},
		{"\x1b[7~", Key{Special: KeyHome}},
		{"\x1b[8~", Key{Special: KeyEnd}},

		{"\x1b[2~", Key{Special: KeyInsert}},
		{"\x1b[3~", Key{Special: KeyDel}},
		{"\x1b[5~", Key{Special: KeyPageUp}},
		{"\x1b[6~", Key{Special: KeyPageDown}},
		{"\x1b[Z", Key{Special: KeyTab, Mod: ModShift}},

		// Function keys, SS3 for F1-F4 and the tilde forms above.
		{"\x1bOP", Key{Special: KeyF1}},
		{"\x1bOQ", Key{Special: KeyF2}},
		{"\x1bOR", Key{Special: KeyF3}},
		{"\x1bOS", Key{Special: KeyF4}},
		{"\x1b[11~", Key{Special: KeyF1}},
		{"\x1b[15~", Key{Special: KeyF5}},
		{"\x1b[17~", Key{Special: KeyF6}},
		{"\x1b[21~", Key{Special: KeyF10}},
		{"\x1b[23~", Key{Special: KeyF11}},
		{"\x1b[24~", Key{Special: KeyF12}},

		// Modified keys. The ";n" parameter is one plus shift, alt, ctrl, super.
		{"\x1b[1;2C", Key{Special: KeyRight, Mod: ModShift}},
		{"\x1b[1;3C", Key{Special: KeyRight, Mod: ModAlt}},
		{"\x1b[1;5C", Key{Special: KeyRight, Mod: ModCtrl}},
		{"\x1b[1;6D", Key{Special: KeyLeft, Mod: ModCtrl | ModShift}},
		{"\x1b[1;8A", Key{Special: KeyUp, Mod: ModCtrl | ModAlt | ModShift}},
		{"\x1b[15;5~", Key{Special: KeyF5, Mod: ModCtrl}},
		{"\x1b[3;2~", Key{Special: KeyDel, Mod: ModShift}},
		{"\x1b[1;5P", Key{Special: KeyF1, Mod: ModCtrl}},

		// modifyOtherKeys form two and the CSI-u form of the same key. Both
		// canonicalise through the Ctrl folding rules, so both give <C-X>.
		{"\x1b[27;5;120~", Key{Rune: 'X', Mod: ModCtrl}},
		{"\x1b[120;5u", Key{Rune: 'X', Mod: ModCtrl}},
		{"\x1b[32;5u", Key{Rune: ' ', Mod: ModCtrl}},
		{"\x1b[97;2u", Key{Rune: 'A'}},
		{"\x1b[120;9u", Key{Rune: 'x', Mod: ModCmd}},

		// A bare Escape once there is a byte after it that starts nothing.
		{"\x1bx", Key{Special: KeyEsc}},
		{"\x1b\x1b", Key{Special: KeyEsc}},

		// Sequences pvim has no key for are consumed as <Ignore> rather than
		// leaking into the buffer as text. This is a cursor position report.
		{"\x1b[24;80R", Key{Special: KeyIgnore}},
	}

	for _, c := range cases {
		var d Decoder
		got, n, st := d.Next([]byte(c.in))
		if st != StatusKey {
			t.Errorf("Next(%q) status %v, want StatusKey", dump([]byte(c.in)), st)
			continue
		}
		if got != c.want {
			t.Errorf("Next(%q) = %v (%+v), want %v (%+v)", dump([]byte(c.in)), got, got, c.want, c.want)
		}
		// Every case here is one whole key and nothing else.
		if c.in != "\x1bx" && c.in != "\x1b\x1b" && n != len(c.in) {
			t.Errorf("Next(%q) consumed %d bytes, want %d", dump([]byte(c.in)), n, len(c.in))
		}
	}
}

// TestDecodeNeedsMore is the ttimeout state machine: the ambiguity of a lone
// Escape is a return value, not a timer, so the caller owns ttimeoutlen and the
// test owns no clock at all.
func TestDecodeNeedsMore(t *testing.T) {
	partials := []string{"\x1b", "\x1b[", "\x1b[1", "\x1b[1;", "\x1b[1;5", "\x1bO", "\xc3"}
	for _, p := range partials {
		var d Decoder
		if _, n, st := d.Next([]byte(p)); st != StatusNeedMore || n != 0 {
			t.Errorf("Next(%q) = %v after %d bytes, want StatusNeedMore and 0", dump([]byte(p)), st, n)
		}
	}

	// ttimeoutlen expires with a lone Escape pending: it is an Escape.
	var d Decoder
	k, n, st := d.Flush([]byte("\x1b"))
	if st != StatusKey || n != 1 || k != (Key{Special: KeyEsc}) {
		t.Errorf("Flush(ESC) = %v, %d, %v; want <Esc>, 1, StatusKey", k, n, st)
	}

	// ttimeoutlen expires halfway into an arrow key, which is what a slow SSH
	// link does. The Escape comes out and the rest is decoded after it.
	k, n, st = d.Flush([]byte("\x1b[1;"))
	if st != StatusKey || n != 1 || k != (Key{Special: KeyEsc}) {
		t.Errorf("Flush(partial CSI) = %v, %d, %v; want <Esc>, 1, StatusKey", k, n, st)
	}

	// The same bytes with the rest arriving in time are one key.
	k, n, st = d.Next([]byte("\x1b[1;5C"))
	if st != StatusKey || n != 6 || k != (Key{Special: KeyRight, Mod: ModCtrl}) {
		t.Errorf("Next(full CSI) = %v, %d, %v; want <C-Right>, 6, StatusKey", k, n, st)
	}

	if _, _, st := d.Next(nil); st != StatusEmpty {
		t.Errorf("Next(nil) = %v, want StatusEmpty", st)
	}
}

// TestDecodeAltIsEscape covers the terminal setting vim does not have. With it
// off, which is the default and what vim does, "\x1bx" is an Escape and then an
// x; feeding those two bytes to vim in insert mode leaves insert mode rather
// than firing an <M-x> mapping.
func TestDecodeAltIsEscape(t *testing.T) {
	var off Decoder
	k, n, _ := off.Next([]byte("\x1bx"))
	if k != (Key{Special: KeyEsc}) || n != 1 {
		t.Errorf("default: Next(ESC x) = %v after %d bytes, want <Esc> after 1", k, n)
	}

	on := Decoder{AltIsEscape: true}
	k, n, _ = on.Next([]byte("\x1bx"))
	if k != (Key{Rune: 'x', Mod: ModAlt}) || n != 2 {
		t.Errorf("AltIsEscape: Next(ESC x) = %v after %d bytes, want <M-x> after 2", k, n)
	}
	// An escape sequence still wins over the Alt reading.
	k, n, _ = on.Next([]byte("\x1b[A"))
	if k != (Key{Special: KeyUp}) || n != 3 {
		t.Errorf("AltIsEscape: Next(ESC [ A) = %v after %d bytes, want <Up> after 3", k, n)
	}
}

func TestDecodeAll(t *testing.T) {
	var d Decoder
	// The vimrc's `.<C-x><C-o>` mapping, as a terminal would deliver it, with a
	// half-arrived arrow key on the end.
	in := []byte(".\x18\x0f\x1b[")
	keys, used := d.DecodeAll(in)
	want := []Key{{Rune: '.'}, {Rune: 'X', Mod: ModCtrl}, {Rune: 'O', Mod: ModCtrl}}
	if !equal(keys, want) || used != 3 {
		t.Errorf("DecodeAll = %v after %d bytes, want %v after 3", keys, used, want)
	}
}

// TestDecodeMouse covers SGR mouse reporting, which is what `set mouse=a` turns
// on and the only mouse protocol pvim speaks.
func TestDecodeMouse(t *testing.T) {
	cases := []struct {
		in   string
		want Key
		col  int
		row  int
	}{
		{"\x1b[<0;10;5M", Key{Special: KeyLeftMouse}, 10, 5},
		{"\x1b[<0;10;5m", Key{Special: KeyLeftRelease}, 10, 5},
		{"\x1b[<1;3;4M", Key{Special: KeyMiddleMouse}, 3, 4},
		{"\x1b[<2;3;4M", Key{Special: KeyRightMouse}, 3, 4},
		{"\x1b[<32;7;8M", Key{Special: KeyLeftDrag}, 7, 8},
		{"\x1b[<35;7;8M", Key{Special: KeyMouseMove}, 7, 8},
		{"\x1b[<64;1;1M", Key{Special: KeyScrollWheelUp}, 1, 1},
		{"\x1b[<65;1;1M", Key{Special: KeyScrollWheelDown}, 1, 1},
		{"\x1b[<66;1;1M", Key{Special: KeyScrollWheelLeft}, 1, 1},
		{"\x1b[<128;2;2M", Key{Special: KeyX1Mouse}, 2, 2},
		{"\x1b[<16;9;9M", Key{Special: KeyLeftMouse, Mod: ModCtrl}, 9, 9},
		{"\x1b[<4;9;9M", Key{Special: KeyLeftMouse, Mod: ModShift}, 9, 9},
	}

	for _, c := range cases {
		var d Decoder
		got, n, st := d.Next([]byte(c.in))
		if st != StatusKey || n != len(c.in) {
			t.Errorf("Next(%q) = %v after %d bytes, want StatusKey after %d", dump([]byte(c.in)), st, n, len(c.in))
			continue
		}
		if got != c.want {
			t.Errorf("Next(%q) = %v, want %v", dump([]byte(c.in)), got, c.want)
		}
		if d.Mouse.Col != c.col || d.Mouse.Row != c.row {
			t.Errorf("Next(%q) mouse at %+v, want col %d row %d", dump([]byte(c.in)), d.Mouse, c.col, c.row)
		}
	}
}

// TestBytesDecodeRoundTrip closes the loop for the keys a terminal can actually
// carry: printing to bytes and decoding them back gives the key again.
//
// The exceptions are all real terminal ambiguities rather than bugs, and each
// one is listed so that the list is a test and not a comment. Alt is the high
// bit on the wire and comes back as the accented rune; <C-?> and <BS> are both
// 0x7f and the erase key wins; <C-h> and <BS> are the pair vim keeps apart the
// same way.
func TestBytesDecodeRoundTrip(t *testing.T) {
	notations := []string{
		"<C-x>", "<C-a>", "<C-\\>", "<C-]>", "<C-^>", "<C-_>",
		"<Nul>", "<Esc>", "<CR>", "<NL>", "<Tab>", "<BS>", "<C-h>",
		"<Space>", "<lt>", "<Bar>", "a", "Z", "é", "日",
		"<S-Tab>", "<Up>", "<Down>", "<Left>", "<Right>", "<Home>", "<End>",
		"<PageUp>", "<PageDown>", "<Insert>", "<Del>",
		"<F1>", "<F2>", "<F3>", "<F4>", "<F5>", "<F10>", "<F11>", "<F12>",
		"<C-Right>", "<S-Left>", "<M-Right>", "<C-S-Up>", "<C-F5>", "<S-F12>",
		"<C-Space>", "<C-1>", "<D-x>", "<M-C-Left>",
	}

	for _, n := range notations {
		keys, err := Parse(n, ",")
		if err != nil || len(keys) != 1 {
			t.Fatalf("Parse(%q) = %v, %v", n, keys, err)
		}
		b := keys[0].ToBytes()
		var d Decoder
		// Flush and not Next: a lone Escape is StatusNeedMore forever, and a
		// keystroke file has no timing to wait on.
		got, used, st := d.Flush(b)
		if st != StatusKey || used != len(b) {
			t.Errorf("%s: decoding %q gave %v after %d of %d bytes", n, dump(b), st, used, len(b))
			continue
		}
		if got != keys[0] {
			t.Errorf("%s: %q decoded back as %v, want %v", n, dump(b), got, keys[0])
		}
	}

	// The lossy ones, spelled out.
	for _, c := range []struct{ notation, back string }{
		{"<M-x>", "ø"},    // Alt is the high bit on the wire and there is no way back
		{"<C-?>", "<BS>"}, // 0x7f is the erase key, exactly as it is in vim
	} {
		keys, err := Parse(c.notation, ",")
		if err != nil {
			t.Fatal(err)
		}
		var d Decoder
		got, _, _ := d.Flush(keys[0].ToBytes())
		if got.String() != c.back {
			t.Errorf("%s through bytes came back as %q, want %q", c.notation, got.String(), c.back)
		}
	}
}
