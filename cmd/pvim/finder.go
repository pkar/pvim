package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkar/pvim/internal/ex"
	"github.com/pkar/pvim/internal/finder"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/tree"
	"github.com/pkar/pvim/internal/vimrc"
	"github.com/pkar/pvim/internal/window"
)

// The wiring for the two plugin replacements: internal/finder on CTRL-P and
// internal/tree on ",".
//
// Neither package knows what a window is. This file is where a match becomes a
// buffer in a split, a keystroke becomes a Result, and a Result becomes an
// ":edit". It is also where the vimrc's variables are read: the two packages
// take plain Go values and never see a vimrc.Val.
//
// # Where the state lives, and why it is in a map
//
// A plugins value per editor, in a package-level map keyed by the editor.
// It belongs on the editor struct as one field and cmd/pvim/editor.go belongs
// to somebody else this week, so it is here in the one shape that needs no
// line in that file. The day the field lands, pluginsFor becomes `e.plug` and
// this map and its two helpers go away; nothing else changes.
//
// The map is written and read on the editor goroutine only -- installPlugins
// runs inside newFrontendEditor, everything else runs from a key or a command
// -- so it needs no lock, and uninstallPlugins takes the entry out when the
// editor quits so a test that builds a hundred editors does not keep a hundred
// of them alive.
var pluginState = map[*editor]*plugins{}

// plugins is the finder and the tree, and the project they are both looking
// at.
//
// The root and the ignore rules are here and not in either package because
// both need them and they must not disagree: a file the finder ignores and the
// tree shows is a bug that reads as a mystery. See internal/finder.Rules for
// which vimrc line becomes which field.
type plugins struct {
	ed *editor

	// root is the project: the nearest ancestor of the file the editor was
	// opened on that holds a .git. Recomputed when the buffer changes, which
	// is what makes CTRL-P in a file from another repository search that
	// repository -- ctrlp's g:ctrlp_working_path_mode of "ra".
	root string
	// rules is the finder's rule set, built once from 'wildignore' and
	// g:ctrlp_custom_ignore.
	rules finder.Rules
	// maxHeight is g:ctrlp_max_height, treeSize g:NERDTreeWinSize,
	// showHidden g:NERDTreeShowHidden and nerdIgnore g:NERDTreeIgnore.
	maxHeight  int
	treeSize   int
	showHidden bool
	nerdIgnore []string

	// The finder, and the window and buffer it is drawn in.
	find    *finder.Finder
	findBuf *ex.Buf
	findWin *window.Window
	// files is the last walk, kept so that CTRL-F to buffers and back does
	// not walk the tree again. F5 throws it away, which is ctrlp's
	// PrtClearCache.
	files []string

	// The tree, and the window and buffer it is drawn in.
	tree    *tree.Tree
	treeBuf *ex.Buf
	treeWin *window.Window

	// ask is the one-line prompt the tree menu puts up, nil when there is
	// none. See asker.
	ask *asker

	// pending is the scratch buffer being swapped in right now, before the
	// field that will hold it has been assigned. openSplit sets it because
	// the persistence guard asks isPluginBuffer during the swap and would
	// otherwise answer no about a buffer that is about to be the tree.
	pending *ex.Buf

	// mru is ctrlp's most-recently-used list and last the file it was last
	// told about. See noteBuffer for why it is a poll and not an event.
	mru  finder.MRU
	last string
}

// asker is a question on the message line that takes its answer a keystroke at
// a time.
//
// It exists because ex.Context.Prompt answers with one byte out of a fixed set
// -- it is the machinery behind "(y/n)?" -- and the tree menu needs a typed
// line for a file name. It is a modal state on the plugins value rather than a
// blocking read for the reason every other prompt in this program is not one
// either: the frontends draw after the handler returns, so a loop that blocked
// for keys would show the person a prompt with nothing they typed in it.
type asker struct {
	prompt string
	text   string
	// choices, when set, makes this a one-key question: the first key that is
	// one of these answers it and nothing is echoed.
	choices string
	done    func(answer string)
}

// installPlugins gives an editor its finder and its tree.
//
// Called from newFrontendEditor, after the vimrc has been read, because every
// value it reads comes from the vimrc. --oracle does not come through there
// and so has no finder, no tree and no CTRL-P, which is what keeps the 809
// graded cases exactly where they were.
func installPlugins(e *editor) *plugins {
	p := &plugins{
		ed:         e,
		maxHeight:  40,
		treeSize:   tree.DefaultWinSize,
		nerdIgnore: tree.DefaultIgnore,
	}
	p.readVars()
	p.root = finder.Root(e.file)
	p.guardPersistence()
	// The file the editor was opened on is the first MRU entry, which is
	// ctrlp's BufWinEnter on the first buffer of the session. Without it the
	// list only ever holds the SECOND file opened, because noteBuffer records
	// a change and startup is not one.
	p.last = e.file
	if e.file != "" {
		if st, err := os.Stat(e.file); err == nil && !st.IsDir() {
			p.mru.Push(e.file)
		}
	}
	pluginState[e] = p
	return p
}

