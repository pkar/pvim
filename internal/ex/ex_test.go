package ex

import (
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/quickfix"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// harness is one editor, one window, one buffer list and a message log, which
// is the smallest thing an ex command can be run against.
type harness struct {
	ctx    *Context
	msgs   []string
	answer byte
	asked  []string
}

// newHarness builds an editor over the given lines with a 24-row window, which
// is what "vim --clean" gives on the harness pseudo-terminal less the command
// line.
func newHarness(t *testing.T, lines ...string) *harness {
	t.Helper()
	data := ""
	if len(lines) > 0 {
		data = strings.Join(lines, "\n") + "\n"
	}
	buf := text.Read([]byte(data))
	ed := mode.New(buf)
	opt := options.Defaults()
	win := window.New(1, buf, opt.GW)
	win.SetHeight(23)
	win.View.Width = 80

	h := &harness{answer: 'y'}
	h.ctx = &Context{
		Ed:   ed,
		Tabs: window.NewTabs(window.NewTabPage(win)),
		Opt:  &opt,
		QF:   &quickfix.Stack{},
		Cmds: UserCommands{},
		Msg:  func(s string) { h.msgs = append(h.msgs, s) },
		Err:  func(s string) { h.msgs = append(h.msgs, s) },
		Prompt: func(q, choices string) (byte, error) {
			h.asked = append(h.asked, q)
			return h.answer, nil
		},
		Bufs: NewBufList(),
	}
	h.ctx.Bufs.Add("", buf)
	ed.SetOptions(ModeOptions(&opt, win))
	return h
}

// allMsgs is everything said, from both places a message can land: this
// package's Context.Msg and the mode machine's own log, which is where the
// commands that borrow a normal-mode key -- ":j", ":>" and ":<" -- put theirs.
// A real frontend wires the two together; a test has to look in both.
func (h *harness) allMsgs() []string {
	return append(append([]string{}, h.ctx.Ed.Messages()...), h.msgs...)
}

// run runs one command line and fails the test if it errored.
func (h *harness) run(t *testing.T, line string) {
	t.Helper()
	if err := h.ctx.RunLine(line); err != nil {
		t.Fatalf(":%s: %v", line, err)
	}
}

// err runs one command line and returns what it answered with.
func (h *harness) err(line string) error { return h.ctx.RunLine(line) }

// text returns the buffer as one string, which is what a table test compares.
func (h *harness) text() string {
	b := h.ctx.buffer()
	var out []string
	for n := 1; n <= b.LineCount(); n++ {
		out = append(out, string(b.Line(n)))
	}
	return strings.Join(out, "\n")
}

// cursor returns the cursor as vim's line() and col() report it, both 1-based,
// which is the form every measurement in these tests was taken in.
func (h *harness) cursor() (int, int) {
	p := h.ctx.Ed.Cursor()
	return p.Line, p.Col + 1
}

const ten = "line one\nline two\nline three\nline four\nline five\n" +
	"line six\nline seven\nline eight\nline nine\nline ten"

func tenLines() []string { return strings.Split(ten, "\n") }

// TestRangeCommands is the measured half of this package: every expectation
// below was read out of /opt/homebrew/bin/vim 9.2.0321 through
// "vim --clean -i NONE --not-a-term -s" with the command in a keys file.
func TestRangeCommands(t *testing.T) {
	for _, tc := range []struct {
		line string
		want string
		msg  string
	}{
		{"1,4d", "line five\nline six\nline seven\nline eight\nline nine\nline ten", "4 fewer lines"},
		{"1,2d", "line three\nline four\nline five\nline six\nline seven\nline eight\nline nine\nline ten", ""},
		{"%d", "", "--No lines in buffer--"},
		{"0d", "line two\nline three\nline four\nline five\nline six\nline seven\nline eight\nline nine\nline ten", ""},
		{"0,2d", "line three\nline four\nline five\nline six\nline seven\nline eight\nline nine\nline ten", ""},
		{"d 3", "line four\nline five\nline six\nline seven\nline eight\nline nine\nline ten", "3 fewer lines"},
		{"$d", "line one\nline two\nline three\nline four\nline five\nline six\nline seven\nline eight\nline nine", ""},
		{"1,4y", ten, "4 lines yanked"},
		{"1,4m8", "line five\nline six\nline seven\nline eight\nline one\nline two\nline three\nline four\nline nine\nline ten", "4 lines moved"},
		{"1,3t0", "line one\nline two\nline three\n" + ten, "3 more lines"},
		{"1,4co8", "line one\nline two\nline three\nline four\nline five\nline six\nline seven\nline eight\n" +
			"line one\nline two\nline three\nline four\nline nine\nline ten", "4 more lines"},
		{"1,4j", "line one line two line three line four\nline five\nline six\nline seven\nline eight\nline nine\nline ten", ""},
		{"1,5>", "\tline one\n\tline two\n\tline three\n\tline four\n\tline five\n" +
			"line six\nline seven\nline eight\nline nine\nline ten", "5 lines >ed 1 time"},
		{"2,3>>", "line one\n\t\tline two\n\t\tline three\nline four\nline five\n" +
			"line six\nline seven\nline eight\nline nine\nline ten", ""},
		{"1,2>3", "line one\n\tline two\n\tline three\n\tline four\nline five\n" +
			"line six\nline seven\nline eight\nline nine\nline ten", "3 lines >ed 1 time"},
		{".,+2d", "line four\nline five\nline six\nline seven\nline eight\nline nine\nline ten", "3 fewer lines"},
		{"2,+3d", "line one\nline five\nline six\nline seven\nline eight\nline nine\nline ten", "3 fewer lines"},
		{"/three/,/five/d", "line one\nline two\nline six\nline seven\nline eight\nline nine\nline ten", "3 fewer lines"},
		{"1;/two/d", "line three\nline four\nline five\nline six\nline seven\nline eight\nline nine\nline ten", ""},
		{"$-1,$d", "line one\nline two\nline three\nline four\nline five\nline six\nline seven\nline eight", ""},
		{"-1d", "line two\nline three\nline four\nline five\nline six\nline seven\nline eight\nline nine\nline ten", ""},
		{".+d", "line one\nline three\nline four\nline five\nline six\nline seven\nline eight\nline nine\nline ten", ""},
	} {
		h := newHarness(t, tenLines()...)
		h.run(t, tc.line)
		if got := h.text(); got != tc.want {
			t.Errorf(":%s\n got %q\nwant %q", tc.line, got, tc.want)
		}
		if tc.msg != "" && !hasMsg(h.allMsgs(), tc.msg) {
			t.Errorf(":%s said %q, want %q in it", tc.line, h.msgs, tc.msg)
		}
		if tc.msg == "" && len(h.msgs) > 0 {
			t.Errorf(":%s said %q, want silence", tc.line, h.msgs)
		}
	}
}

func hasMsg(msgs []string, want string) bool {
	for _, m := range msgs {
		if m == want {
			return true
		}
	}
	return false
}

// TestCursorAfterRangeCommands: where vim leaves the cursor is a per-command
// rule and not "the first line of the range". Measured on a file indented by
// two spaces so that the first non-blank is column 3 and cannot be confused
// with column 1.
func TestCursorAfterRangeCommands(t *testing.T) {
	indented := []string{
		"  line one", "  line two", "  line three", "  line four", "  line five",
		"  line six", "  line seven", "  line eight", "  line nine", "  line ten",
	}
	for _, tc := range []struct {
		start int
		line  string
		want  [2]int
	}{
		{5, "1,3y", [2]int{5, 3}},
		{5, "1,3d", [2]int{1, 3}},
		{5, "2,3co6", [2]int{8, 3}},
		{5, "2,3m6", [2]int{6, 3}},
		{1, "2,4j", [2]int{2, 3}},
		{5, "$d", [2]int{9, 3}},
		{1, "8,10d", [2]int{7, 3}},
		{5, "2,3t0", [2]int{2, 3}},
	} {
		h := newHarness(t, indented...)
		h.ctx.Ed.SetCursor(text.Pos{Line: tc.start, Col: 2})
		h.run(t, tc.line)
		if l, c := h.cursor(); l != tc.want[0] || c != tc.want[1] {
			t.Errorf("%dG:%s left %d,%d, want %d,%d", tc.start, tc.line, l, c, tc.want[0], tc.want[1])
		}
	}
}

// TestRangeErrors: every code measured, with the argument vim appends.
func TestRangeErrors(t *testing.T) {
	for _, tc := range []struct {
		line string
		want string
	}{
		{"99d", "E16: Invalid range"},
		{"1,99d", "E16: Invalid range"},
		{"1-5d", "E16: Invalid range"},
		{"10,12co0", "E16: Invalid range"},
		{"'zd", "E20: Mark not set"},
		{`\/d`, "E35: No previous regular expression"},
		{"/nosuch/d", "E486: Pattern not found: nosuch"},
		{"1,2m1", "E134: Cannot move a range of lines into itself"},
		{"put", `E353: Nothing in register "`},
		{"normal", "E471: Argument required"},
	} {
		h := newHarness(t, tenLines()...)
		err := h.err(tc.line)
		if err == nil {
			t.Errorf(":%s succeeded, want %q", tc.line, tc.want)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf(":%s = %q, want %q", tc.line, err, tc.want)
		}
	}
}

// TestBackwardsRangePrompts is the prompt vim shows and the two answers it
// takes, measured: ":3,1d" then "y" deletes lines 1 to 3, and "n" does
// nothing.
func TestBackwardsRangePrompts(t *testing.T) {
	h := newHarness(t, tenLines()...)
	h.answer = 'y'
	h.run(t, "3,1d")
	if got := h.text(); !strings.HasPrefix(got, "line four") {
		t.Errorf("after y the buffer starts %q, want line four", got)
	}
	if len(h.asked) != 1 || h.asked[0] != "Backwards range given, OK to swap (y/n)?" {
		t.Errorf("prompt was %q", h.asked)
	}

	h = newHarness(t, tenLines()...)
	h.answer = 'n'
	if err := h.err("3,1d"); err != errSilent {
		t.Errorf("after n: %v, want the silent abandon", err)
	}
	if h.text() != ten {
		t.Error("n changed the buffer")
	}
}

// TestPutAndRegisters covers the register argument, which is the part of ":d"
// and ":pu" a range test does not reach.
func TestPutAndRegisters(t *testing.T) {
	h := newHarness(t, tenLines()...)
	h.run(t, "2,4d a")
	h.run(t, "0put a")
	want := "line two\nline three\nline four\nline one\nline five\nline six\nline seven\nline eight\nline nine\nline ten"
	if got := h.text(); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}

	// ":pu!" puts above the line rather than below it.
	h = newHarness(t, tenLines()...)
	h.run(t, "1y z")
	h.run(t, "3pu! z")
	if got := strings.Split(h.text(), "\n")[2]; got != "line one" {
		t.Errorf("line 3 after :3pu! is %q, want line one", got)
	}
}

