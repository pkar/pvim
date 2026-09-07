package motion

import (
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// The named rules. The sweep in sweep_test.go and the delete sweep in
// kind_test.go compare thousands of positions against vim and say nothing
// about which rule broke; these say which rule, in the words :help uses for
// it, with the buffer that shows it. Every expected value in this file was
// measured by running the same keys through /opt/homebrew/bin/vim.

// deleted runs d{keys} the way kind_test.go does and returns the lines left.
func deleted(t *testing.T, content string, from text.Pos, keys string) string {
	t.Helper()
	b := text.Read([]byte(content))
	opt := DefaultOptions()
	ctx := &Context{Curswant: dispCol(b, from, opt.TabStop)}
	got := runKeys(t, b, ctx, from, keys, opt, true, 'd')
	if !got.Ok {
		t.Fatalf("d%s at %d:%d failed", keys, from.Line, from.Col)
	}
	return strings.Join(applyDelete(b, from, got.Pos, got.Kind, got.NoAdjust), "|")
}

// moved runs keys with no operator and returns where the cursor ended up.
func moved(t *testing.T, content string, from text.Pos, keys string) text.Pos {
	t.Helper()
	b := text.Read([]byte(content))
	opt := DefaultOptions()
	ctx := &Context{Curswant: dispCol(b, from, opt.TabStop)}
	return runKeys(t, b, ctx, from, keys, opt, false, 0).Pos
}

// para is an indented paragraph followed by a blank line, which is the buffer
// :help exclusive uses to explain both of its rules.
const para = "   indented para\n   more text\n\nnext para\n"

// TestExclusiveBecomesInclusive is the first rule under :help exclusive: when
// an exclusive motion ends in column one, the end moves back to the last
// character of the previous line and the motion becomes inclusive.
//
// } from the middle of the first line stops at the blank line; without the
// rule the delete would take that blank line's newline with it and join the
// paragraph to what follows.
func TestExclusiveBecomesInclusive(t *testing.T) {
	got := deleted(t, para, text.Pos{Line: 1, Col: 4}, "}")
	if want := "   i||next para"; got != want {
		t.Errorf("d} one past the first non-blank left %q, vim leaves %q", got, want)
	}
}

// TestExclusiveBecomesLinewise is the second rule: when the same motion also
// started at or before the first non-blank of its line, the whole thing
// becomes linewise, indent included.
//
// The two cases below differ by one column and by two rules: from the first
// non-blank the paragraph goes entirely, from one character later it does not.
func TestExclusiveBecomesLinewise(t *testing.T) {
	for _, tc := range []struct {
		name string
		from text.Pos
		want string
	}{
		{"at the first non-blank", text.Pos{Line: 1, Col: 3}, "|next para"},
		{"before it, in the indent", text.Pos{Line: 1, Col: 0}, "|next para"},
		{"one past it", text.Pos{Line: 1, Col: 4}, "   i||next para"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deleted(t, para, tc.from, "}"); got != tc.want {
				t.Errorf("d} left %q, vim leaves %q", got, tc.want)
			}
		})
	}
}

// TestEmptyLineIsAWord is the second of the three word rules everyone gets
// wrong: an empty line is a word, w stops on it, and dw on one deletes the
// line because the exclusive-linewise rule then fires.
func TestEmptyLineIsAWord(t *testing.T) {
	const buf = "alpha beta\ngamma\n\ndelta epsilon\n"
	if got := moved(t, buf, text.Pos{Line: 2, Col: 4}, "w"); got != (text.Pos{Line: 3, Col: 0}) {
		t.Errorf("w onto the empty line landed at %v, vim lands at 3:0", got)
	}
	if got := deleted(t, buf, text.Pos{Line: 3, Col: 0}, "w"); got != "alpha beta|gamma|delta epsilon" {
		t.Errorf("dw on an empty line left %q, vim deletes the line", got)
	}
}

