package mode

import "testing"

// "ga", measured one character at a time against /opt/homebrew/bin/vim
// 9.2.0321.
//
// Every want below came back from
//
//	vim --clean -s keys file
//
// with keys holding ":set columns=300 nomore", ":redir! > msgs.txt", the "l"s
// that put the cursor on the character, "ga", and ":redir END". The wide
// screen is not decoration: vim truncates a message that does not fit, and the
// six-mark case comes back with "..." in the middle of it at 80 columns.
//
// The rows are the whole range the command has to answer for, and each one is
// a different branch: the two spacings, the two spellings of "octal", the two
// hexadecimal widths, the digraph tail and its absence, a control character, a
// NUL byte in a line, an empty line, a character vim will not draw, a
// character above the BMP, and one to six composing marks.
func TestGaMatchesVim(t *testing.T) {
	cases := []struct {
		name string
		line string
		col  int
		want string
	}{
		{"a letter", "abc", 0, "<a>  97,  Hex 61,  Octal 141"},
		{"a digit", "0abc", 0, "<0>  48,  Hex 30,  Octal 060"},
		{"a space", " abc", 0, "< >  32,  Hex 20,  Oct 040, Digr SP"},
		{"a tilde", "~abc", 0, "<~>  126,  Hex 7e,  Oct 176, Digr '?"},
		{"a bang", "!abc", 0, "<!>  33,  Hex 21,  Octal 041"},
		{"a colon", ":abc", 0, "<:>  58,  Hex 3a,  Octal 072"},
		{"a double quote", "\"abc", 0, "<\">  34,  Hex 22,  Octal 042"},
		{"a percent", "%abc", 0, "<%>  37,  Hex 25,  Octal 045"},
		{"a backslash", "\\abc", 0, "<\\>  92,  Hex 5c,  Oct 134, Digr //"},
		{"an empty line", "", 0, "NUL"},
		{"CTRL-A", "\x01abc", 0, "<^A>  1,  Hex 01,  Oct 001, Digr SH"},
		{"a tab", "\tabc", 0, "<^I>  9,  Hex 09,  Oct 011, Digr HT"},
		{"an escape", "\x1babc", 0, "<^[>  27,  Hex 1b,  Oct 033, Digr EC"},
		{"DEL", "\x7fabc", 0, "<^?>  127,  Hex 7f,  Oct 177, Digr DT"},
		{"a NUL byte", "\x00abc", 0, "<^@>  0,  Hex 00,  Octal 000"},
		{"a carriage return", "a\rb", 1, "<^M>  13,  Hex 0d,  Oct 015, Digr CR"},
		{"U+0080", "\u0080x", 0, "<<80>> 128, Hex 0080, Oct 200, Digr PA"},
		{"U+009F", "\u009fx", 0, "<<9f>> 159, Hex 009f, Oct 237, Digr AC"},
		{"U+00A0", "\u00a0x", 0, "<\u00a0> 160, Hex 00a0, Oct 240, Digr NS"},
		{"U+00AD", "\u00adx", 0, "<\u00ad> 173, Hex 00ad, Oct 255, Digr --"},
		{"e acute", "\u00e9abc", 0, "<\u00e9> 233, Hex 00e9, Oct 351, Digr e'"},
		{"a CJK ideograph", "日abc", 0, "<日> 26085, Hex 65e5, Octal 62745"},
		{"an emoji", "\U0001f600abc", 0, "<\U0001f600> 128512, Hex 0001f600, Octal 373000"},
		{"U+200B", "\u200bx", 0, "<<200b>> 8203, Hex 200b, Octal 20013"},
		{"U+2028", "\u2028x", 0, "<\u2028> 8232, Hex 2028, Octal 20050"},
		{"U+FFFD", "\ufffdx", 0, "<\ufffd> 65533, Hex fffd, Octal 177775"},
		{"one combining mark", "e\u0301abc", 0, "<e>  101,  Hex 65,  Octal 145 < \u0301> 769, Hex 0301, Octal 1401"},
		{"two combining marks", "a\u0301\u0308bc", 0, "<a>  97,  Hex 61,  Octal 141 < \u0301> 769, Hex 0301, Octal 1401 < \u0308> 776, Hex 0308, Octal 1410"},
		{"six combining marks", "a\u0301\u0308\u0303\u0304\u0306\u0307x", 0, "<a>  97,  Hex 61,  Octal 141 < \u0301> 769, Hex 0301, Octal 1401 < \u0308> 776, Hex 0308, Octal 1410 < \u0303> 771, Hex 0303, Octal 1403 < \u0304> 772, Hex 0304, Octal 1404 < \u0306> 774, Hex 0306, Octal 1406 < \u0307> 775, Hex 0307, Octal 1407"},
		{"eight combining marks", "a\u0301\u0308\u0303\u0304\u0306\u0307\u030a\u030bx", 0, "<a>  97,  Hex 61,  Octal 141 < \u0301> 769, Hex 0301, Octal 1401 < \u0308> 776, Hex 0308, Octal 1410 < \u0303> 771, Hex 0303, Octal 1403 < \u0304> 772, Hex 0304, Octal 1404 < \u0306> 774, Hex 0306, Octal 1406 < \u0307> 775, Hex 0307, Octal 1407"},
		{"a mark on a multi-byte base", "\u00e9\u0301x", 0, "<\u00e9> 233, Hex 00e9, Oct 351, Digr e' < \u0301> 769, Hex 0301, Octal 1401"},
		{"a lone combining mark", "\u0301x", 0, "< \u0301> 769, Hex 0301, Octal 1401"},
		{"past the last character on a short line", "ab", 1, "<b>  98,  Hex 62,  Octal 142"},
	}
	for _, c := range cases {
		if got := asciiText([]byte(c.line), c.col); got != c.want {
			t.Errorf("%s: ga said\n\t%q\nvim says\n\t%q", c.name, got, c.want)
		}
	}
}

// TestGaThroughTheModeMachine is the same command reached the way a person
// reaches it, which is the half TestGaMatchesVim does not cover: the key
// dispatch, the message line and a cursor that had to be moved first.
func TestGaThroughTheModeMachine(t *testing.T) {
	e := edit(t, "alpha\n", "llga")
	if got, want := e.Message(), "<p>  112,  Hex 70,  Octal 160"; got != want {
		t.Errorf("ga on the third character said %q, want %q", got, want)
	}
	if got := e.Cursor().Col; got != 2 {
		t.Errorf("ga moved the cursor to column %d", got)
	}
}
