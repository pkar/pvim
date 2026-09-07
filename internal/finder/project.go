// Package finder is ctrlp, reduced to the parts this vimrc can reach: the
// project root, the walk that lists a repository, the ignore rules the vimrc
// writes down, the fuzzy match that ranks what the walk found, and the three
// modes ctrlp's own defaults leave reachable -- files, buffers and MRU.
//
// # What is measured and what is not
//
// There is no oracle here. Vim cannot be asked what ctrlp would have ranked:
// the answer lives in vimscript that pvim does not run, and a byte-identical
// ranking was never the goal. So the evidence for this package is of a
// different kind, and each rule below says which kind it is.
//
// - Rules taken from the plugin. The ignore semantics, the root markers, the
// mode list, the key table and the window height are read out of
// ~/.vim/pack/pkar/start/ctrlp.vim and cited at the code that reproduces
// them. Those are reproductions, and a difference from the plugin is a bug.
// - Rules that are a judgment call. The scoring is the big one: ctrlp ranks
// by match length, then file length, then mtime, and this scores fzf-style
// instead, with path-separator and camel-boundary bonuses. That is a
// deliberate departure and score.go says so at length.
// Anything else here that is a choice rather than a reproduction says so
// in its own doc comment, in those words.
//
// The gate is behavioural and not ordinal: CTRL-P, "hueveri",
// CR opens hue_verify.go in ctrlp and in this, and a file inside .git is in
// neither.
//
// # Why the tree lives next door and imports this
//
// internal/tree needs the same two things this package needs first: which
// directory is the project, and which paths a walk must not descend into. The
// rules differ -- the tree applies g:NERDTreeIgnore and shows dotfiles, the
// finder applies 'wildignore' and g:ctrlp_custom_ignore and does not -- but
// the walk, the pattern translation and the root search are one piece of code
// with two rule sets over it, which is why Rules is a value a caller fills in
// rather than a constant this package holds.
//
// An internal/project package holding Root for the finder and the LSP to
// share -- the answer to "autochdir and a project-rooted finder or LSP
// disagree" -- is not written yet, and this is not it: Root is here because
// the finder and the tree both need it, and one copy in one of the two
// packages beats two copies in both. The day internal/project exists, Root and
// its test move there and this file keeps the walk.
package finder

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkar/pvim/internal/regex"
)

// RootMarker is the directory entry that says "this is the project".
//
// One marker and not ctrlp's list. ctrlp's g:ctrlp_root_markers is empty by
// default and its built-in list is `['.git', '.hg', '.svn', '.bzr', '_darcs']`
// (ctrlp#setpathmode, autoload/ctrlp.vim:1958), tried in that order at each
// level. ".git"
// in three separate places and this machine has no hg, svn, bzr or darcs
// checkout on it, so the other four are a list to grow when one appears rather
// than four stat calls per directory per keystroke.
const RootMarker = ".git"

// Root is the nearest ancestor of start that holds a .git, or the directory
// start is in when no ancestor has one.
//
// This is ctrlp's g:ctrlp_working_path_mode of "ra" without the "a" half: walk
// up from the file for a root marker, and fall back rather than failing. The
// fallback is the file's own directory and not the process working directory
// on purpose -- the vimrc sets 'autochdir', so the working directory follows
// the buffer and the two answers are the same in the common case, and in the
// uncommon one (a file opened from somewhere else in a running instance) the
// file's directory is the one the person is looking at.
//
// A start that names a file uses the file's directory; a start that names a
// directory uses itself. An empty start uses the working directory, which is
// what "pvim" with no file argument has.
//
// The marker is looked for with Lstat and not a directory test, because a
// linked worktree's .git is a file holding a "gitdir:" line and a worktree is
// as much a project root as a clone is.
func Root(start string) string {
	dir := startDir(start)
	for {
		if _, err := os.Lstat(filepath.Join(dir, RootMarker)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// The filesystem root, reached without finding a marker.
			return startDir(start)
		}
		dir = parent
	}
}