// TestWordAtEndOfLine is the first: with an operator waiting, w stops after
// the last character of the line rather than running on to the next one, so
// dw on the last word of a line does not pull the following line up.
func TestWordAtEndOfLine(t *testing.T) {
	const buf = "alpha beta\ngamma\n\ndelta epsilon\n"
	if got := deleted(t, buf, text.Pos{Line: 1, Col: 6}, "w"); got != "alpha |gamma||delta epsilon" {
		t.Errorf("dw on the last word of a line left %q, vim leaves \"alpha \"", got)
	}
	if got := moved(t, buf, text.Pos{Line: 1, Col: 6}, "w"); got != (text.Pos{Line: 2, Col: 0}) {
		t.Errorf("w with no operator landed at %v, vim crosses to 2:0", got)
	}
}

// TestChangeWordIsChangeToEndOfWord is the third: cw is ce, unless the cursor
// is on a blank, and it stops inside the word it started in rather than
// running to the end of the next one.
func TestChangeWordIsChangeToEndOfWord(t *testing.T) {
	const buf = "alpha beta\ngamma\n"
	change := func(from text.Pos) (text.Pos, Kind) {
		b := text.Read([]byte(buf))
		opt := DefaultOptions()
		ctx := &Context{Curswant: dispCol(b, from, opt.TabStop)}
		st := runKeys(t, b, ctx, from, "w", opt, true, 'c')
		return st.Pos, st.Kind
	}
	// On the first character of a word: to the end of that word, inclusive.
	if to, kind := change(text.Pos{Line: 1, Col: 0}); to != (text.Pos{Line: 1, Col: 4}) || kind != KindCharInclusive {
		t.Errorf("cw on alpha covers to %v %v, vim covers to 1:4 inclusive", to, kind)
	}
	// On the last character of a word: that one character, not the next word.
	if to, kind := change(text.Pos{Line: 1, Col: 4}); to != (text.Pos{Line: 1, Col: 4}) || kind != KindCharInclusive {
		t.Errorf("cw on the last character covers to %v %v, vim covers 1:4 alone", to, kind)
	}
	// On a blank: w, not e, so only the blank goes.
	if to, kind := change(text.Pos{Line: 1, Col: 5}); to != (text.Pos{Line: 1, Col: 6}) || kind != KindCharExclusive {
		t.Errorf("cw on a blank covers to %v %v, vim covers to 1:6 exclusive", to, kind)
	}
	// And dw on the same blank is the same span, because the rule is about c
	// and not about operators in general.
	if got := deleted(t, buf, text.Pos{Line: 1, Col: 5}, "w"); got != "alphabeta|gamma" {
		t.Errorf("dw on a blank left %q, vim leaves \"alphabeta\"", got)
	}
}

// TestSemicolonAfterTill is the one that changed in vim 8 and that everybody
// remembers wrong: a; repeating a t with no count skips the character it is
// already sitting in front of and stops before the next one. Before vim 8 it
// found the same character again and did not move at all.
//
// The expected columns are what /opt/homebrew/bin/vim does with 'cpo' at its
// default, which does not contain ';'.
func TestSemicolonAfterTill(t *testing.T) {
	const buf = "a.b.c.d\n"
	for _, tc := range []struct {
		keys string
		col  int
	}{
		{"t.", 0},   // already in front of the first dot: no move
		{"t.;", 2},  // skips that dot, stops before the next
		{"t.;;", 4}, // and again
		{"t.,", 0},  // nothing behind it, so the reverse fails
		{"2t.", 2},  // a count does not skip, it counts
		{"t.2;", 2}, // and neither does a repeat with a count
		{"f.", 1},   // f has no skip to make: it lands on the character
		{"f.;", 3},  //
		{"T.", 2},   // from the dot at column 3, backwards
		{"T.;", 2},  // nothing further back, so no move
		{"T.,", 4},  // reversed, and the skip applies to, as well
		{"F.;", 1},  //
	} {
		from := text.Pos{Line: 1, Col: 0}
		if strings.HasPrefix(tc.keys, "T") || strings.HasPrefix(tc.keys, "F") {
			from = text.Pos{Line: 1, Col: 3}
		}
		if got := moved(t, buf, from, tc.keys); got.Col != tc.col {
			t.Errorf("%q from column %d landed at %d, vim lands at %d", tc.keys, from.Col, got.Col, tc.col)
		}
	}
}

