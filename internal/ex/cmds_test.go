package ex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/quickfix"
)

// The commands that are not a range over the current buffer: files, the buffer
// list, ":set", ":help" and the quickfix walk.

func TestWriteAndEdit(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "one.txt")
	two := filepath.Join(dir, "two.txt")
	if err := os.WriteFile(one, []byte("alpha\nbravo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newHarness(t, "alpha", "bravo")
	h.ctx.Bufs.Cur.Name = one

	h.run(t, "e "+two)
	if !hasMsg(h.msgs, `"`+two+`" [New]`) {
		t.Errorf("opening a file that is not there said %q", h.msgs)
	}
	h.msgs = nil
	h.run(t, "normal ihello")
	h.run(t, "w")
	data, err := os.ReadFile(two)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Errorf("wrote %q, want hello", data)
	}
	if !hasMsg(h.msgs, `"`+two+`" [New] 1L, 6B written`) {
		t.Errorf(":w said %q", h.msgs)
	}

	// The alternate file is what was open before, which is what "#" expands to
	// and what ":b#" goes back to.
	if h.ctx.Bufs.Alt == nil || h.ctx.Bufs.Alt.Name != one {
		t.Errorf("alternate is %v, want %s", h.ctx.Bufs.Alt, one)
	}
	h.run(t, "b #")
	if h.ctx.Bufs.Cur.Name != one {
		t.Errorf(":b# went to %q", h.ctx.Bufs.Cur.Name)
	}
}

// TestQuitRefusesAndPrompts is E37 without 'confirm' and the cmdline prompt
// with it, which is the whole of it: the question goes on the command line
// and never into an NSAlert.
func TestQuitRefusesAndPrompts(t *testing.T) {
	h := newHarness(t, "a")
	h.run(t, "normal x")
	if err := h.err("q"); err == nil || err.Error() != "E37: No write since last change (add ! to override)" {
		t.Errorf(":q on a modified buffer answered %v", err)
	}

	// With 'confirm' the same ":q" asks instead, on the command line.
	h = newHarness(t, "a")
	h.run(t, "normal x")
	h.ctx.Opt.G.Confirm = true
	h.answer = 'c'
	if err := h.err("q"); err != errSilent {
		t.Errorf("cancelling the prompt answered %v, want the silent abandon", err)
	}
	if len(h.asked) != 1 || !strings.HasPrefix(h.asked[0], `Save changes to "`) {
		t.Errorf("prompt was %q", h.asked)
	}
	if !strings.HasSuffix(h.asked[0], "[Y]es, (N)o, (C)ancel: ") {
		t.Errorf("prompt did not end with vim's choices: %q", h.asked[0])
	}

	// "N" throws the changes away and the quit goes through.
	h.answer = 'n'
	if err := h.err("q"); err != ErrQuit {
		t.Errorf("answering N gave %v, want the quit", err)
	}
}

// TestBufferList is the ":ls" flag column and the ":b" argument forms.
func TestBufferList(t *testing.T) {
	h := newHarness(t, "a")
	h.ctx.Bufs.Cur.Name = "/tmp/one.txt"
	h.ctx.Bufs.Add("/tmp/two.txt", h.ctx.buffer())

	h.run(t, "ls")
	if len(h.msgs) != 2 {
		t.Fatalf(":ls printed %d lines: %q", len(h.msgs), h.msgs)
	}
	if !strings.HasPrefix(h.msgs[0], `  1 %a  ` /* listed, current, active, no ro, unmodified */) {
		t.Errorf(":ls line one is %q", h.msgs[0])
	}
	if !strings.Contains(h.msgs[0], `"/tmp/one.txt"`) || !strings.HasSuffix(h.msgs[0], "line 1") {
		t.Errorf(":ls line one is %q", h.msgs[0])
	}

	// ":b" takes a number, and a substring that matches exactly one listed
	// buffer.
	if err := h.err("b 2"); err != nil {
		t.Fatalf(":b 2: %v", err)
	}
	if h.ctx.Bufs.Cur.Num != 2 {
		t.Errorf(":b 2 went to buffer %d", h.ctx.Bufs.Cur.Num)
	}
	if err := h.err("b one"); err != nil {
		t.Fatalf(":b one: %v", err)
	}
	if h.ctx.Bufs.Cur.Name != "/tmp/one.txt" {
		t.Errorf(":b one went to %q", h.ctx.Bufs.Cur.Name)
	}
	// Two matches is E93 rather than a guess.
	if err := h.err("b tmp"); err == nil || !strings.HasPrefix(err.Error(), "E93") {
		t.Errorf(":b tmp answered %v, want E93", err)
	}
	// None is E86.
	if err := h.err("b nosuch"); err == nil || !strings.HasPrefix(err.Error(), "E86") {
		t.Errorf(":b nosuch answered %v, want E86", err)
	}
}

