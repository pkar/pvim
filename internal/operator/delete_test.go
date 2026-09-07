package operator

import (
	"testing"

	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// TestDeleteCharwise is dw, d$, x and X: the register comes back charwise and
// the cursor sits on the first character that went, backed onto the last
// character of the line when the delete took the end of it.
func TestDeleteCharwise(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		span       Span
		want       string
		line, col  int
		wantReg    string
		wantRegTyp register.Type
	}{
		{
			// dw on "hello world"
			name:    "dw",
			in:      "hello world\nsecond line\nthird\n",
			span:    Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(1, 7)}},
			want:    "world\nsecond line\nthird",
			line:    1,
			col:     1,
			wantReg: "hello ",
		},
		{
			// ll d$ on "hello world" leaves "he" with the cursor on the "e",
			// not one past it.
			name:    "d$ from column 3",
			in:      "hello world\nsecond line\nthird\n",
			span:    Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 3), End: at(1, 12)}},
			want:    "he\nsecond line\nthird",
			line:    1,
			col:     2,
			wantReg: "llo world",
		},
		{
			// $x: the last character of the line, cursor backs up.
			name:    "x at the end of the line",
			in:      "hello world\n",
			span:    Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 11), End: at(1, 12)}},
			want:    "hello worl",
			line:    1,
			col:     10,
			wantReg: "d",
		},
		{
			// $d0 from the last column deletes everything before it.
			name:    "d0",
			in:      "hello world\n",
			span:    Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(1, 11)}},
			want:    "d",
			line:    1,
			col:     1,
			wantReg: "hello worl",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := buf(tc.in)
			regs := &recorder{}
			got, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Span: tc.span, At: tc.span.Range.Start, Opt: lineOpts()})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if dump(b) != tc.want {
				t.Errorf("buffer\n%q\nwant\n%q", dump(b), tc.want)
			}
			checkCursor(t, got.Cursor, tc.line, tc.col)
			if regs.deletes != 1 {
				t.Fatalf("%d calls to Delete, want 1", regs.deletes)
			}
			if regs.deleted.Type != register.TypeChar {
				t.Errorf("register type %v, want charwise", regs.deleted.Type)
			}
			if s := regs.deleted.String(); s != tc.wantReg {
				t.Errorf("register %q, want %q", s, tc.wantReg)
			}
		})
	}
}

// TestDeleteLinewise is dd, 2dd and dj. The cursor lands on the first non-blank
// of whatever line is under it afterwards, which is the rule an implementation
// that assigns column 0 gets wrong on every indented file.
func TestDeleteLinewise(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		first, last int
		want        string
		line, col   int
		msg         string
	}{
		{"dd on line 1", "hello world\nsecond line\nthird\n", 1, 1, "second line\nthird", 1, 1, ""},
		{"j dd", "hello world\nsecond line\nthird\n", 2, 2, "hello world\nthird", 2, 1, ""},
		{"G dd off the end", "hello world\nsecond line\nthird\n", 3, 3, "hello world\nsecond line", 2, 1, ""},
		{"2dd", "hello world\nsecond line\nthird\n", 1, 2, "third", 1, 1, ""},
		{"dd onto an indent", "\tone\n    two\n\tthree\nfour\n", 2, 2, "\tone\n\tthree\nfour", 2, 2, ""},
		{"dj onto an indent", "\tone\n    two\n\tthree\nfour\n", 1, 2, "\tthree\nfour", 1, 2, ""},
		{"4dd reports", "a\nb\nc\nd\ne\nf\n", 1, 4, "e\nf", 1, 1, "4 fewer lines"},
		{"dd emptying the buffer", "one\n", 1, 1, "", 1, 1, "--No lines in buffer--"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := buf(tc.in)
			regs := &recorder{}
			span := SpanForLines(b, tc.first, tc.last-tc.first+1)
			got, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Span: span, At: at(tc.first, 1), Opt: lineOpts()})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if dump(b) != tc.want {
				t.Errorf("buffer\n%q\nwant\n%q", dump(b), tc.want)
			}
			checkCursor(t, got.Cursor, tc.line, tc.col)
			if got.Message != tc.msg {
				t.Errorf("message %q, want %q", got.Message, tc.msg)
			}
			if regs.deleted.Type != register.TypeLine {
				t.Errorf("register type %v, want linewise", regs.deleted.Type)
			}
		})
	}
}

