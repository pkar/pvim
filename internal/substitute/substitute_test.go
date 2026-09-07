package substitute

import (
	"errors"
	"testing"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
)

// TestRegexOptionsFoldsCaseTheSameWayEverywhere.
//
// ":s", ":g" and the ":s" inside a ":g" all compile their pattern through this
// one function, so 'ignorecase' and 'smartcase' cannot mean one thing in one
// and something else in the other. The vimrc sets both.
func TestRegexOptionsFoldsCaseTheSameWayEverywhere(t *testing.T) {
	o := options.Defaults()
	if err := o.Set("ignorecase", ""); err != nil {
		t.Fatal(err)
	}
	if err := o.Set("smartcase", ""); err != nil {
		t.Fatal(err)
	}
	got := RegexOptions(&o)
	if !got.IgnoreCase || !got.SmartCase {
		t.Errorf("RegexOptions dropped the case settings: %+v", got)
	}
	if got.NoMagic {
		t.Error("'magic' is on by default and RegexOptions turned it off")
	}
	// A nil option state is what a test with no editor hands it, and it has
	// to compile a pattern rather than panic.
	if got := RegexOptions(nil); got.IgnoreCase || got.SmartCase || got.NoMagic {
		t.Errorf("RegexOptions(nil) = %+v, want every flag off", got)
	}
}

// TestPatternErrorIsTwoLines. ":s//X/" prints E35 and then E476, ":s//X/e"
// prints only the E35, and ":s" with nothing to repeat prints only an E33.
// All three measured, and the second line is the sort of thing only an oracle
// notices.
func TestPatternErrorIsTwoLines(t *testing.T) {
	e := PatternError{Err: ErrNoPrevPattern, Extra: true}
	want := []string{"E35: No previous regular expression", "E476: Invalid command"}
	got := e.Messages()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Messages() = %q, want %q", got, want)
	}
	if !errors.Is(e, ErrNoPrevPattern) {
		t.Error("errors.Is cannot see the E35 through the pair")
	}
	if got := (PatternError{Err: ErrNoPrevSub}).Messages(); len(got) != 1 {
		t.Errorf("without the E476 the pair is %q, want one line", got)
	}
	if got := ErrInvalidCommand.Error(); got != "E476: Invalid command" {
		t.Errorf("E476 reads %q", got)
	}
}

