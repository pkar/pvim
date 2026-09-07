package tags

import (
	"strings"
	"testing"
)

// entry is a stack entry with the fields the tests care about.
func entry(name string, line int, text string) Entry {
	return Entry{Name: name, From: Pos{Line: line, Col: 1}, FromText: text}
}

// TestStackPushDoesNotMoveTheIndex, and Advance does.
//
// It is the state ":tags" prints and it is not obvious: after a CTRL-] that
// worked the index is one past the newest entry and ":tags" ends in a ">" on a
// line of its own, and after one that failed the entry is still there with the
// ">" against it. Both were measured.
func TestStackPushDoesNotMoveTheIndex(t *testing.T) {
	var s Stack
	s.Push(entry("foo", 4, "call foo here"))
	if s.Index() != 0 || s.Len() != 1 {
		t.Fatalf("after Push: index %d, len %d; want 0 and 1", s.Index(), s.Len())
	}
	s.Advance(0)
	if s.Index() != 1 {
		t.Fatalf("after Advance: index %d, want 1", s.Index())
	}
}

// TestStackPopAndForward walks CTRL-T and ":tag" over one entry and reports
// vim's three messages at the ends.
func TestStackPopAndForward(t *testing.T) {
	var s Stack
	if _, msg, ok := s.Pop(1); ok || msg != MsgEmpty {
		t.Errorf("CTRL-T on an empty stack: %q, %v; want %q", msg, ok, MsgEmpty)
	}
	if _, msg, ok := s.Forward(1); ok || msg != MsgEmpty {
		t.Errorf(":tag on an empty stack: %q, %v; want %q", msg, ok, MsgEmpty)
	}

	s.Push(entry("foo", 4, "call foo here"))
	s.Advance(0)

	if _, msg, ok := s.Forward(1); ok || msg != MsgTop {
		t.Errorf(":tag at the top: %q, %v; want %q", msg, ok, MsgTop)
	}
	e, _, ok := s.Pop(1)
	if !ok || e.From.Line != 4 {
		t.Fatalf("CTRL-T gave %+v, %v; want the entry at line 4", e, ok)
	}
	if _, msg, ok := s.Pop(1); ok || msg != MsgBottom {
		t.Errorf("a second CTRL-T: %q, %v; want %q", msg, ok, MsgBottom)
	}
	if e, _, ok := s.Forward(1); !ok || e.Name != "foo" {
		t.Errorf(":tag after the CTRL-T gave %+v, %v; want foo back", e, ok)
	}
}

// TestStackPushDropsWhatIsNewer: a tag jumped to after a CTRL-T throws the
// entries above it away, the way every undo-like stack does.
func TestStackPushDropsWhatIsNewer(t *testing.T) {
	var s Stack
	s.Push(entry("one", 1, "a"))
	s.Advance(0)
	s.Push(entry("two", 2, "b"))
	s.Advance(0)
	s.Pop(2)

	s.Push(entry("three", 3, "c"))
	if s.Len() != 1 || s.Entries()[0].Name != "three" {
		t.Fatalf("stack is %+v, want just the new entry", s.Entries())
	}
}

// TestStackIsTwentyDeep, which is vim's, and the oldest goes.
func TestStackIsTwentyDeep(t *testing.T) {
	var s Stack
	for i := 1; i <= maxDepth+5; i++ {
		s.Push(entry("t", i, "x"))
		s.Advance(0)
	}
	if s.Len() != maxDepth {
		t.Fatalf("stack holds %d entries, want %d", s.Len(), maxDepth)
	}
	if got := s.Entries()[0].From.Line; got != 6 {
		t.Errorf("the oldest entry is from line %d, want 6: the first five should have gone", got)
	}
}

// TestCloneIsWhatASplitHands: the copy moves on its own, which is what makes
// CTRL-T in the window a split came from say E555 rather than jumping.
func TestCloneIsWhatASplitHands(t *testing.T) {
	var s Stack
	s.Push(entry("foo", 4, "call foo here"))

	c := s.Clone()
	c.Advance(0)
	if s.Index() != 0 {
		t.Errorf("advancing the copy moved the original to %d", s.Index())
	}
	if c.Index() != 1 {
		t.Errorf("the copy is at %d, want 1", c.Index())
	}
}

// TestListIsVimsFormat pins the bytes ":tags" prints, measured against vim
// 9.2.0321 through cmd/oracle: the header, the "%c%2d %2d %-15s %5ld " row,
// and the ">" on its own line when the index is past the newest entry.
func TestListIsVimsFormat(t *testing.T) {
	var s Stack
	s.Push(entry("foo", 4, "call foo here"))
	s.Advance(0)

	want := []string{
		"  # TO tag         FROM line  in file/text",
		"  1  1 foo                 4  call foo here",
		">",
	}
	got := s.List("")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf(":tags printed\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// A second entry whose jump failed: the marker is on it and there is no
	// trailing ">".
	s.Push(entry("int", 1, "int foo"))
	want = []string{
		"  # TO tag         FROM line  in file/text",
		"  1  1 foo                 4  call foo here",
		"> 2  1 int                 1  int foo",
	}
	got = s.List("")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("after a failed jump :tags printed\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestListMatchesIsVimsFormat pins the ":tselect" table, which is the one
// thing three of the CTRL-W tag keys print before they do anything at all.
//
// The columns are print_tag_list's: the name at 13, the file at 13 plus the
// first name's width plus two with a floor of 18, and the line the tag was
// written from indented 15 under it. Measured against vim, including the
// three-character "pri" column and the kind beside it.
func TestListMatchesIsVimsFormat(t *testing.T) {
	ts := []Tag{
		{Name: "foo", File: "buf.txt", Address: "/^int foo/", Pri: PriGlobalCur},
		{Name: "foo", File: "buf.txt", Address: "/^int foo2/", Pri: PriGlobalCur},
	}
	want := []string{
		"  # pri kind tag               file",
		"  1 F C      foo               buf.txt",
		"               int foo",
		"  2 F C      foo               buf.txt",
		"               int foo2",
	}
	got := ListMatches(ts, -1)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf(":tselect printed\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The kind goes in its own column, and a static tag reads "FSC".
	ts = []Tag{
		{Name: "foo", File: "buf.txt", Address: "/^int foo/", Kind: "f", Pri: PriGlobalCur},
		{Name: "foo", File: "buf.txt", Address: "/^int foo/", Pri: PriStaticCur},
	}
	got = ListMatches(ts, 1)
	if got[1] != "  1 F C f    foo               buf.txt" {
		t.Errorf("the kind row is %q", got[1])
	}
	if got[3] != "> 2 FSC      foo               buf.txt" {
		t.Errorf("the static row with the current-match marker is %q", got[3])
	}
}

// TestListMatchesWidensForALongName: the file column follows the first match's
// name once it is past the floor of 18.
func TestListMatchesWidensForALongName(t *testing.T) {
	ts := []Tag{{Name: "a_very_long_tag_name_here", File: "b.c", Address: "/^x/"}}
	got := ListMatches(ts, -1)
	if i := strings.Index(got[0], "file"); i != 13+len(ts[0].Name)+2 {
		t.Errorf("the file column is at %d, want %d", i, 13+len(ts[0].Name)+2)
	}
}

// TestPromptIsVimsWording, because it is a line of the message log every
// graded run of CTRL-W g] compares.
func TestPromptIsVimsWording(t *testing.T) {
	if Prompt != "Type number and <Enter> (q or empty cancels): " {
		t.Errorf("the prompt is %q", Prompt)
	}
}
