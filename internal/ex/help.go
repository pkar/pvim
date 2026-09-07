package ex

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkar/pvim/internal/text"
)

// ":help", over the doc directory the installed vim ships.
//
// This is difference D-001, and the reason it is registered as
// expected-identical rather than as a departure: pvim opens
// /opt/homebrew/share/vim/vim92/doc read-only and "vim --clean" opens the same
// files, so the day a brew upgrade moves that directory this turns into a
// named failure instead of a help window that is empty for a week.
//
// The tags file is the same one vim reads: one line per tag, tab separated,
// "tag<TAB>file<TAB>search". The search half is an ex command, almost always
// "/*tag*", and jumping to it is a plain search for the literal.

// HelpDirs is where ":help" looks, in order, before the globs below. It is a
// variable so that a test can point it somewhere with three files in it, and
// because the day the doc directory moves the fix is one line and not a hunt.
//
// $VIMRUNTIME comes first and is not in this list, because it is what the
// installed vim itself answers and because a person who has one set means it.
//
// The doc directory is NOT where the register's D-001 says it is, and this
// is the day that row was written for. /opt/homebrew/bin/vim on this machine
// is a symlink into the macvim cask, and asking it prints
//
//	/opt/homebrew/Cellar/macvim/9.2.0321/MacVim.app/Contents/Resources/vim/runtime
//
// so /opt/homebrew/share/vim/vim92/doc does not exist at all. The fixed paths
// stay in the list because a plain homebrew vim does put them there; the glob
// over the cask is what actually finds the files, and the version number
// in the middle of that path is why it is a glob and not a constant.
var HelpDirs = []string{
	"/opt/homebrew/share/vim/vim92/doc",
	"/usr/share/vim/vim92/doc",
	"/usr/local/share/vim/vim92/doc",
}

// helpDir returns the first directory in HelpDirs that has a tags file in it,
// or the empty string. A glob is tried after the fixed list so that a vim 9.3
// upgrade is found rather than being an error nobody can act on.
func helpDir() string {
	if rt := os.Getenv("VIMRUNTIME"); rt != "" {
		if d := filepath.Join(rt, "doc"); hasTags(d) {
			return d
		}
	}
	for _, d := range HelpDirs {
		if hasTags(d) {
			return d
		}
	}
	for _, pattern := range []string{
		"/opt/homebrew/share/vim/vim*/doc",
		"/opt/homebrew/Cellar/macvim/*/MacVim.app/Contents/Resources/vim/runtime/doc",
		"/usr/share/vim/vim*/doc",
		"/usr/local/share/vim/vim*/doc",
	} {
		matches, _ := filepath.Glob(pattern)
		// Newest first, so a box with two versions installed gets the one a
		// person is most likely to be running.
		sort.Sort(sort.Reverse(sort.StringSlice(matches)))
		for _, d := range matches {
			if hasTags(d) {
				return d
			}
		}
	}
	return ""
}

// hasTags reports whether a directory holds a tags file, which is the one
// thing ":help" cannot do without.
func hasTags(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "tags"))
	return err == nil
}

// HelpTag is one line of the tags file.
type HelpTag struct {
	Name string
	File string
	// Search is the ex command that finds the tag in the file, which for every
	// line of vim's own tags file is "/*name*".
	Search string
}

// helpTags reads and caches the tags file. The file is 40,000 lines and is
// read once per session; a ":help" that reparsed it would be the slowest
// command in the editor.
var helpTags struct {
	dir    string
	tags   []HelpTag
	byName map[string]int
}

// loadHelpTags reads the tags file, once.
func loadHelpTags() ([]HelpTag, map[string]int, error) {
	dir := helpDir()
	if dir == "" {
		return nil, nil, ErrHelpNotFound
	}
	if helpTags.dir == dir && helpTags.tags != nil {
		return helpTags.tags, helpTags.byName, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "tags"))
	if err != nil {
		return nil, nil, ErrHelpNotFound
	}
	var tags []HelpTag
	index := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		index[parts[0]] = len(tags)
		tags = append(tags, HelpTag{Name: parts[0], File: filepath.Join(dir, parts[1]), Search: parts[2]})
	}
	helpTags.dir, helpTags.tags, helpTags.byName = dir, tags, index
	return tags, index, nil
}

