package substitute

import (
	"strings"
	"testing"
)

// TestMarksSelectsTheLines is ":g" and ":v" over the same six lines vim was
// asked about: ":g/x/d" leaves the y lines and ":v/x/d" leaves the x lines.
func TestMarksSelectsTheLines(t *testing.T) {
	for _, c := range []struct {
		name   string
		args   string
		invert bool
		want   []int
	}{
		{"g", "/x/d", false, []int{1, 3, 5}},
		{"v", "/x/d", true, []int{2, 4, 6}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSession("x1", "y2", "x3", "y4", "x5", "y6")
			g, err := ParseGlobal(c.args, c.invert)
			if err != nil {
				t.Fatal(err)
			}
			m, pat, err := Marks(s.buf, 1, s.buf.LineCount(), g, &s.st, &s.opt)
			if err != nil {
				t.Fatal(err)
			}
			if pat != "x" {
				t.Errorf("pattern %q, want %q", pat, "x")
			}
			var got []int
			for {
				n, ok := m.Next()
				if !ok {
					break
				}
				got = append(got, n)
			}
			if len(got) != len(c.want) {
				t.Fatalf("lines %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("lines %v, want %v", got, c.want)
				}
			}
		})
	}
}

// TestMarksIsTwoPasses. The marking happens before anything runs, so a command
// that deletes lines does not change which lines were chosen. Deleting them
// one at a time with Shift telling Marked what happened is what internal/ex
// does, and this is that walk.
func TestMarksIsTwoPasses(t *testing.T) {
	s := newSession("x1", "y2", "x3", "y4", "x5", "y6")
	g, _ := ParseGlobal("/x/d", false)
	m, _, err := Marks(s.buf, 1, s.buf.LineCount(), g, &s.st, &s.opt)
	if err != nil {
		t.Fatal(err)
	}
	for {
		n, ok := m.Next()
		if !ok {
			break
		}
		before := s.buf.LineCount()
		s.buf.DeleteLines(n, n)
		m.Shift(s.buf.LineCount() - before)
	}
	if got := s.dump(); got != "y2|y4|y6" {
		t.Errorf("buffer %q, want %q", got, "y2|y4|y6")
	}
}

// TestGlobalMoveReversesTheFile is ":g/^/m0", the case a one-pass
// implementation gets wrong the first time and hangs on the second.
func TestGlobalMoveReversesTheFile(t *testing.T) {
	s := newSession("x1", "y2", "x3", "y4", "x5", "y6")
	g, _ := ParseGlobal("/^/m0", false)
	m, _, err := Marks(s.buf, 1, s.buf.LineCount(), g, &s.st, &s.opt)
	if err != nil {
		t.Fatal(err)
	}
	for {
		n, ok := m.Next()
		if !ok {
			break
		}
		// ":Nm0" by hand: take the line out and put it back at the top.
		line := append([]byte(nil), s.buf.Line(n)...)
		s.buf.DeleteLines(n, n)
		s.buf.InsertLines(1, [][]byte{line})
	}
	if got := s.dump(); got != "y6|x5|y4|x3|y2|x1" {
		t.Errorf("buffer %q, want %q", got, "y6|x5|y4|x3|y2|x1")
	}
}

// TestGlobalNotFoundIsAMessageAndNotAnError.
//
// ":g/zzz/d" says "Pattern not found: zzz" with no E-code, and ":s/zzz/x/"
// says "E486: Pattern not found: zzz" for the same thing. Both measured, and
// the difference is real.
func TestGlobalNotFoundIsAMessageAndNotAnError(t *testing.T) {
	s := newSession("x1", "y2")
	g, _ := ParseGlobal("/zzz/d", false)
	m, pat, err := Marks(s.buf, 1, 2, g, &s.st, &s.opt)
	if err != nil {
		t.Fatal(err)
	}
	if m.Len() != 0 {
		t.Fatalf("marked %d lines, want none", m.Len())
	}
	if got := NotFoundMessage(pat); got != "Pattern not found: zzz" {
		t.Errorf("message %q", got)
	}
	if got := (NotFoundError{Pattern: pat}).Error(); got != "E486: Pattern not found: zzz" {
		t.Errorf("the :s error is %q", got)
	}
}

