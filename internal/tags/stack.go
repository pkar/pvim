package tags

import (
	"fmt"
	"strings"
)

// The tag stack: where a jump came from, so CTRL-T can go back to it.
//
// Vim keeps one per window, twenty entries deep, and it is not a stack of
// positions but of tag commands: an entry remembers the name that was looked
// up and every match the lookup found, so that ":tag" and ":tselect" can walk
// the matches of an entry that is already on it without searching again.
//
// The index is the piece worth reading twice, because it is not the depth. It
// points at the entry a CTRL-T would go back to, which after a successful jump
// is one past the newest entry, and the ">" that ":tags" prints on a line of
// its own is what that state looks like. A jump that FAILED leaves the entry
// on the stack with the index still on it, which is why
//
//	 # TO tag FROM line in file/text
//	 1 1 foo 4 call foo here
//	> 2 1 int 1 int foo
//
// is what vim prints after a CTRL-] that worked and a second one that did not.
// Measured, both halves.
const maxDepth = 20

// Messages the stack itself answers with. Vim's capital A in the two "At"
// codes is vim's; it is not a typo here.
const (
	MsgEmpty  = "E73: Tag stack empty"
	MsgBottom = "E555: At bottom of tag stack"
	MsgTop    = "E556: At top of tag stack"
)

// Pos is a position in a file: this package's own, so that the stack can be
// built and tested without a buffer.
type Pos struct {
	Line, Col int
}

// Entry is one tag command on the stack.
type Entry struct {
	// Name is what was looked up.
	Name string
	// From is where the cursor was when the jump started, which is where
	// CTRL-T goes back to.
	From Pos
	// FromFile is the file the jump started in, and FromText the text of the
	// line it started on with leading white space taken off. ":tags" prints
	// the text when the entry is in the file being edited and the file name
	// when it is not, which is vim's fm_getname.
	FromFile string
	FromText string
	// Matches is every tag the lookup found, in the order they are offered.
	Matches []Tag
	// Cur is which of them the jump landed on, 0-based.
	Cur int
}

// Stack is one window's tag stack.
type Stack struct {
	entries []Entry
	idx     int
}

// Len is how many entries the stack holds.
func (s *Stack) Len() int { return len(s.entries) }

// Index is the entry a CTRL-T would go back to, which is len(entries) when the
// newest jump succeeded and nothing has gone back yet.
func (s *Stack) Index() int { return s.idx }

// Entries is the stack itself, oldest first. The slice is the stack's own and
// callers read it.
func (s *Stack) Entries() []Entry { return s.entries }

// Clone returns a copy, which is what a window split hands the new window.
//
// Vim copies the stack into every window it makes, so a CTRL-] in a split
// leaves the window it split from exactly where it was. The entries are copied
// by value and the match slices are shared: nothing ever writes into one.
func (s *Stack) Clone() *Stack {
	if s == nil {
		return &Stack{}
	}
	out := &Stack{idx: s.idx}
	out.entries = append(out.entries, s.entries...)
	return out
}

// Push records a new tag command at the index, throwing away anything newer,
// and returns the entry's index.
//
// It does not move the index: a jump that fails leaves it pointing at the
// entry that failed, and only Advance moves past it. That is vim's order and
// it is what ":tags" after a failed CTRL-] shows.
func (s *Stack) Push(e Entry) int {
	if s.idx < len(s.entries) {
		s.entries = s.entries[:s.idx]
	}
	s.entries = append(s.entries, e)
	if len(s.entries) > maxDepth {
		// The oldest goes. Vim shifts the whole array down by one, which
		// leaves the index one lower as well.
		s.entries = append(s.entries[:0], s.entries[1:]...)
		if s.idx > 0 {
			s.idx--
		}
	}
	s.idx = len(s.entries) - 1
	return s.idx
}

// Advance records which match the jump landed on and moves the index past the
// entry, which is what makes the jump undoable with CTRL-T.
func (s *Stack) Advance(match int) {
	if s.idx >= len(s.entries) {
		return
	}
	s.entries[s.idx].Cur = match
	s.idx++
}

// SetMatch records a different match on the current entry without moving the
// index: ":tselect" on an entry already on the stack.
func (s *Stack) SetMatch(match int) {
	if i := s.idx - 1; i >= 0 && i < len(s.entries) {
		s.entries[i].Cur = match
	}
}