// TestDeleteBecomesLinewise is op_delete's rule that a charwise delete from a
// line's indent to the end of another line is really linewise.
//
// 2D on "hello world / second line / third" from column 1 leaves "third" alone
// in the buffer and a LINEWISE unnamed register; from column 3 it leaves "he"
// and a charwise one; and 2C from column 1 leaves an empty first line and a
// charwise register, because the rule is d's and not c's. All three were run.
func TestDeleteBecomesLinewise(t *testing.T) {
	const in = "hello world\nsecond line\nthird\n"

	t.Run("2D from column 1", func(t *testing.T) {
		b := buf(in)
		regs := &recorder{}
		got, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Span: SpanForToEndOfLine(b, at(1, 1), 2), At: at(1, 1), Opt: lineOpts()})
		if err != nil {
			t.Fatal(err)
		}
		if dump(b) != "third" {
			t.Errorf("buffer %q, want %q", dump(b), "third")
		}
		if regs.deleted.Type != register.TypeLine {
			t.Errorf("register type %v, want linewise", regs.deleted.Type)
		}
		checkCursor(t, got.Cursor, 1, 1)
	})

	t.Run("2D from column 3", func(t *testing.T) {
		b := buf(in)
		regs := &recorder{}
		got, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Span: SpanForToEndOfLine(b, at(1, 3), 2), At: at(1, 3), Opt: lineOpts()})
		if err != nil {
			t.Fatal(err)
		}
		if dump(b) != "he\nthird" {
			t.Errorf("buffer %q, want %q", dump(b), "he\nthird")
		}
		if regs.deleted.Type != register.TypeChar {
			t.Errorf("register type %v, want charwise", regs.deleted.Type)
		}
		if s := regs.deleted.String(); s != "llo world\nsecond line" {
			t.Errorf("register %q", s)
		}
		checkCursor(t, got.Cursor, 1, 2)
	})

	t.Run("2C from column 1 stays charwise", func(t *testing.T) {
		b := buf(in)
		regs := &recorder{}
		got, err := Apply(Request{Buf: b, Regs: regs, Op: OpChange, Span: SpanForToEndOfLine(b, at(1, 1), 2), At: at(1, 1), Opt: lineOpts()})
		if err != nil {
			t.Fatal(err)
		}
		if dump(b) != "\nthird" {
			t.Errorf("buffer %q, want %q", dump(b), "\nthird")
		}
		if regs.deleted.Type != register.TypeChar {
			t.Errorf("register type %v, want charwise", regs.deleted.Type)
		}
		if !got.Insert {
			t.Error("c did not ask for insert mode")
		}
		checkCursor(t, got.Cursor, 1, 1)
	})

	t.Run("visual is exempt", func(t *testing.T) {
		b := buf(in)
		regs := &recorder{}
		if _, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Visual: true, Span: SpanForToEndOfLine(b, at(1, 1), 2), At: at(1, 1), Opt: lineOpts()}); err != nil {
			t.Fatal(err)
		}
		if regs.deleted.Type != register.TypeChar {
			t.Errorf("register type %v, want charwise: the promotion is normal mode's only", regs.deleted.Type)
		}
	})
}

