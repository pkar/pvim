// Package tags is a ctags file, the matches it holds and the stack of jumps
// through them: the machinery behind CTRL-], CTRL-T, :tag, :tselect, :tjump,
// :pop and the five CTRL-W keys that split a window over a tag.
//
// It reads the format ctags has written since 1996 and nothing else. A line is
//
//	name<Tab>file<Tab>address[;"<Tab>field:value...]
//
// where the address is a line number or a "/pattern/" search command, and the
// fields after the `;"` carry the kind and, for a tag that is local to its own
// file, a bare "file:". Lines starting with "!_TAG_" are the header ctags
// writes and are skipped. Everything else in the format -- the "sort:" hints,
// pseudo-tags, the extended flags -- is read past rather than acted on, and
// what that costs is written down at the bottom of this comment.
//
// What is measured, against vim 9.2.0321 through cmd/oracle on a 40-row
// 120-column pty, with the tags file written out of the buffer by ":1,3w! tags"
// so that both editors read the same bytes:
//
//	CTRL-] jumps to the first match, silently. A count picks the
//	 count'th and is clamped to the last one: "9 CTRL-]"
//	 over two matches lands on the second and says nothing.
//	CTRL-T back to where the jump started. Twice from one jump is
//	 "E555: At bottom of tag stack"; on a stack that has
//	 never held anything it is "E73: Tag stack empty".
//	:tag forward again after a CTRL-T, "E556: At top of tag
//	 stack" when there is nothing newer.
//	no tags file "E433: No tags file", then "E426: Tag not found: x".
//	 Both, in that order, and the second is what a caller
//	 that only ever printed the first would be faking.
//	name not in it "E426: Tag not found: x"
//	file not there E429: File "x" does not exist, and no window is split
//	 for it: vim checks the file before it makes the window.
//	pattern not there "E434: Can't find tag pattern", with the cursor left on
//	 the line it started on and in column 1.
//
// The one difference from vim that is a decision and not an omission: vim
// binary-searches a tags file, because ctags writes them sorted and the files
// are large. A file that is NOT sorted defeats that search and vim reports a
// tag it holds as not found -- measured, "alpha" written after "zeta" is
// E426 in vim. This package reads the whole file and scans it, so it finds
// that tag. It is a superset of vim's answer on a file no ctags wrote and the
// same answer on every file one did, and the alternative is reproducing a
// binary search over byte offsets so that pvim can fail to find something.
//
// Not read, named rather than left to be discovered: the "sort:" pseudo-tag
// (nothing here depends on the order), 'tagbsearch' (there is no binary search
// to turn off), 'taglength' (a tag name is compared in full), 'tagcase' beyond
// what Options.IgnoreCase carries, and the extension fields other than "kind:"
// and "file:", which :tselect prints in vim and does not print here.
//
// 'tagfunc' is the one worth being explicit about, because a tag command that
// quietly ignored it would jump somewhere vim would not. None of the tag
// options are in internal/options -- not 'tags', not 'tagfunc', not
// 'tagbsearch' -- so ":set tagfunc=Foo" is "E518: Unknown option" in this
// editor and no lookup can be diverted through a function. The refusal by name
// belongs here, in Find, on the day the option exists; until then it cannot be
// set and Options.Files is always vim's default.
package tags

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNoTagsFile is what a search reports when not one of the files named in
// 'tags' could be opened. Vim prints it and then goes on to print E426 as
// well, so it is an error the caller reports rather than one it stops on.
var ErrNoTagsFile = errors.New("E433: No tags file")

// NotFound is the message vim prints for a name no tags file holds. It is a
// function and not a constant because the name is in it and every caller
// spells it the same way.
func NotFound(name string) string { return "E426: Tag not found: " + name }

// NoFile is E429, which the caller prints before it makes any window.
func NoFile(name string) string { return `E429: File "` + name + `" does not exist` }

// The priority buckets. Vim sorts every match into one of these and then walks
// them in this order, so a static tag in the file the cursor is in comes ahead
// of a global one in another file however the tags file was written. The
// names are what the "pri" column of :tselect prints.
const (
	// PriStaticCur is a tag with a "file:" field, in the current file.
	PriStaticCur = iota
	// PriGlobalCur is an ordinary tag in the current file.
	PriGlobalCur
	// PriGlobalOther is an ordinary tag in another file.
	PriGlobalOther
	// PriStaticOther is a "file:" tag in another file, which is a tag that
	// cannot be reached from here and is last for that reason.
	PriStaticOther
	// PriIgnoreCase is added to a bucket when the name matched only with
	// 'ignorecase' on. Vim prints the same three letters for it and orders it
	// after every exact match.
	PriIgnoreCase = 4
)