// TestConfirmAnswers walks the "c" flag through every answer vim takes, with
// the answers and the results read off vim 9.2.321 on the same four lines.
func TestConfirmAnswers(t *testing.T) {
	for _, c := range []struct {
		name    string
		answers []Answer
		want    string
		msg     string
		quit    bool
	}{
		{"y y n q", []Answer{Yes, Yes, No, Quit}, "X1|X2|a3|a4", "2 substitutions on 2 lines", true},
		{"all no", []Answer{No, No, No, No}, "a1|a2|a3|a4", "", false},
		{"n then a", []Answer{No, All}, "a1|X2|X3|X4", "3 substitutions on 3 lines", false},
		{"n then l", []Answer{No, Last}, "a1|X2|a3|a4", "1 substitution on 1 line", true},
		{"y then quit", []Answer{Yes, Quit}, "X1|a2|a3|a4", "1 substitution on 1 line", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSession("a1", "a2", "a3", "a4")
			s.set(t, "report=0")
			answers := c.answers
			i := 0
			cmd, err := Parse(KindSubstitute, "/a/X/c", &s.st, &s.opt)
			if err != nil {
				t.Fatal(err)
			}
			res, err := Do(Request{
				Buf: s.buf, First: 1, Last: 4, Cmd: cmd, Opt: &s.opt, State: &s.st,
				Confirm: func(p Prompt) Answer {
					if want := "replace with X (y/n/a/q/l/^E/^Y)?"; p.Text != want {
						t.Errorf("prompt %q, want %q", p.Text, want)
					}
					if i >= len(answers) {
						t.Fatalf("prompted %d times, only %d answers", i+1, len(answers))
					}
					a := answers[i]
					i++
					return a
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := s.dump(); got != c.want {
				t.Errorf("buffer %q, want %q", got, c.want)
			}
			if res.Message != c.msg {
				t.Errorf("message %q, want %q", res.Message, c.msg)
			}
			if res.Quit != c.quit {
				t.Errorf("Quit %v, want %v", res.Quit, c.quit)
			}
		})
	}
}

// TestConfirmPromptShowsTheReplacementAsTyped.
//
// Vim puts the raw replacement in the prompt and not the text it will become:
// ":%s/a\(.\)/<\u\1>/c" asks `replace with <\u\1>`. Measured, and the opposite
// of what a first guess says, so it has a test of its own.
func TestConfirmPromptShowsTheReplacementAsTyped(t *testing.T) {
	s := newSession("a1", "a2")
	cmd, err := Parse(KindSubstitute, `/a\(.\)/<\u\1>/c`, &s.st, &s.opt)
	if err != nil {
		t.Fatal(err)
	}
	seen := ""
	if _, err := Do(Request{
		Buf: s.buf, First: 1, Last: 1, Cmd: cmd, Opt: &s.opt, State: &s.st,
		Confirm: func(p Prompt) Answer { seen = p.Text; return Yes },
	}); err != nil {
		t.Fatal(err)
	}
	if want := `replace with <\u\1> (y/n/a/q/l/^E/^Y)?`; seen != want {
		t.Errorf("prompt %q, want %q", seen, want)
	}
	if got := s.dump(); got != "<1>|a2" {
		t.Errorf("buffer %q, want %q", got, "<1>|a2")
	}
}

// TestConfirmWithNoCallbackSaysYes. cmd/oracle drives the editor with no
// frontend and the "c" flag has to mean something there; saying yes is the
// answer that leaves the buffer where a "c"-less run would.
func TestConfirmWithNoCallbackSaysYes(t *testing.T) {
	s := newSession("a1", "a2")
	if err := s.sub(t, KindSubstitute, "%", "/a/X/c"); err != nil {
		t.Fatal(err)
	}
	if got := s.dump(); got != "X1|X2" {
		t.Errorf("buffer %q, want %q", got, "X1|X2")
	}
}

// TestConfirmDeclinedLeavesTheCursorOnTheLastPrompt.
//
// The "c" flag walks the cursor to each match as it asks, so answering no to
// everything on line 1 while sitting on line 2 ends on line 1, at its first
// non-blank. Measured: from line 2 of " lead", ":1s/\w\+/X/gc" answered n
// reports 1,3.
func TestConfirmDeclinedLeavesTheCursorOnTheLastPrompt(t *testing.T) {
	s := newSession("  lead", "trail  ", "\tmix\t")
	s.cur = text.Pos{Line: 2}
	cmd, err := Parse(KindSubstitute, `/\w\+/X/gc`, &s.st, &s.opt)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Do(Request{
		Buf: s.buf, First: 1, Last: 1, Cmd: cmd, Cursor: s.cur, Opt: &s.opt, State: &s.st,
		Confirm: func(Prompt) Answer { return No },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Cursor.Line != 1 || res.Cursor.Col != 2 {
		t.Errorf("cursor %+v, want line 1 column 2 (vim's 1,3)", res.Cursor)
	}
	if got := s.dump(); got != "  lead|trail  |\tmix\t" {
		t.Errorf("the buffer changed: %q", got)
	}
}

// TestConfirmDeclinedPrintsNothing. The print flags fire on a substitution
// having been made, so ":s/x/y/cp" answered no to every prompt prints nothing.
// Measured, and the pair of the ":s/x/y/np" case in the table next door.
func TestConfirmDeclinedPrintsNothing(t *testing.T) {
	s := newSession("  lead", "trail  ")
	cmd, err := Parse(KindSubstitute, `/\w\+/X/cp`, &s.st, &s.opt)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Do(Request{
		Buf: s.buf, First: 2, Last: 2, Cmd: cmd, Cursor: s.cur, Opt: &s.opt, State: &s.st,
		Confirm: func(Prompt) Answer { return No },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Print != "" {
		t.Errorf("printed %q with nothing substituted", res.Print)
	}
	if res.Message != "" {
		t.Errorf("said %q with nothing substituted", res.Message)
	}
}

// TestPrintFlags is the "p", "l" and "#" output, measured on a line reading
// "\tX2 " in a three-line buffer with 'tabstop' at 8.
func TestPrintFlags(t *testing.T) {
	for _, c := range []struct{ flags, want string }{
		{"p", "        X2 "},
		{"l", "^IX2 $"},
		{"#", "  2         X2 "},
		{"#l", "  2 ^IX2 $"},
	} {
		t.Run(c.flags, func(t *testing.T) {
			s := newSession("a1", "\ta2 ", "a3")
			if err := s.sub(t, KindSubstitute, "2", "/a/X/"+c.flags); err != nil {
				t.Fatal(err)
			}
			if len(s.msg) != 1 {
				t.Fatalf("messages %q, want one line", s.msg)
			}
			if s.msg[0] != c.want {
				t.Errorf("printed %q, want %q", s.msg[0], c.want)
			}
		})
	}
}

// TestNumberWidthGrowsWithTheBuffer. Vim's "#" flag right-aligns the number in
// 'numberwidth' minus one columns, or in as many as the last line number
// needs, whichever is more: a 149-line file prints "120 " and a six-line file
// prints " 3 ".
func TestNumberWidthGrowsWithTheBuffer(t *testing.T) {
	small := text.Read([]byte("a\nb\nc\n"))
	o := options.Defaults()
	if got := numberWidth(small, &o); got != 3 {
		t.Errorf("numberWidth of a 3-line buffer = %d, want 3", got)
	}
	var big []byte
	for i := 0; i < 1500; i++ {
		big = append(big, 'a', '\n')
	}
	if got := numberWidth(text.Read(big), &o); got != 4 {
		t.Errorf("numberWidth of a 1500-line buffer = %d, want 4", got)
	}
}

// TestFirstAmpersandOfASessionKeepsErrors.
//
// Vim's subflags is a static initialised with do_error TRUE, and "&" inherits
// it, so an "&" before any other ":s" has run still raises E486. A zero Flags
// says the opposite, and it says it silently: the same command after any other
// ":s" is correct, which is what hides it.
//
// Measured on "one\ntwo": ":s/zzz/b/&" as the first substitute of the session
// prints "E486: Pattern not found: zzz".
func TestFirstAmpersandOfASessionKeepsErrors(t *testing.T) {
	s := newSession("one", "two")
	err := s.sub(t, KindSubstitute, "%", "/zzz/b/&")
	if _, ok := err.(NotFoundError); !ok {
		t.Errorf("the first & of a session gave %v, want E486", err)
	}
}

// TestFirstAmpersandDoesNotTakeGdefault.
//
// The seed is vim's static and not the defaults a bare ":s" starts from, and
// the two differ under 'gdefault': vim's "&" skips the reset that reads the
// option, so do_all starts FALSE whatever 'gdefault' says. Measured on "one
// one one" with 'gdefault' set: ":s/o/X/&" leaves "Xne one one".
func TestFirstAmpersandDoesNotTakeGdefault(t *testing.T) {
	s := newSession("one one one")
	s.set(t, "gdefault")
	if err := s.sub(t, KindSubstitute, "%", "/o/X/&"); err != nil {
		t.Fatal(err)
	}
	if got := s.dump(); got != "Xne one one" {
		t.Errorf("buffer %q, want %q", got, "Xne one one")
	}
}
