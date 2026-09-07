package mode

import (
	"testing"

	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/operator"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// The half-typed command. These tests stop in the middle of one and look at
// the state, because that is where the count, the register and the forced
// motion live and because a test that only checks the finished edit cannot
// tell 2d3w deleting six words from 3dw deleting three.

// half feeds keys and leaves the command unfinished.
func half(t *testing.T, in, keys string) *Editor {
	t.Helper()
	return edit(t, in, keys)
}

// TestCountsMultiply is the classic bug this package exists to avoid: the
// count before an operator and the count after it are multiplied, so 2d3w is
// six words. Measured against vim, which deletes six.
func TestCountsMultiply(t *testing.T) {
	cases := []struct {
		keys           string
		count1, count2 int
		want           int
	}{
		{"3d", 3, 0, 3},
		{"d3", 0, 3, 3},
		{"2d3", 2, 3, 6},
		{"12d", 12, 0, 12},
		{"d10", 0, 10, 10},
	}
	for _, tc := range cases {
		t.Run(tc.keys, func(t *testing.T) {
			e := half(t, "a b c d e f g h\n", tc.keys)
			if e.pend.count1 != tc.count1 || e.pend.count2 != tc.count2 {
				t.Errorf("counts = %d and %d, want %d and %d",
					e.pend.count1, e.pend.count2, tc.count1, tc.count2)
			}
			if got := e.pend.count(); got != tc.want {
				t.Errorf("count() = %d, want %d", got, tc.want)
			}
			if e.pend.op != operator.OpDelete {
				t.Errorf("op = %v, want d", e.pend.op)
			}
			if e.Mode() != OperatorPending {
				t.Errorf("mode = %v, want operator-pending", e.Mode())
			}
		})
	}
}

// TestZeroIsAMotionUntilACountIsTyped: 0 with no count in front of it is the
// motion to the first column, and 0 after a 1 is a digit. d10| and d0 are two
// different commands and the difference is only this rule.
func TestZeroIsAMotionUntilACountIsTyped(t *testing.T) {
	e := half(t, "hello\n", "d1")
	if e.pend.count2 != 1 {
		t.Fatalf("count2 = %d, want 1", e.pend.count2)
	}
	if e := half(t, "hello\n", "3d1"); e.pend.count() != 3 {
		t.Errorf("3d1 count = %d, want 3 so far", e.pend.count())
	}
	e = half(t, "hello\n", "0")
	if e.pend.count1 != 0 {
		t.Errorf("a bare 0 was taken as a count")
	}
}

// TestRegisterPrefix: "x can be typed before the count or after it, and never
// after the operator. Measured: d"aw is not a delete into a, it is an
// abandoned d followed by an append of "w".
func TestRegisterPrefix(t *testing.T) {
	for _, keys := range []string{`"a2d3`, `2"ad3`} {
		e := half(t, "a b c d e f g h\n", keys)
		if e.pend.reg != 'a' {
			t.Errorf("%s: register = %q, want a", keys, e.pend.reg)
		}
		if got := e.pend.count(); got != 6 {
			t.Errorf("%s: count = %d, want 6", keys, got)
		}
	}
	// Measured: d" is not a delete into a register, it is a d that beeps
	// because " is not a motion. The a after it is an ordinary append, which
	// is why vim turns d"aw on "aa bb cc" into "awa bb cc".
	e := half(t, "aa bb cc\n", `d"`)
	if e.Mode() != Normal || e.pend.op != operator.OpNone || !e.aborted {
		t.Errorf(`d" left the editor in %v with op %v, want the command abandoned`, e.Mode(), e.pend.op)
	}
	if got := buf(edit(t, "aa bb cc\n", "d\"aw\x1b")); got != "awa bb cc\n" {
		t.Errorf(`d"aw gave %q, want %q`, got, "awa bb cc\n")
	}
}

// TestForcedMotion: the v, V and CTRL-V typed between an operator and its
// motion override the motion's own kind, which is what dvj is for.
func TestForcedMotion(t *testing.T) {
	for _, tc := range []struct {
		keys string
		want motion.Force
	}{
		{"dv", motion.ForceChar},
		{"dV", motion.ForceLine},
		{"d\x16", motion.ForceBlock},
	} {
		e := half(t, "one\ntwo\n", tc.keys)
		if e.pend.force != tc.want {
			t.Errorf("%q: force = %v, want %v", tc.keys, e.pend.force, tc.want)
		}
		if e.Mode() != OperatorPending {
			t.Errorf("%q: mode = %v, want operator-pending", tc.keys, e.Mode())
		}
	}
}

// TestEscapeAbandons: Escape throws away a half-typed command and leaves
// visual mode, and does nothing at all in plain normal mode.
func TestEscapeAbandons(t *testing.T) {
	e := half(t, "one\ntwo\n", "2\"ad3\x1b")
	if e.Mode() != Normal || e.pend.op != operator.OpNone || e.pend.count() != 0 || e.pend.reg != 0 {
		t.Errorf("Escape left %v behind: %+v", e.Mode(), e.pend)
	}
	e = half(t, "one\ntwo\n", "v\x1b")
	if e.Mode() != Normal {
		t.Errorf("Escape in visual mode left %v", e.Mode())
	}
}

// TestPrefixWaits: g on its own is not a command and not a mistake, so the
// editor waits rather than beeping.
func TestPrefixWaits(t *testing.T) {
	e := half(t, "one\n", "g")
	if e.aborted {
		t.Error("g on its own beeped; it is the prefix of gg, gu and gJ")
	}
	e = half(t, "one\n", "gy")
	if !e.aborted {
		t.Error("gy is not a command and should have beeped")
	}
}

// TestShowcmd renders what has been typed, which is what the bottom right of
// the screen shows.
func TestShowcmd(t *testing.T) {
	e := half(t, "one two three\n", `2"ad3`)
	if got := e.Showcmd(); got != `2"ad3` {
		t.Errorf("Showcmd = %q, want %q", got, `2"ad3`)
	}
}

// TestVisualModes: v, V and CTRL-V enter and leave, and typing another one
// switches rather than nesting.
func TestVisualModes(t *testing.T) {
	e := half(t, "one\ntwo\n", "v")
	if e.Mode() != VisualChar {
		t.Fatalf("v gave %v", e.Mode())
	}
	if err := e.Keys(decodeKeys([]byte("V"))); err != nil {
		t.Fatal(err)
	}
	if e.Mode() != VisualLine {
		t.Errorf("V in charwise visual gave %v, want linewise", e.Mode())
	}
	if err := e.Keys(decodeKeys([]byte("V"))); err != nil {
		t.Fatal(err)
	}
	if e.Mode() != Normal {
		t.Errorf("V in linewise visual gave %v, want normal", e.Mode())
	}
}

// TestVisualMarks: leaving visual mode leaves '< and '> on the ordered ends,
// and gv brings the selection back.
func TestVisualMarks(t *testing.T) {
	e := edit(t, "hello\nworld\n", "lvjl\x1b")
	lt, gt, ok := e.LastVisual()
	if !ok {
		t.Fatal("leaving visual mode remembered no selection")
	}
	if lt != (text.Pos{Line: 1, Col: 1}) {
		t.Errorf("'< = %+v, want line 1 col 1", lt)
	}
	if gt != (text.Pos{Line: 2, Col: 2}) {
		t.Errorf("'> = %+v, want line 2 col 2", gt)
	}
	if err := e.Keys(decodeKeys([]byte("gv"))); err != nil {
		t.Fatal(err)
	}
	if e.Mode() != VisualChar || e.Cursor() != (text.Pos{Line: 2, Col: 2}) {
		t.Errorf("gv gave mode %v at %+v", e.Mode(), e.Cursor())
	}
}

// TestMacroRecordAndPlay: q records the raw keys, the q that stops it is not
// in them, and @ plays them back. The macro here is an insert so that this
// test says something about the recorder and nothing about the operators.
func TestMacroRecordAndPlay(t *testing.T) {
	e := edit(t, "one\ntwo\n", "qaAX\x1bq")
	v, err := e.Registers().Get('a')
	if err != nil {
		t.Fatalf("reading the register: %v", err)
	}
	if got := string(v.Bytes()); got != "AX\x1b" {
		t.Errorf("register a = %q, want %q", got, "AX\x1b")
	}
	if e.Recording() != 0 {
		t.Error("still recording after the second q")
	}
	if err := e.Keys(decodeKeys([]byte("j@a"))); err != nil {
		t.Fatal(err)
	}
	if got := buf(e); got != "oneX\ntwoX\n" {
		t.Errorf("after @a, buffer = %q, want %q", got, "oneX\ntwoX\n")
	}
}

// TestMacroCountAndAtAt: a count on @ repeats the whole macro, and @@ repeats
// the last one played.
func TestMacroCountAndAtAt(t *testing.T) {
	e := edit(t, "a\nb\nc\nd\n", "qaAX\x1bjq2@a")
	if got := buf(e); got != "aX\nbX\ncX\nd\n" {
		t.Errorf("2@a: buffer = %q, want %q", got, "aX\nbX\ncX\nd\n")
	}
	if err := e.Keys(decodeKeys([]byte("@@"))); err != nil {
		t.Fatal(err)
	}
	if got := buf(e); got != "aX\nbX\ncX\ndX\n" {
		t.Errorf("@@: buffer = %q, want %q", got, "aX\nbX\ncX\ndX\n")
	}
}

// TestDotRepeatsAnInsert: the dot record holds the keys of the change,
// including the text that was typed, and a count on the . replaces the
// original count rather than multiplying by it. Both measured: 3ax<Esc>.
// gives six x and 3ax<Esc>2. gives five.
func TestDotRepeatsAnInsert(t *testing.T) {
	if got := buf(edit(t, "A\n", "3ax\x1b.")); got != "Axxxxxx\n" {
		t.Errorf("3ax<Esc>. gave %q, want %q", got, "Axxxxxx\n")
	}
	if got := buf(edit(t, "A\n", "3ax\x1b2.")); got != "Axxxxx\n" {
		t.Errorf("3ax<Esc>2. gave %q, want %q", got, "Axxxxx\n")
	}
}

// TestDotOnNothing beeps rather than repeating a command that never happened.
func TestDotOnNothing(t *testing.T) {
	e := edit(t, "one\n", ".")
	if !e.aborted {
		t.Error(". with no last change did not beep")
	}
}

// TestUndoAnInsertIsOneStep: everything between i and Escape is one press of
// u, and CTRL-R puts it back.
func TestUndoAnInsertIsOneStep(t *testing.T) {
	e := edit(t, "x\n", "ifoo bar\x1bu")
	if got := buf(e); got != "x\n" {
		t.Errorf("u after an insert gave %q, want the file back", got)
	}
	if err := e.Keys(decodeKeys([]byte("\x12"))); err != nil {
		t.Fatal(err)
	}
	if got := buf(e); got != "foo barx\n" {
		t.Errorf("CTRL-R gave %q, want the insert back", got)
	}
}

// TestUndoLine is U: the line as it was before the run of changes to it, and
// itself a change, so u takes the U back.
func TestUndoLine(t *testing.T) {
	e := edit(t, "hello\n", "iA\x1bAB\x1bU")
	if got := buf(e); got != "hello\n" {
		t.Errorf("U gave %q, want the line back", got)
	}
	if err := e.Keys(decodeKeys([]byte("u"))); err != nil {
		t.Fatal(err)
	}
	if got := buf(e); got != "AhelloB\n" {
		t.Errorf("u after U gave %q, want the U undone", got)
	}
}

// TestQuit: ZZ and ZQ stop the editor through the one error every frontend
// already checks.
func TestQuit(t *testing.T) {
	e := New(text.Read([]byte("x\n")))
	err := e.Keys(decodeKeys([]byte("ZZ")))
	if err != ErrQuit {
		t.Errorf("ZZ = %v, want ErrQuit", err)
	}
}

// TestIsKeywordByte reads 'iskeyword' the way vim writes it. The default's
// "192-255" is what makes every UTF-8 continuation byte part of a word without
// this function knowing what UTF-8 is.
func TestIsKeywordByte(t *testing.T) {
	cases := []struct {
		c    byte
		opt  string
		want bool
	}{
		{'a', "", true},
		{'Z', "", true},
		{'7', "", true},
		{'_', "", true},
		{'-', "", false},
		{' ', "", false},
		{0xc3, "", true},
		{'-', "@,48-57,_,192-255,-", true},
		{'_', "@,48-57", false},
		{'a', "@,^a", false},
	}
	for _, tc := range cases {
		if got := isKeywordByte(tc.c, tc.opt); got != tc.want {
			t.Errorf("isKeywordByte(%q, %q) = %v, want %v", tc.c, tc.opt, got, tc.want)
		}
	}
}

// TestRegisterNamesAreChecked: a register name that is not a register is a
// beep and an abandoned command, not a silent write somewhere else -- and not
// a message either. This asserted E354 until the redirect was measured: vim's
// nv_regname calls clearopbeep(), which says nothing at all, and "!dd leaves
// vim's message file holding one bare newline. E354 is what :let @! and
// setreg('!') print. See TestUnknownRegisterNameIsSilent.
func TestRegisterNamesAreChecked(t *testing.T) {
	e := edit(t, "one\n", `"!`)
	if e.pend.reg != 0 {
		t.Errorf(`"! set the register to %q`, e.pend.reg)
	}
	if e.Message() != "" {
		t.Errorf(`"! said %q; vim beeps and says nothing`, e.Message())
	}
	if !register.Valid('a') {
		t.Skip("internal/register does not accept a yet")
	}
}