// startDir turns whatever a caller had -- a file, a directory, or nothing --
// into the directory a root search starts at.
func startDir(start string) string {
	if start == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "."
		}
		return wd
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		abs = start
	}
	if st, err := os.Stat(abs); err == nil && st.IsDir() {
		return abs
	}
	return filepath.Dir(abs)
}

// Rules is which paths a walk keeps, as the vimrc wrote them.
//
// The three fields are three different dialects and that is not tidiable:
// 'wildignore' holds vim FILE patterns (shell-ish globs, translated by vim's
// own file_pat_to_reg_pat), while g:ctrlp_custom_ignore and g:NERDTreeIgnore
// hold vim REGEXES. Compile turns all three into internal/regex patterns so
// that one matcher answers all of them, and so that the only regexp engine in
// this program stays the one in internal/regex.
type Rules struct {
	// ShowHidden keeps entries whose name starts with a dot. False is
	// ctrlp's g:ctrlp_show_hidden default (autoload/ctrlp.vim:64, and the
	// filter at:439 that drops v:val[0] == "."); the tree passes true
	// because the vimrc sets NERDTreeShowHidden=1.
	ShowHidden bool

	// WildIgnore is 'wildignore', split on commas by the caller or left as
	// the option's raw string -- Compile splits it either way. Vim's rule,
	// from match_file_pat in fileio.c: a pattern holding a path separator is
	// matched against the whole path, one without it against the tail alone,
	// and both are anchored at each end.
	//
	// Files only, which is ctrlp's own placement and not a shortcut:
	// s:GlobPath filters the file half of each directory's entries through
	// glob() to apply &wig (autoload/ctrlp.vim:445) and leaves the directory
	// half alone. So a directory called "build.o" is descended into and the
	// files under a "tmp" directory are dropped one by one by "*/tmp/*",
	// which is the same listing by a slower route.
	WildIgnore []string

	// Dir and File are vim regexes applied to a directory and to a file. The
	// finder fills them from g:ctrlp_custom_ignore's "dir" and "file" keys;
	// the tree puts every g:NERDTreeIgnore pattern in both, because nerdtree
	// applies its list to directories and files alike unless the pattern
	// carries a [[dir]] or [[file]] suffix (lib/nerdtree/path.vim:502).
	Dir, File []string

	// Path is the patterns matched against the whole path whatever Tail
	// says, which is nerdtree's "[[path]]" suffix.
	Path []string

	// Tail matches Dir and File against the entry's name instead of against
	// the path. nerdtree matches the tail -- Path._ignorePatternMatches ends
	// in `self.getLastPathComponent(0) =~# pat` -- and ctrlp matches the path
	// it built up as it walked (ctrlp#dirnfile passes `each`, which is
	// dir.lash.name), so the two rule sets disagree and the difference is a
	// field rather than two matchers.
	Tail bool

	// IgnoreCase folds case in Dir, File and Path. ctrlp forces 'ignorecase'
	// on for the whole of its scan (s:glbs has 'ic': 1, autoload/ctrlp.vim:
	// 116) so its ignore patterns match either case; nerdtree matches with
	// "=~#" and is always case sensitive, so the tree passes false.
	IgnoreCase bool
}

// Matcher is Rules compiled: the question "does this walk keep that path"
// answered without recompiling a pattern per entry.
type Matcher struct {
	showHidden bool
	tail       bool
	// wildPath is the 'wildignore' patterns that hold a path separator and
	// wildTail the ones that do not, already anchored.
	wildPath, wildTail []*regex.Regexp
	dir, file, path    []*regex.Regexp
}

// Compile builds the matcher. An unusable pattern is an error naming it, not a
// pattern silently dropped: a vimrc whose ignore list does not compile is
// ignoring nothing, and finding that out from a walk that returns the whole of
// node_modules is worse than finding it out from a message.
func (r Rules) Compile() (*Matcher, error) {
	m := &Matcher{showHidden: r.ShowHidden, tail: r.Tail}
	for _, pat := range splitPatterns(r.WildIgnore) {
		re, err := regex.Compile(GlobToPattern(pat), regex.Options{})
		if err != nil {
			return nil, err
		}
		if strings.ContainsRune(pat, '/') {
			m.wildPath = append(m.wildPath, re)
		} else {
			m.wildTail = append(m.wildTail, re)
		}
	}
	opt := regex.Options{IgnoreCase: r.IgnoreCase}
	var err error
	if m.dir, err = compileAll(r.Dir, opt); err != nil {
		return nil, err
	}
	if m.file, err = compileAll(r.File, opt); err != nil {
		return nil, err
	}
	if m.path, err = compileAll(r.Path, opt); err != nil {
		return nil, err
	}
	return m, nil
}

