package mode

import (
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/text"
)

// tenLines is a file whose lines say which one they are, so a jump that lands
// on the wrong one says so rather than looking like an off-by-one.
const tenLines = "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"

// jumpLines is the list as a string of line numbers plus the index, which is
// short enough to write a want in and long enough to catch a wrong order.
func jumpLines(e *Editor) string {
	list, idx := e.JumpList()
	var b strings.Builder
	for i, j := range list {
		if i > 0 {
			b.WriteByte(' ')
		}
		if i == idx {
			b.WriteByte('>')
		}
		b.WriteString(strings.TrimSpace(itoa(j.Pos.Line)))
	}
	if idx == len(list) {
		b.WriteString(" >")
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestJumpListStartsWithTheOpenPosition: a buffer nobody has jumped in already
// has one entry, because vim's do_ecmd() calls setpcmark() as it starts
// editing a file. Measured on "vim --clean file" with a single "j" typed:
// getjumplist() answers [[{'lnum': 1, ...}], 1].
func TestJumpListStartsWithTheOpenPosition(t *testing.T) {
	e := edit(t, tenLines, "j")
	if got, want := jumpLines(e), "1 >"; got != want {
		t.Errorf("jumplist = %q, want %q", got, want)
	}
}

// TestJumpListPushesAndWalks is the shape of the whole thing: two jumps put
// two positions in, CTRL-O pushes where it was standing so that CTRL-I can
// come back, and the walk stops at each end with a beep and no move.
//
// Every want here was read out of vim's getjumplist() on a 200-line file and
// then written on this ten-line one; the trace is in the case names.
func TestJumpListPushesAndWalks(t *testing.T) {
	cases := []struct {
		name  string
		keys  string
		list  string
		line  int
		beeps bool
	}{
		// "100G50G" leaves [1, 100] with the index past the end.
		{"two jumps", "5G10G", "1 5 >", 10, false},
		// The first CTRL-O pushes the cursor and steps back over it.
		{"one back", "5G10G\x0f", "1 >5 10", 5, false},
		{"and forward again", "5G10G\x0f\x09", "1 5 >10", 10, false},
		{"back to the far end", "5G10G\x0f\x0f", ">1 5 10", 1, false},
		{"past the far end", "5G10G\x0f\x0f\x0f", ">1 5 10", 1, true},
		// The range is checked before the cursor is pushed, so a count that
		// overshoots leaves the list two entries long and the cursor alone.
		{"a count that overshoots", "5G10G9\x0f", "1 5 >", 10, true},
		// CTRL-I with nothing to go forward to.
		{"forward from the end", "5G10G\x09", "1 5 >", 10, true},
		// j is not a jump, so the only entry is the one the file opened with
		// and CTRL-O goes to line 1.
		{"a plain motion pushes nothing", "5Gjjj\x0f", ">1 8", 1, false},
		// The repeat is dropped when the list is READ, keeping the newer one,
		// which is why the order comes out 1, 10, 5 and not 1, 5, 10: the
		// four pushes were 1, 1, 5, 10, 5 and the dedup kept the last of
		// each. vim's "100G50G100G50G" answers [[1, 50, 100], 3], which is
		// this with the two line numbers swapped.
		{"a repeated line is dropped", "5G10G5G10G", "1 10 5 >", 10, false},
		// m' is the documented way to put the cursor in by hand.
		{"m quote pushes", "3Gjm'", "1 4 >", 4, false},
		{"and CTRL-O finds it", "3Gjm'\x0f", "1 >4", 4, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := edit(t, tenLines, tc.keys)
			if got := jumpLines(e); got != tc.list {
				t.Errorf("jumplist = %q, want %q", got, tc.list)
			}
			if got := e.Cursor().Line; got != tc.line {
				t.Errorf("cursor on line %d, want %d", got, tc.line)
			}
			if got := e.Aborted(); got != tc.beeps {
				t.Errorf("Aborted() = %v, want %v", got, tc.beeps)
			}
		})
	}
}

// TestJumpListHoldsAHundred is JUMPLISTSIZE, which is a fixed 100 in vim's C
// and not an option. The oldest goes when the hundred and first arrives.
func TestJumpListHoldsAHundred(t *testing.T) {
	var b strings.Builder
	for i := range 200 {
		b.WriteString(itoa(i + 1))
		b.WriteByte('\n')
	}
	// 150 jumps to distinct lines, each pushing the line it left.
	var keys strings.Builder
	for i := range 150 {
		keys.WriteString(itoa(i + 2))
		keys.WriteByte('G')
	}
	e := edit(t, b.String(), keys.String())
	list, idx := e.JumpList()
	if len(list) != jumpListMax {
		t.Fatalf("jumplist has %d entries, want %d", len(list), jumpListMax)
	}
	if idx != jumpListMax {
		t.Errorf("index = %d, want %d", idx, jumpListMax)
	}
	// The last jump was from line 150, and the oldest survivor is line 51.
	if got := list[len(list)-1].Pos.Line; got != 150 {
		t.Errorf("newest entry is line %d, want 150", got)
	}
	if got := list[0].Pos.Line; got != 51 {
		t.Errorf("oldest entry is line %d, want 51", got)
	}
}

// TestJumpListRoundTrips is what persistence needs: the list read back out of
// a history file goes in as it was and comes out the same.
func TestJumpListRoundTrips(t *testing.T) {
	e := edit(t, tenLines, "")
	want := []Jump{
		{File: "/tmp/a.go", Pos: text.Pos{Line: 40, Col: 3}},
		{File: "/tmp/b.go", Pos: text.Pos{Line: 7, Col: 0}},
	}
	e.SetJumpList(want, 1)
	got, idx := e.JumpList()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("JumpList() = %+v, want %+v", got, want)
	}
	if idx != 1 {
		t.Errorf("index = %d, want 1", idx)
	}
	// An index out of range is clamped rather than trusted: a history file
	// written by another version must not be able to index past the slice.
	e.SetJumpList(want, 99)
	if _, idx := e.JumpList(); idx != 2 {
		t.Errorf("index = %d after an out-of-range set, want 2", idx)
	}
	e.ClearJumps()
	if list, idx := e.JumpList(); len(list) != 0 || idx != 0 {
		t.Errorf("ClearJumps left %+v at %d", list, idx)
	}
}

