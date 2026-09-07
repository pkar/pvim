package ex

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/text"
)

// The buffer list.
//
// internal/text holds a buffer's contents and nothing about the file it came
// from: no name, no number, no "has it been written since it changed". That is
// the right split -- a Buffer is the text and an undo tree over it -- but ":w",
// ":ls", ":b" and the ":q" that 'confirm' prompts about all need the other
// half, and this is where it lives. It is in internal/ex rather than in a
// package of its own because every command that reads it is an ex command.

// Buf is one buffer in the list: a text.Buffer and everything vim knows about
// it that is not its contents.
type Buf struct {
	// Num is the buffer number ":ls" prints and ":b 3" takes. Numbers are
	// never reused, which is why the list keeps a counter rather than using
	// the slice index.
	Num int
	// Name is the file name as it will be written, absolute once the buffer
	// has been associated with a file. Empty for a buffer that has never had
	// one, which is what ":enew" makes and what ":w" answers E32 for.
	Name string
	// Text is the contents.
	Text *text.Buffer
	// Listed is 'buflisted': ":ls" shows only listed buffers and ":ls!" shows
	// all of them. A help buffer and the ":!"-output scratch buffer are not
	// listed.
	Listed bool
	// Scratch is 'buftype=nofile': the buffer has no file behind it, ":w"
	// refuses and ":q" does not prompt.
	Scratch bool
	// ReadOnly is 'readonly' as a property of THIS buffer, which ":help" sets
	// and which the crash-recovery prompt's [O]pen Read-Only sets. It is not
	// the same field as options.Buffer.ReadOnly, which is the option ":set ro"
	// writes and which belongs to whichever buffer the option state is
	// currently describing; ":w" refuses over either. See Context.readOnly.
	ReadOnly bool
	// NewFile says the name does not exist on disk, which is the "[New]" in
	// the message ":w" prints.
	NewFile bool
	// savedSeq is the undo sequence number at the last write. Modified
	// compares it with the buffer's current one, which is how a buffer that
	// was changed and then undone back to where it was reads as unmodified,
	// the same as it does in vim.
	savedSeq int
	// Cursor is where the cursor was when this buffer was last left, which is
	// where ":b" puts it back.
	Cursor text.Pos
}

// Modified reports whether the buffer has changed since it was last written.
func (b *Buf) Modified() bool {
	if b == nil || b.Text == nil {
		return false
	}
	return b.Text.UndoSeq() != b.savedSeq
}

// MarkSaved records that the buffer has just been written.
func (b *Buf) MarkSaved() { b.savedSeq = b.Text.UndoSeq() }

// Display is the name ":ls" and the messages show: the file name shortened
// against the working directory, or "[No Name]" for a buffer that has never
// had one.
func (b *Buf) Display() string {
	if b.Name == "" {
		return "[No Name]"
	}
	return ShortName(b.Name)
}