// TestChangeLinewise is the c that catches people out. It does not delete the
// lines: it replaces all of them with one line holding the first line's indent
// under 'autoindent', or an empty one without it, and starts insert there.
func TestChangeLinewise(t *testing.T) {
	const in = "\tone\n    two\n\tthree\nfour\n"

	t.Run("cc with autoindent", func(t *testing.T) {
		b := buf(in)
		opt := lineOpts()
		opt.AutoIndent = true
		got, err := Apply(Request{Buf: b, Regs: &recorder{}, Op: OpChange, Span: SpanForLines(b, 2, 1), At: at(2, 1), Opt: opt})
		if err != nil {
			t.Fatal(err)
		}
		if dump(b) != "\tone\n    \n\tthree\nfour" {
			t.Errorf("buffer %q", dump(b))
		}
		checkCursor(t, got.Cursor, 2, 5)
		if !got.Insert {
			t.Error("cc did not ask for insert mode")
		}
	})

	t.Run("cc without autoindent", func(t *testing.T) {
		b := buf(in)
		got, err := Apply(Request{Buf: b, Regs: &recorder{}, Op: OpChange, Span: SpanForLines(b, 2, 1), At: at(2, 1), Opt: lineOpts()})
		if err != nil {
			t.Fatal(err)
		}
		if dump(b) != "\tone\n\n\tthree\nfour" {
			t.Errorf("buffer %q", dump(b))
		}
		checkCursor(t, got.Cursor, 2, 1)
	})

	t.Run("cj with autoindent leaves one line", func(t *testing.T) {
		b := buf(in)
		opt := lineOpts()
		opt.AutoIndent = true
		got, err := Apply(Request{Buf: b, Regs: &recorder{}, Op: OpChange, Span: SpanForLines(b, 1, 2), At: at(1, 1), Opt: opt})
		if err != nil {
			t.Fatal(err)
		}
		if dump(b) != "\t\n\tthree\nfour" {
			t.Errorf("buffer %q", dump(b))
		}
		checkCursor(t, got.Cursor, 1, 2)
		if got.Message != "" {
			t.Errorf("message %q, want none: one line less is at 'report'", got.Message)
		}
	})
}

// TestYankCursor is the two-part rule. A charwise yank moves the cursor to the
// start of what it took; a linewise yank moves only the LINE, so w yy on
// "hello world" leaves the cursor in column 7 and not column 1.
func TestYankCursor(t *testing.T) {
	const in = "hello world\nsecond line\nthird\n"

	t.Run("w yy keeps the column", func(t *testing.T) {
		b := buf(in)
		regs := &recorder{}
		got, err := Apply(Request{Buf: b, Regs: regs, Op: OpYank, Span: SpanForLines(b, 1, 1), At: at(1, 7), Opt: lineOpts()})
		if err != nil {
			t.Fatal(err)
		}
		checkCursor(t, got.Cursor, 1, 7)
		if regs.yanked.Type != register.TypeLine || regs.yanked.String() != "hello world\n" {
			t.Errorf("register %q %v", regs.yanked.String(), regs.yanked.Type)
		}
	})

	t.Run("G yk moves up and keeps the column", func(t *testing.T) {
		b := buf(in)
		got, err := Apply(Request{Buf: b, Regs: &recorder{}, Op: OpYank, Span: SpanForLines(b, 2, 2), At: at(3, 1), Opt: lineOpts()})
		if err != nil {
			t.Fatal(err)
		}
		checkCursor(t, got.Cursor, 2, 1)
	})

	t.Run("$ yb moves back to the start", func(t *testing.T) {
		b := buf(in)
		span := Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 7), End: at(1, 11)}}
		got, err := Apply(Request{Buf: b, Regs: &recorder{}, Op: OpYank, Span: span, At: at(1, 11), Opt: lineOpts()})
		if err != nil {
			t.Fatal(err)
		}
		checkCursor(t, got.Cursor, 1, 7)
	})

	t.Run("w yw stays put", func(t *testing.T) {
		b := buf(in)
		span := Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 7), End: at(1, 12)}}
		got, err := Apply(Request{Buf: b, Regs: &recorder{}, Op: OpYank, Span: span, At: at(1, 7), Opt: lineOpts()})
		if err != nil {
			t.Fatal(err)
		}
		checkCursor(t, got.Cursor, 1, 7)
		if dump(b) != "hello world\nsecond line\nthird" {
			t.Error("a yank changed the buffer")
		}
	})
}

// TestYankMessage is op_yank's report, including the two things about it that
// are not guessable: a charwise yank of one line says nothing whatever 'report'
// is, and a named register is spelled with an opening quote and no closing one.
func TestYankMessage(t *testing.T) {
	cases := []struct {
		name string
		span Span
		reg  byte
		want string
	}{
		{"3yy", SpanForLines(buf("a\nb\nc\nd\n"), 1, 3), 0, "3 lines yanked"},
		{`"a3yy`, SpanForLines(buf("a\nb\nc\nd\n"), 1, 3), 'a', `3 lines yanked into "a`},
		{"2yy is at 'report'", SpanForLines(buf("a\nb\nc\nd\n"), 1, 2), 0, ""},
		{
			"charwise over three lines",
			Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(3, 2)}},
			0, "3 lines yanked",
		},
		{
			"charwise within one line",
			Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(1, 2)}},
			0, "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := buf("a\nb\nc\nd\n")
			got, err := Apply(Request{Buf: b, Regs: &recorder{}, Op: OpYank, Span: tc.span, Register: tc.reg, At: at(1, 1), Opt: lineOpts()})
			if err != nil {
				t.Fatal(err)
			}
			if got.Message != tc.want {
				t.Errorf("message %q, want %q", got.Message, tc.want)
			}
		})
	}
}