// TestPrintForms: ":p", ":nu" and ":l" differ only in what goes round each
// line, and all three were measured.
func TestPrintForms(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"2,3p", []string{"line two", "line three"}},
		{"2,3nu", []string{"  2 line two", "  3 line three"}},
		{"2,3l", []string{"line two$", "line three$"}},
		{"1,3#", []string{"  1 line one", "  2 line two", "  3 line three"}},
	} {
		h := newHarness(t, tenLines()...)
		h.run(t, tc.line)
		if strings.Join(h.msgs, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf(":%s said %q, want %q", tc.line, h.msgs, tc.want)
		}
	}

	// ":=" with no range is the last line of the file, measured: 10.
	h := newHarness(t, tenLines()...)
	h.run(t, "=")
	if len(h.msgs) != 1 || h.msgs[0] != "10" {
		t.Errorf(`":=" said %q, want ["10"]`, h.msgs)
	}
}

// TestMarksListing is the column arithmetic, which is where "close enough" is
// a diff on every run. Measured after "3Gmaggyy2Gx".
func TestMarksListing(t *testing.T) {
	h := newHarness(t, tenLines()...)
	h.run(t, "3k a")
	h.run(t, "marks a")
	want := []string{
		"mark line  col file/text",
		" a      3    0 line three",
	}
	if strings.Join(h.msgs, "\n") != strings.Join(want, "\n") {
		t.Errorf(":marks said\n%q\nwant\n%q", h.msgs, want)
	}
}

