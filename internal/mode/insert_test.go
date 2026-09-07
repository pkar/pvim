package mode

import (
	"testing"

	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// Every expectation in this file was produced by running the same keystrokes
// through /opt/homebrew/bin/vim 9.2 under a pty:
//
//	vim --clean -i NONE --not-a-term -s KEYS FILE
//
// with the buffer, the cursor and the registers dumped afterwards through
// writefile(). A case whose comment says "measured" is one where the answer
// is not what a reading of :help suggests.

// edit runs a keystroke script and returns the editor. The keys are raw bytes,
// exactly as a keys file holds them, so "\x1b" is Escape and "\x17" is CTRL-W.
func edit(t *testing.T, in, keys string) *Editor {
	t.Helper()
	e := New(text.Read([]byte(in)))
	for _, k := range decodeKeys([]byte(keys)) {
		if err := e.Key(k); err != nil {
			t.Fatalf("key %v: %v", k, err)
		}
	}
	return e
}

// editOpt is edit with the options changed first.
func editOpt(t *testing.T, in, keys string, set func(*Options)) *Editor {
	t.Helper()
	e := New(text.Read([]byte(in)))
	o := DefaultOptions()
	set(&o)
	e.SetOptions(o)
	for _, k := range decodeKeys([]byte(keys)) {
		if err := e.Key(k); err != nil {
			t.Fatalf("key %v: %v", k, err)
		}
	}
	return e
}

func buf(e *Editor) string { return string(e.Buffer().Bytes()) }

func TestInsertBasics(t *testing.T) {
	cases := []struct {
		name, in, keys, want string
		line, col            int
	}{
		{"i inserts before the cursor", "xy\n", "iab\x1b", "abxy\n", 1, 1},
		{"a inserts after it", "xy\n", "aab\x1b", "xaby\n", 1, 2},
		{"A goes to the end", "xy\n", "Aab\x1b", "xyab\n", 1, 3},
		{"I goes to the first non-blank", "  xy\n", "IZ\x1b", "  Zxy\n", 1, 2},
		{"gI goes to column one", "  xy\n", "gIZ\x1b", "Z  xy\n", 1, 0},
		{"o opens below", "x\ny\n", "oz\x1b", "x\nz\ny\n", 2, 0},
		{"O opens above", "x\ny\n", "jOz\x1b", "x\nz\ny\n", 2, 0},
		// Measured: the count repeats the whole inserted text, and for o it
		// opens a line per repeat.
		{"a count repeats the text", "A\n", "3ax\x1b", "Axxx\n", 1, 3},
		{"a count on o opens lines", "X\n", "3ofoo\x1b", "X\nfoo\nfoo\nfoo\n", 4, 2},
		{"Escape steps the cursor left", "\n", "iab\x1b", "ab\n", 1, 1},
		{"backspace joins lines", "ab\ncd\n", "jI\x08Z\x1b", "abZcd\n", 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := edit(t, tc.in, tc.keys)
			if got := buf(e); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
			if got := e.Cursor(); got != (text.Pos{Line: tc.line, Col: tc.col}) {
				t.Errorf("cursor = %+v, want line %d col %d", got, tc.line, tc.col)
			}
			if e.Mode() != Normal {
				t.Errorf("mode = %v, want normal", e.Mode())
			}
		})
	}
}

