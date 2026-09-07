package git

import (
	"fmt"
	"strconv"
	"strings"
)

// The status buffer: what ":Gstatus" and ":G" show, and what "-" acts on.

// Section is which of the three lists a status line belongs to.
type Section uint8

// The sections, in the order the buffer shows them. Fugitive has more --
// Rebasing, Unpushed, Unpulled, and a stash list -- and this has three.
//
// That is a judgment call and not a measurement. The three here are the ones
// a "-" can act on, which is the whole of what: staging and
// unstaging. Unpushed and Unpulled are a log in a heading, and a person who
// wants them has ":Git log @{u}.." one keystroke away in a scratch split.
// They are not here because a heading that cannot be acted on teaches a hand
// to skip past headings.
const (
	NoSection Section = iota
	Untracked
	Unstaged
	Staged
)

// Name is the heading this section is drawn under.
func (s Section) Name() string {
	switch s {
	case Untracked:
		return "Untracked"
	case Unstaged:
		return "Unstaged"
	case Staged:
		return "Staged"
	}
	return ""
}

// Entry is one file in one section.
type Entry struct {
	// Code is the one-letter status: git's own, from the porcelain format.
	// "M" modified, "A" added, "D" deleted, "R" renamed, "C" copied, "U"
	// unmerged, "?" untracked.
	Code byte
	// Path is relative to the working tree root, with forward slashes.
	Path string
	// From is where a rename came from, empty for everything else.
	From string
}

// Status is the whole of what the status buffer shows.
type Status struct {
	// Head is the branch name, or the short commit id when the head is
	// detached.
	Head string
	// Detached is a head that is not on a branch.
	Detached bool
	// Untracked, Unstaged and Staged are the three lists, each already in
	// git's order, which is path order.
	Untracked, Unstaged, Staged []Entry
}

// Status reads the repository's state.
//
// It reads porcelain v1 with -z, not v2 and not the human format. v1 because
// its two-letter code is exactly the two lists this buffer wants -- the index
// letter is the staged half and the worktree letter the unstaged half -- and
// -z because a path with a space, a quote or a newline in it is a path git
// will quote in every other format and not in this one.
func (r *Repo) Status() (*Status, error) {
	out, err := r.output("status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	s := &Status{}
	if err := r.head(s); err != nil {
		return nil, err
	}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if len(rec) < 4 {
			continue
		}
		x, y, path := rec[0], rec[1], rec[3:]
		if x == 'R' || x == 'C' {
			// A rename is two NUL-separated paths, the new one first and the
			// old one in the record after it.
			if i+1 < len(fields) {
				i++
				s.Staged = append(s.Staged, Entry{Code: x, Path: path, From: fields[i]})
				if y != ' ' && y != 0 {
					s.Unstaged = append(s.Unstaged, Entry{Code: y, Path: path})
				}
				continue
			}
		}
		switch {
		case x == '?' && y == '?':
			s.Untracked = append(s.Untracked, Entry{Code: '?', Path: path})
			continue
		case x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D'):
			// Unmerged. Both letters describe the conflict rather than two
			// halves of a change, so it goes in the unstaged list once and
			// keeps the letter git would print for it.
			s.Unstaged = append(s.Unstaged, Entry{Code: 'U', Path: path})
			continue
		}
		if x != ' ' && x != 0 {
			s.Staged = append(s.Staged, Entry{Code: x, Path: path})
		}
		if y != ' ' && y != 0 {
			s.Unstaged = append(s.Unstaged, Entry{Code: y, Path: path})
		}
	}
	return s, nil
}