// TestPercentIsTwoMotions: a bare % is the matchpair jump and is inclusive
// charwise, and {count}% is a percentage of the file and is linewise. One key,
// two kinds, which is why Result carries its own.
func TestPercentIsTwoMotions(t *testing.T) {
	const buf = "func f() {\n\tone\n\ttwo\n}\nafter\n"
	b := text.Read([]byte(buf))
	opt := DefaultOptions()
	ctx := &Context{}
	m, _ := ByKeys("%")

	res := m.Do(Request{Buf: b, Ctx: ctx, From: text.Pos{Line: 1, Col: 9}, Opt: opt})
	if !res.Ok || res.To != (text.Pos{Line: 4, Col: 0}) || res.Kind != KindCharInclusive {
		t.Errorf("%% on the brace gave %v %v, vim gives 4:0 inclusive", res.To, res.Kind)
	}
	res = m.Do(Request{Buf: b, Ctx: ctx, From: text.Pos{Line: 1, Col: 0}, Count: 50, Opt: opt})
	if !res.Ok || res.To.Line != 3 || res.Kind != KindLine {
		t.Errorf("50%% of a five line file gave %v %v, vim gives line 3 linewise", res.To, res.Kind)
	}
	res = m.Do(Request{Buf: b, Ctx: ctx, From: text.Pos{Line: 1, Col: 0}, Count: 101, Opt: opt})
	if res.Ok {
		t.Error("101% succeeded; vim beeps at anything over 100")
	}
}

// TestMarkKinds: ` is exclusive charwise and ' is linewise, which is the whole
// difference between them and the reason d'a and d`a take different text.
func TestMarkKinds(t *testing.T) {
	const buf = "one\n   two\nthree\n"
	b := text.Read([]byte(buf))
	b.SetMark('a', text.Pos{Line: 2, Col: 5})
	opt := DefaultOptions()
	ctx := &Context{}

	back, _ := ByKeys("`")
	res := back.Do(Request{Buf: b, Ctx: ctx, From: text.Pos{Line: 1, Col: 0}, Arg: 'a', Opt: opt})
	if !res.Ok || res.To != (text.Pos{Line: 2, Col: 5}) || res.Kind != KindCharExclusive || !res.Jump {
		t.Errorf("`a gave %v %v jump=%v, want 2:5 exclusive jump", res.To, res.Kind, res.Jump)
	}
	quote, _ := ByKeys("'")
	res = quote.Do(Request{Buf: b, Ctx: ctx, From: text.Pos{Line: 1, Col: 0}, Arg: 'a', Opt: opt})
	if !res.Ok || res.To != (text.Pos{Line: 2, Col: 3}) || res.Kind != KindLine {
		t.Errorf("'a gave %v %v, want 2:3 linewise", res.To, res.Kind)
	}
	res = quote.Do(Request{Buf: b, Ctx: ctx, From: text.Pos{Line: 1, Col: 0}, Arg: 'z', Opt: opt})
	if res.Ok || res.Message == "" {
		t.Errorf("'z on an unset mark gave %v with message %q, want a failure with E20", res.To, res.Message)
	}
}

// TestIsKeywordChangesWords: 'iskeyword' is what decides where a word ends,
// and taking it as an option rather than a constant is the difference between
// w stopping inside foo_bar and running past it.
func TestIsKeywordChangesWords(t *testing.T) {
	const buf = "foo-bar baz\n"
	b := text.Read([]byte(buf))
	m, _ := ByKeys("w")

	opt := DefaultOptions()
	res := m.Do(Request{Buf: b, Ctx: &Context{}, From: text.Pos{Line: 1, Col: 0}, Opt: opt})
	if res.To.Col != 3 {
		t.Errorf("w with the default 'iskeyword' stopped at %d, want the hyphen at 3", res.To.Col)
	}
	opt.IsKeyword = "@,48-57,_,192-255,-"
	res = m.Do(Request{Buf: b, Ctx: &Context{}, From: text.Pos{Line: 1, Col: 0}, Opt: opt})
	if res.To.Col != 8 {
		t.Errorf("w with - in 'iskeyword' stopped at %d, want baz at 8", res.To.Col)
	}
}