// MustCompile is Compile for rules written in this repository.
func (r Rules) MustCompile() *Matcher {
	m, err := r.Compile()
	if err != nil {
		panic("finder: " + err.Error())
	}
	return m
}

func compileAll(pats []string, opt regex.Options) ([]*regex.Regexp, error) {
	out := make([]*regex.Regexp, 0, len(pats))
	for _, p := range pats {
		if p == "" {
			continue
		}
		re, err := regex.Compile(p, opt)
		if err != nil {
			return nil, err
		}
		out = append(out, re)
	}
	return out, nil
}

// splitPatterns accepts either a list of patterns or the raw comma-separated
// option string, because 'wildignore' arrives as one string from
// internal/options and as a list from a test.
func splitPatterns(in []string) []string {
	var out []string
	for _, s := range in {
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// SkipDir reports whether a walk must not descend into a directory. path is
// relative to the root and slash-separated; name is its last component.
func (m *Matcher) SkipDir(path, name string) bool {
	if m == nil {
		return false
	}
	if m.hidden(name) {
		return true
	}
	return matchAny(m.path, "/"+path) || matchAny(m.dir, m.subject(path, name))
}

// SkipFile reports whether a walk must leave a file out.
func (m *Matcher) SkipFile(path, name string) bool {
	if m == nil {
		return false
	}
	if m.hidden(name) {
		return true
	}
	if matchAny(m.path, "/"+path) || matchAny(m.file, m.subject(path, name)) {
		return true
	}
	return m.wild(path, name)
}

// subject is what a Dir or File pattern is matched against.
//
// With Tail it is the entry's own name. Without it, it is the path with a
// leading separator glued on, and the separator is load-bearing: ctrlp's
// default ignore dictionary is `\v[\/](\.git|\.hg|\.svn|_darcs|\.bzr)$`
// (autoload/ctrlp.vim:11-53), which demands a path separator in front of the
// name, and ctrlp can demand it because the path it matches is absolute --
// s:dyncwd with every component appended as it walks. Matching a bare ".git"
// against that pattern finds nothing, and the .git directory ends up in the
// listing the gate says it must not be in.
//
// The path is the project's and not the filesystem's, which is the one
// difference from ctrlp: a pattern anchored with "^" means the project root
// here and the volume root there. Every pattern in ctrlp's default dictionary
// and in this vimrc's is suffix-anchored or unanchored, so none of them can
// tell; what it buys is that the name of the directory the project happens to
// sit in -- a t.TempDir() under /var/folders in these tests -- cannot make a
// file match or not match.
func (m *Matcher) subject(path, name string) string {
	if m.tail {
		return name
	}
	return "/" + path
}

func (m *Matcher) hidden(name string) bool {
	return !m.showHidden && strings.HasPrefix(name, ".")
}

// wild is vim's match_file_pat: a pattern with a path separator in it is
// matched against the whole path and one without it against the tail.
func (m *Matcher) wild(path, name string) bool {
	return matchAny(m.wildTail, name) || matchAny(m.wildPath, "/"+path)
}

func matchAny(res []*regex.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// GlobToPattern turns a vim file pattern into an anchored vim regex, which is
// what vim's file_pat_to_reg_pat does before it matches 'wildignore'.
//
// The four rules that matter, and they are vim's and not shell's:
//
// - "*" is ".*" and crosses path separators, which is what makes the
// vimrc's "*/tmp/*" mean "anywhere under a tmp directory" rather than
// "one level down".
// - "?" is one character, spelled "." in a vim regex.
// - "[abc]" is passed through: a vim collection and a file pattern's
// bracket expression are the same syntax.
// - everything else is literal, so the magic characters vim's magic mode
// would otherwise read -- "." "~" "\" "^" "$" -- are escaped.
//
// The result is anchored with ^ and $ because file_pat_to_reg_pat anchors, and
// an unanchored "*.o" would match "foo.old".
func GlobToPattern(glob string) string {
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		case '[':
			// The whole collection, up to its ']', copied as written. An
			// unterminated "[" is a literal one, which is also vim's answer.
			if end := closingBracket(glob, i); end > 0 {
				b.WriteString(glob[i : end+1])
				i = end
				continue
			}
			b.WriteString(`\[`)
		case '.', '~', '\\', '^', '$':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('$')
	return b.String()
}

// closingBracket finds the "]" that ends the collection opened at i, or -1.
// A "]" immediately after the "[" or after a "^" is a literal one, which is
// the rule in both dialects.
func closingBracket(s string, i int) int {
	j := i + 1
	if j < len(s) && s[j] == '^' {
		j++
	}
	if j < len(s) && s[j] == ']' {
		j++
	}
	for ; j < len(s); j++ {
		if s[j] == ']' {
			return j
		}
	}
	return -1
}

// WalkOptions bounds a walk.
type WalkOptions struct {
	// MaxFiles stops the walk once this many files have been kept. ctrlp's
	// g:ctrlp_max_files is 10000 and this is the same number, for the same
	// reason: a walk of $HOME by accident should stop rather than take the
	// editor with it.
	MaxFiles int
	// MaxDepth is ctrlp's g:ctrlp_max_depth, 40 there and here.
	MaxDepth int
}

// DefaultWalk is ctrlp's two limits.
func DefaultWalk() WalkOptions { return WalkOptions{MaxFiles: 10000, MaxDepth: 40} }

// Walk lists every file under root that the matcher keeps, as slash-separated
// paths relative to root, sorted.
//
// Sorted, and that is a choice rather than a reproduction: ctrlp lists in the
// order globpath() returns and then sorts by its own comparators, so its input
// order is the filesystem's. Sorting here makes a walk of one tree give one
// answer twice, which is what makes the tests in this package assertions
// rather than observations, and the scorer reorders everything anyway.
//
// Symbolic links to directories are not followed. ctrlp's
// g:ctrlp_follow_symlinks defaults to 0 and this is the same answer arrived at
// for the same reason: a link back up the tree is an infinite walk, and the
// depth limit is a worse defence than not going.
func Walk(root string, m *Matcher, o WalkOptions) ([]string, error) {
	if o.MaxFiles <= 0 {
		o.MaxFiles = DefaultWalk().MaxFiles
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = DefaultWalk().MaxDepth
	}
	var out []string
	if err := walkDir(root, "", m, o, 0, &out); err != nil && !errors.Is(err, errWalkFull) {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// errWalkFull unwinds the recursion at MaxFiles. Walk swallows it: a listing
// that hit the cap is a short listing and not a failure, which is what ctrlp
// does too -- s:GlobPath simply stops recursing once s:maxf says the count is
// past g:ctrlp_max_files.
var errWalkFull = errors.New("finder: file limit reached")

func walkDir(root, rel string, m *Matcher, o WalkOptions, depth int, out *[]string) error {
	if depth > o.MaxDepth {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		if rel == "" {
			return err
		}
		// A directory that cannot be read is a directory with nothing in it.
		// A permission error four levels down must not take the whole listing
		// with it, which is what ctrlp's globpath() does as well.
		return nil
	}
	for _, e := range entries {
		name := e.Name()
		path := name
		if rel != "" {
			path = rel + "/" + name
		}
		if e.IsDir() {
			if m.SkipDir(path, name) {
				continue
			}
			if err := walkDir(root, path, m, o, depth+1, out); err != nil {
				return err
			}
			continue
		}
		if e.Type()&os.ModeSymlink != 0 {
			// A link to a directory is not descended into and a link to a
			// file is listed like the file it points at.
			if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err == nil && st.IsDir() {
				continue
			}
		}
		if m.SkipFile(path, name) {
			continue
		}
		*out = append(*out, path)
		if len(*out) >= o.MaxFiles {
			return errWalkFull
		}
	}
	return nil
}