// Pop is CTRL-T and ":pop": go back count entries and return the one to go
// back to.
//
// The message is the whole of the failure: E73 when nothing has ever been
// pushed and E555 when the index is already at the oldest entry, which are two
// different states and two different messages in vim.
func (s *Stack) Pop(count int) (Entry, string, bool) {
	if len(s.entries) == 0 {
		return Entry{}, MsgEmpty, false
	}
	if s.idx <= 0 {
		return Entry{}, MsgBottom, false
	}
	if count < 1 {
		count = 1
	}
	if count > s.idx {
		count = s.idx
	}
	s.idx -= count
	return s.entries[s.idx], "", true
}

// Forward is ":tag" with no argument: to the entry the last CTRL-T came back
// from.
func (s *Stack) Forward(count int) (Entry, string, bool) {
	if len(s.entries) == 0 {
		return Entry{}, MsgEmpty, false
	}
	if s.idx >= len(s.entries) {
		return Entry{}, MsgTop, false
	}
	if count < 1 {
		count = 1
	}
	if s.idx+count > len(s.entries) {
		count = len(s.entries) - s.idx
	}
	e := s.entries[s.idx+count-1]
	s.idx += count
	return e, "", true
}

// Current is the entry the index sits on, which is the one a ":tselect" with
// no argument re-offers.
func (s *Stack) Current() (Entry, bool) {
	if i := s.idx - 1; i >= 0 && i < len(s.entries) {
		return s.entries[i], true
	}
	return Entry{}, false
}

// List is ":tags": the header, one line per entry, and the ">" on its own line
// when the index is past the newest one.
//
// The format is vim's "%c%2d %2d %-15s %5ld " followed by the file name or
// the line text. Measured against vim 9.2.0321:
//
//	 # TO tag FROM line in file/text
//	 1 1 foo 4 call foo here
//	>
func (s *Stack) List(curFile string) []string {
	out := []string{"  # TO tag         FROM line  in file/text"}
	for i, e := range s.entries {
		marker := ' '
		if i == s.idx {
			marker = '>'
		}
		where := e.FromFile
		if e.FromFile == "" || sameFile(e.FromFile, curFile) {
			where = e.FromText
		}
		out = append(out, fmt.Sprintf("%c%2d %2d %-15s %5d  %s",
			marker, i+1, e.Cur+1, e.Name, e.From.Line, where))
	}
	if s.idx == len(s.entries) {
		out = append(out, ">")
	}
	return out
}

// Prompt is what ":tselect" and a ":tjump" with more than one match ask.
const Prompt = "Type number and <Enter> (q or empty cancels): "

// ListMatches is the table ":tselect" prints above that prompt.
//
// cur is the match the entry is on and is marked with a ">"; pass -1 for a new
// tag, which vim leaves unmarked. The column arithmetic is vim's
// print_tag_list: the name at column 13, the file at 13 plus the width of the
// first match's name plus two with a floor of 18, and the line the tag was
// written from indented 15 under it.
func ListMatches(ts []Tag, cur int) []string {
	if len(ts) == 0 {
		return nil
	}
	taglen := len(ts[0].Name) + 2
	if taglen < 18 {
		taglen = 18
	}

	var b strings.Builder
	b.WriteString("  # pri kind tag")
	advance(&b, 13+taglen)
	b.WriteString("file")
	out := []string{b.String()}

	for i, t := range ts {
		b.Reset()
		if i == cur {
			b.WriteByte('>')
		} else {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%2d %s ", i+1, t.PriName())
		b.WriteString(t.Kind)
		advance(&b, 13)
		b.WriteString(t.Name)
		b.WriteByte(' ')
		advance(&b, 13+taglen)
		b.WriteString(t.File)
		out = append(out, b.String())

		b.Reset()
		advance(&b, 15)
		b.WriteString(addressText(t))
		out = append(out, b.String())
	}
	return out
}

// advance pads to a column, and does nothing when the text is already past it.
// It is vim's msg_advance, which also never truncates.
func advance(b *strings.Builder, col int) {
	for b.Len() < col {
		b.WriteByte(' ')
	}
}

// addressText is the line ":tselect" prints under a match: the search pattern
// with its delimiters, its "^" and its "$" taken off, or the line number when
// the address is one.
func addressText(t Tag) string {
	p, ok := t.Pattern()
	if !ok {
		return t.Address
	}
	p = strings.TrimPrefix(p, "^")
	p = strings.TrimSuffix(p, "$")
	// Vim's msg_outtrans turns every white-space run in the line it echoes
	// into single spaces on the way out. A tab in a source line is what makes
	// this show up.
	return strings.ReplaceAll(p, "\t", " ")
}