// TestEmptySpanDoesNothing: d0 in column 0 covers nothing. Measured through
// cmd/oracle, vim does not beep at it: an empty region is only an error when
// 'cpoptions' holds E and the default does not, so op_delete runs, moves no
// bytes, writes no register and returns. The undo header it numbers on the way
// is the caller's business and shows up in undotree().seq_cur.
func TestEmptySpanDoesNothing(t *testing.T) {
	b := buf("hello\n")
	regs := &recorder{}
	span := Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(1, 1)}}
	if _, err := Apply(Request{Buf: b, Regs: regs, Op: OpDelete, Span: span, At: at(1, 1), Opt: lineOpts()}); err != nil {
		t.Fatalf("error %v, want none", err)
	}
	if regs.sawAnyCall {
		t.Error("an empty delete wrote a register")
	}
	if dump(b) != "hello" {
		t.Errorf("buffer %q", dump(b))
	}
}

// TestNumberedRegisterFlagTravels: the motion decides whether a small delete
// still goes to "1, and this package's only job is to pass the answer through
// without deciding anything of its own.
func TestNumberedRegisterFlagTravels(t *testing.T) {
	for _, want := range []bool{false, true} {
		b := buf("hello world\n")
		regs := &recorder{}
		span := Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(1, 6)}}
		if _, err := Apply(Request{
			Buf: b, Regs: regs, Op: OpDelete, Span: span, At: at(1, 1),
			NumberedRegister: want, Opt: lineOpts(),
		}); err != nil {
			t.Fatal(err)
		}
		if regs.useRegOne != want {
			t.Errorf("useRegOne %v, want %v", regs.useRegOne, want)
		}
	}
}

// TestNilRegistersEdits: a caller with nowhere to put the text still gets the
// edit. It is what a test wants and what a delete into "_ already amounts to.
func TestNilRegistersEdits(t *testing.T) {
	b := buf("hello world\n")
	span := Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(1, 7)}}
	if _, err := Apply(Request{Buf: b, Op: OpDelete, Span: span, At: at(1, 1), Opt: lineOpts()}); err != nil {
		t.Fatal(err)
	}
	if dump(b) != "world" {
		t.Errorf("buffer %q", dump(b))
	}
}