// priNames are vim's mt_names: the three characters :tselect prints in its
// "pri" column. F is a full match of the name, S a static tag, C the current
// file.
var priNames = [4]string{"FSC", "F C", "F  ", "FS "}

// Tag is one line of a tags file.
type Tag struct {
	// Name is the tag name, the first field.
	Name string
	// File is the file name exactly as the tags file spells it, which is what
	// E429 quotes.
	File string
	// Path is File resolved against the directory the tags file was read
	// from, which is what the editor opens.
	Path string
	// Address is the third field with the ";\"" fields taken off: a line
	// number or a "/pattern/" search command.
	Address string
	// Kind is the "kind:" field, or the bare one-letter kind ctags writes in
	// its place. Empty when the tags file carries no fields.
	Kind string
	// Static says the tag carried a "file:" field, which ctags writes for a
	// name that is local to its own file.
	Static bool
	// Pri is the priority bucket, one of the Pri constants, possibly with
	// PriIgnoreCase added.
	Pri int
}

// PriName is the three characters :tselect prints for this match.
func (t Tag) PriName() string { return priNames[t.Pri&3] }

// LineNumber reads an address that is a plain line number.
func (t Tag) LineNumber() (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(t.Address))
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// Pattern reads an address that is a search command, and returns the pattern
// between the delimiters with ctags' escaping undone.
//
// The delimiter is "/" or "?" and ctags escapes an embedded one with a
// backslash. The "^" and "$" it wraps the line in are left in the pattern:
// they are what makes the search land on the line the tag was written from
// rather than on a later line that happens to contain the same text.
func (t Tag) Pattern() (string, bool) {
	s := t.Address
	if len(s) < 2 || (s[0] != '/' && s[0] != '?') {
		return "", false
	}
	delim := s[0]
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && (s[i+1] == delim || s[i+1] == '\\'):
			b.WriteByte(s[i+1])
			i++
		case c == delim:
			return b.String(), true
		default:
			b.WriteByte(c)
		}
	}
	// An address with no closing delimiter. Vim searches for what it has, so
	// this does too rather than refusing a file that is very slightly wrong.
	return b.String(), true
}

// Options are what a search for a name depends on beyond the name.
type Options struct {
	// Files is 'tags', already split: the tags files to read, in order.
	Files []string
	// Dir is the working directory a plain name in Files resolves against.
	Dir string
	// CurDir is the directory of the file being edited, which is what an
	// entry beginning with "./" resolves against. Empty falls back to Dir.
	CurDir string
	// CurFile is the file the cursor is in, as an absolute path where one is
	// known. It decides the "current file" half of the priority buckets and
	// nothing else.
	CurFile string
	// IgnoreCase adds the case-insensitive matches after the exact ones,
	// which is 'ignorecase' with 'tagcase' at its default of "followic".
	IgnoreCase bool
}

// DefaultOption is vim's 'tags': the tags file beside the current file, then
// the one in the working directory.
const DefaultOption = "./tags,tags"

