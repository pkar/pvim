package tree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The three menu items that touch the filesystem: (a)dd, (d)elete and (m)ove,
// which is nerdtree_plugin/fs_menu.vim's whole always-present list:
//
//	call NERDTreeAddMenuItem({'text': '(a)dd a childnode', ...})
//	call NERDTreeAddMenuItem({'text': '(m)ove the current node', ...})
//	call NERDTreeAddMenuItem({'text': '(d)elete the current node', ...})
//
// The rest of that file is platform glue -- reveal in the Finder, open with
// the system editor, quicklook, copy, copy path, chmod, run a command -- and
// none of it is here.
//
// The prompting is not here. cmd/pvim owns the command line and does the
// asking; this file is what happens after the answer, so that the rules about
// what a trailing slash means and what happens to an open buffer are testable
// without a terminal.

// MenuPrompt is the menu itself, in nerdtree's words and order.
//
// nerdtree draws the list, waits for a key and runs the item whose shortcut
// was pressed (lib/nerdtree/menu_controller.vim). pvim has one message line
// rather than a window to draw a menu in, so the three items are one line and
// the shortcut is the next keystroke -- the same interaction, one line high.
const MenuPrompt = "Menu: (a)dd a childnode, (m)ove the current node, (d)elete the current node"

// The prompts, from s:inputPrompt (nerdtree_plugin/fs_menu.vim:58), in the
// non-minimal form with the rule the person needs on the same line as the
// question, because pvim's command line is one row and nerdtree's is three.
const (
	AddPrompt    = "Add a childnode. Enter the dir/file name to be created. Dirs end with a '/': "
	MovePrompt   = "Rename the current node. Enter the new path for the node: "
	DeletePrompt = "Delete the current node: "
	// DeleteDirPrompt is the second question a non-empty directory asks. The
	// answer has to be the word "yes" and not a keystroke, which is
	// nerdtree's own defence: `let confirmed = choice ==# 'yes'`.
	DeleteDirPrompt = "STOP! Directory is not empty! To delete, type 'yes': "
)

// Errors the menu answers with. They are plain errors and not E-codes: no vim
// command produces them, so there is no code to reproduce.
var (
	// ErrExists is nerdtree's "This destination already exists, Try again."
	ErrExists = errors.New("this destination already exists")
	// ErrAborted is an empty answer at a prompt: "Node Creation Aborted." and
	// "Node Renaming Aborted."
	ErrAborted = errors.New("aborted")
)

// Add creates a child of dir.
//
// The name is taken relative to dir when it is relative and used as written
// when it is absolute, which is what nerdtree's pre-filled prompt makes
// natural: the input starts as the directory's own path with a slash on the
// end, and whatever is typed after it is an absolute path.
//
// A name ending in a separator makes a directory and anything else makes a
// file, which is the rule the prompt states. Missing parents are created
// either way (Path.Create does mkdir(..., 'p')).
func (t *Tree) Add(dir *Node, name string) (*Node, error) {
	if dir == nil {
		return nil, ErrAborted
	}
	if !dir.Dir {
		dir = dir.parent
		if dir == nil {
			return nil, ErrAborted
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrAborted
	}
	isDir := strings.HasSuffix(name, "/")
	full := name
	if !filepath.IsAbs(full) {
		full = filepath.Join(dir.Path, name)
	}
	full = filepath.Clean(full)
	if _, err := os.Lstat(full); err == nil {
		return nil, ErrExists
	}
	if isDir {
		if err := os.MkdirAll(full, 0o755); err != nil {
			return nil, err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	}
	// The tree is reloaded from the nearest loaded ancestor rather than from
	// the root, because a file created three directories down under a
	// directory nobody has opened has nothing to appear in.
	t.reload(t.nearestLoaded(full))
	// And then opened down to it, which is what nerdtree does before it puts
	// the cursor on the new node: "a/b/c" typed at the prompt makes three
	// directories and shows all three.
	t.reveal(full)
	t.render()
	return t.Find(full), nil
}

// Delete removes a node. A directory goes with everything under it, which is
// the "rm -rf" nerdtree shells out to; the caller is what asked the "type
// yes" question first.
func (t *Tree) Delete(n *Node) error {
	if n == nil || n.parent == nil {
		// The root refuses, because deleting the directory the tree is
		// rooted at leaves nothing to draw.
		return ErrAborted
	}
	if err := os.RemoveAll(n.Path); err != nil {
		return err
	}
	parent := n.parent
	t.reload(parent)
	t.render()
	return nil
}

// NotEmpty reports whether a directory has anything in it, which is the
// question that decides which of the two delete prompts is asked.
func NotEmpty(n *Node) bool {
	if n == nil || !n.Dir {
		return false
	}
	f, err := os.Open(n.Path)
	if err != nil {
		return false
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	return err == nil && len(names) > 0
}

// Rename moves a node. The destination is taken as written when absolute and
// relative to the node's own directory otherwise.
//
// The old path is returned so that the caller can do the half of this that
// belongs to the editor: nerdtree asks "The old file is open in buffer N.
// Replace this buffer with the new file?" and swaps the buffer over, and the
// buffer list is cmd/pvim's.
func (t *Tree) Rename(n *Node, dest string) (from, to string, err error) {
	if n == nil || n.parent == nil {
		return "", "", ErrAborted
	}
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return "", "", ErrAborted
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(filepath.Dir(n.Path), dest)
	}
	dest = filepath.Clean(dest)
	if dest == n.Path {
		return "", "", ErrAborted
	}
	if _, err := os.Lstat(dest); err == nil {
		return "", "", ErrExists
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", "", err
	}
	from = n.Path
	if err := os.Rename(from, dest); err != nil {
		return "", "", err
	}
	t.reload(t.nearestLoaded(dest))
	t.reload(n.parent)
	t.render()
	return from, dest, nil
}

// reveal opens every directory between the root and path so that the node for
// path exists and is drawn.
func (t *Tree) reveal(path string) {
	n := t.root
	for n != nil && n.Path != path {
		next := (*Node)(nil)
		for _, c := range n.children {
			if c.Path == path || strings.HasPrefix(path, c.Path+string(filepath.Separator)) {
				next = c
				break
			}
		}
		if next == nil {
			return
		}
		if next.Dir && next.Path != path {
			if !next.loaded {
				t.load(next)
			}
			next.Open = true
		}
		n = next
	}
}

// Find is the node for a path, or nil when the tree has not loaded it.
func (t *Tree) Find(path string) *Node {
	var walk func(n *Node) *Node
	walk = func(n *Node) *Node {
		if n.Path == path {
			return n
		}
		for _, c := range n.children {
			if strings.HasPrefix(path, c.Path) {
				if hit := walk(c); hit != nil {
					return hit
				}
			}
		}
		return nil
	}
	return walk(t.root)
}

// nearestLoaded is the deepest loaded directory that holds path, which is the
// one a reload has to start from.
func (t *Tree) nearestLoaded(path string) *Node {
	best := t.root
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.children {
			if !c.Dir || !c.loaded {
				continue
			}
			if strings.HasPrefix(path, c.Path+string(filepath.Separator)) {
				best = c
				walk(c)
			}
		}
	}
	walk(t.root)
	return best
}