// head fills in the branch name.
func (r *Repo) head(s *Status) error {
	out, err := r.output("symbolic-ref", "--short", "HEAD")
	if err == nil {
		s.Head = strings.TrimSpace(out)
		return nil
	}
	// Detached, or a repository with no commits yet. rev-parse answers the
	// first and fails on the second, where the branch name is still readable
	// out of HEAD itself.
	if out, err := r.output("rev-parse", "--short", "HEAD"); err == nil {
		s.Head, s.Detached = strings.TrimSpace(out), true
		return nil
	}
	out, err = r.output("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	s.Head = strings.TrimSpace(out)
	return nil
}

// Clean reports whether there is nothing to commit and nothing untracked.
func (s *Status) Clean() bool {
	return len(s.Untracked) == 0 && len(s.Unstaged) == 0 && len(s.Staged) == 0
}

// LineKind is what one line of the rendered status buffer is.
type LineKind uint8

// The kinds. Every line of the buffer is one of them, which is what lets "-"
// and <CR> ask one question and get an answer for any cursor position.
const (
	Blank LineKind = iota
	Header
	Heading
	File
)

// Line is one rendered line, with what it refers to.
type Line struct {
	Kind    LineKind
	Section Section
	Entry   Entry
}

// Render draws the status buffer and the map from line number to what is on
// it. Line numbers are 1-based, so lines[0] is line 1 and refs[0] describes
// it.
//
// The shape is fugitive's, copied from a run of ":Git":
//
//	Head: master
//	Help: g?
//
//	Untracked (1)
//	? untracked.txt
//
//	Unstaged (3)
//	M both.txt
//	D gone.txt
//	M tracked.txt
//
//	Staged (2)
//	A both.txt
//	A staged.txt
//
// with the empty sections left out, exactly as fugitive leaves them out. The
// "Help: g?" line is kept even though "g?" is not a key here, because the
// second line of this buffer is where a hand looks for the first file and a
// buffer that is one line taller in a repository with a rebase in progress is
// a buffer whose first file is somewhere else every time.
func (s *Status) Render() ([]string, []Line) {
	var lines []string
	var refs []Line
	add := func(text string, ref Line) {
		lines = append(lines, text)
		refs = append(refs, ref)
	}
	head := "Head: " + s.Head
	if s.Detached {
		head = "Head: (detached) " + s.Head
	}
	add(head, Line{Kind: Header})
	add("Help: g?", Line{Kind: Header})
	for _, sec := range []struct {
		s Section
		e []Entry
	}{{Untracked, s.Untracked}, {Unstaged, s.Unstaged}, {Staged, s.Staged}} {
		if len(sec.e) == 0 {
			continue
		}
		add("", Line{Kind: Blank})
		add(fmt.Sprintf("%s (%d)", sec.s.Name(), len(sec.e)), Line{Kind: Heading, Section: sec.s})
		for _, e := range sec.e {
			text := string(e.Code) + " " + e.Path
			if e.From != "" {
				text = string(e.Code) + " " + e.From + " -> " + e.Path
			}
			add(text, Line{Kind: File, Section: sec.s, Entry: e})
		}
	}
	if s.Clean() {
		add("", Line{Kind: Blank})
		add("nothing to commit, working tree clean", Line{Kind: Header})
	}
	return lines, refs
}

// Stage adds paths to the index. An untracked path is added whole; a modified
// one has its worktree contents staged.
func (r *Repo) Stage(paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := r.output(append([]string{"add", "--"}, paths...)...)
	return err
}

// Unstage takes paths back out of the index, leaving the working tree alone.
//
// "git reset -q HEAD --" and not "git restore --staged", because a repository
// with no commit yet has no HEAD and this is the form that says so with a
// message rather than the form that fails obscurely. See unstageNoHead.
func (r *Repo) Unstage(paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	if _, err := r.output("rev-parse", "--verify", "HEAD"); err != nil {
		_, err := r.output(append([]string{"rm", "--cached", "-q", "--"}, paths...)...)
		return err
	}
	_, err := r.output(append([]string{"reset", "-q", "HEAD", "--"}, paths...)...)
	return err
}

// Toggle is what "-" does to one file: stage it if it has unstaged changes,
// unstage it if it does not.
//
// The rule is the section the cursor is in and not the file's state, which is
// the judgment call. A file that is both staged and unstaged -- fugitive
// shows it twice, and so does this -- has two lines, and "-" on the unstaged
// one stages the rest of it while "-" on the staged one takes the staged part
// back. Deciding from the file instead of from the line would make one of
// those two lines do nothing.
func (r *Repo) Toggle(sec Section, path string) error {
	switch sec {
	case Untracked, Unstaged:
		return r.Stage(path)
	case Staged:
		return r.Unstage(path)
	}
	return nil
}

// ToggleSection is "-" on a heading: every file under it at once.
//
// Also a judgment call, and the one fugitive makes: a "-" on "Unstaged (3)"
// stages all three. It is here because the alternative -- a heading that does
// nothing -- makes a person type "-" three times for the commonest thing
// there is, which is staging everything they just changed.
func (r *Repo) ToggleSection(s *Status, sec Section) error {
	var paths []string
	var list []Entry
	switch sec {
	case Untracked:
		list = s.Untracked
	case Unstaged:
		list = s.Unstaged
	case Staged:
		list = s.Staged
	}
	for _, e := range list {
		paths = append(paths, e.Path)
	}
	if len(paths) == 0 {
		return nil
	}
	if sec == Staged {
		return r.Unstage(paths...)
	}
	return r.Stage(paths...)
}

// atoi is strconv.Atoi with a zero for anything that is not a number, which
// is what the porcelain parsers want: a field git did not print is a zero and
// not an error that loses the other twenty fields with it.
func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