// guardPersistence keeps the swap file away from the two scratch buffers.
//
// Both of them have a name -- "ControlP" and "NERD_tree_1", which is what the
// plugins call theirs and what the status line shows -- and everything below
// editor.open treats a name as a file: reopenPersist reads it, initPersist
// makes a swap file for it, and recoverSwap puts an E325 prompt up if one is
// already there. Left alone, opening the match window wrote
// /tmp/%...%ControlP.swp, and the next run of the same test asked the person
// whether to recover a crashed edit of a file that has never existed.
//
// The hook internal/ex already calls on every buffer swap is the place to stop
// it, and cmd/pvim owns the function that hook points at. So this wraps it: a
// plugin buffer is swapped in with the persistence layer set aside, and the
// swap file of the buffer being left is closed on the way, because
// editor.persistAfterKey syncs whatever buffer is current into whatever swap
// is open and a "j" in the tree would otherwise write the tree into the last
// file's recovery data.
//
// What it costs: while the finder or the tree is the current buffer, the file
// behind the other window has no swap file, so a crash in that second loses
// what a crash a keystroke earlier would have kept. Leaving the plugin window
// puts it back, because the same hook runs the other way and initPersist makes
// a fresh one. The alternative is a persist that holds one swap per buffer,
// which is written up as the fix in editor.go's own reopenPersist comment and
// belongs to whoever owns persistence.
func (p *plugins) guardPersistence() {
	e := p.ed
	if e.ctx == nil {
		return
	}
	prev := e.ctx.Open
	e.ctx.Open = func(b *ex.Buf) {
		if !p.isPluginBuffer(b) {
			if prev != nil {
				prev(b)
			}
			return
		}
		pers := e.pers
		e.pers = nil
		if prev != nil {
			prev(b)
		}
		e.pers = pers
		if e.pers != nil {
			_ = e.pers.closeSwap()
		}
	}
}

// pluginsFor is the editor's plugins, or nil for an editor that never had any:
// --oracle, and every test that builds an editor with newEditor directly.
func pluginsFor(e *editor) *plugins { return pluginState[e] }

// pluginsForSession is the same answer asked from the other side, because
// cmd/pvim/cmdline.go has a session and not an editor and a session field
// would mean changing another file. The scan is over the live
// editors, of which a running pvim has exactly one.
func pluginsForSession(s *session) *plugins {
	for e, p := range pluginState {
		if e.sess == s {
			return p
		}
	}
	return nil
}

// uninstallPlugins forgets an editor. See the note on pluginState.
func uninstallPlugins(e *editor) { delete(pluginState, e) }

// readVars pulls the six variables the vimrc sets for these two plugins out of
// what the loader kept.
//
// internal/vimrc's ReadVars is the list of names that get this far; anything
// else the vimrc sets was logged and dropped, so a name missing here means the
// vimrc did not set it and the default stands.
func (p *plugins) readVars() {
	if n, ok := p.varNum("g:ctrlp_max_height"); ok && n > 0 {
		p.maxHeight = n
	}
	if n, ok := p.varNum("g:NERDTreeWinSize"); ok && n > 0 {
		p.treeSize = n
	}
	if n, ok := p.varNum("g:NERDTreeShowHidden"); ok {
		p.showHidden = n != 0
	}
	if l, ok := p.varList("g:NERDTreeIgnore"); ok {
		p.nerdIgnore = l
	}

	p.rules = finder.Rules{IgnoreCase: true}
	if p.ed.opt != nil {
		p.rules.WildIgnore = []string{p.ed.opt.G.WildIgnore}
	}
	// g:ctrlp_custom_ignore is the awkward one: a dictionary literal spread
	// over three lines with backslash continuations. internal/vimrc parses it,
	// so this reads the two keys rather than hardcoding the patterns -- a vimrc
	// that changes them changes what the finder ignores with no change here.
	if d, ok := p.varDict("g:ctrlp_custom_ignore"); ok {
		if v, ok := d["dir"]; ok {
			p.rules.Dir = []string{v.String()}
		}
		if v, ok := d["file"]; ok {
			p.rules.File = []string{v.String()}
		}
	} else {
		// ctrlp's own default, from s:ignore() in autoload/ctrlp.vim:11. A
		// vimrc with no dictionary in it still keeps .git out of the finder,
		// which is what the gate asks for.
		p.rules.Dir = []string{`\v[\/](\.git|\.hg|\.svn|_darcs|\.bzr)$`}
	}
}