// TestMarkNames: the mark motions take a character and it is not always a
// letter. ” and “ are the position before the latest jump, '. and `. are the
// last change, '^ is where insert mode stopped, and '[ and '] are the ends of
// the last change or yank. Either quote may be used after either, which is why
// both map to the same name.
func TestMarkNames(t *testing.T) {
	for _, tc := range []struct {
		arg  byte
		name byte
	}{
		{'a', 'a'}, {'z', 'z'},
		// A-Z are global marks. They name a file as well as a place, and the
		// file half needs more than one buffer (mark.go:16 says so), but the
		// place half works in a one-file session and vim proves it:
		// "GmAgg'A" on a three-line file leaves the cursor at [0, 3, 1, 0].
		// markName has accepted them since it was written; this list did not,
		// and the list was the wrong one.
		{'A', 'A'}, {'Z', 'Z'},
		{'\'', text.MarkLastJump}, {'`', text.MarkLastJump},
		{'.', text.MarkLastChange}, {'^', text.MarkLastInsert},
		{'[', text.MarkChangeStart}, {']', text.MarkChangeEnd},
	} {
		got, ok := markName(tc.arg)
		if !ok || got != tc.name {
			t.Errorf("markName(%q) = %q %v, want %q true", tc.arg, got, ok, tc.name)
		}
	}
	for _, arg := range []byte{'1', ' ', 0} {
		if _, ok := markName(arg); ok {
			t.Errorf("markName(%q) accepted a name this buffer does not keep", arg)
		}
	}
}

// TestLastChangeMark walks the whole way: an edit sets '. in internal/text,
// and `. and '. come back to it with the two kinds the two keys have.
func TestLastChangeMark(t *testing.T) {
	b := text.Read([]byte("one\n   two three\nfour\n"))
	b.Replace(text.Range{Start: text.Pos{Line: 2, Col: 7}, End: text.Pos{Line: 2, Col: 12}}, []byte("THREE"))
	opt := DefaultOptions()

	back, _ := ByKeys("`")
	res := back.Do(Request{Buf: b, Ctx: &Context{}, From: text.Pos{Line: 1}, Arg: '.', Opt: opt})
	if !res.Ok || res.To.Line != 2 || res.Kind != KindCharExclusive {
		t.Errorf("`. gave %v %v ok=%v, want a charwise exclusive jump to line 2", res.To, res.Kind, res.Ok)
	}
	quote, _ := ByKeys("'")
	res = quote.Do(Request{Buf: b, Ctx: &Context{}, From: text.Pos{Line: 1}, Arg: '.', Opt: opt})
	if !res.Ok || res.To != (text.Pos{Line: 2, Col: 3}) || res.Kind != KindLine {
		t.Errorf("'. gave %v %v, want 2:3 linewise", res.To, res.Kind)
	}
}

// TestBackspaceAcrossALineBoundary is the one motion that turns the exclusive
// adjustment off, and the reason Result carries a flag for it.
//
// 'whichwrap' is "b,s" out of the box, so <BS> in column one goes to the
// previous line and d<BS> there joins the two lines. It does that by putting
// the end of the motion after the previous line's last byte, which is a place
// the exclusive-inclusive rule would immediately move it back from, taking a
// character with it that nobody asked for. The values below are what
// /opt/homebrew/bin/vim does.
func TestBackspaceAcrossALineBoundary(t *testing.T) {
	const buf = "ab\ncd\n   ef\n\ngh\n"
	for _, tc := range []struct {
		from text.Pos
		keys string
		op   byte
		want string
	}{
		// The join: only the line separator goes.
		{text.Pos{Line: 2, Col: 0}, "<BS>", 'd', "abcd|   ef||gh"},
		// The flag holds for the rest of the count: a second <BS> moves left
		// inside the line above and the end still may not be adjusted.
		{text.Pos{Line: 2, Col: 0}, "2<BS>", 'd', "acd|   ef||gh"},
		// A previous line with nothing in it has no last byte to sit after,
		// so the flag is not set and the ordinary rules make this linewise.
		{text.Pos{Line: 5, Col: 0}, "<BS>", 'd', "ab|cd|   ef|gh"},
		// Inside a line it is an ordinary exclusive motion.
		{text.Pos{Line: 3, Col: 3}, "<BS>", 'd', "ab|cd|  ef||gh"},
	} {
		b := text.Read([]byte(buf))
		opt := DefaultOptions()
		ctx := &Context{Curswant: dispCol(b, tc.from, opt.TabStop)}
		got := runKeys(t, b, ctx, tc.from, tc.keys, opt, true, tc.op)
		if !got.Ok {
			t.Fatalf("d%s at %v failed", tc.keys, tc.from)
		}
		lines := strings.Join(applyDelete(b, tc.from, got.Pos, got.Kind, got.NoAdjust), "|")
		if lines != tc.want {
			t.Errorf("d%s at %d:%d left %q, vim leaves %q", tc.keys, tc.from.Line, tc.from.Col, lines, tc.want)
		}
	}
}

