package mode

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/clip"
	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// Cases measured against /opt/homebrew/bin/vim 9.2.0321 on this machine, each
// one run through cmd/oracle under both option profiles before it was written
// down. They are here rather than in testdata/keys because they were found by
// reading this package and each one names the rule it is about; the oracle
// runs the same shapes end to end.
//
// Every "want" below is what vim did, not what :help says, and where the two
// disagree the comment says so.

// cursor is the cursor as a 1-based line and a 0-based column, which is how
// text.Pos already spells it.
func cursor(e *Editor) text.Pos { return e.Cursor() }

// regOf is a register's type letter and its text, the two things the oracle's
// state dump compares.
func regOf(t *testing.T, e *Editor, name byte) (string, string) {
	t.Helper()
	v, err := e.Registers().Get(name)
	if err != nil {
		t.Fatalf("reading register %q: %v", name, err)
	}
	return v.RegType(), string(v.Bytes())
}

// TestBackspaceMotionWithOperatorJoins is d<BS> and c<BS> at the start of a
// line, where 'whichwrap' contains "b" and the motion crosses the boundary.
//
// Two things have to be right at once and either alone leaves the span empty:
// the motion has to be told which operator is waiting, because it puts its end
// AFTER the previous line's last byte only for d and c, and the operator has
// to skip the :help exclusive adjustment that would move that end straight
// back. Measured: "abc\ndef" with jd<BS> gives "abcdef" and a charwise unnamed
// register holding one newline.
func TestBackspaceMotionWithOperatorJoins(t *testing.T) {
	cases := []struct {
		name, in, keys, want string
		line, col            int
	}{
		{"delete joins the two lines", "abc\ndef\n", "jd\x08", "abcdef\n", 1, 3},
		{"change joins and then types", "abc\ndef\n", "jc\x08X\x1b", "abcXdef\n", 1, 3},
		{"a blank line above still joins", "   \ndef\n", "jd\x08", "   def\n", 1, 3},
		{"an empty line above goes linewise", "\ndef\n", "jd\x08", "def\n", 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := edit(t, tc.in, tc.keys)
			if got := buf(e); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
			if got := cursor(e); got.Line != tc.line || got.Col != tc.col {
				t.Errorf("cursor = %v, want line %d col %d", got, tc.line, tc.col)
			}
		})
	}

	// The register the delete left behind, which is the other half of the
	// evidence that the separator went with it.
	e := edit(t, "abc\ndef\n", "jd\x08")
	if typ, txt := regOf(t, e, register.Unnamed); typ != "v" || txt != "\n" {
		t.Errorf(`unnamed register = %q %q, want "v" and one newline`, typ, txt)
	}
	// An empty line above makes the exclusive region start in the indent, and
	// the indent rule then makes the whole thing linewise.
	e = edit(t, "\ndef\n", "jd\x08")
	if typ, _ := regOf(t, e, register.Unnamed); typ != "V" {
		t.Errorf("with an empty line above, register type = %q, want V", typ)
	}
}

// TestReadOnlyRegisterBeeps: a yank or a delete into one of the registers vim
// fills itself beeps and does nothing at all. It used to travel up as an error
// and take the whole script with it, which is worse than a wrong answer: the
// oracle saw no artifacts rather than a diff.
//
// Measured on AAA/BBB/CCC/DDD: "%dd, "~dd, "/dd, ".dd, ":dd and "/yy all leave
// the buffer alone and undotree().seq_cur at 0, and the keystroke after them
// still runs.
func TestReadOnlyRegisterBeeps(t *testing.T) {
	const in = "AAA\nBBB\nCCC\nDDD\n"
	for _, keys := range []string{`"%dd`, `"~dd`, `"/dd`, `".dd`, `":dd`, `"/yy`, `"%x`, `"%D`} {
		t.Run(keys, func(t *testing.T) {
			e := edit(t, in, keys)
			if got := buf(e); got != in {
				t.Errorf("buffer = %q, want it untouched", got)
			}
			if typ, _ := regOf(t, e, register.Unnamed); typ != "" {
				t.Errorf("the unnamed register was written: type %q", typ)
			}
			if len(e.Redirect()) != 0 {
				t.Errorf("said %q, want nothing", e.Redirect())
			}
		})
	}
	// And the next key still runs, which is what "beeps and carries on" means.
	if got := buf(edit(t, in, `"%ddx`)); got != "AA\nBBB\nCCC\nDDD\n" {
		t.Errorf(`"%%ddx gave %q, want the x to have run`, got)
	}
	// c is the one that carries on into insert mode without deleting: op_change
	// calls op_delete, gets the same refusal and starts edit() anyway.
	if got := buf(edit(t, "aaa bbb\nccc\n", "\"%ccZ\x1b")); got != "Zaaa bbb\nccc\n" {
		t.Errorf(`"%%ccZ gave %q, want "Zaaa bbb\nccc\n"`, got)
	}
	// An operator that does not touch a register ignores the name entirely.
	if got := buf(edit(t, "aaa\nbbb\n", "\"%J")); got != "aaa bbb\n" {
		t.Errorf(`"%%J gave %q, want the join to have happened`, got)
	}
}