// varVal is a variable the vimrc set, under either spelling.
//
// Either spelling because the vimrc writes "NERDTreeShowHidden" with no scope
// prefix and internal/vimrc qualifies it to "g:NERDTreeShowHidden" on the way
// in, exactly as vim does; a caller here should not have to know which side of
// that it is on.
func (p *plugins) varVal(name string) (vimrc.Val, bool) {
	if l, ok := p.ed.vars[name]; ok {
		return l.Val, true
	}
	bare := strings.TrimPrefix(name, "g:")
	if l, ok := p.ed.vars[bare]; ok {
		return l.Val, true
	}
	return vimrc.Val{}, false
}

func (p *plugins) varNum(name string) (int, bool) {
	v, ok := p.varVal(name)
	if !ok || v.Kind != vimrc.ValueNumber {
		return 0, false
	}
	return v.Num, true
}

func (p *plugins) varList(name string) ([]string, bool) {
	v, ok := p.varVal(name)
	if !ok || v.Kind != vimrc.ValueList {
		return nil, false
	}
	out := make([]string, 0, len(v.List))
	for _, e := range v.List {
		out = append(out, e.String())
	}
	return out, true
}

func (p *plugins) varDict(name string) (map[string]vimrc.Val, bool) {
	v, ok := p.varVal(name)
	if !ok || v.Kind != vimrc.ValueDict {
		return nil, false
	}
	return v.Dict, true
}

// pluginKey is the whole of the keyboard this file owns.
//
// It runs in front of the mapping layer and the mode machine, which is what
// makes the finder modal: while it is up, every key is the finder's, exactly as
// ctrlp's own buffer-local mappings over every printable character make every
// key ctrlp's. The tree is the other way round -- it takes the twelve keys it
// binds and hands back everything else, so j, k, gg, G and CTRL-D move the
// cursor in the tree buffer as they do in nerdtree.
//
// A false answer means the key was not this file's and the editor should go on
// as if none of this existed.
func pluginKey(e *editor, k key.Key) bool {
	p := pluginsFor(e)
	if p == nil {
		return false
	}
	p.noteBuffer()

	if p.ask != nil {
		p.askKey(k)
		return true
	}
	if p.find != nil && p.onFinder() {
		p.finderKey(k)
		return true
	}
	if p.tree != nil && p.onTree() {
		if p.treeKey(k) {
			return true
		}
		// Not one of the tree's keys. It falls through to CTRL-P below and
		// then to the editor, which is what makes j, k, gg and CTRL-D move
		// the cursor in the tree buffer exactly as they do in nerdtree's.
	}
	// CTRL-P opens the finder, and only from normal mode: it is vim's
	// keyword completion in insert mode and ctrlp does not bind it there
	// either (g:ctrlp_map is a plain "nnoremap").
	if k == key.Ctrl('p') && e.ed.Mode() == mode.Normal {
		p.openFinder()
		return true
	}
	return false
}

// noteBuffer keeps the MRU list and the project root up to date.
//
// A poll on every keystroke and not an event, because pvim has no BufWinEnter
// to hang one off: internal/vimrc parses autocommands and cmd/pvim fires four
// events, none of them the three ctrlp records on. Comparing a string on each
// key is cheap and it is right at exactly the moments that matter -- the key
// after a ":e", a CTRL-P open or a socket handing over a file.
func (p *plugins) noteBuffer() {
	cur := p.ed.file
	if cur == p.last {
		return
	}
	p.last = cur
	if cur == "" || p.isPluginBuffer(p.ed.cur()) {
		return
	}
	if st, err := os.Stat(cur); err != nil || st.IsDir() {
		// ctrlp's s:addtomrufs skips a file that is not readable, and a
		// buffer that has never been written is not a file yet.
		return
	}
	p.mru.Push(cur)
	// 'autochdir' moves the working directory under the buffer; the project
	// moves with it, and a file from another repository has to search that
	// repository. ctrlp answers this with working_path_mode "ra" and this is
	// the same answer.
	p.root = finder.Root(cur)
}

func (p *plugins) isPluginBuffer(b *ex.Buf) bool {
	return b != nil && (b == p.findBuf || b == p.treeBuf || b == p.pending)
}

func (p *plugins) onFinder() bool { return p.findBuf != nil && p.ed.cur() == p.findBuf }
func (p *plugins) onTree() bool   { return p.treeBuf != nil && p.ed.cur() == p.treeBuf }