// TestDeleteOnEmptyLineWritesNoRegister is d$ and dg_ on a blank line, the
// register half of vim's "Check for trying to delete (e.g. "D") in an empty
// line": op_delete returns before the yank, so whatever was in the unnamed
// register is still there afterwards.
//
// Run on "alpha beta\n\ngamma\n" with "ywj$d$p": vim leaves line 2 as
// "alpha " because the yw survived, and it prints `reg "\tv\talpha ` in the
// state dump where pvim printed a charwise empty value.
func TestDeleteOnEmptyLineWritesNoRegister(t *testing.T) {
	for _, tc := range []struct {
		name string
		span func(b *text.Buffer) Span
	}{
		{"d$", func(b *text.Buffer) Span {
			s, err := SpanForMotion(b, at(2, 1), motionAt(2, 1, true), motion.ForceNone, lineOpts())
			if err != nil {
				t.Fatal(err)
			}
			return s
		}},
		{"D", func(b *text.Buffer) Span { return SpanForToEndOfLine(b, at(2, 1), 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := buf("alpha\n\nbravo\n")
			rec := &recorder{}
			got, err := Apply(Request{Buf: b, Regs: rec, Op: OpDelete, Span: tc.span(b), At: at(2, 1), Opt: lineOpts()})
			if err != nil {
				t.Fatal(err)
			}
			if rec.sawAnyCall {
				t.Errorf("wrote a register: %q %v", rec.deleted.String(), rec.deleted.Type)
			}
			if dump(b) != "alpha\n\nbravo" {
				t.Errorf("buffer %q", dump(b))
			}
			if len(b.ChangeList()) != 0 {
				t.Errorf("changelist %v, want nothing recorded", b.ChangeList())
			}
			checkCursor(t, got.Cursor, 2, 1)
		})
	}
}

// TestDeleteInclusiveOntoEmptyLineIsLinewise is 2dg_ over a line and a blank
// one. op_delete's "imitate the strange Vi behaviour" rule promotes a
// multi-line charwise delete to linewise when what is left of the last line is
// blank and the start is in the indent, and a span whose end was inflated past
// the end of an empty line never reaches it. Run on "abc\n\nxyz\n": vim
// leaves "xyz" and a linewise register holding "abc\n\n".
func TestDeleteInclusiveOntoEmptyLineIsLinewise(t *testing.T) {
	b := buf("abc\n\nxyz\n")
	span, err := SpanForMotion(b, at(1, 1), motionAt(2, 1, true), motion.ForceNone, lineOpts())
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	if _, err := Apply(Request{Buf: b, Regs: rec, Op: OpDelete, Span: span, At: at(1, 1), Opt: lineOpts()}); err != nil {
		t.Fatal(err)
	}
	if dump(b) != "xyz" {
		t.Errorf("buffer %q, want the two lines gone", dump(b))
	}
	if rec.deleted.Type != register.TypeLine || rec.deleted.String() != "abc\n\n" {
		t.Errorf("register %v %q, want linewise \"abc\\n\\n\"", rec.deleted.Type, rec.deleted.String())
	}
}

// TestYankEmptyRegion is y0 in column one. An empty region is not an error to
// y: op_yank runs on it and the register comes back holding one empty line, so
// getregtype('"') answers "v" where it answered "" before and the next p
// pastes nothing rather than pasting what was there before.
//
// Run on "hello world\n" with "y0P": vim's state dump prints `reg "\tv\t` and
// no message at all, where returning ErrNoRange here printed
// "E353: Nothing in register "" and left the previous yank in place.
func TestYankEmptyRegion(t *testing.T) {
	b := buf("hello world\n")
	rec := &recorder{}
	span := Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(1, 1)}}
	got, err := Apply(Request{Buf: b, Regs: rec, Op: OpYank, Span: span, At: at(1, 1), Opt: lineOpts()})
	if err != nil {
		t.Fatalf("error %v, want an empty yank", err)
	}
	if rec.yanks != 1 {
		t.Fatalf("%d yanks, want one", rec.yanks)
	}
	if rec.yanked.Type != register.TypeChar || len(rec.yanked.Lines) != 1 || len(rec.yanked.Lines[0]) != 0 {
		t.Errorf("register %v %q, want one charwise empty line", rec.yanked.Type, rec.yanked.String())
	}
	if got.Message != "" {
		t.Errorf("message %q, want none", got.Message)
	}
	checkCursor(t, got.Cursor, 1, 1)
}

// TestYankOnEmptyLineYanksNothing is y$ on a blank line, which is the same
// empty region as y0 reached the other way. Only a change reads
// EndsOnEmptyLine, because op_change is the one that has to tell an inclusive
// empty region from an exclusive one; op_yank has no shortcut for either and
// writes the empty value both times.
//
// Run on "alpha beta\n\ngamma\n" with "ywjy$P": vim leaves the buffer alone,
// prints nothing, and the state dump comes back `reg "\tv\t` with the yanked
// word gone.
func TestYankOnEmptyLineYanksNothing(t *testing.T) {
	b := buf("alpha\n\nbravo\n")
	span, err := SpanForMotion(b, at(2, 1), motionAt(2, 1, true), motion.ForceNone, lineOpts())
	if err != nil {
		t.Fatal(err)
	}
	if !span.EndsOnEmptyLine {
		t.Fatal("the span under test is not the one y$ builds on a blank line")
	}
	rec := &recorder{}
	got, err := Apply(Request{Buf: b, Regs: rec, Op: OpYank, Span: span, At: at(2, 1), Opt: lineOpts()})
	if err != nil {
		t.Fatalf("error %v, want an empty yank", err)
	}
	if rec.yanks != 1 {
		t.Fatalf("%d yanks, want one", rec.yanks)
	}
	if rec.yanked.Type != register.TypeChar || len(rec.yanked.Lines) != 1 || len(rec.yanked.Lines[0]) != 0 {
		t.Errorf("register %v %q, want one charwise empty line", rec.yanked.Type, rec.yanked.String())
	}
	if got.Message != "" {
		t.Errorf("message %q, want none", got.Message)
	}
	if dump(b) != "alpha\n\nbravo" {
		t.Errorf("buffer %q", dump(b))
	}
	checkCursor(t, got.Cursor, 2, 1)
}