// TestLinewiseYankKeepsTheColumn: a linewise yank leaves the cursor on the
// first line it took, in the column the operator was typed in.
//
// From a visual selection the rule is different and was measured over thirteen
// shapes: column zero unless the selection covers more than one line and the
// cursor is on the first of them.
func TestLinewiseYankKeepsTheColumn(t *testing.T) {
	const in = "abcdefgh\nijklmnop\nqrstuvwx\n"
	cases := []struct {
		keys string
		col  int
	}{
		{"lyy", 1},
		{"lY", 1},
		{"l2yy", 1},
		{"lyj", 1},
		{"jlyk", 1},
		// Visual: forward or single-line selections lose the column.
		{"llVy", 0},
		{"llVhy", 0},
		{"lVjy", 0},
		{"llVjy", 0},
		// Backward over more than one line keeps it.
		{"jllVky", 2},
		{"jllVkhy", 1},
		{"llVjoy", 2},
		// And the linewise shorthand Y follows the same rule.
		{"jllvkY", 2},
		{"llVjY", 0},
	}
	for _, tc := range cases {
		t.Run(tc.keys, func(t *testing.T) {
			e := edit(t, in, tc.keys)
			if got := cursor(e); got.Line != 1 || got.Col != tc.col {
				t.Errorf("cursor = %v, want line 1 col %d", got, tc.col)
			}
		})
	}
}

// TestRecordingAppendKeepsTheOldType is q into an uppercase register.
//
// It is a third append rule and neither of the two internal/register has: the
// OLD register's type and width survive and the recording always merges onto
// its last line. Measured: "ayy then qAxq leaves "a linewise holding "AAAx",
// where the :let @A rule would give charwise "AAA"/"x".
func TestRecordingAppendKeepsTheOldType(t *testing.T) {
	e := edit(t, "AAA\nBBB\n", `"ayyqAxq`)
	if typ, txt := regOf(t, e, 'a'); typ != "V" || txt != "AAAx\n" {
		t.Errorf(`"a = %q %q, want "V" and "AAAx\n"`, typ, txt)
	}

	// A blockwise register keeps its width too.
	e = edit(t, "AAA\nBBB\n", "\x16j\"ayqAxq")
	if typ, txt := regOf(t, e, 'a'); typ != "\x161" || txt != "A\nBx" {
		t.Errorf(`"a = %q %q, want CTRL-V 1 and "A\nBx"`, typ, txt)
	}

	// An uppercase name whose register is empty is a plain write.
	e = edit(t, "AAA\n", "qAxq")
	if typ, txt := regOf(t, e, 'a'); typ != "v" || txt != "x" {
		t.Errorf(`"a = %q %q, want "v" and "x"`, typ, txt)
	}
}

// TestRedoRegisterIncrementsOnEveryRepeat is :help redo-register: each . after
// a delete into a numbered register uses the next one along.
//
// Measured on AAA/BBB/CCC/DDD: "1dd.. leaves 1=CCC 2=BBB 3=AAA 4=CCC, so the
// three deletes went to "1, "2 and "3. Incrementing a copy of the record gets
// the first repeat right and sends the second one to "2 again, which "1dd. on
// its own cannot see.
func TestRedoRegisterIncrementsOnEveryRepeat(t *testing.T) {
	e := edit(t, "AAA\nBBB\nCCC\nDDD\n", `"1dd..`)
	want := map[byte]string{'1': "CCC\n", '2': "BBB\n", '3': "AAA\n", '4': "CCC\n"}
	for name, w := range want {
		if _, txt := regOf(t, e, name); txt != w {
			t.Errorf(`"%c = %q, want %q`, name, txt, w)
		}
	}
}

// TestUnknownRegisterNameIsSilent: a name vim does not know beeps and says
// nothing. E354 is what :let @! and setreg('!') print; nv_regname calls
// clearopbeep(), which has no message at all. Measured: "!dd leaves vim's
// redirect holding one bare newline, and the dd that follows the beep yanks
// into the unnamed register.
func TestUnknownRegisterNameIsSilent(t *testing.T) {
	for _, keys := range []string{`"!`, `"]`} {
		e := edit(t, "one\ntwo\n", keys)
		if e.pend.reg != 0 {
			t.Errorf("%s set the register to %q", keys, e.pend.reg)
		}
		if e.Message() != "" {
			t.Errorf("%s said %q, want silence", keys, e.Message())
		}
		if len(e.Redirect()) != 0 {
			t.Errorf("%s wrote %q to the redirect, want nothing", keys, e.Redirect())
		}
	}
	// The command after the beep is an ordinary one.
	e := edit(t, "one\ntwo\n", `"!dd`)
	if got := buf(e); got != "two\n" {
		t.Errorf(`"!dd gave %q, want the dd to have run`, got)
	}
}

// TestPutFromATypedButEmptyRegister: three registers report themselves as
// charwise and hold nothing, and a put from each of them is its own answer.
//
// Value.Empty() is a test on the line count and a one-empty-line value is not
// empty by it, so the guard that was meant to catch these never fired.
// Measured, all three on a fresh buffer: "_p says nothing, changes nothing and
// still leaves undotree().seq_cur at 1 because do_put saved undo first; "/p
// prints E35 and leaves it at 0, because get_spec_reg answers before the save;
// ".p prints E29 and leaves it at 0 too.
func TestPutFromATypedButEmptyRegister(t *testing.T) {
	const in = "AAA\nBBB\n"

	e := edit(t, in, `yy"_p`)
	if got := buf(e); got != in {
		t.Errorf(`"_p changed the buffer: %q`, got)
	}
	if e.Message() != "" {
		t.Errorf(`"_p said %q, want nothing`, e.Message())
	}
	if got := len(e.Buffer().ChangeList()); got != 0 {
		t.Errorf(`"_p left %d changelist entries, want 0`, got)
	}

	e = edit(t, in, `"/p`)
	if got := buf(e); got != in {
		t.Errorf(`"/p changed the buffer: %q`, got)
	}
	if got := e.Message(); got != "E35: No previous regular expression" {
		t.Errorf(`"/p said %q, want E35`, got)
	}
	if got := len(e.Buffer().ChangeList()); got != 0 {
		t.Errorf(`"/p left %d changelist entries, want 0`, got)
	}

	e = edit(t, in, `".p`)
	if got := buf(e); got != in {
		t.Errorf(`".p changed the buffer: %q`, got)
	}
	if got := e.Message(); got != "E29: No inserted text yet" {
		t.Errorf(`".p said %q, want E29`, got)
	}
	if e.Mode() != Normal {
		t.Errorf(`".p left the editor in %v, want normal mode`, e.Mode())
	}

	// Once something has been inserted, ".p is the put it always was.
	e = edit(t, in, "ixy\x1bj\".p")
	if got := buf(e); got != "xyAAA\nBBxyB\n" {
		t.Errorf(`".p after an insert gave %q`, got)
	}
}