// TestNormalOverRange is ":normal" with a range, which runs the keys once per
// line and is what makes ":%normal A;" append to every line.
func TestNormalOverRange(t *testing.T) {
	h := newHarness(t, "a", "b", "c")
	h.run(t, "%normal A;")
	if got := h.text(); got != "a;\nb;\nc;" {
		t.Errorf("got %q, want a;\\nb;\\nc;", got)
	}
}

// TestUserCommandJsonPretty is the vimrc's only user command, end to end
// except for the shell: the definition parses, the attributes land, and the
// replacement expands to the filter line the range names.
func TestUserCommandJsonPretty(t *testing.T) {
	h := newHarness(t, tenLines()...)
	h.run(t, `command! -range -nargs=0 -bar JsonPretty <line1>,<line2>!jq --sort-keys '.'`)
	u := h.ctx.Cmds["JsonPretty"]
	if u == nil {
		t.Fatal("JsonPretty was not defined")
	}
	if u.NArgs != "0" || u.Range != "." || !u.Bar {
		t.Errorf("attributes = %+v, want nargs 0, range . and bar", u)
	}
	got, err := u.Expand(Cmd{Lines: LineRange{First: 2, Last: 5, Given: 2}})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if got != `2,5!jq --sort-keys '.'` {
		t.Errorf("Expand = %q", got)
	}

	// Redefining without the bang is E174, which is what catches a vimrc
	// sourced twice; the vimrc's own definition has the bang for that reason.
	if err := h.err(`command -range -nargs=0 JsonPretty x`); err == nil ||
		!strings.HasPrefix(err.Error(), "E174") {
		t.Errorf("redefinition answered %v, want E174", err)
	}
}