// pluginCommand is the ex side: the two command lines this file answers.
//
// It is consulted before internal/ex sees the line, and it answers true only
// for a line it took. Everything else goes to the ex layer untouched.
//
// # Why ":NERDTreeToggle" is here and not a user command
//
// The vimrc has `nnoremap <leader>, :NERDTreeToggle<CR>` and to
// define that as a real user command so the mapping works unchanged. A real
// user command is an entry in ex.UserCommands, and a UserCommand is a name and
// a REPLACEMENT STRING: ":command! Foo bar" makes ":Foo" run ":bar", and there
// is no way to give one a Go body. So a real user command would have to expand
// to some other ex command that reaches this code, and every spelling of that
// is worse than this: the honest ones need a hook in internal/ex, and the
// dishonest ones abuse ":edit" of a file that does not exist.
//
// What internal/ex needs is one field -- `Run func(*Context, Cmd) error` on
// UserCommand, tried at the top of runUser -- and then this becomes four lines
// registering NERDTreeToggle in e.ctx.Cmds and this interception goes away.
// The same field is what :Git, :Gstatus, :Gblame and the LSP commands need
// too, so it is written down here rather than worked around twice. Until it
// exists, ":NERDTreeToggle" is not in ":command"'s listing and answers E492
// nowhere, because it never reaches the table.
//
// The name has to be typed in full. vim resolves a user command by unique
// prefix, so real nerdtree answers ":NERDTreeTog" as well; this matches the
// whole name, because a prefix rule over one command would swallow every
// ":N..." line the ex layer has an answer for. The vimrc's mapping types the
// whole name, so nothing this config does notices.
//
// ":e <dir>" is here for a different reason and it is not a workaround:
// internal/ex's openFile does os.ReadFile on the name and a directory comes
// back EISDIR, so ":e ." says "E484: Can't open file" before any hook this
// file could decorate is reached. That one wants a hook too -- a Context
// callback consulted when the name is a directory -- and it is the same
// report.
func pluginCommand(s *session, line string) (bool, error) {
	p := pluginsForSession(s)
	if p == nil {
		return false, nil
	}
	cmd, err := ex.Parse(line)
	if err != nil {
		return false, nil
	}
	switch {
	case cmd.Typed == "NERDTreeToggle":
		p.toggleTree(strings.TrimSpace(cmd.Args))
		return true, nil
	case cmd.Name == "edit":
		arg := strings.TrimSpace(cmd.Args)
		if arg == "" {
			return false, nil
		}
		// The tree replaces netrw, which is what ~/.vim/.netrwhist was being
		// written for. pvim has never had netrw and writes no history file;
		// this is the other half of that, which is that ":e ." now opens
		// something rather than failing.
		//
		// ":edit" and not the whole family: ":sp <dir>" and ":tabedit <dir>"
		// still answer E484, where nerdtree would open a tree in the new
		// window. They are the same three lines each and they are left out
		// until the hook exists, because four copies of an interception is
		// four things to delete rather than one.
		if st, err := os.Stat(expandTilde(arg)); err == nil && st.IsDir() {
			p.openTree(expandTilde(arg))
			return true, nil
		}
	}
	return false, nil
}

// ---------------------------------------------------------------- the finder

// openFinder puts the match window up.
func (p *plugins) openFinder() {
	if p.find != nil {
		return
	}
	// Out of the tree window first, if that is where CTRL-P was pressed: the
	// match window is a horizontal split of the current one, and splitting
	// the tree would leave it 80 columns wide and half as tall rather than
	// putting the matches across the bottom of the screen.
	if p.treeWin != nil && p.ed.ctx.Window() == p.treeWin && p.otherWindow() != nil {
		if err := p.ed.ctx.RunLine("wincmd w"); err != nil {
			p.ed.ed.Say(err.Error())
			return
		}
	}
	cur := p.ed.ctx.Window()
	if cur == nil {
		return
	}
	height := p.finderHeight(cur)
	p.find = finder.New(p.root, height)
	p.find.Current = p.ed.file
	p.loadFinderItems()

	buf, b, w := p.openSplit("ControlP", p.find.Lines(), window.Horizontal, false, height)
	if buf == nil {
		p.find = nil
		return
	}
	p.findBuf, p.findWin = b, w
	p.drawFinder()
}

// finderHeight is g:ctrlp_max_height, capped at what the window can give.
//
// ctrlp's own cap is `min([s:mw_max, &lines])` (autoload/ctrlp.vim:2501) and
// then `res height` on a window it opened with "botright 1new", so on a 40-row
// terminal with g:ctrlp_max_height=40 the match window really does take
// everything but a row or two. The second cap here is the same arithmetic
// TabPage.Split does -- a horizontal split has the window's height less its
// two status lines to share, and the window being split keeps at least
// 'winminheight' of it -- worked out in front rather than after, because the
// number is also how many matches the finder keeps and a finder that kept
// forty when the window can draw eighteen would have twenty-two of them off
// the top.
func (p *plugins) finderHeight(cur *window.Window) int {
	h := p.maxHeight
	if h > p.ed.rows {
		h = p.ed.rows
	}
	if room := cur.View.Height - 3; h > room {
		h = room
	}
	if h < 1 {
		h = 1
	}
	return h
}

