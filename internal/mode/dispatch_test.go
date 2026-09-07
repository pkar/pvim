package mode

import "testing"

// The dispatch, end to end: a key sequence in and a buffer out.
//
// These are the cases that go wrong when the state machine hands the wrong
// count, kind or register down, rather than when a motion or an operator is
// wrong in itself. Every want below came from the same script through
// /opt/homebrew/bin/vim --clean.
func TestNormalDispatch(t *testing.T) {
	const words = "a b c d e f g h i j k l\n"
	cases := []struct{ name, in, keys, want string }{
		{"x deletes one character", "hello\n", "x", "ello\n"},
		{"a count on x", "hello\n", "3x", "lo\n"},
		{"x past the end of the line stops there", "ab\n", "20x", "\n"},
		{"x on an empty line does nothing", "a\n\nb\n", "jx", "a\n\nb\n"},
		{"X at column one does nothing", "abc\n", "X", "abc\n"},
		{"X takes what is behind the cursor", "abcdef\n", "3lX", "abdef\n"},
		{"D takes the rest of the line", "one two\n", "wD", "one \n"},
		{"dd takes the line", "one\ntwo\n", "dd", "two\n"},
		{"a count on dd", "a\nb\nc\nd\n", "2dd", "c\nd\n"},
		{"dw takes a word", "one two\n", "dw", "two\n"},
		{"the two counts multiply", words, "2d3w", "g h i j k l\n"},
		{"the count after the operator", words, "d3w", "d e f g h i j k l\n"},
		{"the count before it", words, "3dw", "d e f g h i j k l\n"},
		{"J joins two lines", "one\ntwo\n", "J", "one two\n"},
		{"gJ joins without a space", "one\ntwo\n", "gJ", "onetwo\n"},
		{"r replaces one character", "abc\n", "rz", "zbc\n"},
		{"a count on r", "abcdef\n", "3rz", "zzzdef\n"},
		{"r past the end of the line does nothing", "ab\n", "20rz", "ab\n"},
		{"~ toggles and moves on", "abc\n", "~~", "ABc\n"},
		{"a count on ~", "abcdef\n", "5~", "ABCDEf\n"},
		{"CTRL-A adds one", "x 41 y\n", "\x01", "x 42 y\n"},
		{"a count on CTRL-A", "x 41 y\n", "10\x01", "x 51 y\n"},
		{"CTRL-A on a negative number", "z -3 w\n", "\x01", "z -2 w\n"},
		{"CTRL-A in hexadecimal", "0x0f\n", "\x01", "0x10\n"},
		{"CTRL-A keeps leading zeros", "007\n", "\x01", "008\n"},
		{"CTRL-X takes one away", "x 41 y\n", "\x18", "x 40 y\n"},
		{"the dot repeats a delete", "a\nb\nc\nd\n", "dd.", "c\nd\n"},
		{"a new count on the dot replaces the old", words, "dw3.", "e f g h i j k l\n"},
		{"a visual charwise delete", "abcdefgh\n", "lv3ld", "afgh\n"},
		{"a visual linewise delete", "a\nb\nc\n", "Vjd", "c\n"},
		{"o swaps the ends of a selection", "abcde\nfghij\n", "llvjohd", "aij\n"},
		{"a blockwise insert", "abc\ndef\nghi\n", "l\x16jjIX\x1b", "aXbc\ndXef\ngXhi\n"},
		{"a blockwise append", "abc\ndef\nghi\n", "l\x16jjAX\x1b", "abXc\ndeXf\nghXi\n"},
		{"a blockwise append past a short line", "abcd\ne\nfghi\n", "ll\x16jjAX\x1b", "abcXd\ne  X\nfghXi\n"},
		{"a blockwise insert skips a short line", "abcd\ne\nfghi\n", "ll\x16jjIX\x1b", "abXcd\ne\nfgXhi\n"},
		{"a $ block appends to every end", "abc\ndefgh\ngh\n", "l\x16jj$AX\x1b", "abcX\ndefghX\nghX\n"},
		{"a blockwise change", "abc\ndef\nghi\n", "l\x16jjcXY\x1b", "aXYc\ndXYf\ngXYi\n"},
		{"a blockwise replace", "abcde\nfghij\nklmno\n", "l\x16jlrX", "aXXde\nfXXij\nklmno\n"},
		{"a visual replace", "abcde\nfghij\nklmno\n", "lvjlrX", "aXXXX\nXXXij\nklmno\n"},
		{"gv brings the selection back", "abcde\nfghij\nkl\n", "vjy\x1bgvd", "ghij\nkl\n"},
		{"a text object", "one two three\n", "wdiw", "one  three\n"},
		{"p puts a line below", "one\ntwo\n", "yyp", "one\none\ntwo\n"},
		{"P puts it above", "one\ntwo\n", "yyjP", "one\none\ntwo\n"},
		{"p of a charwise register at the end of a line", "one\ntwo\n", "ylj$p", "one\ntwoo\n"},
		{"ddp swaps two lines", "one\ntwo\nthree\n", "ddp", "two\none\nthree\n"},
		{"xp swaps two characters", "one\n", "xp", "noe\n"},
		{"a count on p", "one\ntwo\n", "yy3p", "one\none\none\none\ntwo\n"},
		{"gp leaves the cursor after the put", "one\ntwo\n", "yygpx", "one\none\nwo\n"},
		{"a visual put replaces the selection", "one\ntwo\n", "yyjVp", "one\none\n"},
		{"a search motion", "alpha\nbeta\n", "d/beta\r", "beta\n"},
		// Measured: /foo from the top finds the second one, and n wraps back
		// to the first, so the x lands on line 1.
		{"n repeats it and wraps", "foo\nbar\nfoo\n", "/foo\rnx", "oo\nbar\nfoo\n"},
		{"the star search", "foo\nbar\nfoo\n", "*x", "foo\nbar\noo\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buf(edit(t, tc.in, tc.keys)); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMacroStopsOnAFailedSearch is why a beep is not an error. The macro is
// recorded with a search in it that finds nothing on the second run, and vim
// stops the macro there rather than carrying on with the rest of its keys.
func TestMacroStopsOnAFailedSearch(t *testing.T) {
	e := edit(t, "gamma\nalpha\nbeta\n", "qa/gamma\rxq3@a")
	if got := buf(e); got != "amma\nalpha\nbeta\n" {
		t.Errorf("buffer = %q, want the macro to have stopped after one x", got)
	}
}

// TestVisualRepeat is :help visual-repeat: the dot after an operator on a
// selection does the same operator to the same AMOUNT of text at the cursor,
// and not the keys that made the selection. Measured: vjd on alpha, beta,
// gamma, delta leaves "eta" and the dot then takes "eta" and the "g" below it.
func TestVisualRepeat(t *testing.T) {
	cases := []struct{ name, in, keys, want string }{
		{"charwise over two lines", "alpha\nbeta\ngamma\ndelta\n", "vjd.", "amma\ndelta\n"},
		{"linewise", "a\nb\nc\nd\ne\n", "Vjd.", "e\n"},
		{"on one line, the same number of characters", "abcdefgh\n", "vld.", "efgh\n"},
		{"a visual change", "one\ntwo\n", "vlcXY\x1bj0.", "XYe\nXYo\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buf(edit(t, tc.in, tc.keys)); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestVisualDollarTakesTheLineBreak: a selection made with $ runs to the end
// of the line and takes the break with it, so v$d joins the next line up.
// Measured, including the trailing newline in the register.
func TestVisualDollarTakesTheLineBreak(t *testing.T) {
	e := edit(t, "hello\nsecond\nthird\n", "lv$d")
	if got := buf(e); got != "hsecond\nthird\n" {
		t.Errorf("buffer = %q, want %q", got, "hsecond\nthird\n")
	}
}

// TestChangeWordActsLikeChangeToEnd is :help cw: with the cursor on a
// non-blank, cw does not take the white space after the word.
func TestChangeWordActsLikeChangeToEnd(t *testing.T) {
	if got := buf(edit(t, "hello world again\n", "llcwXX\x1b")); got != "heXX world again\n" {
		t.Errorf("cw from inside a word gave %q", got)
	}
	// On a blank it is still w: the change reaches the start of the next word.
	if got := buf(edit(t, "a   bcd\n", "lcwX\x1b")); got != "aXbcd\n" {
		t.Errorf("cw on white space gave %q", got)
	}
}
