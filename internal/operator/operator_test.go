package operator

import "testing"

// TestOpString is what 'showcmd' paints in the bottom right and what a dot
// record prints in a test failure. It is a table because the two-key operators
// are where a switch loses an entry.
func TestOpString(t *testing.T) {
	cases := map[Op]string{
		OpNone: "", OpDelete: "d", OpChange: "c", OpYank: "y",
		OpShiftLeft: "<", OpShiftRight: ">", OpIndent: "=",
		OpLower: "gu", OpUpper: "gU", OpToggle: "g~", OpRot13: "g?",
		OpFormat: "gq", OpFormatKeep: "gw", OpFold: "zf", OpJoin: "J",
	}
	for op, want := range cases {
		if got := op.String(); got != want {
			t.Errorf("Op(%d).String() = %q, want %q", op, got, want)
		}
	}
}

// TestChanges decides whether an undo block is opened and whether the buffer
// is marked modified. y and zf are the two operators that read and do not
// write, and an editor that opens an undo step for a yank puts an empty step
// in the tree that u then walks over.
func TestChanges(t *testing.T) {
	for op, want := range map[Op]bool{
		OpNone: false, OpYank: false, OpFold: false,
		OpDelete: true, OpChange: true, OpShiftRight: true, OpUpper: true, OpJoin: true,
	} {
		if got := op.Changes(); got != want {
			t.Errorf("%v.Changes() = %v, want %v", op, got, want)
		}
	}
}

// TestLinewise: <, > and = are linewise whatever motion completed them, so
// >w on the middle of a line shifts the whole line. dd and yy get there a
// different way and are not in this list.
func TestLinewise(t *testing.T) {
	for op, want := range map[Op]bool{
		OpShiftLeft: true, OpShiftRight: true, OpIndent: true,
		OpDelete: false, OpChange: false, OpYank: false, OpFormat: false,
	} {
		if got := op.Linewise(); got != want {
			t.Errorf("%v.Linewise() = %v, want %v", op, got, want)
		}
	}
}

// TestReportZeroNamesEveryChange: 'report' is a number and 0 is the value
// people actually set it to. Rewriting a zero as 2 makes ":set report=0"
// unreachable, and it silences 2dd as well as dd because the boundary is
// strictly greater than. Run with :set report=0 on "abcdef\nghi\n", vim
// prints "1 line less" for dd and "2 fewer lines" for 2dd.
func TestReportZeroNamesEveryChange(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		want  string
	}{
		{"dd", 1, "1 line less"},
		{"2dd", 2, "2 fewer lines"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := buf("abcdef\nghi\njkl\n")
			opt := lineOpts()
			opt.Report = 0
			got, err := Apply(Request{Buf: b, Op: OpDelete, Span: SpanForLines(b, 1, tc.count), At: at(1, 1), Opt: opt})
			if err != nil {
				t.Fatal(err)
			}
			if got.Message != tc.want {
				t.Errorf("message %q, want %q", got.Message, tc.want)
			}
		})
	}
}