// loadFinderItems fills the three lists.
func (p *plugins) loadFinderItems() {
	if p.files == nil {
		m, err := p.rules.Compile()
		if err != nil {
			p.ed.ed.Say("ctrlp: " + err.Error())
			p.files = []string{}
		} else {
			files, err := finder.Walk(p.root, m, finder.DefaultWalk())
			if err != nil {
				p.ed.ed.Say("ctrlp: " + err.Error())
				files = []string{}
			}
			p.files = files
		}
	}
	items := make([]finder.Item, 0, len(p.files))
	for _, f := range p.files {
		items = append(items, finder.Item{Display: f, Path: filepath.Join(p.root, f)})
	}
	p.find.SetItems(finder.Files, items)

	var bufs []finder.Item
	if p.ed.ctx != nil && p.ed.ctx.Bufs != nil {
		for _, b := range p.ed.ctx.Bufs.Bufs {
			if !b.Listed || b.Name == "" || p.isPluginBuffer(b) {
				continue
			}
			bufs = append(bufs, finder.Item{Display: p.short(b.Name), Path: b.Name})
		}
	}
	p.find.SetItems(finder.Buffers, bufs)

	var mru []finder.Item
	for _, m := range p.mru.List() {
		mru = append(mru, finder.Item{Display: p.short(m), Path: m})
	}
	p.find.SetItems(finder.MRUFiles, mru)
}