// TestUserCommandRuns takes the same command all the way through dispatch,
// with a filter that needs no jq on the box.
func TestUserCommandRuns(t *testing.T) {
	h := newHarness(t, "b", "a", "c")
	h.run(t, `command! -range -nargs=0 -bar Sorted <line1>,<line2>!sort`)
	h.run(t, "1,3Sorted")
	if got := h.text(); got != "a\nb\nc" {
		t.Errorf("got %q, want a\\nb\\nc", got)
	}
}

// TestFilter is the ":{range}!" path the vimrc's JsonPretty rides on.
func TestFilter(t *testing.T) {
	h := newHarness(t, tenLines()...)
	h.run(t, "1,3!tr a-z A-Z")
	want := "LINE ONE\nLINE TWO\nLINE THREE\nline four\nline five\n" +
		"line six\nline seven\nline eight\nline nine\nline ten"
	if got := h.text(); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if !hasMsg(h.msgs, "3 lines filtered") {
		t.Errorf("said %q, want 3 lines filtered", h.msgs)
	}
}

// TestReadCommand is ":r !cmd", which puts the output after the range's last
// line and takes nothing out.
func TestReadCommand(t *testing.T) {
	h := newHarness(t, "a", "b")
	h.run(t, "1r !echo hello")
	if got := h.text(); got != "a\nhello\nb" {
		t.Errorf("got %q, want a\\nhello\\nb", got)
	}
}