// ShortName is vim's shorten_fname plus home_replace: the name to print for a
// file whose full path is stored.
//
// A file under the working directory is shown relative to it, one under the
// home directory keeps a "~", and anything else stays absolute. Vim never
// climbs out of the directory with "..": a name that is not underneath it is
// printed in full.
//
// Storing the full path is right and printing it is not. ":e other.txt" prints
// `"other.txt" [New]`, ":ls" lists "other.txt", and "pvim README.md" then ":sp"
// puts "README.md" in both status lines; printing the stored name put the whole
// path in every one of them, and msgs.txt is one of the three artifacts
// cmd/oracle diffs.
func ShortName(name string) string {
	if name == "" {
		return ""
	}
	if wd, err := os.Getwd(); err == nil {
		if rel, ok := under(name, wd); ok {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, ok := under(name, home); ok {
			return "~/" + rel
		}
	}
	return name
}

// under reports whether name is inside dir, and what is left of it when the
// directory and its separator are taken off. The directory itself is not
// "inside" it: there is nothing left to print.
func under(name, dir string) (string, bool) {
	if dir == "" || !strings.HasPrefix(name, dir) {
		return "", false
	}
	rest := name[len(dir):]
	if len(rest) < 2 || rest[0] != filepath.Separator {
		return "", false
	}
	return rest[1:], true
}

// BufList is every buffer, in the order they were created.
type BufList struct {
	Bufs []*Buf
	// Cur is the buffer the current window is showing. It is a pointer rather
	// than an index because a ":bd" of an earlier buffer must not silently
	// move it.
	Cur *Buf
	// Alt is the alternate buffer: what CTRL-^ and ":b#" go back to and what
	// "#" expands to.
	Alt  *Buf
	next int
}

// NewBufList returns an empty list.
func NewBufList() *BufList { return &BufList{next: 1} }

// Add appends a buffer for name with those contents and returns it. The name
// is stored as given; callers that want it absolute call Abs first.
func (l *BufList) Add(name string, b *text.Buffer) *Buf {
	if l.next == 0 {
		l.next = 1
	}
	nb := &Buf{Num: l.next, Name: name, Text: b, Listed: true, Cursor: text.Pos{Line: 1}}
	nb.savedSeq = b.UndoSeq()
	l.next++
	l.Bufs = append(l.Bufs, nb)
	if l.Cur == nil {
		l.Cur = nb
	}
	return nb
}

// ByName returns the buffer with exactly this name, or nil.
func (l *BufList) ByName(name string) *Buf {
	for _, b := range l.Bufs {
		if b.Name == name {
			return b
		}
	}
	return nil
}

// ByNum returns the buffer with this number, or nil.
func (l *BufList) ByNum(n int) *Buf {
	for _, b := range l.Bufs {
		if b.Num == n {
			return b
		}
	}
	return nil
}

// Match resolves the argument of ":b", which is a number, a "#", or a
// substring of a file name.
//
// Vim's rule for the substring form, and the reason this is not a
// strings.Contains one-liner: exactly one listed buffer has to match, and more
// than one is E93. A full name match wins over a substring, so ":b foo" opens
// foo rather than being ambiguous with foobar.
func (l *BufList) Match(arg string) (*Buf, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return nil, ErrArgumentRequired
	}
	if arg == "#" {
		if l.Alt == nil {
			return nil, ErrNoAlternate
		}
		return l.Alt, nil
	}
	if n, err := strconv.Atoi(arg); err == nil {
		b := l.ByNum(n)
		if b == nil {
			return nil, withName(ErrNoSuchBuffer, arg)
		}
		return b, nil
	}
	if b := l.ByName(arg); b != nil {
		return b, nil
	}
	var found *Buf
	n := 0
	for _, b := range l.Bufs {
		if !b.Listed {
			continue
		}
		if strings.Contains(b.Name, arg) {
			found = b
			n++
		}
	}
	switch n {
	case 0:
		return nil, withName(ErrNoSuchBuffer, arg)
	case 1:
		return found, nil
	default:
		return nil, withName(ErrMoreThanOneMatch, arg)
	}
}

// Step returns the buffer count on from cur in the list, wrapping, which is
// what ":bn" and ":bp" walk. Unlisted buffers are skipped.
func (l *BufList) Step(cur *Buf, count int) *Buf {
	listed := make([]*Buf, 0, len(l.Bufs))
	for _, b := range l.Bufs {
		if b.Listed {
			listed = append(listed, b)
		}
	}
	if len(listed) == 0 {
		return nil
	}
	at := 0
	for i, b := range listed {
		if b == cur {
			at = i
		}
	}
	n := len(listed)
	at = ((at+count)%n + n) % n
	return listed[at]
}

// Remove takes a buffer out of the list, which is ":bd".
func (l *BufList) Remove(b *Buf) {
	for i, x := range l.Bufs {
		if x == b {
			l.Bufs = append(l.Bufs[:i], l.Bufs[i+1:]...)
			break
		}
	}
	if l.Alt == b {
		l.Alt = nil
	}
}

// Abs makes a file name absolute against the working directory, the way vim
// stores one. A "~" prefix is expanded, because the vimrc writes ":set
// undodir=~/.cache/vim" and a file name typed at the ":e" prompt has the same
// shape.
func Abs(name string) string {
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "~/") || name == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			name = filepath.Join(home, strings.TrimPrefix(name, "~"))
		}
	}
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	wd, err := os.Getwd()
	if err != nil {
		return name
	}
	return filepath.Join(wd, name)
}