// short is ctrlp's g:ctrlp_mruf_map_string: a path under the project shows
// relative to it and anything else shows as it is.
func (p *plugins) short(path string) string {
	if rel, err := filepath.Rel(p.root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

// finderKey feeds one key to the finder and acts on what it asked for.
func (p *plugins) finderKey(k key.Key) {
	r := p.find.Key(k)
	switch r.Action {
	case finder.Close:
		p.closeFinder()
	case finder.Refresh:
		p.files = nil
		p.loadFinderItems()
		p.drawFinder()
	case finder.Open, finder.OpenTab, finder.OpenVSplit, finder.OpenSplit:
		path := r.Item.Path
		p.closeFinder()
		p.openPath(path, openWhere(r.Action))
	default:
		p.drawFinder()
	}
}

// openWhere maps the finder's four accept actions onto the ex command that
// does each one.
func openWhere(a finder.Action) string {
	switch a {
	case finder.OpenTab:
		return "tabedit"
	case finder.OpenVSplit:
		return "vsplit"
	case finder.OpenSplit:
		return "split"
	default:
		return "edit"
	}
}

// drawFinder refills the match window, sizes it to the results and puts the
// prompt under it.
func (p *plugins) drawFinder() {
	lines := p.find.Lines()
	p.setLines(p.findBuf, lines)
	p.resizeFinder(len(lines))
	if p.findWin != nil {
		p.findWin.View.TopLine = 1
	}
	// The caret sits on the selected line, which on a bottom-to-top window is
	// counted from the end.
	sel := len(lines)
	if n := len(p.find.Matches()); n > 0 {
		sel = n - indexOfSelection(p.find)
	}
	p.ed.ed.SetCursor(text.Pos{Line: sel, Col: 0})
	// The prompt goes on the message line, which is the row under the match
	// window, which is where ctrlp echoes it. One Say per keystroke leaves a
	// line per keystroke in the message log, which nothing but ":messages"
	// would ever notice and which the mode machine has no way to overwrite in
	// place.
	p.ed.ed.Say(p.find.Prompt() + "   [" + p.find.Mode().String() + "]")
	p.ed.sess.sync()
}

// resizeFinder makes the match window exactly as tall as it has results.
//
// This is ctrlp's `let height = min([max([s:mw_min, s:res_count]), s:winmaxh])`
// (autoload/ctrlp.vim:741), run on every redraw: the window grows as a query
// widens and shrinks as it narrows, and with two matches it is two rows and
// not forty rows of tildes.
//
// The two sizes are moved together because internal/window's share() keeps
// stored sizes only when they add up to the space the frame has -- a Size
// written to one child on its own is shared away again on the next frame. See
// the note on openSplit for why the size goes through the tree at all.
func (p *plugins) resizeFinder(want int) {
	if p.findWin == nil {
		return
	}
	tab := p.ed.tabs.Current()
	if tab == nil {
		return
	}
	parent := parentNodeOf(tab.Root, p.findWin)
	if parent == nil || parent.Dir != window.Horizontal || len(parent.Children) < 2 {
		return
	}
	var self, sibling *window.Node
	for i, c := range parent.Children {
		if c.Win == p.findWin {
			self = c
			if i > 0 {
				sibling = parent.Children[i-1]
			} else {
				sibling = parent.Children[1]
			}
		}
	}
	if self == nil || sibling == nil {
		return
	}
	total := self.Size + sibling.Size
	if want < 1 {
		want = 1
	}
	if want > total-1 {
		want = total - 1
	}
	if want == self.Size || want < 1 {
		return
	}
	sibling.Size = total - want
	self.Size = want
	tab.Relayout()
	p.ed.relayout()
}

// parentNodeOf is the frame a window hangs in, or nil for a window that is the
// whole tab. internal/window keeps find() to itself, and this needs only the
// one level.
func parentNodeOf(n *window.Node, w *window.Window) *window.Node {
	if n == nil || n.Leaf() {
		return nil
	}
	for _, c := range n.Children {
		if c.Win == w {
			return n
		}
		if hit := parentNodeOf(c, w); hit != nil {
			return hit
		}
	}
	return nil
}

// indexOfSelection is the selection's place in the ranked list, which the
// finder does not expose directly and which the caret needs.
func indexOfSelection(f *finder.Finder) int {
	sel, ok := f.Selected()
	if !ok {
		return 0
	}
	for i, m := range f.Matches() {
		if m == sel {
			return i
		}
	}
	return 0
}

// closeFinder takes the match window away and goes back to where CTRL-P was
// pressed.
func (p *plugins) closeFinder() {
	p.closeWindow(p.findWin, p.findBuf)
	p.find, p.findBuf, p.findWin = nil, nil, nil
	// Say and not NewMessageLine: the message line shows the last thing said,
	// and messages.empty() writes a newline into the redirect without adding
	// a line to the log, so the prompt this finder had been drawing every
	// keystroke would still be on the screen after it closed.
	p.ed.ed.Say("")
}

// ------------------------------------------------------------------ the tree

// toggleTree is ":NERDTreeToggle", whose three cases are nerdtree's own
// (Creator.toggleTabTree): never opened, opens; open, closes; opened before and
// closed, opens again where it was.
func (p *plugins) toggleTree(dir string) {
	switch {
	case p.treeWin != nil:
		p.closeTree()
	case p.tree != nil && dir == "":
		p.showTree()
	default:
		root := p.root
		if dir != "" {
			root = expandTilde(dir)
		}
		p.openTree(root)
	}
}

// openTree builds a tree at dir and shows it.
func (p *plugins) openTree(dir string) {
	t, err := tree.New(dir, p.showHidden, p.nerdIgnore)
	if err != nil {
		p.ed.ed.Say("NERDTree: " + err.Error())
		return
	}
	p.tree = t
	if p.treeWin != nil {
		p.drawTree(t.FirstNodeLine())
		return
	}
	p.showTree()
}

// showTree opens the window for a tree that already exists.
func (p *plugins) showTree() {
	if p.tree == nil {
		return
	}
	buf, b, w := p.openSplit("NERD_tree_1", p.tree.Lines(), window.Vertical, true, p.treeSize)
	if buf == nil {
		return
	}
	p.treeBuf, p.treeWin = b, w
	p.drawTree(p.tree.FirstNodeLine())
}

func (p *plugins) closeTree() {
	p.closeWindow(p.treeWin, p.treeBuf)
	p.treeBuf, p.treeWin = nil, nil
}

// treeKey feeds one key to the tree. It answers false for a key the tree does
// not bind, which the editor then gets: that is how j and k still move.
func (p *plugins) treeKey(k key.Key) bool {
	if k == key.Rune('Z') {
		// The tree buffer is vim's 'buftype=nofile' and ZZ on one is E382 in
		// vim. pvim has no 'buftype', so the error is spelled out here rather
		// than letting ZZ write a file called NERD_tree_1 into the working
		// directory.
		p.ed.ed.Say("E382: Cannot write, 'buftype' option is set")
		return true
	}
	line := p.ed.ed.Cursor().Line
	r := p.tree.Key(k, line)
	if !r.Handled {
		return false
	}
	switch r.Action {
	case tree.Redraw:
		p.drawTree(r.Line)
	case tree.Move:
		p.moveCursor(r.Line)
	case tree.Menu:
		p.treeMenu(r.Node)
	case tree.Open:
		p.openFromTree(r.Node.Path, "edit")
	case tree.OpenTab:
		p.openFromTree(r.Node.Path, "tabedit")
	case tree.OpenVSplit:
		p.openFromTree(r.Node.Path, "vsplit")
	case tree.OpenSplit:
		p.openFromTree(r.Node.Path, "split")
	}
	return true
}

// drawTree refills the tree buffer and puts the cursor on a line.
func (p *plugins) drawTree(line int) {
	p.setLines(p.treeBuf, p.tree.Lines())
	if err := p.tree.Err(); err != nil {
		p.ed.ed.Say("NERDTree: " + err.Error())
	}
	p.moveCursor(line)
}

func (p *plugins) moveCursor(line int) {
	if line <= 0 {
		line = 1
	}
	p.ed.ed.SetCursor(text.Pos{Line: line, Col: 0})
	p.ed.sess.sync()
}

// openFromTree opens a file with the tree left where it is, which is
// nerdtree's {'where': 'p'}: the file goes in the window the tree is not in.
//
// With the tree as the only window there is no previous window to go to, and
// nerdtree makes one; ":botright vsplit" is that, on the side the tree is not.
func (p *plugins) openFromTree(path, how string) {
	if how == "edit" {
		if p.otherWindow() == nil {
			how = "botright vsplit"
		} else if err := p.ed.ctx.RunLine("wincmd w"); err != nil {
			p.ed.ed.Say(err.Error())
			return
		}
	}
	p.openPath(path, how)
}

// otherWindow is any window in the current tab that is not the tree's.
func (p *plugins) otherWindow() *window.Window {
	tab := p.ed.tabs.Current()
	if tab == nil {
		return nil
	}
	for _, w := range tab.Windows() {
		if w != p.treeWin {
			return w
		}
	}
	return nil
}

// treeMenu is the "m" key: the three filesystem items, asked for one line at a
// time.
func (p *plugins) treeMenu(n *tree.Node) {
	p.ask = &asker{
		prompt:  tree.MenuPrompt,
		choices: "amd",
		done: func(answer string) {
			switch answer {
			case "a":
				p.askAdd(n)
			case "m":
				p.askMove(n)
			case "d":
				p.askDelete(n)
			}
		},
	}
	p.ed.ed.SayPrompt(tree.MenuPrompt)
}

func (p *plugins) askAdd(n *tree.Node) {
	dir := n
	if !n.Dir {
		dir = n.Parent()
	}
	seed := ""
	if dir != nil {
		seed = dir.Path + string(filepath.Separator)
	}
	p.startAsk(tree.AddPrompt, seed, func(answer string) {
		node, err := p.tree.Add(n, answer)
		if err != nil {
			p.sayMenuErr(err, "Node Creation Aborted.")
			return
		}
		p.drawTree(p.tree.LineOf(node))
	})
}

func (p *plugins) askMove(n *tree.Node) {
	p.startAsk(tree.MovePrompt, n.Path, func(answer string) {
		from, to, err := p.tree.Rename(n, answer)
		if err != nil {
			p.sayMenuErr(err, "Node Renaming Aborted.")
			return
		}
		p.renameBuffer(from, to)
		p.drawTree(p.tree.LineOf(p.tree.Find(to)))
	})
}

func (p *plugins) askDelete(n *tree.Node) {
	prompt, choices := tree.DeletePrompt+n.Path+" (yN): ", "yn"
	if tree.NotEmpty(n) {
		prompt, choices = tree.DeleteDirPrompt+n.Path+": ", ""
	}
	done := func(answer string) {
		if answer != "y" && answer != "yes" {
			p.ed.ed.Say("Delete Aborted.")
			return
		}
		if err := p.tree.Delete(n); err != nil {
			p.sayMenuErr(err, "Delete Aborted.")
			return
		}
		p.drawTree(p.tree.FirstNodeLine())
	}
	if choices == "" {
		p.startAsk(prompt, "", done)
		return
	}
	p.ask = &asker{prompt: prompt, choices: choices, done: done}
	p.ed.ed.SayPrompt(prompt)
}

func (p *plugins) sayMenuErr(err error, aborted string) {
	if errors.Is(err, tree.ErrAborted) {
		p.ed.ed.Say(aborted)
		return
	}
	p.ed.ed.Say("NERDTree: " + err.Error())
}

// renameBuffer is the half of a rename that belongs to the editor: a file that
// was open under its old name is renamed with it.
//
// nerdtree asks first -- "The old file is open in buffer 3. Replace this
// buffer with the new file? (yN)" -- and does the swap through a temporary
// buffer to survive a case-insensitive filesystem. This renames without
// asking, which is a judgment call: the alternative is a buffer whose ":w"
// recreates the file that was just moved, and nobody wants that outcome badly
// enough to be asked about it.
func (p *plugins) renameBuffer(from, to string) {
	if p.ed.ctx == nil || p.ed.ctx.Bufs == nil {
		return
	}
	for _, b := range p.ed.ctx.Bufs.Bufs {
		switch {
		case b.Name == from:
			b.Name = to
		case strings.HasPrefix(b.Name, from+string(filepath.Separator)):
			b.Name = to + strings.TrimPrefix(b.Name, from)
		default:
			continue
		}
		if b == p.ed.cur() {
			p.ed.file = b.Name
			p.ed.ed.SetFileName(b.Name)
		}
	}
}

// ------------------------------------------------------------------ the ask

// startAsk puts a typed prompt up, seeded with text the person can delete.
func (p *plugins) startAsk(prompt, seed string, done func(string)) {
	p.ask = &asker{prompt: prompt, text: seed, done: done}
	p.ed.ed.SayPrompt(prompt + seed)
}

// askKey is one keystroke at a prompt.
func (p *plugins) askKey(k key.Key) {
	a := p.ask
	switch {
	case k.Special == key.KeyEsc, k == key.Ctrl('c'):
		p.ask = nil
		p.ed.ed.Say("")
		return
	case a.choices != "":
		if !k.IsRune() || k.Rune > 0x7f {
			return
		}
		if !strings.ContainsRune(a.choices, k.Rune) {
			return
		}
		p.ask = nil
		p.ed.ed.SayAnswer(byte(k.Rune))
		a.done(string(k.Rune))
		return
	case k.Special == key.KeyCR:
		p.ask = nil
		p.ed.ed.Say("")
		a.done(a.text)
		return
	case k.Special == key.KeyBS, k == key.Ctrl('h'):
		if r := []rune(a.text); len(r) > 0 {
			a.text = string(r[:len(r)-1])
		}
	case k == key.Ctrl('u'):
		a.text = ""
	case k.IsRune() && k.Mod == 0 && k.Rune >= ' ' && k.Rune != 0x7f:
		a.text += string(k.Rune)
	default:
		return
	}
	p.ed.ed.Say(a.prompt + a.text)
}

// --------------------------------------------------------------- the windows

// openSplit makes a scratch buffer in a new window of the given size.
//
// This is internal/ex's own openScratch, which is unexported, written again
// against the exported half of the package. The size is the reason it is not
// ":botright new" and then a resize: TabPage.Split takes the size and writes
// both siblings' Size fields so they add up, and internal/window's share()
// keeps sizes only when they do -- a Size assigned to one child on its own is
// re-shared away on the next frame.
//
// Not "botright" or "topleft" but a split of the CURRENT window, which is the
// same thing when there is one window and is not when there are three. That is
// a judgment call and it is the small one: ctrlp opens `botright 1new` and
// nerdtree `topleft vertical 31 new`, both of which take a band off the whole
// screen. Doing that means splitting the frame at the root of the layout tree,
// and TabPage.Split splits a window.
func (p *plugins) openSplit(name string, lines [][]byte, dir window.Dir, before bool, size int) (*text.Buffer, *ex.Buf, *window.Window) {
	e := p.ed
	tab := e.tabs.Current()
	cur := e.ctx.Window()
	if tab == nil || cur == nil {
		return nil, nil, nil
	}
	buf := text.New()
	nb := e.ctx.Bufs.Add(name, buf)
	nb.Scratch = true
	nb.Listed = false

	fresh := window.New(nextWindowID(e), buf, e.opt.GW)
	fresh.View.Width = cur.View.Width
	fresh.View.Height = cur.View.Height
	fresh.Opt = cur.Opt
	fresh.SetHeight(cur.View.Height)
	if err := tab.Split(cur, fresh, dir, before, size); err != nil {
		e.ctx.Bufs.Remove(nb)
		e.ed.Say(err.Error())
		return nil, nil, nil
	}
	tab.Cur = fresh
	e.ctx.Bufs.Cur = nb
	// Through the hook and not straight to editor.open, because the hook is
	// where guardPersistence keeps the swap file away from a buffer that has
	// a name and no file, and named before the call for the same reason.
	p.pending = nb
	e.ctx.Open(nb)
	p.pending = nil
	e.relayout()
	p.setLines(nb, lines)
	return buf, nb, fresh
}

// nextWindowID is internal/ex's nextWinID, which is unexported for the same
// reason openScratch is.
func nextWindowID(e *editor) int {
	n := 0
	for _, t := range e.tabs.Pages {
		for _, w := range t.Windows() {
			if w.ID > n {
				n = w.ID
			}
		}
	}
	return n + 1
}

// closeWindow puts a plugin window away and forgets its buffer.
//
// ":close" and not TabPage.Close, because the ex layer knows what has to
// happen afterwards: which window becomes current, and that the editor and the
// mode machine follow it to the buffer that window is showing. Doing it by
// hand here would be a second copy of followWindow.
func (p *plugins) closeWindow(w *window.Window, b *ex.Buf) {
	e := p.ed
	if w == nil {
		return
	}
	if tab := e.tabs.Current(); tab != nil && len(tab.Windows()) > 1 {
		tab.Cur = w
		e.ctx.Bufs.Cur = b
		if err := e.ctx.RunLine("close"); err != nil {
			e.ed.Say(err.Error())
		}
	}
	if b != nil {
		e.ctx.Bufs.Remove(b)
	}
	e.relayout()
	e.sess.sync()
}

// setLines replaces a scratch buffer's whole contents.
func (p *plugins) setLines(b *ex.Buf, lines [][]byte) {
	if b == nil || b.Text == nil {
		return
	}
	if len(lines) == 0 {
		lines = [][]byte{{}}
	}
	buf := b.Text
	buf.InsertLines(0, lines)
	buf.DeleteLines(len(lines)+1, buf.LineCount())
	b.MarkSaved()
}

// openPath runs the ex command that opens a file.
func (p *plugins) openPath(path, how string) {
	if path == "" {
		return
	}
	if err := p.ed.ctx.RunLine(how + " " + escapeExArg(path)); err != nil {
		p.ed.ed.Say(err.Error())
		return
	}
	p.ed.sess.sync()
}