// TestEmptyRegionWithAnOperator is vim's nv_left and nv_right: they beep at
// the edge of a line only when nothing is waiting on the motion. The C reads
// "else if (cap->oap->op_type == OP_NOP && n == cap->count1) beep_flush();
// else break;", so with d, c or y pending the loop just ends and the operator
// runs over an empty region. That numbers an undo header over a buffer nobody
// touched, and for c it starts insert, which is why ch in column one is not a
// no-op.
//
// Measured. On "hello\n": `dh` leaves undotree().seq_cur at 1, `chX<Esc>`
// leaves the buffer "Xhello" with "-- INSERT --" on the message line, and `yh`
// leaves the unnamed register an empty charwise yank. On "alpha\n\nbravo\n":
// `jdl` leaves seq_cur at 1. With no operator the same keys beep and leave
// seq_cur at 0.
func TestEmptyRegionWithAnOperator(t *testing.T) {
	for _, tc := range []struct {
		name string
		buf  string
		from text.Pos
		keys string
	}{
		{"h in column one", "hello\n", text.Pos{Line: 1, Col: 0}, "h"},
		{"3h in column one", "hello\n", text.Pos{Line: 1, Col: 0}, "3h"},
		{"<BS> in column one of the first line", "hello\n", text.Pos{Line: 1, Col: 0}, "<BS>"},
		{"l on an empty line", "alpha\n\nbravo\n", text.Pos{Line: 2, Col: 0}, "l"},
		{"3l on an empty line", "alpha\n\nbravo\n", text.Pos{Line: 2, Col: 0}, "3l"},
		{"l on an empty last line", "alpha\n\n", text.Pos{Line: 2, Col: 0}, "l"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opt := DefaultOptions()

			b := text.Read([]byte(tc.buf))
			ctx := &Context{Curswant: dispCol(b, tc.from, opt.TabStop)}
			if runKeys(t, b, ctx, tc.from, tc.keys, opt, false, 0).Ok {
				t.Errorf("%s with no operator succeeded; vim beeps", tc.keys)
			}

			b = text.Read([]byte(tc.buf))
			ctx = &Context{Curswant: dispCol(b, tc.from, opt.TabStop)}
			got := runKeys(t, b, ctx, tc.from, tc.keys, opt, true, 'd')
			if !got.Ok {
				t.Fatalf("d%s failed, so the mode machine beeps and drops the operator; "+
					"vim runs it over the empty region", tc.keys)
			}
			if got.Pos != tc.from {
				t.Errorf("d%s moved to %d:%d, want the region empty at %d:%d",
					tc.keys, got.Pos.Line, got.Pos.Col, tc.from.Line, tc.from.Col)
			}
			if got.Kind != KindCharExclusive {
				t.Errorf("d%s is %s, want exclusive so that the region stays empty", tc.keys, got.Kind)
			}
			if lines := strings.Join(applyDelete(b, tc.from, got.Pos, got.Kind, got.NoAdjust), "|"); lines != unchanged(tc.buf) {
				t.Errorf("d%s left %q, vim leaves the buffer alone", tc.keys, lines)
			}
		})
	}
}

// unchanged is the buffer as applyDelete prints it when nothing was deleted.
func unchanged(content string) string {
	return strings.Join(strings.Split(strings.TrimSuffix(content, "\n"), "\n"), "|")
}

