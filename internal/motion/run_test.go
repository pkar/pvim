package motion

import (
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// The test-side driver: the few dozen lines the mode machine will also need,
// written here so that a test case is a string of keys and not a Request
// literal. It reads a count, looks the motion up by the notation the table is
// keyed on, collects the argument f and ` need, and carries the wanted column
// from one motion to the next the way the editor will.

// runState is where a key sequence left the cursor.
type runState struct {
	Pos      text.Pos
	Kind     Kind
	Ok       bool
	NoAdjust bool
	Ctx      *Context
}

// runKeys types keys at a buffer and reports where the cursor ended up.
//
// pending and op say whether an operator is waiting and which one, because
// three motions answer differently when one is: w stops at the end of a line,
// cw is ce, and <BS> across a line boundary lands after the last byte.
func runKeys(t *testing.T, b *text.Buffer, ctx *Context, from text.Pos, keys string, opt Options, pending bool, op byte) runState {
	t.Helper()
	st := runState{Pos: from, Ok: true, Ctx: ctx}
	for i := 0; i < len(keys); {
		count := 0
		for i < len(keys) && keys[i] >= '0' && keys[i] <= '9' && !(keys[i] == '0' && count == 0) {
			count = count*10 + int(keys[i]-'0')
			i++
		}
		if i >= len(keys) {
			t.Fatalf("keys %q end in a count", keys)
		}
		name := keys[i : i+1]
		if name == "<" {
			if j := strings.IndexByte(keys[i:], '>'); j > 0 {
				name = keys[i : i+j+1]
			}
		}
		if strings.ContainsAny(name, "g[]") && i+1 < len(keys) {
			if _, ok := ByKeys(keys[i : i+2]); ok {
				name = keys[i : i+2]
			}
		}
		m, ok := ByKeys(name)
		if !ok {
			t.Fatalf("no motion bound to %q in %q", name, keys)
		}
		i += len(name)
		var arg byte
		if m.NeedsArg {
			if i >= len(keys) {
				t.Fatalf("keys %q end before %s's argument", keys, name)
			}
			arg = keys[i]
			i++
		}
		res := m.Do(Request{
			Buf: b, Ctx: ctx, From: st.Pos, Count: count, Arg: arg,
			Pending: pending, Op: op, Opt: opt,
		})
		// A motion that fails beeps and the next key still runs, so the
		// sequence carries on from where the cursor already is. Only the last
		// motion's answer is reported.
		// The wanted column is read whether or not the motion happened: $ on
		// the last line of the buffer beeps and still leaves j aiming at the
		// end of the line.
		if res.Curswant != CurswantKeep {
			ctx.Curswant = res.Curswant
		}
		st.Ok = res.Ok
		if !res.Ok {
			continue
		}
		st.Pos, st.Kind, st.NoAdjust = res.To, res.Kind, res.NoAdjust
	}
	return st
}

// vimKeys turns the notation runKeys reads into the bytes a vim script has to
// hold: the two motions bound to a control character and the two bound to a
// character a shell would eat.
func vimKeys(keys string) string {
	r := strings.NewReplacer(
		"<BS>", "\x08",
		"<Space>", " ",
		"<CR>", "\r",
		"<NL>", "\n",
		"<C-N>", "\x0e",
		"<C-P>", "\x10",
	)
	return r.Replace(keys)
}