// TestInsertDeleteBack is CTRL-W and CTRL-U, both measured. The three CTRL-W
// rules are the ones an implementation gets wrong: it takes the trailing white
// space with the word, it stops at a change of character class, and with
// "start" in 'backspace' it crosses the point the insert began at.
func TestInsertDeleteBack(t *testing.T) {
	cases := []struct{ name, in, keys, want string }{
		{"CTRL-W takes one word", "start \n", "Afoo bar\x17X\x1b", "start foo X\n"},
		{"CTRL-W crosses the insert start", "hello world\n", "A\x17X\x1b", "hello X\n"},
		{"CTRL-W takes trailing blanks with it", "\n", "Aabc   \x17X\x1b", "X\n"},
		{"CTRL-W stops at a class change", "\n", "Afoo.bar\x17X\x1b", "foo.X\n"},
		{"CTRL-U takes what was typed", "start \n", "Afoo bar\x15X\x1b", "start X\n"},
		{"CTRL-U with nothing typed takes the line", "hello world\n", "A\x15X\x1b", "X\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buf(edit(t, tc.in, tc.keys)); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestInsertCtrlUBreaksUndo: measured, and the reason CTRL-U and CTRL-W are not
// the same function. u after "Afoo bar CTRL-U X Escape" puts "foo bar" back
// rather than undoing the whole insert, so CTRL-U ends the undo block and
// CTRL-W does not.
func TestInsertCtrlUBreaksUndo(t *testing.T) {
	e := edit(t, "start \n", "Afoo bar\x15X\x1bu")
	if got := buf(e); got != "start foo bar\n" {
		t.Errorf("after u, buffer = %q, want %q", got, "start foo bar\n")
	}
	e = edit(t, "start \n", "Afoo bar\x17X\x1bu")
	if got := buf(e); got != "start \n" {
		t.Errorf("CTRL-W broke the undo block: buffer = %q, want %q", got, "start \n")
	}
}

// TestInsertShift is CTRL-T and CTRL-D. Both round to a multiple of
// 'shiftwidth' whether or not 'shiftround' is set, which is why CTRL-T on a
// two-space indent with sw=4 gives four spaces and not six.
func TestInsertShift(t *testing.T) {
	cases := []struct {
		name, in, keys, want string
		et                   bool
	}{
		{"CTRL-T adds a shiftwidth", "foo\n", "A\x14X\x1b", "    fooX\n", true},
		{"CTRL-T rounds down first", "  foo\n", "A\x14X\x1b", "    fooX\n", true},
		{"CTRL-T twice makes a tab", "foo\n", "A\x14\x14X\x1b", "\tfooX\n", false},
		{"CTRL-D takes one away", "        foo\n", "A\x04X\x1b", "    fooX\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := editOpt(t, tc.in, tc.keys, func(o *Options) {
				o.ShiftWidth, o.ExpandTab, o.TabStop = 4, tc.et, 8
			})
			if got := buf(e); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestInsertAutoIndent: Enter copies the indent, and an indent nothing was
// typed on top of is taken away again on the way out, which is what leaves a
// blank line blank instead of full of spaces.
func TestInsertAutoIndent(t *testing.T) {
	cases := []struct{ name, in, keys, want string }{
		{"Enter carries the indent", "    foo\n", "A\rx\x1b", "    foo\n    x\n"},
		{"an untyped indent is dropped", "    foo\n", "A\r\x1b", "    foo\n\n"},
		{"backspace eats one character of it", "    foo\n", "A\r\x08x\x1b", "    foo\n   x\n"},
		{"o carries it too", "    foo\n", "ox\x1b", "    foo\n    x\n"},
		{"splitting a line carries it", "    foo bar\n", "fo i\rx\x1b", "    fo\n    xo bar\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := editOpt(t, tc.in, tc.keys, func(o *Options) { o.AutoIndent = true })
			if got := buf(e); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
		})
	}
	e := edit(t, "    foo\n", "A\rx\x1b")
	if got := buf(e); got != "    foo\nx\n" {
		t.Errorf("without autoindent, buffer = %q, want %q", got, "    foo\nx\n")
	}
}

// TestInsertLiteral is CTRL-V: a decimal byte, a hexadecimal one, a Unicode
// code point, and any other key as itself.
func TestInsertLiteral(t *testing.T) {
	cases := []struct{ name, keys, want string }{
		{"three decimal digits", "A\x16065\x1b", "zA\n"},
		{"u and four hex digits", "A\x16u00e9\x1b", "zé\n"},
		{"x and two hex digits", "A\x16x41Q\x1b", "zAQ\n"},
		{"a tab as itself", "A\x16\tQ\x1b", "z\tQ\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buf(edit(t, "z\n", tc.keys)); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestInsertCopyKeys is CTRL-A, CTRL-E and CTRL-Y: the last insert, the
// character below and the character above.
func TestInsertCopyKeys(t *testing.T) {
	if got := buf(edit(t, "x\n", "ifoo\x1bA \x01\x1b")); got != "foox foo\n" {
		t.Errorf("CTRL-A: buffer = %q, want %q", got, "foox foo\n")
	}
	if got := buf(edit(t, "ab\nXYZ\n", "i\x05\x05\x1b")); got != "XYab\nXYZ\n" {
		t.Errorf("CTRL-E: buffer = %q, want %q", got, "XYab\nXYZ\n")
	}
	if got := buf(edit(t, "ABC\nxy\n", "jI\x19\x19\x1b")); got != "ABC\nABxy\n" {
		t.Errorf("CTRL-Y: buffer = %q, want %q", got, "ABC\nABxy\n")
	}
}

// TestInsertRegister is CTRL-R x.
func TestInsertRegister(t *testing.T) {
	e := New(text.Read([]byte("x\n")))
	if err := e.Registers().Set('a', register.Char([]byte("PUT"))); err != nil {
		t.Fatalf("setting the register: %v", err)
	}
	for _, k := range decodeKeys([]byte("A-\x12a\x1b")) {
		if err := e.Key(k); err != nil {
			t.Fatalf("key %v: %v", k, err)
		}
	}
	if got := buf(e); got != "x-PUT\n" {
		t.Errorf("buffer = %q, want %q", got, "x-PUT\n")
	}
}

// TestReplaceMode is R and the stack its backspace pops.
//
// The third case is the one that needs the stack: "ab" typed over with XYZQ
// appends Z and Q past the end of the line, so backspacing over them deletes
// them, and the one after that restores the b it covered. Measured.
func TestReplaceMode(t *testing.T) {
	cases := []struct {
		name, in, keys, want string
		col                  int
	}{
		{"overwrites", "abcdef\n", "RXY\x1b", "XYcdef\n", 1},
		{"appends past the end", "ab\n", "RXYZQ\x1b", "XYZQ\n", 3},
		{"backspace restores", "abcdef\n", "RXY\x08\x08\x1b", "abcdef\n", 0},
		{"backspace over appended text deletes it", "ab\n", "RXYZQ\x08\x08\x08\x1b", "Xb\n", 0},
		{"a count repeats", "1234567890\n", "3Rab\x1b", "ababab7890\n", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := edit(t, tc.in, tc.keys)
			if got := buf(e); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
			if got := e.Cursor().Col; got != tc.col {
				t.Errorf("cursor column = %d, want %d", got, tc.col)
			}
		})
	}
}

// TestLastInsertRegister: leaving insert fills ". with what was typed, which
// is what CTRL-A reads and what the oracle's state dump prints.
func TestLastInsertRegister(t *testing.T) {
	e := edit(t, "x\n", "ifoo\x1b")
	v, err := e.Registers().Get(register.LastInsert)
	if err != nil {
		t.Fatalf("reading \".: %v", err)
	}
	if got := string(v.Bytes()); got != "foo" {
		t.Errorf("\". = %q, want %q", got, "foo")
	}
}

// TestCompletion is CTRL-N under the two 'completeopt' settings that matter.
// With the vimrc's noselect and noinsert the first press must put nothing in
// the buffer at all; with vim's own default it inserts the first match.
func TestCompletion(t *testing.T) {
	const in = "hello\nhe\n"

	e := editOpt(t, in, "jA\x0e", func(o *Options) { o.CompleteOpt = "menu,menuone,noselect,noinsert" })
	if got := buf(e); got != in {
		t.Errorf("with noinsert the first CTRL-N changed the buffer: %q", got)
	}
	if _, _, active := e.Completion(); !active {
		t.Error("with noinsert the first CTRL-N did not open the menu")
	}
	if err := e.Key(decodeKeys([]byte("\x0e"))[0]); err != nil {
		t.Fatalf("second CTRL-N: %v", err)
	}
	if got := buf(e); got != "hello\nhello\n" {
		t.Errorf("after the second CTRL-N, buffer = %q, want %q", got, "hello\nhello\n")
	}

	e = editOpt(t, in, "jA\x0e", func(o *Options) { o.CompleteOpt = "menu,preview" })
	if got := buf(e); got != "hello\nhello\n" {
		t.Errorf("with the default completeopt, buffer = %q, want %q", got, "hello\nhello\n")
	}
}

// TestBackspaceKeepsTheInsertStart pins what a backspace does NOT do to the
// point the insert began at, which is where a CTRL-U and a CTRL-W stop.
//
// Measured over "map } gt": "A BS C CTRL-W" leaves "map } ", so the CTRL-W
// walked the whole word even though the BS had taken the cursor back past the
// end of the line the "A" started at, and "A BS C CTRL-U" empties the line for
// the same reason. Moving the start back with the cursor -- which vim's
// ins_bs() tail reads as if it did -- gives "map } g" for both.
func TestBackspaceKeepsTheInsertStart(t *testing.T) {
	e := edit(t, "map } gt\n", "A\x08C\x17")
	if got, want := string(e.Buffer().Line(1)), "map } "; got != want {
		t.Errorf("A BS C CTRL-W left %q, want %q", got, want)
	}
	e = edit(t, "map } gt\n", "A\x08C\x15")
	if got, want := string(e.Buffer().Line(1)), ""; got != want {
		t.Errorf("A BS C CTRL-U left %q, want %q", got, want)
	}
	// And two backspaces that cross it do not move it either: the CTRL-U here
	// stops one character in and leaves the "v".
	e = edit(t, "map } gt\n", "aX\x08\x08v:\x15")
	if got, want := string(e.Buffer().Line(1)), "vap } gt"; got != want {
		t.Errorf("aX BS BS v : CTRL-U left %q, want %q", got, want)
	}
}

// TestFunctionKeyInsertsItsName is vim's insert_special(): a function key
// insert mode has nothing to do with goes in as its own <> name, in two
// pieces, so that the changelist reports the ">" and not the "<".
//
// Measured, "i" then the F4 sequence over "alpha": vim leaves "<F4>alpha"
// with the cursor on the ">" and the changelist on column 3.
func TestFunctionKeyInsertsItsName(t *testing.T) {
	e := edit(t, "alpha\n", "i\x1bOS\x1b")
	if got, want := string(e.Buffer().Line(1)), "<F4>alpha"; got != want {
		t.Errorf("line is %q, want %q", got, want)
	}
	if got, want := e.Cursor(), (text.Pos{Line: 1, Col: 3}); got != want {
		t.Errorf("cursor at %v, want %v", got, want)
	}
	// The keypad, which decodes to the character it prints and goes in as an
	// ordinary one: "\x1bOs" is keypad 3.
	e = edit(t, "alpha\n", "i\x1bOs\x1b")
	if got, want := string(e.Buffer().Line(1)), "3alpha"; got != want {
		t.Errorf("line is %q, want %q", got, want)
	}
}
