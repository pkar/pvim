// Package tree is nerdtree, reduced to the parts this vimrc can reach: a left
// split holding a directory listing, the ignore list and the hidden-file
// switch the vimrc sets, the nine keys, and the three menu
// items that touch the filesystem.
//
// # What is measured and what is not
//
// There is no oracle. vim cannot be asked what nerdtree would have drawn, so
// every rule here is either read out of the plugin at
// ~/.vim/pack/pkar/start/nerdtree and cited at the code that reproduces it, or
// it is a judgment call and says so in those words. The two kinds are kept
// apart on purpose: a reproduction that turns out to differ from the plugin is
// a bug, and a judgment call that differs is a decision somebody can argue
// with.
//
// # The one place this vimrc and the gate disagree
//
// The gate for the tree says "`,` shows the same tree, dotfiles included,
// node_modules and .git hidden in both". The first half and the second half
// cannot both be true. This vimrc sets NERDTreeShowHidden=1 and a
// NERDTreeIgnore of ['\~$','\.pyc$','\*NTUSER*','\*ntuser*','\NTUSER.DAT',
// '\ntuser.ini'], and nerdtree hides a file only when one of those patterns
// matches its name (lib/nerdtree/path.vim:471) or when it starts with a dot
// and hidden files are off (:450). Neither is true of .git or of node_modules,
// so nerdtree draws both, and any reproduction that hides them is not showing
// the same tree.
//
// This package is faithful to the plugin: .git and node_modules are in the
// tree, exactly as they are in nerdtree under this vimrc. The finder is the
// half of the gate that does hide them, because g:ctrlp_custom_ignore says so,
// and that half is reproduced exactly. A person who wants them out of the tree
// as well adds one pattern per line to NERDTreeIgnore -- '^\.git$[[dir]]' and
// '^node_modules$[[dir]]' -- and this package honours the [[dir]] suffix that
// makes them mean directories only.
package tree

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkar/pvim/internal/finder"
)

// The defaults the vimrc overrides, kept here so that a caller with no vimrc
// -- a test, or "pvim --clean" -- draws the tree nerdtree would draw.
const (
	// DefaultWinSize is g:NERDTreeWinSize (plugin/NERD_tree.vim:89). This
	// vimrc sets 80, which is a third of a 240-column window and is that
	// vimrc's own choice.
	DefaultWinSize = 31
	// ArrowExpandable and ArrowCollapsible are g:NERDTreeDirArrowExpandable
	// and its collapsible twin on a machine that is not Windows and not
	// Cygwin (plugin/NERD_tree.vim:59-65).
	ArrowExpandable  = "▸" // U+25B8 BLACK RIGHT-POINTING SMALL TRIANGLE
	ArrowCollapsible = "▾" // U+25BE BLACK DOWN-POINTING SMALL TRIANGLE
	// UpDirLine is nerdtree's fixed second line (lib/nerdtree/ui.vim:553).
	UpDirLine = ".. (up a dir)"
	// HelpLine is what the banner is reduced to when the help is not showing
	// (lib/nerdtree/ui.vim, the non-minimal header).
	HelpLine = `" Press ? for help`
	// IndentWidth is two spaces per level (UI.IndentWid, ui.vim:308, and the
	// repeat(' ', depth-1) in tree_file_node.vim:313).
	IndentWidth = 2
)

// DefaultIgnore is g:NERDTreeIgnore's own default (plugin/NERD_tree.vim:41),
// used when the vimrc sets none. This vimrc sets six patterns of its own and
// they replace this rather than adding to it, which is what a ":let" does.
var DefaultIgnore = []string{`\~$`}

// Node is one file or directory in the tree.
type Node struct {
	// Name is the last path component and Path the absolute path.
	Name, Path string
	// Dir, Link and Exec are the three things the display string can say
	// about a node: a directory gets a trailing "/" and an arrow, a symbolic
	// link gets " -> target", an executable gets a trailing "*"
	// (lib/nerdtree/path.vim:46-90).
	Dir, Link, Exec bool
	// LinkTarget is where a symbolic link points, as readlink gives it.
	LinkTarget string
	// Open says a directory is expanded. Only a directory is ever open.
	Open bool

	parent   *Node
	children []*Node
	loaded   bool
}