// TestVisualLinewiseShorthandRepeats: D, X, C, S and R on a selection record
// the selection as the change . repeats, and they record it linewise whatever
// the selection was. Without it the repeat replays the bare letter as a
// normal-mode command, which is a different command on one line.
//
// Measured on one..seven: VjD3G. deletes lines five and six, and vjX3G. does
// the same, because vim records vjX as V over two whole lines.
func TestVisualLinewiseShorthandRepeats(t *testing.T) {
	const in = "one\ntwo\nthree\nfour\nfive\nsix\nseven\n"
	cases := []struct{ name, keys, want string }{
		{"D", "VjD3G.", "three\nfour\nseven\n"},
		{"X on a charwise selection", "vjX3G.", "three\nfour\nseven\n"},
		{"C", "VjCZZ\x1b3G.", "ZZ\nthree\nZZ\nsix\nseven\n"},
		{"S on a charwise selection", "vjSWW\x1b3G.", "WW\nthree\nWW\nsix\nseven\n"},
		{"R", "vjRQQ\x1b3G.", "QQ\nthree\nQQ\nsix\nseven\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buf(edit(t, in, tc.keys)); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBlockInsertRepeats: . after a blockwise I or A makes the same block at
// the cursor and types the same text down it. It used to replay the bare I or
// A as a normal-mode command on the cursor line and lose the block's height.
func TestBlockInsertRepeats(t *testing.T) {
	const in = "aaaa\nbbbb\ncccc\ndddd\n"
	if got := buf(edit(t, in, "\x16jIX\x1b3G.")); got != "Xaaaa\nXbbbb\nXcccc\nXdddd\n" {
		t.Errorf("CTRL-V j I X then . gave %q", got)
	}
	if got := buf(edit(t, in, "\x16jAX\x1b3G.")); got != "aXaaa\nbXbbb\ncXccc\ndXddd\n" {
		t.Errorf("CTRL-V j A X then . gave %q", got)
	}
}

// TestBlockChangeSpreadsDown: the text typed into a blockwise change goes into
// every line of the block, and a $ block takes it at each line's own end.
//
// Two things were wrong. The change did not carry the block's ToEOL, and the
// short-line skip was off by one: vim's block_prep sets is_short when the line
// ends BEFORE the block's column and not when it ends on it, which after a
// block delete is every lower line.
func TestBlockChangeSpreadsDown(t *testing.T) {
	cases := []struct{ name, in, keys, want string }{
		{"a short line still takes the text", "aaaa\nbb\ncccc\ndddd\n", "l\x16jjjcXY\x1b",
			"aXYaa\nbXY\ncXYcc\ndXYdd\n"},
		{"a $ block takes it at every end", "aaaa\nbb\ncccc\n", "l\x16jj$cZ\x1b",
			"aZ\nbZ\ncZ\n"},
		{"I on a line that ends on the column", "aaaa\nb\ncccc\n", "l\x16jjIX\x1b",
			"aXaaa\nbX\ncXccc\n"},
		{"I skips a line that ends before it", "aaaa\n\ncccc\n", "l\x16jjIX\x1b",
			"aXaaa\n\ncXccc\n"},
		{"I two columns in", "aaaa\nbb\ncccc\n", "ll\x16jjIX\x1b",
			"aaXaa\nbbX\nccXcc\n"},
		{"I past the end of a short line", "aaaa\nbb\ncccc\n", "lll\x16jjIX\x1b",
			"aaaXa\nbb\ncccXc\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buf(edit(t, tc.in, tc.keys)); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestForcedMotionOnATextObject: the v, V or CTRL-V typed between an operator
// and a text object is not dropped. vim's nv_object leaves the same oap a
// motion would and do_pending_operator applies motion_force to it, adjustments
// and all -- so dvip on a paragraph deletes the first line only and comes back
// linewise, because the charwise force made it exclusive and the
// exclusive-in-the-indent rule made it whole lines again.
func TestForcedMotionOnATextObject(t *testing.T) {
	cases := []struct {
		name, in, keys, want, regType, regText string
	}{
		{"V forces a word object linewise", "alpha beta\ngamma\n", "dViw",
			"gamma\n", "V", "alpha beta\n"},
		{"v forces a paragraph charwise and back", "aa\nbb\n\ncc\n", "dvip",
			"bb\n\ncc\n", "V", "aa\n"},
		{"v toggles an inclusive object to exclusive", "alpha beta\ngamma\n", "dviw",
			"a beta\ngamma\n", "v", "alph"},
		{"and the same for aw", "alpha beta\ngamma\n", "dvaw",
			" beta\ngamma\n", "v", "alpha"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := edit(t, tc.in, tc.keys)
			if got := buf(e); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
			typ, txt := regOf(t, e, register.Unnamed)
			if typ != tc.regType || txt != tc.regText {
				t.Errorf("register = %q %q, want %q %q", typ, txt, tc.regType, tc.regText)
			}
		})
	}
}

// TestForcedMotionOnADoubledOperator is dvd, dVd and d CTRL-V d.
//
// vim's nv_lineop makes the motion linewise and do_pending_operator applies
// motion_force to it like any other, so dvd is an empty charwise region that
// deletes nothing and leaves the unnamed register alone, where a doubled
// operator that ignores the force takes a whole line.
func TestForcedMotionOnADoubledOperator(t *testing.T) {
	const in = "l1\nl2\nl3\nl4\n"
	cases := []struct{ name, keys, want, regType string }{
		{"v makes it an empty region", "dvd", in, ""},
		{"V is the linewise it already was", "dVd", "l2\nl3\nl4\n", "V"},
		{"CTRL-V is one character wide", "d\x16d", "1\nl2\nl3\nl4\n", "\x161"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := edit(t, in, tc.keys)
			if got := buf(e); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
			if typ, _ := regOf(t, e, register.Unnamed); typ != tc.regType {
				t.Errorf("register type = %q, want %q", typ, tc.regType)
			}
		})
	}
}

// TestUndoClampsToTheLastCharacter: an undo header remembers where the change
// started, which is an insert-mode column, and normal mode may not sit past
// the last byte. Measured: u after Axy<Esc> leaves vim's cursor in column 3 of
// "zzz" and not in the column 4 that does not exist.
func TestUndoClampsToTheLastCharacter(t *testing.T) {
	cases := []struct {
		keys string
		col  int
	}{
		{"Axy\x1bu", 2},
		{"A1\x1bA2\x1bu", 3},
		{"Afoo bar\x15X\x1bu", 9},
		{"Afoo bar\x15X\x1bu\x12", 3},
		{"Axy\x1bAz\x1bg-g-", 2},
	}
	for _, tc := range cases {
		t.Run(tc.keys, func(t *testing.T) {
			e := edit(t, "zzz\n", tc.keys)
			if got := cursor(e); got.Col != tc.col {
				t.Errorf("cursor = %v, want column %d", got, tc.col)
			}
		})
	}
}

// TestCtrlUInColumnZeroJoins: vim's ins_bs() asks about the column before it
// asks which backspace this is, so CTRL-U at the start of a line deletes the
// line break like every other one. It used to do nothing at all.
//
// The three-CTRL-U case is the one that pins where the insert's starting point
// goes: it stops at the column A began at, so "zzz" survives.
func TestCtrlUInColumnZeroJoins(t *testing.T) {
	cases := []struct {
		name, in, keys, want string
		line, col            int
	}{
		{"the second CTRL-U joins", "zzz\n", "A abc\rdef\x15\x15X\x1b", "zzz abcX\n", 1, 7},
		{"the third stops at the insert's start", "zzz\n", "A abc\rdef\x15\x15\x15X\x1b", "zzzX\n", 1, 3},
		{"CTRL-U in column zero of line two", "zzz\nyyy\n", "jI\x15X\x1b", "zzzXyyy\n", 1, 3},
		{"and again on the joined line", "zzz\nyyy\n", "jI\x15\x15X\x1b", "Xyyy\n", 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := edit(t, tc.in, tc.keys)
			if got := buf(e); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
			if got := cursor(e); got.Line != tc.line || got.Col != tc.col {
				t.Errorf("cursor = %v, want line %d col %d", got, tc.line, tc.col)
			}
		})
	}
	// 'backspace' without "eol" refuses the crossing, as it does for BS.
	e := editOpt(t, "zzz\n", "A abc\rdef\x15\x15X\x1b", func(o *Options) { o.Backspace = "indent,start" })
	if got := buf(e); got != "zzz abc\nX\n" {
		t.Errorf("without eol in 'backspace', CTRL-U gave %q", got)
	}
}

// TestCtrlUStopsAtTheIndentUnderAutoindent: vim computes CTRL-U's mincol from
// "curbuf->b_p_ai || cindent_on()" and never asks 'backspace' about it. The
// two were the other way round here, so both of these were wrong and in
// opposite directions.
func TestCtrlUStopsAtTheIndentUnderAutoindent(t *testing.T) {
	const in = "    foobar\nsecond\n"
	cases := []struct {
		name string
		set  func(*Options)
		want string
		col  int
	}{
		{"autoindent keeps the indent", func(o *Options) { o.AutoIndent = true }, "    X\nsecond\n", 4},
		{"noautoindent takes it, whatever 'backspace' says",
			func(o *Options) { o.AutoIndent = false; o.Backspace = "eol,start" }, "X\nsecond\n", 0},
		{"autoindent keeps it, whatever 'backspace' says",
			func(o *Options) { o.AutoIndent = true; o.Backspace = "eol,start" }, "    X\nsecond\n", 4},
		{"without start in 'backspace' nothing goes",
			func(o *Options) { o.AutoIndent = true; o.Backspace = "eol" }, "    foobarX\nsecond\n", 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := editOpt(t, in, "A\x15X\x1b", tc.set)
			if got := buf(e); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
			if got := cursor(e); got.Col != tc.col {
				t.Errorf("cursor = %v, want column %d", got, tc.col)
			}
		})
	}
	// An autoindent nothing was typed on top of is not an indent to stop at:
	// the whole of it goes.
	e := editOpt(t, "    foo\nbar\n", "o\x15X\x1b", func(o *Options) { o.AutoIndent = true })
	if got := buf(e); got != "    foo\nX\nbar\n" {
		t.Errorf("o then CTRL-U under autoindent gave %q", got)
	}
}

// TestVisualReplaceCursorAndReport: op_replace zeroes the start column of a
// linewise region before it puts the cursor back, and hands changed_lines()
// the block's own left column where a line-oriented command hands it zero.
func TestVisualReplaceCursorAndReport(t *testing.T) {
	e := edit(t, "aaaa\nbbbb\n", "lVjrz")
	if got := cursor(e); got.Line != 1 || got.Col != 0 {
		t.Errorf("lVjrz left the cursor at %v, want line 1 column 0", got)
	}
	if list := e.Buffer().ChangeList(); len(list) != 1 || list[0].Line != 1 {
		t.Errorf("lVjrz reported the change at %v, want line 1", list)
	}
	// Indent makes no difference: it is column one and not the first non-blank.
	e = edit(t, "    aaaa\nbbbb\n", "wVjrz")
	if got := cursor(e); got.Col != 0 {
		t.Errorf("wVjrz left the cursor in column %d, want 0", got.Col)
	}

	e = edit(t, "aaaa\nbbbb\ncccc\n", "l\x16jjlrz")
	if list := e.Buffer().ChangeList(); len(list) != 1 || list[0].Line != 1 || list[0].Col != 1 {
		t.Errorf("a blockwise r reported %v, want line 1 column 1", list)
	}
}

// TestKeptMessageIsRedisplayedInAMacro: main_loop prints keep_msg again after
// every command and guards the whole block with stuff_empty(). A macro replay
// is the typeahead and not the stuff buffer, so it does not suppress it; a dot
// repeat is the stuff buffer and does. What a macro suppresses is showmode,
// which Editor.atMacro already handles.
//
// Measured on twelve lines: qq3ddq2@q leaves "3 fewer lines" twice per
// replayed iteration in vim's redirect and once here.
func TestKeptMessageIsRedisplayedInAMacro(t *testing.T) {
	e := edit(t, strings.Repeat("x\n", 12), "qq3ddq2@q")
	if got := strings.Count(string(e.Redirect()), "3 fewer lines"); got != 6 {
		t.Errorf("the redirect holds %d copies of \"3 fewer lines\", want 6:\n%q",
			got, e.Redirect())
	}
	// A dot repeat is the stuff buffer and still suppresses the redisplay,
	// which is the case the flag was there for: 3dd. leaves four copies and
	// not six.
	e = edit(t, strings.Repeat("x\n", 12), "3dd.")
	if got := strings.Count(string(e.Redirect()), "3 fewer lines"); got != 4 {
		t.Errorf("after 3dd. the redirect holds %d copies, want 4:\n%q", got, e.Redirect())
	}
}

// TestInsertVisualModeMessage: a visual mode entered from insert mode's CTRL-O
// is a mode of its own and vim says so, with the "(insert)" restart_edit still
// in front of it. Two things kept it quiet here: modeMessage never reached the
// oneShot branch for a visual mode, and commandDone refused the redraw that
// would have printed it.
func TestInsertVisualModeMessage(t *testing.T) {
	cases := []struct{ keys, want string }{
		{"i\x0fvlly\x1b", "-- INSERT ---- (insert) ---- (insert) VISUAL ---- INSERT --\n"},
		{"i\x0fVy\x1b", "-- INSERT ---- (insert) ---- (insert) VISUAL LINE ---- INSERT --\n"},
		{"i\x0f\x16lly\x1b", "-- INSERT ---- (insert) ---- (insert) VISUAL BLOCK ---- INSERT --\n"},
	}
	for _, tc := range cases {
		t.Run(tc.keys, func(t *testing.T) {
			e := edit(t, "alpha beta\n", tc.keys)
			if got := string(e.Redirect()); got != tc.want {
				t.Errorf("redirect = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCompletionMessages is the CTRL-N message stream, which is most of what
// keyword completion is on a headless run and which nothing measured before.
//
// The shape, measured a press at a time: the first press shows the submode
// with "-- Searching...", prints "Scanning tags." as an ordinary message
// because 'complete' contains "t", and then shows where it got to; every press
// after that shows the bare submode and then where it got to; and the key that
// ends the completion shows where it got to once more before "-- INSERT --".
func TestCompletionMessages(t *testing.T) {
	const sub = "-- Keyword completion (^N^P)"
	const head = "-- INSERT --" + sub + " -- Searching...\nScanning tags."

	cases := []struct{ name, in, keys, buf, msgs string }{
		{"the only match", "alphabet beta\ngamma\nal\n", "3GA\x0e\x1b",
			"alphabet beta\ngamma\nalphabet\n",
			head + sub + " The only match" + sub + " The only match-- INSERT --\n"},
		{"CTRL-P is the same stream", "alphabet beta\ngamma\nal\n", "3GA\x10\x1b",
			"alphabet beta\ngamma\nalphabet\n",
			head + sub + " The only match" + sub + " The only match-- INSERT --\n"},
		{"two candidates", "alpha alpine\nal\n", "2GA\x0e\x0e\x1b",
			"alpha alpine\nalpine\n",
			head + sub + " match 1 of 2" + sub + sub + " match 2 of 2" + sub + " match 2 of 2-- INSERT --\n"},
		{"cycling back to what was typed", "alphabet beta\ngamma\nal\n", "3GA\x0e\x0e\x1b",
			"alphabet beta\ngamma\nal\n",
			head + sub + " The only match" + sub + sub + " Back at original" + sub + " Back at original-- INSERT --\n"},
		{"no match at all", "alphabet beta\ngamma\nzq\n", "3GA\x0e\x1b",
			"alphabet beta\ngamma\nzq\n",
			head + sub + " Pattern not found" + sub + " Pattern not found-- INSERT --\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := edit(t, tc.in, tc.keys)
			if got := buf(e); got != tc.buf {
				t.Errorf("buffer = %q, want %q", got, tc.buf)
			}
			if got := string(e.Redirect()); got != tc.msgs {
				t.Errorf("redirect =\n%q\nwant\n%q", got, tc.msgs)
			}
		})
	}
}

// TestCompletionReportsTheWholeWord: vim's ins_compl_new_leader takes the word
// being completed out and puts it back whatever it then finds, so a completion
// numbers an undo header and lands in the changelist even when there is no
// match, and the entry names the last byte of the word that is now there.
func TestCompletionReportsTheWholeWord(t *testing.T) {
	cases := []struct {
		name, in, keys string
		line, col      int
	}{
		{"a match reports its last byte", "alphabet beta\ngamma\nal\n", "3GA\x0e\x1b", 3, 7},
		{"no match reports the typed word", "alphabet beta\ngamma\nzq\n", "3GA\x0e\x1b", 3, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := edit(t, tc.in, tc.keys)
			list := e.Buffer().ChangeList()
			if len(list) != 1 || list[0].Line != tc.line || list[0].Col != tc.col {
				t.Errorf("changelist = %v, want one entry at line %d column %d",
					list, tc.line, tc.col)
			}
		})
	}
	// With 'noselect' nothing is inserted and the report is still made.
	e := editOpt(t, "alphabet beta\ngamma\nal\n", "3GA\x0e\x1b",
		func(o *Options) { o.CompleteOpt = "menu,menuone,noselect,noinsert" })
	if got := buf(e); got != "alphabet beta\ngamma\nal\n" {
		t.Errorf("with noinsert the buffer changed: %q", got)
	}
	if list := e.Buffer().ChangeList(); len(list) != 1 || list[0].Col != 1 {
		t.Errorf("with noinsert the changelist = %v, want one entry in column 1", list)
	}
}

// TestOpenLineModeMessage: an insert that moved lines around says the mode
// message a second time, except when the cursor ends on the buffer's last line
// or when O opened its first one. Both exemptions were measured a shape at a
// time over twenty cases; a bare O on line one is one keystroke and it was
// wrong under both option profiles.
func TestOpenLineModeMessage(t *testing.T) {
	const in = "one\ntwo\nthree\n"
	const once = "-- INSERT --\n"
	const twice = "-- INSERT ---- INSERT --\n"
	cases := []struct{ name, in, keys, want string }{
		{"O on the first line", in, "O\x1b", once},
		{"O on the first line and a Tab", in, "O\t\x1b", once},
		{"o on the first line", in, "o\x1b", twice},
		{"O on the second line", in, "jO\x1b", twice},
		{"o on the last line", in, "Go\x1b", once},
		{"O on the last line", in, "GO\x1b", twice},
		{"Enter on the last line", in, "3GA\r\x1b", once},
		{"Enter on the first line", in, "ggi\r\x1b", twice},
		{"O on the first line, then Enter", in, "O\r\x1b", once},
		{"a join back onto line one", in, "jI\x08\x1b", twice},
		{"a join onto the last line", in, "GI\x08\x1b", once},
		{"a one-line file", "only\n", "o\x1b", once},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := edit(t, tc.in, tc.keys)
			if got := string(e.Redirect()); got != tc.want {
				t.Errorf("redirect = %q, want %q", got, tc.want)
			}
		})
	}
}

// editWindow is edit with a window under it, which is the only thing H, M and
// L need and the one thing a headless run does not have.
func editWindow(t *testing.T, in, keys string, w motion.Window) *Editor {
	t.Helper()
	e := New(text.Read([]byte(in)))
	e.SetWindow(w)
	for _, k := range decodeKeys([]byte(keys)) {
		if err := e.Key(k); err != nil {
			t.Fatalf("key %v: %v", k, err)
		}
	}
	return e
}

// fourHundredLines is "line001" through "line400", the buffer the jump mark
// cases below were measured on.
func fourHundredLines() string {
	var b strings.Builder
	for i := 1; i <= 400; i++ {
		fmt.Fprintf(&b, "line%03d\n", i)
	}
	return b.String()
}

// TestJumpMarkSurvivesACommandThatDidNotMove is vim's checkpcmark(), which
// normal_cmd runs after every command: a jump that left the cursor exactly
// where it started hands the last-jump mark back to whatever set it last, so
// the jump
// that did not move does not consume the mark.
//
// Measured on "line001".."line400" in a 39-row window, 120 columns, the file
// written out afterwards and diffed for the line that lost a byte to x:
//
//	100GM``x line 1, because M was already on line 100
//	100GztH``x line 1, and 100GzbL``x line 1, for the same reason
//	100G100G``x line 1
//	GG``x line 1
//	100GH``x line 100, because H did move
//	100GjM``x line 101
//	100G0%``x line 100 when that line is "(abcdefg)": the % moved the
//	 COLUMN and nothing else, which is enough to keep the mark.
//	 EQUAL_POS compares the column too, so a rule written on the
//	 line number alone would get this one wrong.
func TestJumpMarkSurvivesACommandThatDidNotMove(t *testing.T) {
	// Top and Bottom put line 100 in the middle of a 39-row window, which is
	// where "100G" leaves it, so M is the line the cursor is already on and H
	// and L are not.
	win := motion.Window{Top: 81, Bottom: 119, Height: 39}
	lines := fourHundredLines()
	percent := strings.Replace(lines, "line100\n", "(abcdefg)\n", 1)
	cases := []struct {
		name, in, keys string
		// edited is the line x took a byte off.
		edited int
	}{
		{"M did not move", lines, "100GM``x", 1},
		{"H did move", lines, "100GH``x", 100},
		{"M after a j did move", lines, "100GjM``x", 101},
		{"G twice", lines, "100G100G``x", 1},
		{"G to the end twice", lines, "GG``x", 1},
		{"a bare jump keeps the mark", lines, "100G``x", 1},
		{"% moved the column only", percent, "100G0%``x", 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := editWindow(t, tc.in, tc.keys, win)
			want := strings.Split(tc.in, "\n")
			want[tc.edited-1] = want[tc.edited-1][1:]
			if got, w := buf(e), strings.Join(want, "\n"); got != w {
				for i := range want {
					g := strings.Split(got, "\n")
					if i < len(g) && g[i] != want[i] {
						t.Fatalf("line %d = %q, want %q", i+1, g[i], want[i])
					}
				}
				t.Fatalf("buffer differs in length")
			}
		})
	}
}

// TestJumpMarkRestoreIsNotJustHML: the last-jump mark a G gave itself is put
// back the same way, and the apostrophe form reads the restored one. Measured:
//
//	100GM''x takes a byte off line 1 in vim, not off line 100
func TestJumpMarkRestoreIsNotJustHML(t *testing.T) {
	win := motion.Window{Top: 81, Bottom: 119, Height: 39}
	e := editWindow(t, fourHundredLines(), "100GM", win)
	if got, ok := e.Buffer().Mark(text.MarkLastJump); !ok || got != (text.Pos{Line: 1}) {
		t.Fatalf("'' = %v (set %v), want line 1 column 0", got, ok)
	}
	e = editWindow(t, fourHundredLines(), "100GM''x", win)
	if got := strings.SplitN(buf(e), "\n", 2)[0]; got != "ine001" {
		t.Errorf("line 1 = %q, want %q", got, "ine001")
	}
}

// The three buffers the visual-put cases below run on. Named because the same
// three lines appear in a dozen places and a literal in each one is a place for
// a typo to hide.
const (
	threeLines = "alpha beta\nsecond line\nthird one\n"
	fourLines  = "one\ntwo\nthree\nfour\n"
	twoLines   = "hello\nworld\n"
)

// TestVisualPutDirectionAndType is vim's nv_put_opt() deciding, before do_put()
// runs, which way a visual put goes and what type the register goes in as.
//
// Both rules are invisible until a selection reaches an edge. In the middle of
// a buffer the delete leaves the cursor exactly where the selection started and
// the register goes in in front of it; at the end of a line or the end of the
// buffer there is nothing left to go in front of, the cursor has been pushed
// back, and vim's
//
//	if ((VIsual_mode != 'V' && curwin->w_cursor.col < curbuf->b_op_start.col)
//	 || (VIsual_mode == 'V' && curwin->w_cursor.lnum < curbuf->b_op_start.lnum))
//	 dir = FORWARD;
//
// turns the put around. And a linewise selection puts its register as lines
// whatever the register's own type is, which is vim's PUT_LINE flag: without it
// a charwise register put over a whole line is joined onto the line below.
//
// Every want below was taken from /opt/homebrew/bin/vim 9.2.0321 through
// "vim --clean -i NONE --not-a-term -s", with the buffer and the cursor read
// back by getline() and line()/col(). The columns are 0-based here and 1-based
// there.
func TestVisualPutDirectionAndType(t *testing.T) {
	cases := []struct {
		name, in, keys, want string
		line, col            int
	}{
		// The end of the buffer, linewise. The delete of the last lines leaves
		// the cursor on the line above, so the put goes after it, and the
		// buffer comes back to what it was.
		{"a linewise put over the last lines puts them back", threeLines,
			"jVjyVjp", threeLines, 2, 0},
		{"and after the line it was left on, not before it", fourLines,
			"jVjyGkVjp", "one\ntwo\ntwo\nthree\n", 3, 0},
		{"one last line", fourLines, "yyGVp", "one\ntwo\nthree\none\n", 4, 0},
		{"one last line, charwise register", twoLines, "vlyjVp", "hello\nhe\n", 2, 0},

		// The end of a line, charwise. "hello" with the last character selected
		// leaves the cursor on the "l" before it, so "he" goes in after.
		{"a charwise put at the end of a line goes after the cursor", twoLines,
			"vly4lvp", "hellhe\nworld\n", 1, 5},
		{"on a later line too", twoLines, "vlyj4lvp", "hello\nworlhe\n", 2, 5},

		// PUT_LINE: a charwise register over a linewise selection is its own
		// line and is not joined onto the one that follows it.
		{"a charwise register over a whole line becomes a line", threeLines,
			"yljVp", "alpha beta\na\nthird one\n", 2, 0},
		{"the same at the top of the buffer", fourLines,
			"yljVp", "one\no\nthree\nfour\n", 2, 0},

		// The middle of the buffer, which is where the direction is backward
		// and where an implementation that always went backward looked right.
		{"a linewise put in the middle still goes before", fourLines,
			"yyjVp", "one\none\nthree\nfour\n", 2, 0},
		{"on the first line", fourLines, "GyyggVp", "four\ntwo\nthree\nfour\n", 1, 0},
		{"a line put over itself changes nothing", twoLines, "yyVp", twoLines, 1, 0},
		{"nor does the second one", twoLines, "jyyVp", twoLines, 2, 0},
		{"a charwise put in the middle of a line", twoLines,
			"vlyvlp", twoLines, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := edit(t, tc.in, tc.keys)
			if got := buf(e); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
			if got := cursor(e); got.Line != tc.line || got.Col != tc.col {
				t.Errorf("cursor = %v, want line %d col %d", got, tc.line, tc.col)
			}
		})
	}
}

// TestVisualPFillsNoRegister is :help v_P, which is one sentence and one line
// of C: "Unlike v_p, the previously selected text is not put into any
// register." vim aims the delete at the black hole -- nv_put_opt's
// "cap->oap->regname = keep_registers ? '_' : NUL" -- where v_p aims it at the
// default register.
//
// The consequence is the one the help is written for, and it is a buffer
// difference and not only a register one: in vim a second P pastes the same
// text again, and in an editor whose P fills the registers it pastes whatever
// the first P took out. Measured on "alpha beta / second line / third one":
//
//	yljvlP leaves @" holding "a", and @- empty
//	yljvlPjvlP leaves "alpha beta / acond line / aird one"
func TestVisualPFillsNoRegister(t *testing.T) {
	e := edit(t, threeLines, "yljvlP")
	if got := buf(e); got != "alpha beta\nacond line\nthird one\n" {
		t.Errorf("buffer = %q", got)
	}
	if typ, txt := regOf(t, e, register.Unnamed); typ != "v" || txt != "a" {
		t.Errorf(`@" = %q %q, want "v" and the "a" that yl yanked`, typ, txt)
	}
	if _, txt := regOf(t, e, register.SmallDelete); txt != "" {
		t.Errorf(`@- = %q, want empty: v_P deletes into no register at all`, txt)
	}
	if _, txt := regOf(t, e, '1'); txt != "" {
		t.Errorf(`@1 = %q, want empty`, txt)
	}

	// v_p is the other half of the same rule and has to keep filling them, or
	// this is a fix that broke the thing it was next to.
	e = edit(t, twoLines, "vly4lvp")
	if typ, txt := regOf(t, e, register.Unnamed); typ != "v" || txt != "o" {
		t.Errorf(`after v_p, @" = %q %q, want the deleted "o"`, typ, txt)
	}
	if typ, txt := regOf(t, e, register.SmallDelete); typ != "v" || txt != "o" {
		t.Errorf(`after v_p, @- = %q %q, want the deleted "o"`, typ, txt)
	}

	// And the buffer difference, which is what makes this worth a test rather
	// than a comment: the second P pastes the same "a" again.
	e = edit(t, threeLines, "yljvlPjvlP")
	if got, want := buf(e), "alpha beta\nacond line\naird one\n"; got != want {
		t.Errorf("after two P, buffer = %q, want %q", got, want)
	}
	if got := cursor(e); got.Line != 3 || got.Col != 0 {
		t.Errorf("cursor = %v, want line 3 column 0", got)
	}
}

// TestAutoSelectWritesOnAChangedSelection is 'clipboard' containing
// "autoselect" counted rather than read: how many times the editor put the
// selection on the clipboard, which no comparison of the clipboard's contents
// can see because the contents are the same every time.
//
// vim's clip_update_selection() runs at the end of every normal_cmd() and
// compares clip->start, clip->end and clip->vmode before it writes anything, so
// a count digit, a half-typed g, the a of a text object and a command that
// beeped all cost nothing. An editor that writes once per keystroke instead
// pays a main-thread round trip to NSPasteboard, 215us to 240us by
// internal/gui's own benchmark, for each of them.
//
// The wants are vim's, measured on /opt/homebrew/bin/vim 9.2 with +clipboard
// through a pty at 'clipboard'=unnamed,autoselect, by reading
// NSPasteboard.general.changeCount either side of each script and halving the
// delta, since vim spends two changeCount steps per copy.
//
// "vlh" is the case that says this is a comparison and not a suppression: the
// selection is back where "v" left it and it still writes, because it is not
// where the LAST write left it.
func TestAutoSelectWritesOnAChangedSelection(t *testing.T) {
	cases := []struct {
		keys  string
		write int
	}{
		{"v", 1}, {"vl", 2}, {"vll", 3}, {"vlh", 3},
		{"V", 1}, {"Vj", 2}, {"vG", 2}, {"v\x16", 2},

		// The eight that used to write once per keystroke.
		{"v2", 1},      // a count digit selects nothing new
		{"v22", 1},     // nor does a second one
		{"v2l", 2},     // the l it belongs to does
		{"vo", 1},      // o swaps the ends and the selection is the same
		{"vg", 1},      // a half-typed g command
		{"vaw", 2},     // a, then the object that finishes it
		{"v\x18", 1},   // CTRL-X, which does nothing in visual mode
		{"v\"a", 1},    // a register prefix
		{"v10j", 2},    // the vimrc's cost: two digits and one motion
		{"vlllhhh", 7}, // every one of these moves an edge
	}
	for _, tc := range cases {
		t.Run(tc.keys, func(t *testing.T) {
			e := New(text.Read([]byte(threeLines)))
			m := &clip.Memory{}
			e.Registers().SetClipboard(m)
			o := DefaultOptions()
			o.Clipboard = "unnamed,autoselect"
			e.SetOptions(o)
			for _, k := range decodeKeys([]byte(tc.keys)) {
				// A key that beeps is part of the point; only a real error
				// is worth stopping for.
				if err := e.Key(k); err != nil {
					t.Fatalf("key %v: %v", k, err)
				}
			}
			if got := m.Writes(); got != tc.write {
				t.Errorf("clipboard writes = %d, want %d", got, tc.write)
			}
		})
	}

	// Without "autoselect" nothing is written at all, which is the guard that
	// says the comparison above did not become the whole condition.
	e := New(text.Read([]byte(threeLines)))
	m := &clip.Memory{}
	e.Registers().SetClipboard(m)
	o := DefaultOptions()
	o.Clipboard = "unnamed"
	e.SetOptions(o)
	for _, k := range decodeKeys([]byte("vlljj")) {
		if err := e.Key(k); err != nil {
			t.Fatalf("key %v: %v", k, err)
		}
	}
	if got := m.Writes(); got != 0 {
		t.Errorf("without autoselect, clipboard writes = %d, want 0", got)
	}
}