// TestChangeOnEmptyLineYanksNothing is C on a blank line, and it is the exact
// mirror of the delete above. op_change does not take op_delete's empty-line
// shortcut -- that check is guarded by op_type == OP_DELETE -- so the yank
// before the delete still happens and the unnamed register comes back charwise
// empty.
//
// Run on "alpha beta\n\ngamma\n" with "ywjCX<Esc>p": vim leaves line 2 as "X"
// because the C emptied the register, and pvim left "Xalpha " because it did
// not.
func TestChangeOnEmptyLineYanksNothing(t *testing.T) {
	b := buf("alpha\n\nbravo\n")
	rec := &recorder{}
	got, err := Apply(Request{Buf: b, Regs: rec, Op: OpChange, Span: SpanForToEndOfLine(b, at(2, 1), 1), At: at(2, 1), Opt: lineOpts()})
	if err != nil {
		t.Fatal(err)
	}
	if rec.deletes != 1 {
		t.Fatalf("%d register writes, want one", rec.deletes)
	}
	if rec.deleted.Type != register.TypeChar || len(rec.deleted.Lines) != 1 || len(rec.deleted.Lines[0]) != 0 {
		t.Errorf("register %v %q, want one charwise empty line", rec.deleted.Type, rec.deleted.String())
	}
	if !got.Insert {
		t.Error("C did not start insert mode")
	}
	if dump(b) != "alpha\n\nbravo" {
		t.Errorf("buffer %q", dump(b))
	}
}

// TestChangeEmptyExclusiveKeepsRegister is c0 in column one, where the same
// zero-width span means the opposite thing. There oap->empty is set, op_delete
// returns before the yank, and the register keeps what it had. Measured both
// ways with "yw" first: after "jc0X<Esc>p" the yanked word is still there to
// paste, and after "jCX<Esc>p" it is not.
func TestChangeEmptyExclusiveKeepsRegister(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"c0 on a line with text", "alpha beta\n"},
		{"c0 on a blank line", "\nbravo\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := buf(tc.in)
			span, err := SpanForMotion(b, at(1, 1), motionAt(1, 1, false), motion.ForceNone, lineOpts())
			if err != nil {
				t.Fatal(err)
			}
			rec := &recorder{}
			got, err := Apply(Request{Buf: b, Regs: rec, Op: OpChange, Span: span, At: at(1, 1), Opt: lineOpts()})
			if err != nil {
				t.Fatal(err)
			}
			if rec.sawAnyCall {
				t.Errorf("wrote a register: %q", rec.deleted.String())
			}
			if !got.Insert {
				t.Error("c0 did not start insert mode")
			}
		})
	}
}

// TestYankCountsAnEmptyLastLine is "ly2)" over
// "hello world\nfoo bar baz\n\nlast_word here\n", where the sentence motion
// stops in column 1 of line 4, the exclusive adjustment moves the end onto
// line 3, and line 3 is empty so the end is in column 0 of a line the yank
// does cover. vim says "3 lines yanked"; counting the columns instead says two
// and prints nothing, because two is not more than 'report'.
func TestYankCountsAnEmptyLastLine(t *testing.T) {
	b := buf("hello world\nfoo bar baz\n\nlast_word here\n")
	span, err := SpanForMotion(b, at(1, 2), motionAt(4, 1, false), motion.ForceNone, lineOpts())
	if err != nil {
		t.Fatal(err)
	}
	got, err := Apply(Request{Buf: b, Regs: &recorder{}, Op: OpYank, Span: span, At: at(1, 2), Opt: lineOpts()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Message != "3 lines yanked" {
		t.Errorf("message %q, want %q", got.Message, "3 lines yanked")
	}
}