// TestRepeatFindWithNoPreviousFind: ; and, with nothing to repeat beep and
// say nothing. vim's nv_csearch calls clearopbeep() when searchc() finds no
// last_csearch, and clearopbeep() does not touch the message line. E35 is "No
// previous regular expression", it belongs to n, N and a bare //, and
// internal/search prints it there.
//
// Measured on "axbxcxdxexf\n" through the oracle: `;`, `,` and `d;` leave
// vim's redirected message file holding nothing but the newline :redir END
// writes, under both option profiles.
func TestRepeatFindWithNoPreviousFind(t *testing.T) {
	b := text.Read([]byte("axbxcxdxexf\n"))
	for _, keys := range []string{";", ","} {
		m, ok := ByKeys(keys)
		if !ok {
			t.Fatalf("no motion bound to %q", keys)
		}
		for _, pending := range []bool{false, true} {
			res := m.Do(Request{
				Buf: b, Ctx: &Context{}, From: text.Pos{Line: 1, Col: 0},
				Pending: pending, Opt: DefaultOptions(),
			})
			if res.Ok {
				t.Errorf("%q with no previous find succeeded; vim beeps", keys)
			}
			if res.Message != "" {
				t.Errorf("%q printed %q; vim prints nothing at all", keys, res.Message)
			}
		}
	}
}

// TestSentenceCountPastTheEndFails: a count on ( or ) that cannot be met fails
// the whole motion, which abandons the operator. vim's findsent returns FAIL
// rather than stopping at the edge of the buffer, and the linewise family (j,
// k, +, -, _) is the one that clamps.
//
// This is the difference that destroys a file rather than misplacing a cursor.
// 12) clamped to the end of the buffer makes d's span column one of line one
// to the end of the last line, the exclusive-to-linewise rule promotes it, and
// 12d) deletes everything.
//
// Measured on "One two.\nThree.\n" with /opt/homebrew/bin/vim: forwards from
// 1:0, 1) lands at 2:0 and 2) and 3) land on the last character of the buffer,
// and 4) and up leave the cursor where it was; backwards from 2:5, 1( lands at
// 2:0 and 2( at 1:0, and 3( and up leave the cursor where it was. The columns
// below are 0-based, so vim's "2 6" is 2:5 here.
func TestSentenceCountPastTheEndFails(t *testing.T) {
	const buf = "One two.\nThree.\n"
	for _, tc := range []struct {
		from  text.Pos
		keys  string
		to    text.Pos
		fails bool
	}{
		{text.Pos{Line: 1, Col: 0}, ")", text.Pos{Line: 2, Col: 0}, false},
		{text.Pos{Line: 1, Col: 0}, "2)", text.Pos{Line: 2, Col: 5}, false},
		{text.Pos{Line: 1, Col: 0}, "3)", text.Pos{Line: 2, Col: 5}, false},
		{text.Pos{Line: 1, Col: 0}, "4)", text.Pos{}, true},
		{text.Pos{Line: 1, Col: 0}, "12)", text.Pos{}, true},
		{text.Pos{Line: 2, Col: 5}, "(", text.Pos{Line: 2, Col: 0}, false},
		{text.Pos{Line: 2, Col: 5}, "2(", text.Pos{Line: 1, Col: 0}, false},
		{text.Pos{Line: 2, Col: 5}, "3(", text.Pos{}, true},
		{text.Pos{Line: 2, Col: 5}, "12(", text.Pos{}, true},
	} {
		b := text.Read([]byte(buf))
		opt := DefaultOptions()
		ctx := &Context{Curswant: dispCol(b, tc.from, opt.TabStop)}
		got := runKeys(t, b, ctx, tc.from, tc.keys, opt, true, 'd')
		if got.Ok != !tc.fails {
			t.Errorf("%q from %d:%d: ok=%v, vim says ok=%v",
				tc.keys, tc.from.Line, tc.from.Col, got.Ok, !tc.fails)
			continue
		}
		if tc.fails {
			// The operator is abandoned, so the buffer has to survive.
			if lines := strings.Join(applyDelete(b, tc.from, got.Pos, got.Kind, got.NoAdjust), "|"); lines != "One two.|Three." {
				t.Errorf("d%s left %q, vim leaves the buffer alone", tc.keys, lines)
			}
			continue
		}
		if got.Pos != tc.to {
			t.Errorf("%q from %d:%d landed at %d:%d, vim lands at %d:%d",
				tc.keys, tc.from.Line, tc.from.Col,
				got.Pos.Line, got.Pos.Col, tc.to.Line, tc.to.Col)
		}
	}
}
