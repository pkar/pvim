package key

import "testing"

// The SS3 table, measured against /opt/homebrew/bin/vim 9.2.0321 by typing
// each three-byte sequence in insert mode under a pty with TERM=xterm and
// reading the buffer back: "\x1bOs" puts a "3" in, "\x1bOk" a "+", "\x1bOM"
// a line break, and "\x1bOl", which is in neither half of the table, leaves
// Escape, "O" and "l" -- an opened line with an "l" on it.
func TestDecodeSS3(t *testing.T) {
	cases := []struct {
		in   string
		want Key
		used int
	}{
		{"\x1bOA", Key{Special: KeyUp}, 3},
		{"\x1bOP", Key{Special: KeyF1}, 3},
		{"\x1bOS", Key{Special: KeyF4}, 3},
		{"\x1bOH", Key{Special: KeyHome}, 3},

		{"\x1bOj", Rune('*'), 3},
		{"\x1bOk", Rune('+'), 3},
		{"\x1bOm", Rune('-'), 3},
		{"\x1bOn", Rune('.'), 3},
		{"\x1bOo", Rune('/'), 3},
		{"\x1bOp", Rune('0'), 3},
		{"\x1bOs", Rune('3'), 3},
		{"\x1bOy", Rune('9'), 3},
		{"\x1bOM", Key{Special: KeyCR}, 3},

		// Not a key: vim reads the Escape and leaves the rest.
		{"\x1bOl", Key{Special: KeyEsc}, 1},
		{"\x1bOz", Key{Special: KeyEsc}, 1},
		{"\x1bO0", Key{Special: KeyEsc}, 1},
	}
	for _, tc := range cases {
		var d Decoder
		got, n, st := d.Flush([]byte(tc.in))
		if st != StatusKey {
			t.Errorf("%q: status %v, want a key", tc.in, st)
			continue
		}
		if got != tc.want || n != tc.used {
			t.Errorf("%q decoded to %v after %d bytes, want %v after %d", tc.in, got, n, tc.want, tc.used)
		}
	}
}