// Parent is the node this one hangs off, nil at the root.
func (n *Node) Parent() *Node { return n.parent }

// Children is the loaded children, in display order. A directory that has
// never been opened has none.
func (n *Node) Children() []*Node { return n.children }

// Tree is one tree: a root, what is expanded under it, and the rules that
// decide which children exist at all.
type Tree struct {
	// ShowHidden is g:NERDTreeShowHidden, which the "I" key toggles. This
	// vimrc sets it, so it starts true in pvim and false in a plain nerdtree.
	ShowHidden bool
	// Ignore is g:NERDTreeIgnore as the vimrc wrote it, patterns and
	// [[dir]]/[[file]]/[[path]] suffixes and all.
	Ignore []string

	root *Node
	// lines is the last render, and nodes the node each of those lines shows,
	// nil for the three header lines. They are the whole of how a keystroke
	// on line 14 finds out what it is on.
	lines [][]byte
	nodes []*Node
	// help says the "?" banner is expanded.
	help bool
	// err is the last filesystem error a reload hit, shown once by cmd/pvim.
	err error
}

// New builds a tree rooted at dir and opens the root.
func New(dir string, showHidden bool, ignore []string) (*Tree, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		abs = filepath.Dir(abs)
	}
	t := &Tree{ShowHidden: showHidden, Ignore: ignore}
	t.root = &Node{Name: filepath.Base(abs), Path: abs, Dir: true}
	t.Open(t.root)
	return t, t.err
}

// Root is the directory the tree is rooted at.
func (t *Tree) Root() *Node { return t.root }

// Err is the last error a directory read hit, or nil.
func (t *Tree) Err() error { return t.err }

// Open expands a directory, reading its children if this is the first time.
func (t *Tree) Open(n *Node) {
	if n == nil || !n.Dir {
		return
	}
	if !n.loaded {
		t.load(n)
	}
	n.Open = true
	t.render()
}

// Close collapses a directory. The root refuses, which is nerdtree's
// s:activateDirNode: "cannot close tree root".
func (t *Tree) Close(n *Node) {
	if n == nil || !n.Dir || n == t.root {
		return
	}
	n.Open = false
	t.render()
}

// Toggle is the "o" key on a directory.
func (t *Tree) Toggle(n *Node) {
	if n == nil || !n.Dir {
		return
	}
	if n.Open {
		t.Close(n)
		return
	}
	t.Open(n)
}