// TestGlobalWritesBothPatternSlots. Vim saves a ":g" pattern into the search
// slot AND the substitute slot, which is why ":g/x/p" and then ":%s//Q/"
// replaces x. Measured; a one-slot model gets it wrong in silence.
func TestGlobalWritesBothPatternSlots(t *testing.T) {
	s := newSession("x1", "y2", "x3")
	s.st.NoteSearch("nothing")
	g, _ := ParseGlobal("/x/p", false)
	if _, _, err := Marks(s.buf, 1, 3, g, &s.st, &s.opt); err != nil {
		t.Fatal(err)
	}
	if s.st.SearchPattern != "x" || s.st.SubPattern != "x" {
		t.Fatalf("state after :g is search=%q sub=%q, want both x", s.st.SearchPattern, s.st.SubPattern)
	}
	if err := s.sub(t, KindSubstitute, "%", "//Q/"); err != nil {
		t.Fatal(err)
	}
	if got := s.dump(); got != "Q1|y2|Q3" {
		t.Errorf("buffer %q, want %q", got, "Q1|y2|Q3")
	}
}

// TestGlobalHoldsTheSubstituteMessagesBack.
//
// ":g/x/s//Q/" over six lines says "3 substitutions on 3 lines" once and not
// three times: vim's global_busy suppresses every inner message and prints one
// total at the end. Measured.
func TestGlobalHoldsTheSubstituteMessagesBack(t *testing.T) {
	s := newSession("x1", "y2", "x3", "y4", "x5", "y6")
	g, _ := ParseGlobal("/x/s//Q/", false)
	m, _, err := Marks(s.buf, 1, 6, g, &s.st, &s.opt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.st.EnterGlobal(false); err != nil {
		t.Fatal(err)
	}
	for {
		n, ok := m.Next()
		if !ok {
			break
		}
		cmd, err := Parse(KindSubstitute, "//Q/", &s.st, &s.opt)
		if err != nil {
			t.Fatal(err)
		}
		res, err := Do(Request{Buf: s.buf, First: n, Last: n, Cmd: cmd, Opt: &s.opt, State: &s.st})
		if err != nil {
			t.Fatal(err)
		}
		if res.Message != "" {
			t.Errorf("a :s inside a :g printed %q", res.Message)
		}
	}
	if got := s.st.LeaveGlobal(&s.opt); got != "3 substitutions on 3 lines" {
		t.Errorf("the global total is %q", got)
	}
	if got := s.dump(); got != "Q1|y2|Q3|y4|Q5|y6" {
		t.Errorf("buffer %q", got)
	}
}

// TestGlobalSwallowsE486Inside. ":g/x/s/zzz/Q/" over a file full of x is
// silent, not six errors. Measured.
func TestGlobalSwallowsE486Inside(t *testing.T) {
	s := newSession("x1", "y2", "x3")
	if err := s.st.EnterGlobal(false); err != nil {
		t.Fatal(err)
	}
	cmd, err := Parse(KindSubstitute, "/zzz/Q/", &s.st, &s.opt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Do(Request{Buf: s.buf, First: 1, Last: 1, Cmd: cmd, Opt: &s.opt, State: &s.st}); err != nil {
		t.Errorf("a :s inside a :g raised %v", err)
	}
	if got := s.st.LeaveGlobal(&s.opt); got != "" {
		t.Errorf("the global said %q with nothing to say", got)
	}
}

// TestNestedGlobalNeedsNoRange.
//
// E147 says "recursive" and reads as if nesting alone were refused. It is not:
// ":g/x/g/1/d" runs and ":g/x/1,2g/1/d" is the error. Measured, both.
func TestNestedGlobalNeedsNoRange(t *testing.T) {
	var st State
	if err := st.EnterGlobal(false); err != nil {
		t.Fatal(err)
	}
	if err := st.EnterGlobal(false); err != nil {
		t.Errorf("a nested :g with no range was refused: %v", err)
	}
	st.LeaveGlobal(nil)
	if err := st.EnterGlobal(true); err != ErrGlobalRecursive {
		t.Errorf("a nested :g with a range gave %v, want E147", err)
	}
	if got := ErrGlobalRecursive.Error(); got != "E147: Cannot do :global recursive with a range" {
		t.Errorf("E147 reads %q", got)
	}
	st.LeaveGlobal(nil)
	if err := st.EnterGlobal(true); err != nil {
		t.Errorf("a :g with a range at the top level was refused: %v", err)
	}
}

// TestParseGlobalErrors are the two ways a ":g" command line is wrong on its
// own, both measured: nothing after it at all, and a letter for a delimiter.
func TestParseGlobalErrors(t *testing.T) {
	if _, err := ParseGlobal("", false); err != ErrGlobalMissing {
		t.Errorf("empty :g gave %v, want E148", err)
	}
	if got := ErrGlobalMissing.Error(); got != "E148: Regular expression missing from :global" {
		t.Errorf("E148 reads %q", got)
	}
	if _, err := ParseGlobal("xax", false); err != ErrLetterDelim {
		t.Errorf(":gxax gave %v, want E146", err)
	}
	if _, err := ParseGlobal(`\q`, false); err != ErrBackslashDelim {
		t.Errorf(`:g\q gave %v, want E10`, err)
	}
}

// TestParseGlobalBackslashForms are ":g\/cmd" and ":g\&cmd", the two
// spellings that take a remembered pattern instead of a new one and the only
// two that can say which of the two they mean.
func TestParseGlobalBackslashForms(t *testing.T) {
	g, err := ParseGlobal(`\/d`, false)
	if err != nil {
		t.Fatal(err)
	}
	if !g.HavePattern || g.Pattern != "" || g.Which != FromLast || g.Command != "d" {
		t.Errorf(`:g\/d parsed as %+v`, g)
	}
	g, err = ParseGlobal(`\&d`, false)
	if err != nil {
		t.Fatal(err)
	}
	if g.Which != FromSubstitute || g.Command != "d" {
		t.Errorf(`:g\&d parsed as %+v`, g)
	}
}

// TestGlobalDefaultCommandIsPrint. ":g/x/" with nothing after it prints the
// matching lines, which is what makes it a grep. The empty Command is that,
// and internal/ex is what turns it into a print.
func TestGlobalDefaultCommandIsPrint(t *testing.T) {
	g, err := ParseGlobal("/x/", false)
	if err != nil {
		t.Fatal(err)
	}
	if g.Command != "" {
		t.Errorf("command %q, want empty", g.Command)
	}
}

// TestGlobalStillPrintsThePrintFlags.
//
// global_busy holds the COUNT back and nothing else: vim's do_sub skips its
// own message while a ":g" is running and still calls print_line for each
// "p", "l" or "#". Measured on a1, a2, a3:
//
//	:g/a/s//X/p X1 X2 X3 then the total
//	:g/a/s//X/# 1 X1 2 X2 3 X3 then the total
//	:g/a/s//X/l X1$ X2$ X3$ then the total
func TestGlobalStillPrintsThePrintFlags(t *testing.T) {
	for _, c := range []struct {
		flag string
		want []string
	}{
		{"p", []string{"X1", "X2", "X3"}},
		{"#", []string{"  1 X1", "  2 X2", "  3 X3"}},
		{"l", []string{"X1$", "X2$", "X3$"}},
	} {
		t.Run(c.flag, func(t *testing.T) {
			s := newSession("a1", "a2", "a3")
			g, _ := ParseGlobal("/a/s//X/"+c.flag, false)
			m, _, err := Marks(s.buf, 1, 3, g, &s.st, &s.opt)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.st.EnterGlobal(false); err != nil {
				t.Fatal(err)
			}
			var got []string
			for {
				n, ok := m.Next()
				if !ok {
					break
				}
				cmd, err := Parse(KindSubstitute, "//X/"+c.flag, &s.st, &s.opt)
				if err != nil {
					t.Fatal(err)
				}
				res, err := Do(Request{Buf: s.buf, First: n, Last: n, Cmd: cmd, Opt: &s.opt, State: &s.st})
				if err != nil {
					t.Fatal(err)
				}
				if res.Message != "" {
					t.Errorf("a :s inside a :g printed the count %q", res.Message)
				}
				if res.Print != "" {
					got = append(got, res.Print)
				}
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("printed %q, want %q", got, c.want)
			}
			if total := s.st.LeaveGlobal(&s.opt); total != "3 substitutions on 3 lines" {
				t.Errorf("the global total is %q", total)
			}
		})
	}
}