// TestSetGoesThroughOptions: ":set" is four lines of glue over
// internal/options, and the two things worth testing here are that the glue
// exists and that the mode machine hears about the change.
func TestSetGoesThroughOptions(t *testing.T) {
	h := newHarness(t, "a")
	h.run(t, "set shiftwidth=4")
	if h.ctx.Opt.B.ShiftWidth != 4 {
		t.Errorf("shiftwidth = %d", h.ctx.Opt.B.ShiftWidth)
	}
	if got := h.ctx.Ed.Options().ShiftWidth; got != 4 {
		t.Errorf("the mode machine still has shiftwidth %d; :set has to push", got)
	}
	h.msgs = nil
	h.run(t, "set shiftwidth?")
	if len(h.msgs) != 1 || !strings.Contains(h.msgs[0], "shiftwidth=4") {
		t.Errorf(`":set sw?" said %q`, h.msgs)
	}
	if err := h.err("set nosuchopt"); err == nil || !strings.HasPrefix(err.Error(), "E518") {
		t.Errorf(":set nosuchopt answered %v, want E518", err)
	}
}

// TestBangOutput is difference D-003: the output of ":!" goes into a scratch
// buffer rather than onto the command line with a press-enter prompt.
func TestBangOutput(t *testing.T) {
	h := newHarness(t, "a")
	h.run(t, "!echo hello")
	// The split may or may not have been made, depending on how far
	// internal/window's tree has got; either way the text has to have gone
	// somewhere and not been thrown away.
	if b := h.ctx.bufs().ByName(":!echo hello"); b != nil {
		if got := string(b.Text.Line(1)); got != "hello" {
			t.Errorf("the scratch buffer holds %q", got)
		}
		return
	}
	if !hasMsg(h.msgs, "hello") {
		t.Errorf(":!echo hello said %q and made no scratch buffer", h.msgs)
	}
}

// TestQuickfixWalk is ":cc", ":cn" and ":cp" over a list somebody else filled.
func TestQuickfixWalk(t *testing.T) {
	h := newHarness(t, "a")
	if err := h.err("cc"); err != quickfix.ErrNoList {
		t.Errorf(":cc with no list answered %v, want E42", err)
	}

	Push(h.ctx.QF, &quickfix.List{
		Title: "test",
		Idx:   0,
		Entries: []quickfix.Entry{
			{FileName: "f.txt", LNum: 1, Text: "first", Valid: true},
			{Text: "not a location"},
			{FileName: "f.txt", LNum: 3, Text: "third", Valid: true},
		},
	})
	h.msgs = nil
	// ":cn" skips the entry with no location, which is what makes a
	// compiler's "In file included from" lines invisible to it.
	if err := h.err("cn"); err != nil {
		t.Fatalf(":cn: %v", err)
	}
	if !hasMsg(h.msgs, "(3 of 3): third") {
		t.Errorf(":cn said %q", h.msgs)
	}
	if err := h.err("cn"); err != quickfix.ErrNoMore {
		t.Errorf(":cn past the end answered %v, want E553", err)
	}
	h.msgs = nil
	if err := h.err("cp"); err != nil {
		t.Fatalf(":cp: %v", err)
	}
	if !hasMsg(h.msgs, "(1 of 3): first") {
		t.Errorf(":cp said %q", h.msgs)
	}

	// ":clist" shows every entry, the one with no location included.
	h.msgs = nil
	h.run(t, "clist")
	want := []string{" 1 f.txt:1: first", " 2 || not a location", " 3 f.txt:3: third"}
	if strings.Join(h.msgs, "\n") != strings.Join(want, "\n") {
		t.Errorf(":clist said\n%q\nwant\n%q", h.msgs, want)
	}
}