// TestUnimplementedIsNotUnknown: a command in the table with no handler
// answers E319 and not E492, because the two mean different things and the
// difference is what tells a reader whether to look in vim's documentation or
// in this repository.
func TestUnimplementedIsNotUnknown(t *testing.T) {
	h := newHarness(t, "a")
	// ":vimgrep" is unimplemented and is in the table so that ":vim" resolves to
	// it rather than to ":visual". Any handlerless name does here; this one is
	// picked because it will be among the last to get a handler, and the day it
	// is wired this test wants a different name and not a deleted assertion.
	err := h.err("vimgrep /x/ *.go")
	if err == nil || !strings.HasPrefix(err.Error(), "E319") {
		t.Errorf(`":vimgrep" answered %v, want E319 while it has no handler`, err)
	}
	err = h.err("nosuchcommand")
	if err == nil || !strings.HasPrefix(err.Error(), "E492") {
		t.Errorf(`":nosuchcommand" answered %v, want E492`, err)
	}
}

// TestMessagesAndMarks is the half of a command that is neither its buffer nor
// its cursor: what it says, where it leaves a mark, and what it answers with.
//
// Every expectation was read off /opt/homebrew/bin/vim 9.2.0321 through
// "vim --clean -i NONE --not-a-term -s" with ":redir" around the script, and
// each one is a case in testdata/keys as well, so the oracle grades the same
// thing end to end.
func TestMessagesAndMarks(t *testing.T) {
	t.Run("v that selected nothing found it everywhere", func(t *testing.T) {
		h := newHarness(t, "a1", "a2", "a3")
		h.run(t, "v/a/d")
		if !hasMsg(h.allMsgs(), "Pattern found in every line: a") {
			t.Errorf(`:v/a/d said %q`, h.msgs)
		}
		// The other way round is the other sentence, which is what makes the
		// pair worth one test: vim branches on the inversion and not on the
		// count.
		h = newHarness(t, "a1", "a2", "a3")
		h.run(t, "g/zzz/d")
		if !hasMsg(h.allMsgs(), "Pattern not found: zzz") {
			t.Errorf(`:g/zzz/d said %q`, h.msgs)
		}
	})

	t.Run("a nested global says nothing when its line does not match", func(t *testing.T) {
		h := newHarness(t, "a1", "a2")
		h.run(t, "g/^/g/zz/d")
		if len(h.msgs) != 0 {
			t.Errorf(`:g/^/g/zz/d said %q, want silence`, h.msgs)
		}
	})

	t.Run("a global reports its substitutions or its lines, never both", func(t *testing.T) {
		h := newHarness(t, "a1", "a2", "a3")
		h.run(t, `g/a/s//X\rY/`)
		want := []string{"3 substitutions on 3 lines"}
		if strings.Join(h.msgs, "|") != strings.Join(want, "|") {
			t.Errorf("said %q, want %q", h.msgs, want)
		}
	})

	t.Run("print expands tabs and control characters", func(t *testing.T) {
		for _, tc := range []struct {
			line string
			cmd  string
			want string
		}{
			{"\tbaz foo", "1p", "        baz foo"},
			{"\tbaz foo", "1nu", "  1         baz foo"},
			{"\tbaz foo", "1l", "^Ibaz foo$"},
			{"a\x01b", "1p", "a^Ab"},
			{"a\x01b", "1l", "a^Ab$"},
		} {
			h := newHarness(t, tc.line)
			h.run(t, tc.cmd)
			if len(h.msgs) != 1 || h.msgs[0] != tc.want {
				t.Errorf(":%s on %q said %q, want %q", tc.cmd, tc.line, h.msgs, tc.want)
			}
		}
	})

	t.Run("k marks the first non-blank", func(t *testing.T) {
		for _, tc := range []struct {
			line string
			want int
		}{
			{"  foo bar", 2},
			{"\t indented", 2},
			{"flush", 0},
		} {
			h := newHarness(t, tc.line)
			h.run(t, "1k a")
			p, ok := h.ctx.buffer().Mark('a')
			if !ok || p.Line != 1 || p.Col != tc.want {
				t.Errorf(":1k a on %q left %+v (set %v), want column %d", tc.line, p, ok, tc.want)
			}
		}
	})

	t.Run("put with an empty register still moves and still saves undo", func(t *testing.T) {
		h := newHarness(t, tenLines()...)
		err := h.err("3pu")
		if err == nil || err.Error() != `E353: Nothing in register "` {
			t.Fatalf(":3pu answered %v", err)
		}
		if l, _ := h.cursor(); l != 3 {
			t.Errorf("cursor line = %d, want 3", l)
		}
		if seq := h.ctx.buffer().UndoSeq(); seq != 1 {
			t.Errorf("undo sequence = %d, want the header vim's do_put numbers", seq)
		}
	})

	t.Run("errors quote what vim quotes", func(t *testing.T) {
		for _, tc := range []struct {
			line string
			want string
		}{
			{"1k ab", "E488: Trailing characters: ab"},
			{"1k abc", "E488: Trailing characters: abc"},
			{"1mark ab", "E488: Trailing characters: ab"},
			{"r nosuch.txt", "E484: Can't open file nosuch.txt"},
		} {
			h := newHarness(t, tenLines()...)
			err := h.err(tc.line)
			if err == nil || err.Error() != tc.want {
				t.Errorf(":%s answered %v, want %q", tc.line, err, tc.want)
			}
		}
	})

	t.Run("a failed read still numbers an undo header", func(t *testing.T) {
		h := newHarness(t, tenLines()...)
		_ = h.err("r nosuch.txt")
		if seq := h.ctx.buffer().UndoSeq(); seq != 1 {
			t.Errorf("undo sequence = %d, want the header vim's ex_read saves", seq)
		}
	})

	t.Run("backslash slash is the search slot and not the substitute one", func(t *testing.T) {
		h := newHarness(t, "foo/bar/foo baz")
		h.run(t, "s/o/0/")
		if err := h.err(`\/=`); err == nil || err.Error() != "E35: No previous regular expression" {
			t.Errorf(`:\/= after a :s answered %v, want E35`, err)
		}
		// "\&" is the other slot and the same ":s" filled it.
		if err := h.err(`\&=`); err != nil && err.Error() == "E35: No previous regular expression" {
			t.Errorf(`:\&= after a :s answered E35, want the substitute pattern`)
		}
	})
}