// TestJumpListDedupKeepsTheFileApart: two entries repeat only when they are in
// the same file as well as on the same line, which is what stops a restored
// list from collapsing lines that were never in the same buffer.
func TestJumpListDedupKeepsTheFileApart(t *testing.T) {
	e := edit(t, tenLines, "")
	e.SetJumpList([]Jump{
		{File: "a", Pos: text.Pos{Line: 3, Col: 0}},
		{File: "b", Pos: text.Pos{Line: 3, Col: 0}},
		{File: "a", Pos: text.Pos{Line: 5, Col: 0}},
	}, 3)
	if list, _ := e.JumpList(); len(list) != 3 {
		t.Errorf("JumpList() collapsed two files onto one line: %+v", list)
	}
	e.SetJumpList([]Jump{
		{File: "a", Pos: text.Pos{Line: 3, Col: 0}},
		{File: "a", Pos: text.Pos{Line: 9, Col: 0}},
		{File: "a", Pos: text.Pos{Line: 3, Col: 1}},
	}, 3)
	list, _ := e.JumpList()
	if len(list) != 2 || list[0].Pos.Line != 9 || list[1].Pos.Line != 3 {
		t.Errorf("JumpList() = %+v, want line 9 then line 3", list)
	}
}

// TestRemapHookRunsMacroKeys: a macro replay goes through the frontend's map
// layer and a dot repeat does not, which is vim's difference between the
// typeahead do_execreg() pushes into and the stuff buffer the redo record
// rides. Without the hook installed both go straight into Key.
func TestRemapHookRunsMacroKeys(t *testing.T) {
	e := edit(t, "abc\n", "qqxq")
	var seen []key.Key
	e.SetRemap(func(k key.Key) error {
		seen = append(seen, k)
		return e.Key(k)
	})
	if err := e.Key(key.Rune('@')); err != nil {
		t.Fatal(err)
	}
	if err := e.Key(key.Rune('q')); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != key.Rune('x') {
		t.Fatalf("the map layer saw %v, want one x", seen)
	}
	if got := string(e.Buffer().Line(1)); got != "c" {
		t.Errorf("line 1 = %q, want %q", got, "c")
	}

	// The dot repeat does not go through it.
	seen = nil
	if err := e.Key(key.Rune('.')); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 {
		t.Errorf("the map layer saw a dot repeat: %v", seen)
	}
}

// TestAbortedFollowsTheBeep is what the map layer asks about: vim throws the
// rest of a right-hand side away when a command in it fails, and a failed
// search fails by beeping and not by saying anything the caller can see.
func TestAbortedFollowsTheBeep(t *testing.T) {
	e := edit(t, "abc\n", "")
	if e.Aborted() {
		t.Error("Aborted() is true before anything ran")
	}
	if err := e.Key(key.Rune('h')); err != nil {
		t.Fatal(err)
	}
	if !e.Aborted() {
		t.Error("h in column zero did not set Aborted()")
	}
	if err := e.Key(key.Rune('l')); err != nil {
		t.Fatal(err)
	}
	if e.Aborted() {
		t.Error("Aborted() stayed set over a command that worked")
	}
}

// TestJumpListFollowsTheText is the half of vim's mark_adjust() that reaches
// into a window: a jumplist entry below an edit moves with the lines, and an
// entry ON a line that was deleted is kept and pinned to the top of the hole
// rather than dropped. That second rule is one_adjust_nodel, which is what the
// changelist gets and not what a named mark gets.
//
// Measured: "8G2Gdd<C-O>" lands on line 7 in vim, which is line 8 followed up
// by the delete above it.
func TestJumpListFollowsTheText(t *testing.T) {
	e := edit(t, tenLines, "8G2Gdd")
	if got, want := jumpLines(e), "1 7 >"; got != want {
		t.Errorf("after a delete above it, jumplist = %q, want %q", got, want)
	}
	if err := e.Key(key.Ctrl('o')); err != nil {
		t.Fatal(err)
	}
	if got := e.Cursor().Line; got != 7 {
		t.Errorf("CTRL-O landed on line %d, want 7", got)
	}

	// The entry's own line goes away: it stays in the list, on the line the
	// hole left behind.
	e = edit(t, tenLines, "8G3G8Gdd")
	if got, want := jumpLines(e), "1 8 3 >"; got != want {
		t.Fatalf("with the entry's own line deleted, jumplist = %q, want %q", got, want)
	}
	// The 3G is itself a jump, so it pushes line 8 again; the dd that follows
	// pulls both 8s up to 7 and pins the entry on line 3, and the dedup keeps
	// the newer of each.
	e = edit(t, tenLines, "8G3G8Gdd3Gdd")
	if got, want := jumpLines(e), "1 3 7 >"; got != want {
		t.Errorf("after a second delete above it, jumplist = %q, want %q", got, want)
	}
}