// Refresh is the "R" key: throw away every loaded child and read the root
// again. nerdtree's s:refreshRoot says "All nodes below this will be lost and
// the root dir will be reloaded", and this is that, with the set of open
// directories kept by path so the tree does not collapse under the person
// looking at it.
func (t *Tree) Refresh() {
	open := map[string]bool{}
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.Dir && n.Open {
			open[n.Path] = true
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(t.root)

	t.root.children, t.root.loaded, t.root.Open = nil, false, false
	t.err = nil
	var reopen func(n *Node)
	reopen = func(n *Node) {
		if !open[n.Path] {
			return
		}
		if !n.loaded {
			t.load(n)
		}
		n.Open = true
		for _, c := range n.children {
			reopen(c)
		}
	}
	reopen(t.root)
	t.root.Open = true
	if !t.root.loaded {
		t.load(t.root)
	}
	t.render()
}

// ChangeRoot is the "C" key: make n the root. A file makes its directory the
// root, which is nerdtree's NERDTree.changeRoot (lib/nerdtree/nerdtree.vim:11).
func (t *Tree) ChangeRoot(n *Node) {
	if n == nil {
		return
	}
	if !n.Dir {
		n = n.parent
		if n == nil {
			return
		}
	}
	n.parent = nil
	t.root = n
	t.Open(n)
}

// Up is the "u" key: the parent directory becomes the root, and the directory
// that was the root is closed.
//
// nerdtree#ui_glue#upDir(0) transplants the old root into the new one and then
// closes it; "U" (upDir(1)) leaves it open. Only "u" was asked for, and the
// difference is one bool, so both are here and cmd/pvim binds that one.
func (t *Tree) Up(keepOpen bool) {
	parent := filepath.Dir(t.root.Path)
	if parent == t.root.Path {
		// Already at the filesystem root. nerdtree echoes "already at root
		// directory" and does nothing.
		return
	}
	old := t.root
	fresh := &Node{Name: filepath.Base(parent), Path: parent, Dir: true}
	t.root = fresh
	t.load(fresh)
	// Transplant: the child of the new root with the old root's path is
	// replaced by the old root itself, so everything expanded under it stays
	// expanded. That is TreeDirNode.transplantChild.
	for i, c := range fresh.children {
		if c.Path == old.Path {
			old.parent = fresh
			fresh.children[i] = old
			break
		}
	}
	if !keepOpen {
		old.Open = false
	}
	fresh.Open = true
	t.render()
}

// ToggleHidden is the "I" key.
func (t *Tree) ToggleHidden() {
	t.ShowHidden = !t.ShowHidden
	// Every loaded directory has to be read again: the children that were
	// filtered out were never made, so there is nothing to unhide.
	t.reload(t.root)
	t.render()
}

// ToggleHelp is the "?" key. Only the banner changes; the help text itself is
// cmd/pvim's, because it names the keys cmd/pvim bound.
func (t *Tree) ToggleHelp() {
	t.help = !t.help
	t.render()
}

// Help reports whether the banner is expanded.
func (t *Tree) Help() bool { return t.help }

// reload re-reads every directory that has already been read.
func (t *Tree) reload(n *Node) {
	if n == nil || !n.Dir || !n.loaded {
		return
	}
	open := map[string]bool{}
	for _, c := range n.children {
		if c.Dir && c.Open {
			open[c.Path] = true
		}
	}
	kept := map[string]*Node{}
	for _, c := range n.children {
		kept[c.Path] = c
	}
	n.loaded = false
	t.load(n)
	for _, c := range n.children {
		if old, ok := kept[c.Path]; ok && old.Dir && c.Dir && old.loaded {
			// Keep the subtree that was already read rather than throwing it
			// away: "I" must not collapse the tree.
			c.children, c.loaded, c.Open = old.children, old.loaded, open[c.Path]
			for _, g := range c.children {
				g.parent = c
			}
			t.reload(c)
		}
	}
}

// load reads one directory's children, applying the ignore list and the
// hidden switch, and sorts them.
func (t *Tree) load(n *Node) {
	n.loaded = true
	n.children = nil
	m, err := t.matcher()
	if err != nil {
		t.err = err
		return
	}
	entries, err := os.ReadDir(n.Path)
	if err != nil {
		t.err = err
		return
	}
	for _, e := range entries {
		name := e.Name()
		dir := e.IsDir()
		link := e.Type()&os.ModeSymlink != 0
		full := filepath.Join(n.Path, name)
		var target string
		if link {
			// A link to a directory is drawn as a directory, which is what
			// nerdtree does: Path.readInfo resolves it and appends a "/" to
			// the target (lib/nerdtree/path.vim:643).
			if st, err := os.Stat(full); err == nil {
				dir = st.IsDir()
			}
			target, _ = os.Readlink(full)
		}
		// The path handed to the matcher is the entry's name, because
		// nerdtree matches the tail; the [[path]] patterns get the whole
		// path, which is what the Rules split is for.
		if dir {
			if m.SkipDir(strings.TrimPrefix(full, "/"), name) {
				continue
			}
		} else if m.SkipFile(strings.TrimPrefix(full, "/"), name) {
			continue
		}
		c := &Node{Name: name, Path: full, Dir: dir, Link: link, LinkTarget: target, parent: n}
		if !dir {
			// Stat and not the DirEntry's own info, which is an lstat: a
			// symbolic link's own mode bits are 0777 on macOS and every link
			// would be drawn with the executable marker. nerdtree asks
			// getfperm() about the path, which follows the link too.
			if info, err := os.Stat(full); err == nil {
				c.Exec = info.Mode()&0o111 != 0
			}
		}
		n.children = append(n.children, c)
	}
	sortChildren(n.children)
}

// matcher compiles the ignore list, splitting nerdtree's three suffixes into
// the three fields internal/finder's Rules has.
//
// The suffixes are nerdtree's Path._ignorePatternMatches (path.vim:502):
// "[[path]]" matches the whole path, "[[dir]]" only directories, "[[file]]"
// only files, and a pattern with none of them matches the name of anything.
// Case sensitive, because that function ends in "=~#" and there is no \c in
// sight.
func (t *Tree) matcher() (*finder.Matcher, error) {
	r := finder.Rules{ShowHidden: t.ShowHidden, Tail: true}
	for _, p := range t.Ignore {
		switch {
		case strings.HasSuffix(p, "[[path]]"):
			r.Path = append(r.Path, strings.TrimSuffix(p, "[[path]]"))
		case strings.HasSuffix(p, "[[dir]]"):
			r.Dir = append(r.Dir, strings.TrimSuffix(p, "[[dir]]"))
		case strings.HasSuffix(p, "[[file]]"):
			r.File = append(r.File, strings.TrimSuffix(p, "[[file]]"))
		default:
			r.Dir = append(r.Dir, p)
			r.File = append(r.File, p)
		}
	}
	return r.Compile()
}

// sortChildren is nerdtree's order under its own defaults.
//
// g:NERDTreeSortOrder is ['\/$', '*', '\.swp$', '\.bak$', '\~$']
// (plugin/NERD_tree.vim:70) and Path.getSortOrderIndex matches each name --
// with a "/" glued on for a directory -- against that list in order, taking
// the first hit and falling back to the index of '*'. So directories are group
// 0 because only they end in "/", swap files, backups and tilde files are
// groups 2, 3 and 4, and everything else is group 1.
//
// Within a group the comparison is the name, lower-cased because
// g:NERDTreeCaseSensitiveSort is 0, with the leading dot kept because
// g:NERDTreeSortHiddenFirst is 1 -- which is what puts .git and .gitignore
// above README in a tree that shows hidden files.
//
// Not reproduced: g:NERDTreeNaturalSort (0 by default, so "f10" sorts before
// "f2" here as it does there) and the [[timestamp]], [[size]] and
// [[extension]] tags, none of which the default order uses. A user
// g:NERDTreeSortOrder is not read at all, because internal/vimrc's ReadVars
// does not carry one -- see the report in cmd/pvim/finder.go.
func sortChildren(cs []*Node) {
	sort.SliceStable(cs, func(i, j int) bool {
		gi, gj := sortGroup(cs[i]), sortGroup(cs[j])
		if gi != gj {
			return gi < gj
		}
		li, lj := strings.ToLower(cs[i].Name), strings.ToLower(cs[j].Name)
		if li != lj {
			return li < lj
		}
		return cs[i].Name < cs[j].Name
	})
}

func sortGroup(n *Node) int {
	if n.Dir {
		return 0
	}
	switch {
	case strings.HasSuffix(n.Name, ".swp"):
		return 2
	case strings.HasSuffix(n.Name, ".bak"):
		return 3
	case strings.HasSuffix(n.Name, "~"):
		return 4
	}
	return 1
}

// Lines is the buffer's contents and NodeAt the node a line is showing.
func (t *Tree) Lines() [][]byte { return t.lines }

// NodeAt takes a one-based buffer line and gives the node drawn on it, or nil
// for a header line.
func (t *Tree) NodeAt(line int) *Node {
	if line < 1 || line > len(t.nodes) {
		return nil
	}
	return t.nodes[line-1]
}

// LineOf is NodeAt backwards: the one-based line a node is drawn on, or 0.
func (t *Tree) LineOf(n *Node) int {
	for i, x := range t.nodes {
		if x == n {
			return i + 1
		}
	}
	return 0
}

// FirstNodeLine is the line the first real node is on, which is where the
// cursor goes when the tree opens. nerdtree puts it on the root.
func (t *Tree) FirstNodeLine() int {
	for i, n := range t.nodes {
		if n != nil {
			return i + 1
		}
	}
	return 1
}

// render redraws the whole buffer.
//
// The header is nerdtree's, minus the bookmark table: the help banner, the
// "up a dir" line, then the root's own path with a trailing slash
// (Path._strForUI, path.vim:750). The root is a node like any other for the
// purposes of NodeAt, which is what makes "C" and "o" on the root line work.
func (t *Tree) render() {
	t.lines = t.lines[:0]
	t.nodes = t.nodes[:0]

	add := func(s string, n *Node) {
		t.lines = append(t.lines, []byte(s))
		t.nodes = append(t.nodes, n)
	}
	if t.help {
		for _, l := range helpLines {
			add(l, nil)
		}
	} else {
		add(HelpLine, nil)
		add("", nil)
	}
	add(UpDirLine, nil)
	rootPath := t.root.Path
	if !strings.HasSuffix(rootPath, "/") {
		rootPath += "/"
	}
	add(rootPath, t.root)
	t.renderChildren(t.root, 1, add)
}

func (t *Tree) renderChildren(n *Node, depth int, add func(string, *Node)) {
	if !n.Open {
		return
	}
	for _, c := range n.children {
		add(line(c, depth), c)
		if c.Dir {
			t.renderChildren(c, depth+1, add)
		}
	}
}

// line is one node's rendered line.
//
// Indentation is two spaces per level below the root, and a file gets two more
// so its name lines up with the directory names beside it rather than with
// their arrows: that is the isDirectory conditional in
// TreeFileNode._renderToString (tree_file_node.vim:313).
//
// Not reproduced: the cascade. nerdtree collapses a chain of directories with
// one child each onto one line -- "a/b/c/" -- under
// g:NERDTreeCascadeSingleChildDir, which defaults on. It is a display trick
// with a cursor-position consequence on every key that walks the tree, and
// leaving it out costs a line per directory and nothing else. Judgment call.
func line(n *Node, depth int) string {
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", IndentWidth*(depth-1)))
	if n.Dir {
		if n.Open {
			b.WriteString(ArrowCollapsible)
		} else {
			b.WriteString(ArrowExpandable)
		}
		b.WriteString(" ")
	} else {
		b.WriteString("  ")
	}
	b.WriteString(n.Name)
	if n.Dir {
		b.WriteString("/")
	}
	if n.Exec {
		b.WriteString("*")
	}
	if n.Link && n.LinkTarget != "" {
		b.WriteString(" -> " + n.LinkTarget)
	}
	return b.String()
}

// helpLines is the "?" banner.
//
// nerdtree's is generated from the mappings it made, with a section per scope
// and the quick-help subset first. This is the subset pvim binds, written out,
// because generating it would mean this package knowing which keys cmd/pvim
// chose and it does not.
var helpLines = []string{
	`" ----------------------------`,
	`" File node mappings~`,
	`" o: open in prev window`,
	`" t: open in new tab`,
	`" i: open split`,
	`" s: open vsplit`,
	`"`,
	`" Directory node mappings~`,
	`" o: open & close node`,
	`" x: close parent of node`,
	`" C: change tree root to node`,
	`"`,
	`" Tree navigation mappings~`,
	`" p: go to parent`,
	`" u: move tree root up a dir`,
	`"`,
	`" Filesystem mappings~`,
	`" m: Show menu`,
	`"`,
	`" Tree filtering mappings~`,
	`" I: hidden files (on)`,
	`" R: refresh tree`,
	`"`,
	`" Other mappings~`,
	`" ?: toggle help`,
	`" ----------------------------`,
	"",
}