// TestNormalLeavesFinishedCommandsRunning: vim supplies an Escape to a
// ":normal" only while a command is waiting for another key, so a command that
// COMPLETED leaves its state behind. Measured: ":normal v" then "lld" deletes
// three characters and ":normal qa" then "xq" leaves "x" alone in register a.
func TestNormalLeavesFinishedCommandsRunning(t *testing.T) {
	h := newHarness(t, "abcdef")
	h.run(t, "normal v")
	if m := h.ctx.Ed.Mode(); m != mode.VisualChar {
		t.Fatalf("mode after :normal v = %v, want visual", m)
	}
	if err := h.ctx.runKeys("lld"); err != nil {
		t.Fatal(err)
	}
	if got := h.text(); got != "def" {
		t.Errorf("buffer = %q, want def", got)
	}

	h = newHarness(t, "abcdef")
	h.run(t, "normal qa")
	if h.ctx.Ed.Recording() != 'a' {
		t.Fatalf("recording after :normal qa = %q, want a", h.ctx.Ed.Recording())
	}
	if err := h.ctx.runKeys("xq"); err != nil {
		t.Fatal(err)
	}
	val, err := h.ctx.Ed.Registers().Get('a')
	if err != nil {
		t.Fatal(err)
	}
	if got := val.String(); got != "x" {
		t.Errorf("@a = %q, want x; the injected Escape was being recorded", got)
	}

	// The control, and it is the whole point of the change: an insert that
	// never ended still gets the Escape.
	h = newHarness(t, "abcdef")
	h.run(t, "normal ihi")
	if m := h.ctx.Ed.Mode(); m != mode.Normal {
		t.Errorf("mode after :normal ihi = %v, want normal", m)
	}
	if got := h.text(); got != "hiabcdef" {
		t.Errorf("buffer = %q, want hiabcdef", got)
	}
}