// TestHelpTags is difference D-001: the doc directory the installed vim ships
// is the one ":help" reads, so the day a brew upgrade moves it this fails with
// a name on it.
func TestHelpTags(t *testing.T) {
	if helpDir() == "" {
		t.Skip("no vim doc directory on this box")
	}
	tag, err := FindHelpTag(":marks")
	if err != nil {
		t.Fatalf(`:help :marks: %v`, err)
	}
	if !strings.HasSuffix(tag.File, "motion.txt") {
		t.Errorf(":marks resolved to %q, want motion.txt", tag.File)
	}
	// An option name resolves through the "'option'" spelling, which is the
	// second thing vim tries and the reason ":help scrolloff" works.
	if _, err := FindHelpTag("scrolloff"); err != nil {
		t.Errorf(`:help scrolloff: %v`, err)
	}
	// E149 with the word in it, measured.
	_, err = FindHelpTag("nosuchtag")
	if err == nil || err.Error() != "E149: Sorry, no help for nosuchtag" {
		t.Errorf(":help nosuchtag answered %v", err)
	}
}

// TestHelpOpens takes ":help" all the way, which is the half that reads a file
// and puts a cursor on a line.
func TestHelpOpens(t *testing.T) {
	if helpDir() == "" {
		t.Skip("no vim doc directory on this box")
	}
	h := newHarness(t, "a")
	if err := h.err("help :marks"); err != nil {
		t.Fatalf(":help :marks: %v", err)
	}
	cur := h.ctx.bufs().Cur
	if !strings.HasSuffix(cur.Name, "motion.txt") {
		t.Errorf(":help :marks opened %q, want motion.txt", cur.Name)
	}
	if !cur.ReadOnly {
		t.Error("the help buffer is writable")
	}
	line, _ := h.cursor()
	if !strings.Contains(string(h.ctx.buffer().Line(line)), "*:marks*") {
		t.Errorf("the cursor landed on %q", h.ctx.buffer().Line(line))
	}
}

// TestReadFile is ":r file", which puts a file in after the range's last line
// and where ":0r" puts one above the first.
func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(name, []byte("x\ny\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, "a", "b")
	h.run(t, "1r "+name)
	if got := h.text(); got != "a\nx\ny\nb" {
		t.Errorf("got %q", got)
	}

	h = newHarness(t, "a", "b")
	h.run(t, "0r "+name)
	if got := h.text(); got != "x\ny\na\nb" {
		t.Errorf(":0r gave %q", got)
	}
}

// TestBufferNamesAreShortened is vim's shorten_fname: the name is stored in
// full and printed relative to the working directory.
//
// Measured: in a directory holding in.txt, ":e other.txt" prints
// `"other.txt" [New]` and ":ls" lists "in.txt" and "other.txt", both without a
// path. Storing the full name is right -- ":w" needs it -- and handing it to
// the message line raw put the whole path into msgs.txt, into ":ls", into the
// 'confirm' prompt and into the status line.
func TestBufferNamesAreShortened(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// The working directory as the process sees it, not as t.TempDir spells
	// it: on macOS the two differ by a symlink and a prefix test would fail on
	// the difference rather than on the behaviour.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	h := newHarness(t, "alpha")
	h.ctx.Bufs.Cur.Name = filepath.Join(wd, "in.txt")
	if got := h.ctx.Bufs.Cur.Display(); got != "in.txt" {
		t.Errorf("Display() = %q, want in.txt", got)
	}

	h.run(t, "e other.txt")
	if len(h.msgs) != 1 || h.msgs[0] != `"other.txt" [New]` {
		t.Errorf(`:e other.txt said %q, want ["other.txt" [New]]`, h.msgs)
	}

	h.msgs = nil
	h.run(t, "ls")
	for _, m := range h.msgs {
		if strings.Contains(m, wd) {
			t.Errorf(":ls printed a full path: %q", m)
		}
	}

	// A name that is not under the working directory stays absolute, because
	// vim never climbs out with "..".
	h.ctx.Bufs.Cur.Name = "/nowhere/else.txt"
	if got := h.ctx.Bufs.Cur.Display(); got != "/nowhere/else.txt" {
		t.Errorf("Display() = %q, want the absolute name", got)
	}
}

// TestWriteIntoAMissingDirectory is E212 and not E484: vim's buf_write answers
// "Can't open file for writing" for a target it cannot create, and quotes the
// name in front of it. Measured: `:w nodir/x.txt` prints the progress line and
// then `"nodir/x.txt" E212: Can't open file for writing`.
func TestWriteIntoAMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	h := newHarness(t, "alpha")
	err := h.err("w nodir/x.txt")
	want := `"nodir/x.txt" E212: Can't open file for writing`
	if err == nil || err.Error() != want {
		t.Errorf(":w nodir/x.txt answered %v, want %q", err, want)
	}
	if len(h.msgs) != 1 || h.msgs[0] != `"nodir/x.txt" ` {
		t.Errorf("progress line was %q", h.msgs)
	}
}