// SplitOption takes 'tags' apart on its commas.
//
// A backslash protects a comma or a space inside one entry, which is how vim
// spells a file name with either in it. An empty value falls back to vim's
// default rather than searching nothing, which is what a ":set tags=" would
// otherwise mean here.
func SplitOption(v string) []string {
	if strings.TrimSpace(v) == "" {
		v = DefaultOption
	}
	var out []string
	var cur strings.Builder
	for i := 0; i < len(v); i++ {
		switch c := v[i]; {
		case c == '\\' && i+1 < len(v):
			cur.WriteByte(v[i+1])
			i++
		case c == ',':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// Find returns every tag named name, in the order vim would offer them.
//
// The error is ErrNoTagsFile and only that: a file that is there and
// unreadable counts as one that is not there, because vim's message for both
// is the same and it is about the search and not about the file.
func Find(name string, o Options) ([]Tag, error) {
	if o.Dir == "" {
		o.Dir = "."
	}
	curDir := o.CurDir
	if curDir == "" {
		curDir = o.Dir
	}

	files := o.Files
	if len(files) == 0 {
		files = SplitOption("")
	}

	opened := 0
	var buckets [8][]Tag
	seen := map[string]bool{}
	for _, entry := range files {
		path := entry
		switch {
		case filepath.IsAbs(entry):
		case strings.HasPrefix(entry, "./"):
			path = filepath.Join(curDir, entry[2:])
		default:
			path = filepath.Join(o.Dir, entry)
		}
		// One tags file per directory that resolves to the same one. The
		// default 'tags' names ./tags and tags, which are one file whenever
		// the buffer is in the working directory, and vim offers each match
		// from it once.
		key := path
		if abs, err := filepath.Abs(path); err == nil {
			key = filepath.Clean(abs)
		}
		if seen[key] {
			continue
		}
		seen[key] = true

		found, err := read(path, name, o)
		if err != nil {
			continue
		}
		opened++
		for _, t := range found {
			buckets[t.Pri] = append(buckets[t.Pri], t)
		}
	}
	if opened == 0 {
		return nil, ErrNoTagsFile
	}

	var out []Tag
	for _, b := range buckets {
		out = append(out, b...)
	}
	return out, nil
}

// read opens one tags file and returns the lines naming the tag.
func read(path, name string, o Options) ([]Tag, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dir := filepath.Dir(path)
	var out []Tag
	sc := bufio.NewScanner(f)
	// A tags file line is a source line with a pattern in it, and a minified
	// javascript file gives ctags a very long one. 1MB, because the default
	// 64KB is a limit somebody would meet in real use and a truncated line
	// reads as a corrupt tags file rather than as a long one.
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "!_TAG_") {
			continue
		}
		t, ok := parse(line)
		if !ok {
			continue
		}
		switch {
		case t.Name == name:
		case o.IgnoreCase && strings.EqualFold(t.Name, name):
			t.Pri = PriIgnoreCase
		default:
			continue
		}
		t.Path = t.File
		if !filepath.IsAbs(t.Path) {
			t.Path = filepath.Join(dir, t.File)
		}
		t.Pri += bucket(t, o.CurFile)
		out = append(out, t)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// bucket is the priority of a match: static or not, this file or another.
func bucket(t Tag, curFile string) int {
	cur := curFile != "" && sameFile(t.Path, curFile)
	switch {
	case t.Static && cur:
		return PriStaticCur
	case cur:
		return PriGlobalCur
	case t.Static:
		return PriStaticOther
	default:
		return PriGlobalOther
	}
}

// sameFile reports whether two names point at one file, by name rather than by
// inode: the tags file may well name a file that does not exist, and asking
// the operating system about it would be a stat per line of a 200k-line tags
// file.
func sameFile(a, b string) bool {
	if a == b {
		return true
	}
	aa, erra := filepath.Abs(a)
	bb, errb := filepath.Abs(b)
	return erra == nil && errb == nil && filepath.Clean(aa) == filepath.Clean(bb)
}

// parse takes one line of a tags file apart.
func parse(line string) (Tag, bool) {
	name, rest, ok := strings.Cut(line, "\t")
	if !ok || name == "" {
		return Tag{}, false
	}
	file, addr, ok := strings.Cut(rest, "\t")
	if !ok || file == "" || addr == "" {
		return Tag{}, false
	}
	t := Tag{Name: name, File: file}
	t.Address, ok = cutFields(addr, &t)
	if !ok {
		return Tag{}, false
	}
	return t, true
}

// cutFields splits the address off the ";\"" extension fields behind it and
// reads the two fields this package acts on.
//
// The terminator is a `;"` that ends the address, which ctags writes as a
// comment the ex command it is pretending to be would ignore. It only counts
// outside the search command's delimiters, or a pattern holding `;"` would cut
// its own address in half.
func cutFields(addr string, t *Tag) (string, bool) {
	end := len(addr)
	if addr[0] == '/' || addr[0] == '?' {
		delim := addr[0]
		i := 1
		for i < len(addr) {
			if addr[i] == '\\' {
				i += 2
				continue
			}
			if addr[i] == delim {
				i++
				break
			}
			i++
		}
		end = i
	} else {
		// A line number address ends at the first character that is not one.
		i := 0
		for i < len(addr) && addr[i] >= '0' && addr[i] <= '9' {
			i++
		}
		if i == 0 {
			return "", false
		}
		end = i
	}
	command := addr[:end]

	fields := strings.TrimPrefix(strings.TrimSpace(addr[end:]), `;"`)
	for _, f := range strings.Split(fields, "\t") {
		f = strings.TrimSpace(f)
		switch {
		case f == "":
		case f == "file:" || strings.HasPrefix(f, "file:"):
			t.Static = true
		case strings.HasPrefix(f, "kind:"):
			t.Kind = f[len("kind:"):]
		case !strings.Contains(f, ":"):
			// ctags writes the kind on its own when it is one letter and
			// "kind:" only under --fields=+K.
			t.Kind = f
		}
	}
	return command, true
}
