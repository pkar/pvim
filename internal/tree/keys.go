package tree

import "github.com/pkar/pvim/internal/key"

// Action is what a keystroke asked the editor to do that the tree cannot do
// itself: open a file somewhere, or put a menu up.
type Action int

// The actions.
const (
	// Nothing happened that the caller has to act on. Handled says whether
	// the key was the tree's at all.
	None Action = iota
	// Open, OpenTab, OpenVSplit and OpenSplit are "o" and <CR> on a file,
	// "t", "s" and "i". nerdtree's own words for the four are
	// {'where': 'p'}, 't', 'v' and 'h' (autoload/nerdtree/ui_glue.vim:533-574)
	// -- note that "s" is the VERTICAL split and "i" the horizontal one,
	// which reads backwards and is what the plugin does.
	Open
	OpenTab
	OpenVSplit
	OpenSplit
	// Menu is "m": the filesystem menu, whose items are add, delete and
	// rename here (nerdtree_plugin/fs_menu.vim). The prompting belongs to the
	// caller, which owns the command line.
	Menu
	// Redraw says the tree changed shape and the buffer has to be refilled.
	Redraw
	// Move says only the cursor has to go somewhere: "p", and the line the
	// tree wants the cursor on after a redraw.
	Move
)

// Result is what a key did.
type Result struct {
	// Handled is false for a key the tree has no binding for, which is most
	// of them: j, k, gg, G, CTRL-D and every other motion belong to the
	// editor, exactly as they do in a nerdtree buffer, and the caller passes
	// them on when this is false.
	Handled bool
	Action  Action
	// Node is the node the action applies to, nil when there is none.
	Node *Node
	// Line is where the cursor should end up, one-based, or 0 to leave it.
	Line int
}

// Key feeds one keystroke to the tree, with the cursor on the given one-based
// line.
//
// The bindings are nerdtree's defaults (plugin/NERD_tree.vim:103-141) narrowed
// to the nine plus the four that come free with them:
//
//	o <CR> activate: a directory opens and closes, a file opens
//	t open in a new tab
//	s open in a vertical split
//	i open in a horizontal split
//	m the filesystem menu
//	C make the node under the cursor the root
//	u move the root up a directory, closing the old root
//	R refresh the root from disk
//	I toggle hidden files
//	x close the parent of the node under the cursor
//	p put the cursor on the parent
//	? toggle the help banner
//
// Not bound, and each is a feature rather than a key: "U" (up, keeping the old
// root open), "r" and "e" (refresh or open one directory), "f" and "F" (toggle
// the ignore filter and the file filter), "B" and the bookmark family, "A"
// (zoom), "cd" and "CD", the "g"-prefixed openers, and the mouse. The tree
// itself can do "U" -- see Tree.Up -- and it is unbound because
// "u" and not both.
func (t *Tree) Key(k key.Key, line int) Result {
	n := t.NodeAt(line)
	switch {
	case k == key.Rune('o'), k.Special == key.KeyCR:
		if n == nil {
			return Result{Handled: true}
		}
		if n.Dir {
			t.Toggle(n)
			return Result{Handled: true, Action: Redraw, Node: n, Line: t.LineOf(n)}
		}
		return Result{Handled: true, Action: Open, Node: n}
	case k == key.Rune('t'):
		return t.openWith(n, OpenTab)
	case k == key.Rune('s'):
		return t.openWith(n, OpenVSplit)
	case k == key.Rune('i'):
		return t.openWith(n, OpenSplit)
	case k == key.Rune('m'):
		if n == nil {
			return Result{Handled: true}
		}
		return Result{Handled: true, Action: Menu, Node: n}
	case k == key.Rune('C'):
		if n == nil {
			return Result{Handled: true}
		}
		t.ChangeRoot(n)
		return Result{Handled: true, Action: Redraw, Line: t.FirstNodeLine()}
	case k == key.Rune('u'):
		old := t.root
		t.Up(false)
		return Result{Handled: true, Action: Redraw, Line: t.LineOf(old)}
	case k == key.Rune('R'):
		t.Refresh()
		return Result{Handled: true, Action: Redraw, Line: t.FirstNodeLine()}
	case k == key.Rune('I'):
		t.ToggleHidden()
		return Result{Handled: true, Action: Redraw, Line: t.FirstNodeLine()}
	case k == key.Rune('x'):
		// nerdtree's s:closeCurrentDir: the node's parent closes, unless that
		// parent is the root, which cannot be closed.
		if n == nil {
			return Result{Handled: true}
		}
		p := n.parent
		if p == nil || p == t.root {
			return Result{Handled: true}
		}
		t.Close(p)
		return Result{Handled: true, Action: Redraw, Line: t.LineOf(p)}
	case k == key.Rune('p'):
		if n == nil || n.parent == nil {
			return Result{Handled: true}
		}
		return Result{Handled: true, Action: Move, Node: n.parent, Line: t.LineOf(n.parent)}
	case k == key.Rune('?'):
		t.ToggleHelp()
		return Result{Handled: true, Action: Redraw, Line: t.FirstNodeLine()}
	}
	return Result{}
}

// openWith is "t", "s" and "i", which are file keys: nerdtree scopes all three
// to FileNode and Bookmark, so on a directory line they are unbound and the
// key does nothing at all.
func (t *Tree) openWith(n *Node, a Action) Result {
	if n == nil || n.Dir {
		return Result{Handled: true}
	}
	return Result{Handled: true, Action: a, Node: n}
}