// FindHelpTag resolves a ":help" argument.
//
// Vim tries several spellings before it gives up, and the order matters
// because the same word is often a tag twice: an exact match, then the same
// with a "'" round it for an option name, then a case-insensitive match, then
// the first tag that starts with it. Only when all four fail is it E149.
func FindHelpTag(arg string) (HelpTag, error) {
	tags, index, err := loadHelpTags()
	if err != nil {
		return HelpTag{}, err
	}
	if arg == "" {
		arg = "help.txt"
	}
	for _, try := range []string{arg, "'" + arg + "'"} {
		if i, ok := index[try]; ok {
			return tags[i], nil
		}
	}
	lower := strings.ToLower(arg)
	for _, t := range tags {
		if strings.ToLower(t.Name) == lower {
			return t, nil
		}
	}
	for _, t := range tags {
		if strings.HasPrefix(t.Name, arg) {
			return t, nil
		}
	}
	return HelpTag{}, withSpace(ErrNoHelpFor, arg)
}

// exHelp is ":help".
//
// The file opens read-only in a split, and the cursor lands on the tag's own
// line, which is the "*tag*" the search half of the tags line names. A tag
// that is in no tags file is E149 with the word in it, measured:
// ":help nosuchtag" says "E149: Sorry, no help for nosuchtag".
func exHelp(c *Context, cmd Cmd) error {
	arg := trimArgs(cmd.Args)
	tag, err := FindHelpTag(arg)
	if err != nil {
		return err
	}
	data, rerr := os.ReadFile(tag.File)
	if rerr != nil {
		return withSpace(ErrCannotOpen, ShortName(tag.File))
	}

	b := c.bufs().ByName(tag.File)
	if b == nil {
		b = c.bufs().Add(tag.File, text.Read(data))
		b.ReadOnly = true
		b.Listed = false
	}

	// Help opens in a split rather than over the buffer being edited, which is
	// what vim does and what makes ":help" not lose your place. A split that
	// cannot be made -- no tab pages, which is what a test builds -- falls
	// back to opening it in this window.
	if tab := c.tab(); tab != nil && c.Window() != nil {
		w := newWindow(c, b)
		if err := tab.Split(c.Window(), w, splitDir(false), true, 0); err == nil {
			tab.Cur = w
		}
	}
	c.swapBuffer(b)
	c.moveTo(helpLine(b.Text, tag))
	return nil
}

// helpLine finds the line the tag is defined on, by looking for the literal
// the tags file's search command names.
//
// The search half is "/*tagname*" for every line vim ships, so the literal is
// what is left after the "/" with no pattern interpretation at all. That is
// deliberate: a "*" in a help tag is a star and not a repetition, and running
// it through internal/regex would make ":help i_CTRL-W" land on the wrong
// line.
func helpLine(b *text.Buffer, tag HelpTag) int {
	needle := strings.TrimPrefix(tag.Search, "/")
	needle = strings.TrimSuffix(needle, "\r")
	if needle == "" {
		return 1
	}
	for n := 1; n <= b.LineCount(); n++ {
		if strings.Contains(string(b.Line(n)), needle) {
			return n
		}
	}
	return 1
}

// HelpTagNames returns every help tag, for wildmenu completion after ":help".
// It answers nothing rather than an error when the doc directory is missing,
// because a completion that fails is a completion that offers no candidates.
func HelpTagNames(prefix string) []string {
	tags, _, err := loadHelpTags()
	if err != nil {
		return nil
	}
	var out []string
	for _, t := range tags {
		if strings.HasPrefix(t.Name, prefix) {
			out = append(out, t.Name)
		}
	}
	sort.Strings(out)
	return out
}
