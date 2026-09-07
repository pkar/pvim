package main

// The git commands and the keys that go with them: what this editor keeps of
// fugitive, plus diff mode.
//
// internal/git shells out to git and owns every answer about a repository;
// internal/diff owns the line diff, the filler arithmetic, ]c and [c and what
// do and dp move. This file is the third of the three and it is the only one
// that knows what a window is: it opens the splits, puts the buffers in them,
// dispatches the keys and puts the messages on the message line.
//
// # Where this hangs off the editor
//
// The state is a package-level map keyed by *session, made on the first ":G"
// command. It belongs on the editor struct as one field, and
// cmd/pvim/editor.go belongs to somebody else this week, so it is here in the
// shape that needs no line in that file: everything this file wants -- the
// mode machine, the ex context, the tab list, the option state, the relayout
// and the buffer-open hook -- reaches through the session, and the two hooks
// that dispatch into it are both handed one. cmd/pvim/finder.go keeps a map
// of its own for the same reason and says so.
//
// # What is not here, and where it has to go
//
// Diff mode draws two things this file cannot draw. Filler lines are display
// rows with no buffer line behind them, and DiffAdd, DiffChange, DiffText and
// DiffDelete are highlight groups over buffer lines; both belong to
// internal/screen, which owns the Grid and which another change is in. So the
// whole of the diff model is computed here and measured against `vim -d` in
// internal/diff, and none of it is painted yet: ]c, [c, do and dp all work on
// the real hunks, and the two windows show their two buffers with no filler
// rows between the hunks and no colour on them. The seam is written up in the
// report and it is two calls: a Filler(win, lnum) the renderer asks before it
// draws a line, and four group names in internal/screen's table.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/pkar/pvim/internal/diff"
	"github.com/pkar/pvim/internal/ex"
	"github.com/pkar/pvim/internal/git"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// gitState is one gitPlugin per session. See the note at the top of the file
// for why it is a map and not a field on the editor, and cmd/pvim/finder.go's
// pluginState for the other half of the same story.
//
// Keyed by the session and not by the editor, which is what lets this file
// need no line in cmd/pvim/editor.go at all: everything it wants -- the mode
// machine, the ex context, the tab list, the option state and the relayout --
// hangs off the session or off the context the session carries, and the key
// hook and the command hook are both handed one.
//
// Written and read on the editor goroutine only: every entry is made by a
// command and every read is from a command or a key, both of which run there.
// The commit runs off it and communicates through an atomic pointer, which is
// the one thing in this file that crosses goroutines.
//
// One entry per session that has run a ":G" command, and there is one session
// per running editor. Nothing removes an entry, because a session has no
// close: a test that built thousands would hold them, and none does.
var gitState = map[*session]*gitPlugin{}

// gitPlugin is the status buffer, the blame window and the diff pairs of one
// session.
type gitPlugin struct {
	s    *session
	repo *git.Repo

	// The status buffer: what ":Gstatus" opened, the state it was drawn from
	// and what each of its lines refers to.
	statusBuf *ex.Buf
	statusWin *window.Window
	status    *git.Status
	refs      []git.Line

	// The blame window: the column, the file window it is locked to, and the
	// blame it was drawn from.
	blameBuf  *ex.Buf
	blameWin  *window.Window
	blameOn   *window.Window
	blameFile string
	blame     []git.BlameLine
	// blameTop is the last line the two windows were synchronised at, so
	// that the scroll lock can tell which of the two moved.
	blameTop int

	// diffs are the windows in diff mode, in pairs.
	diffs []*gitDiff

	// pending is the half of a two-key command that has arrived: 'c' for cc,
	// ']' or '[' for the hunk jumps, 'd' for do and dp.
	pending key.Key

	// commit is the git commit started by "cc", which runs off the editor
	// goroutine because it blocks until the tab holding COMMIT_EDITMSG is
	// closed and that close is a keystroke this editor has to be free to
	// read. Its result is picked up on the next key. See startCommit.
	commit atomic.Pointer[commitResult]
	// running says a commit is in flight, so that a second "cc" says so
	// rather than starting a second git.
	running bool

	// editorCmd is what git is told to run for the commit message, empty for
	// "this binary, with --wait". A test sets it to a shell script: in a test
	// binary os.Executable is the test binary, and handing THAT to git as its
	// editor would run the whole suite again inside itself.
	editorCmd string
}

// commitResult is what the background git commit left behind.
type commitResult struct {
	out []byte
	err error
}

// gitDiff is two windows showing two versions of one file.
type gitDiff struct {
	win [2]*window.Window
	buf [2]*ex.Buf
	opt diff.Options
	// name is what the message line calls the left-hand side, which is the
	// revision the file is being compared against.
	name string
}

// gitFor is the session's git state, made on demand.
func gitFor(s *session) *gitPlugin {
	if s == nil || s.ctx == nil {
		return nil
	}
	if p, ok := gitState[s]; ok {
		return p
	}
	p := &gitPlugin{s: s}
	gitState[s] = p
	return p
}

// cur is the buffer every command acts on.
func (p *gitPlugin) cur() *ex.Buf {
	if p.s.ctx.Bufs == nil {
		return nil
	}
	return p.s.ctx.Bufs.Cur
}

// file is the name of that buffer, empty for one that has never had a file.
func (p *gitPlugin) file() string {
	if b := p.cur(); b != nil {
		return b.Name
	}
	return ""
}

// say puts a line on the message line, and relayout redraws after a window
// changed shape. Both are one call each and both are here so that the rest of
// the file reads as what it does rather than as where it reaches.
func (p *gitPlugin) say(msg string) { p.s.ed.Say(msg) }

func (p *gitPlugin) relayout() {
	if p.s.relayout != nil {
		p.s.relayout()
	}
}

// show makes a buffer the one the editor is acting on, through the ex layer's
// own Open hook, which is what carries the registers and the search pattern
// across a buffer swap.
func (p *gitPlugin) show(b *ex.Buf) {
	if p.s.ctx.Open != nil {
		p.s.ctx.Open(b)
	}
}

// ------------------------------------------------------------------ commands

// gitCommand answers the ":G" family before the ex layer sees the line.
//
// It is a hook in cmd/pvim/cmdline.go for the same reason
// cmd/pvim/finder.go's pluginCommand is: internal/ex has a user command table
// with a replacement STRING in it and no way to give a command a Go body, so
// a command implemented in Go cannot be registered and has to be intercepted.
// The seam that would make this unnecessary is one field on ex.UserCommand --
// a Run func(*Context, Cmd) error consulted by runUser before it expands the
// replacement -- and it is reported rather than written, because internal/ex
// is somebody else's this week and because the finder, the tree and the LSP
// all want the same field.
//
// A true answer means the line was this file's and the ex layer must not see
// it. An error is a message, not a stop.
func gitCommand(s *session, line string) (bool, error) {
	cmd, err := ex.Parse(line)
	if err != nil {
		return false, nil
	}
	name := cmd.Typed
	if name == "" || name[0] != 'G' {
		return false, nil
	}
	switch name {
	case "G", "Git", "Gstatus", "Gblame", "Gdiff", "Gcommit", "Gedit":
	default:
		return false, nil
	}
	p := gitFor(s)
	if p == nil {
		return false, nil
	}
	p.forget()
	if err := p.openRepo(); err != nil {
		return true, err
	}
	args := strings.TrimSpace(cmd.Args)
	switch name {
	case "Gstatus", "G":
		return true, p.showStatus()
	case "Git":
		if args == "" {
			return true, p.showStatus()
		}
		return true, p.runGit(args)
	case "Gblame":
		return true, p.showBlame(args)
	case "Gdiff":
		return true, p.showDiff(args)
	case "Gcommit":
		return true, p.startCommit(args)
	case "Gedit":
		return true, p.openUnderCursor()
	}
	return false, nil
}

// open finds the repository the current buffer is in, or reports that there
// is none.
func (p *gitPlugin) openRepo() error {
	dir := p.file()
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = wd
	} else {
		dir = filepath.Dir(dir)
	}
	if p.repo != nil && p.repo.Dir == dir {
		return nil
	}
	r, err := git.Find(dir)
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
			return errors.New("not a git repository")
		}
		return err
	}
	p.repo = r
	return nil
}

// runGit is ":Git {args}": the subcommand's output in a scratch split.
//
// The arguments are split on white space and not handed to a shell. That is
// the judgment call and it is deliberate: a shell would mean ":Git commit -m
// 'two words'" worked and ":Git log --grep=$HOME" did something surprising,
// and the editor already has ":!" for the shell. Quoting is honoured so that
// the first of those two still works.
func (p *gitPlugin) runGit(args string) error {
	argv := splitArgs(args)
	if len(argv) == 0 {
		return p.showStatus()
	}
	out, err := p.repo.Run(argv...)
	lines := splitLines(out)
	if len(lines) == 0 {
		if err != nil {
			return err
		}
		p.say("git " + args + ": nothing to show")
		return nil
	}
	p.openSplitBuf(":Git "+args, lines, window.Horizontal, false, 0)
	// A failed git is still shown, because what it said is why it failed; the
	// message line says it failed so that an empty-looking split is not read
	// as success.
	if err != nil {
		p.say("git " + argv[0] + ": " + err.Error())
	}
	return nil
}

// showStatus opens or refreshes the status buffer.
func (p *gitPlugin) showStatus() error {
	st, err := p.repo.Status()
	if err != nil {
		return err
	}
	p.status = st
	lines, refs := st.Render()
	p.refs = refs
	if p.statusBuf != nil && p.statusWin != nil {
		p.setLines(p.statusBuf, toBytes(lines))
		p.focus(p.statusWin, p.statusBuf)
		p.clampCursor(len(lines))
		return nil
	}
	_, b, w := p.openSplitBuf(gitStatusName(p.repo.Root), toBytes(lines), window.Horizontal, true, 0)
	if b == nil {
		return errors.New("no room for the status window")
	}
	p.statusBuf, p.statusWin = b, w
	// The cursor starts on the first file rather than on "Head:", because
	// that is the line a "-" is for.
	for i, r := range refs {
		if r.Kind == git.File {
			p.s.ed.SetCursor(text.Pos{Line: i + 1, Col: 0})
			break
		}
	}
	p.s.sync()
	return nil
}

// gitStatusName is what the status buffer is called, which is what the status
// line shows while the cursor is in it.
//
// ".git/index" under the working tree root, because that is the file the "-"
// key writes to and because fugitive's own name for this buffer resolves to
// the same path. A judgment call: the alternative was a bracketed label like
// the finder's "ControlP", and a path is better here because a person with
// two repositories open in two tabs can tell the two status buffers apart.
func gitStatusName(root string) string {
	return filepath.Join(root, ".git", "index")
}

// showBlame is ":Gblame": the blame column in a left split, scroll locked to
// the file it came from.
func (p *gitPlugin) showBlame(args string) error {
	b := p.cur()
	if b == nil || b.Name == "" {
		return errors.New("no file to blame")
	}
	if b.Modified() {
		// git blames what is on disk. Saying so beats blaming a stale file
		// and letting the line numbers drift.
		p.say("blaming the file on disk; this buffer has unwritten changes")
	}
	rel, err := p.repo.Rel(b.Name)
	if err != nil {
		return err
	}
	bl, err := p.repo.Blame(rel, splitArgs(args)...)
	if err != nil {
		return err
	}
	if p.blameWin != nil {
		p.closeBlame()
	}
	p.blame = bl
	p.blameFile = rel
	p.blameOn = p.s.ctx.Window()
	lines := toBytes(git.RenderBlame(bl))
	width := 0
	for _, l := range lines {
		if len(l) > width {
			width = len(l)
		}
	}
	_, nb, w := p.openSplitBuf(rel+" (blame)", lines, window.Vertical, true, width)
	if nb == nil {
		return errors.New("no room for the blame window")
	}
	p.blameBuf, p.blameWin = nb, w
	if p.blameOn != nil {
		w.View.TopLine = p.blameOn.View.TopLine
		p.blameTop = w.View.TopLine
		line := p.blameOn.View.Cursor.Line
		if line < 1 {
			line = 1
		}
		p.s.ed.SetCursor(text.Pos{Line: min(line, len(bl)), Col: 0})
	}
	p.sayBlame()
	p.s.sync()
	return nil
}

// closeBlame takes the blame window away.
func (p *gitPlugin) closeBlame() {
	p.closeWin(p.blameWin, p.blameBuf)
	p.blameBuf, p.blameWin, p.blameOn, p.blame = nil, nil, nil, nil
	p.blameFile = ""
}

// sayBlame puts the subject of the commit under the cursor on the message
// line, which is the thing an eight character hash is standing in for.
func (p *gitPlugin) sayBlame() {
	line := p.s.ed.Cursor().Line
	if line < 1 || line > len(p.blame) {
		return
	}
	b := p.blame[line-1]
	if b.Uncommitted() {
		p.say("not committed yet")
		return
	}
	p.say(b.Abbrev + " " + b.Summary)
}

// showDiff is ":Gdiff": the file as the index has it, in a vertical split
// beside the file as it is now, with both windows in diff mode.
//
// The argument is a revision, so ":Gdiff HEAD~1" works and ":Gdiff" is the
// index. Vertical because the vimrc's 'diffopt' says vertical.
func (p *gitPlugin) showDiff(rev string) error {
	b := p.cur()
	if b == nil || b.Name == "" {
		return errors.New("no file to diff")
	}
	rel, err := p.repo.Rel(b.Name)
	if err != nil {
		return err
	}
	var blob []byte
	name := rel + " (index)"
	if rev == "" {
		blob, err = p.repo.IndexBlob(rel)
	} else {
		blob, err = p.repo.Blob(rev, rel)
		name = rel + " (" + rev + ")"
	}
	if err != nil {
		return err
	}
	cur := p.s.ctx.Window()
	curBuf := b
	opt := diff.ParseOptions(p.s.ctx.Opt.G.DiffOpt)
	// 'diffopt' says which way to split, which is what the "vertical" in the
	// vimrc's own "diffopt=vertical,filler,iwhite" is for. Vim's default has
	// no "vertical" in it and splits horizontally; so does this.
	dir := window.Horizontal
	if opt.Vertical {
		dir = window.Vertical
	}
	_, nb, w := p.openSplitBuf(name, git.Lines(blob), dir, true, 0)
	if nb == nil {
		return errors.New("no room for the diff window")
	}
	nb.ReadOnly = true
	d := &gitDiff{
		win:  [2]*window.Window{w, cur},
		buf:  [2]*ex.Buf{nb, curBuf},
		opt:  opt,
		name: name,
	}
	p.diffs = append(p.diffs, d)
	pair := p.pairOf(d)
	p.say(gitDiffSummary(pair))
	p.s.sync()
	return nil
}

// gitDiffSummary is what the message line says when a diff opens, so that a
// diff with no filler rows on the screen still tells a person how many hunks
// there are. It comes out the day internal/screen can draw them.
func gitDiffSummary(pair *diff.Pair) string {
	n := len(pair.Changes())
	switch n {
	case 0:
		return "no differences"
	case 1:
		return "1 hunk; ]c and [c to move, do and dp to move a hunk"
	}
	return itoa(n) + " hunks; ]c and [c to move, do and dp to move a hunk"
}

// startCommit is "cc" and ":Gcommit": git commit with this editor as the
// editor, off the editor's own goroutine.
//
// Off the goroutine is not an optimisation, it is the only way this can work.
// "pvim --wait" opens a tab in THIS instance over the unix socket and blocks
// until the tab closes; the tab closes because somebody typed ":wq" in it; the
// keys that do the typing are read by the editor goroutine. Run git commit on
// that goroutine and it waits for a client that waits for the goroutine that
// is waiting for git. The round trip itself is later work's and is tested end to
// end by TestEditorGitCommit in cmd/pvim/instance_test.go; this is the same
// thing started from a key.
func (p *gitPlugin) startCommit(args string) error {
	if p.running {
		return errors.New("a commit is already open")
	}
	self := p.editorCmd
	if self == "" {
		bin, err := os.Executable()
		if err != nil {
			return err
		}
		self = bin + " --wait"
	}
	p.running = true
	p.say("git commit: write the message and close the tab")
	repo := p.repo
	argv := splitArgs(args)
	go func() {
		out, err := repo.Commit(self, argv...)
		p.commit.Store(&commitResult{out: out, err: err})
	}()
	return nil
}

// reapCommit picks up a finished commit. Called on every key, because there
// is no way for a goroutine to interrupt the editor and no event loop to post
// to in the terminal frontend.
func (p *gitPlugin) reapCommit() {
	res := p.commit.Swap(nil)
	if res == nil {
		return
	}
	p.running = false
	if res.err != nil {
		// The LAST line of a failed commit and the FIRST of one that worked.
		// git puts the answer at opposite ends: a commit that worked starts
		// with "[master 1a2b3c4] the subject" and one that did not ends with
		// the reason -- "nothing to commit, working tree clean" under two
		// lines of branch status, or whatever a hook printed last.
		msg := gitLastLine(res.out)
		if msg == "" {
			msg = res.err.Error()
		}
		p.say("git commit: " + msg)
	} else {
		p.say(firstLine(res.out))
	}
	if p.statusBuf != nil {
		if err := p.showStatus(); err != nil {
			p.say(err.Error())
		}
	}
}

// ---------------------------------------------------------------------- keys

// gitKey is the key hook. A true answer means the key was this file's.
//
// It is called for every key so that the blame window's scroll lock and the
// finished-commit pickup happen without a timer, both of which are polls for
// the same reason cmd/pvim/finder.go's noteBuffer is one: this editor has no
// CursorMoved autocommand and no way for a goroutine to wake the loop.
func gitKey(s *session, k key.Key) bool {
	p, ok := gitState[s]
	if !ok {
		return false
	}
	p.forget()
	p.reapCommit()
	defer p.syncBlame()

	if p.pending != (key.Key{}) {
		pend := p.pending
		p.pending = key.Key{}
		return p.second(pend, k)
	}
	if s.ed.Mode() != mode.Normal {
		return false
	}
	switch {
	case p.onStatus():
		return p.statusKey(k)
	case p.onBlame():
		return p.blameKey(k)
	}
	if d := p.diffOf(s.ctx.Window()); d != nil {
		switch k {
		case key.Rune(']'), key.Rune('['), key.Rune('d'):
			p.pending = k
			return true
		}
	}
	return false
}

// second finishes a two-key command. A key that does not finish one is handed
// back to the mode machine behind the key that was held, which is how "]p"
// and "dw" still work in a diff window.
func (p *gitPlugin) second(first, k key.Key) bool {
	switch {
	case first == key.Rune('c'):
		if k == key.Rune('c') {
			if err := p.startCommit(""); err != nil {
				p.say(err.Error())
			}
			return true
		}
	case first == key.Rune(']') || first == key.Rune('['):
		if k == key.Rune('c') {
			p.jump(first == key.Rune(']'))
			return true
		}
	case first == key.Rune('d'):
		if k == key.Rune('o') || k == key.Rune('p') {
			p.getput(k == key.Rune('p'))
			return true
		}
	}
	// Not one of ours after all: replay both keys into the mode machine, in
	// order, so that the command a person actually typed runs.
	if err := p.s.pass(first); err != nil {
		return true
	}
	if err := p.s.pass(k); err != nil {
		return true
	}
	return true
}

// onStatus and onBlame say which of this file's buffers the cursor is in.
func (p *gitPlugin) onStatus() bool {
	return p.statusBuf != nil && p.cur() == p.statusBuf
}

func (p *gitPlugin) onBlame() bool {
	return p.blameBuf != nil && p.cur() == p.blameBuf
}

// statusKey is the status buffer's own keys: "-" to stage and unstage, "cc"
// to commit, <CR> to open the file the cursor is on, and "R" to refresh.
//
// Every other key falls through to the editor, so j, k, gg and G move in the
// buffer as they do anywhere else. That is nerdtree's arrangement rather than
// ctrlp's and it is the right one for a buffer a person reads.
func (p *gitPlugin) statusKey(k key.Key) bool {
	switch k {
	case key.Rune('-'):
		p.toggle()
		return true
	case key.Rune('c'):
		p.pending = k
		return true
	case key.Rune('R'):
		if err := p.showStatus(); err != nil {
			p.say(err.Error())
		}
		return true
	case key.Key{Special: key.KeyCR}:
		p.openUnderCursorSay()
		return true
	}
	return false
}

// toggle is "-": stage or unstage what the cursor is on.
func (p *gitPlugin) toggle() {
	ref, ok := p.ref()
	if !ok {
		p.say("nothing to stage here")
		return
	}
	var err error
	switch ref.Kind {
	case git.File:
		err = p.repo.Toggle(ref.Section, ref.Entry.Path)
	case git.Heading:
		err = p.repo.ToggleSection(p.status, ref.Section)
	default:
		p.say("nothing to stage here")
		return
	}
	if err != nil {
		p.say(err.Error())
		return
	}
	if err := p.showStatus(); err != nil {
		p.say(err.Error())
		return
	}
	// The cursor stays on the row it was on rather than following the file,
	// which is what makes "-" repeated on one row stage a whole section: the
	// file under it is the next one every time.
	p.clampCursor(len(p.refs))
}

// ref is what the status buffer's cursor line refers to.
func (p *gitPlugin) ref() (git.Line, bool) {
	line := p.s.ed.Cursor().Line
	if line < 1 || line > len(p.refs) {
		return git.Line{}, false
	}
	return p.refs[line-1], true
}

// openUnderCursor opens the file the status buffer's cursor is on, in the
// window the status buffer was split off.
func (p *gitPlugin) openUnderCursor() error {
	ref, ok := p.ref()
	if !ok || ref.Kind != git.File {
		return errors.New("no file here")
	}
	path := p.repo.Abs(ref.Entry.Path)
	return p.s.ctx.RunLine("edit " + escapeExArg(path))
}

func (p *gitPlugin) openUnderCursorSay() {
	if err := p.openUnderCursor(); err != nil {
		p.say(err.Error())
		return
	}
	p.s.sync()
}

// blameKey is the blame window's keys: <CR> shows the commit under the cursor
// in a scratch split, "q" closes the window.
func (p *gitPlugin) blameKey(k key.Key) bool {
	switch k {
	case key.Key{Special: key.KeyCR}:
		p.showCommit()
		return true
	case key.Rune('q'):
		p.closeBlame()
		return true
	}
	return false
}

// showCommit is <CR> in the blame window: `git show` of the commit that line
// came from, in a scratch split.
func (p *gitPlugin) showCommit() {
	line := p.s.ed.Cursor().Line
	if line < 1 || line > len(p.blame) {
		return
	}
	b := p.blame[line-1]
	if b.Uncommitted() {
		p.say("not committed yet")
		return
	}
	out, err := p.repo.Run("show", "--stat", "--patch", b.Hash)
	if err != nil && len(out) == 0 {
		p.say(err.Error())
		return
	}
	p.openSplitBuf(b.Abbrev+" "+b.Summary, splitLines(out), window.Horizontal, false, 0)
}

// syncBlame is the scroll lock. Whichever of the two windows moved, the other
// one follows.
//
// A poll on every key and not 'scrollbind', because internal/window has no
// such option and adding one is a line in somebody else's package. The
// behaviour is the same for the one thing scrollbind is for here, which is
// that the blame column stays beside the line it belongs to.
func (p *gitPlugin) syncBlame() {
	if p.blameWin == nil || p.blameOn == nil {
		return
	}
	var followed *window.Window
	switch {
	case p.blameWin.View.TopLine != p.blameTop:
		p.blameOn.View.TopLine = p.blameWin.View.TopLine
		followed = p.blameOn
	case p.blameOn.View.TopLine != p.blameTop:
		p.blameWin.View.TopLine = p.blameOn.View.TopLine
		followed = p.blameWin
	default:
		return
	}
	p.blameTop = p.blameWin.View.TopLine
	// The window that followed has to take its cursor with it, or the next
	// session.sync scrolls it straight back to wherever the cursor was left.
	// Vim's 'scrollbind' does the same thing for the same reason: a window
	// whose cursor is off the top of it is not a state either editor keeps.
	if followed.View.Cursor.Line < p.blameTop {
		followed.View.Cursor.Line = p.blameTop
		if p.s.ctx.Window() == followed {
			p.s.ed.SetCursor(text.Pos{Line: p.blameTop, Col: 0})
		}
	}
	if p.onBlame() {
		p.sayBlame()
	}
}

// ----------------------------------------------------------------- diff mode

// diffOf is the diff pair a window belongs to, or nil.
func (p *gitPlugin) diffOf(w *window.Window) *gitDiff {
	if w == nil {
		return nil
	}
	for _, d := range p.diffs {
		if d.win[0] == w || d.win[1] == w {
			return d
		}
	}
	return nil
}

// side is which half of the pair a window is.
func (d *gitDiff) side(w *window.Window) diff.Side {
	if d.win[0] == w {
		return diff.A
	}
	return diff.B
}

// pairOf diffs the two buffers as they are now.
//
// Recomputed on every use rather than cached, because a buffer edited in a
// diff window changes the answer and there is no hook that fires when it
// does. Two buffers of a few thousand lines is well under a millisecond, and
// caching it would mean a stale hunk under "do", which silently moves the
// wrong lines.
func (p *gitPlugin) pairOf(d *gitDiff) *diff.Pair {
	return diff.New(bufLines(d.buf[0]), bufLines(d.buf[1]), d.opt)
}

// jump is ]c and [c.
func (p *gitPlugin) jump(forward bool) {
	w := p.s.ctx.Window()
	d := p.diffOf(w)
	if d == nil {
		return
	}
	s := d.side(w)
	pair := p.pairOf(d)
	count := max(1, showcmdCount(p.s.ed.Showcmd()))
	p.s.clearCount()
	var line int
	var moved bool
	if forward {
		line, moved = pair.Next(s, p.s.ed.Cursor().Line, count)
	} else {
		line, moved = pair.Prev(s, p.s.ed.Cursor().Line, count)
	}
	if !moved {
		// Vim beeps and says E387/E388 here. A beep and no message is what
		// this editor does for every other motion that cannot move, so it is
		// what this does; the difference is registered nowhere because
		// nothing grades it.
		_ = p.s.ed.Beep()
		return
	}
	p.s.ed.SetCursor(text.Pos{Line: line, Col: 0})
	p.s.sync()
}

// getput is do and dp: move the hunk under the cursor from one side to the
// other.
func (p *gitPlugin) getput(put bool) {
	w := p.s.ctx.Window()
	d := p.diffOf(w)
	if d == nil {
		return
	}
	s := d.side(w)
	pair := p.pairOf(d)
	c, ok := pair.BlockAt(s, p.s.ed.Cursor().Line)
	if !ok {
		p.say("no diff hunk here")
		return
	}
	from := s
	if !put {
		from = s.Other()
	}
	to := from.Other()
	if d.buf[to] == nil || d.buf[to].Text == nil {
		return
	}
	if d.buf[to].ReadOnly {
		// The left-hand side of ":Gdiff" is a blob out of the object store
		// and writing to it would mean staging a hunk. That is a thing
		// fugitive does and this does not: the line is "-" in the
		// status buffer to stage a FILE, and a hunk-level stage wants a patch
		// applied to the index, which is a different command with a different
		// failure mode. Saying where the key that does work lives beats
		// answering E21 and leaving a person to guess.
		p.say("the " + d.name + " side is read-only; stage with - in :Gstatus")
		return
	}
	first, last, repl := diff.Get(c, from, bufLines(d.buf[from]))
	dst := d.buf[to].Text
	if last >= first {
		dst.DeleteLines(first, last)
	}
	if len(repl) > 0 {
		// InsertLines puts its lines BEFORE line first, and the delete above
		// has already pulled the line that followed the block up to that
		// number, so the same call is right for a change and for an insert.
		dst.InsertLines(first, repl)
	}
	p.s.sync()
}

// bufLines is a buffer's lines, which is what internal/diff takes.
func bufLines(b *ex.Buf) [][]byte {
	if b == nil || b.Text == nil {
		return nil
	}
	n := b.Text.LineCount()
	out := make([][]byte, n)
	for i := 1; i <= n; i++ {
		out[i-1] = b.Text.Line(i)
	}
	return out
}

// ------------------------------------------------------------------- windows

// openSplitBuf makes a scratch buffer in a new window, the way
// cmd/pvim/finder.go's openSplit does and for the same reason: internal/ex's
// own openScratch is unexported and this is it written against the exported
// half of the package.
func (p *gitPlugin) openSplitBuf(name string, lines [][]byte, dir window.Dir, before bool, size int) (*text.Buffer, *ex.Buf, *window.Window) {
	ctx := p.s.ctx
	tab := ctx.Tabs.Current()
	cur := ctx.Window()
	if tab == nil || cur == nil {
		return nil, nil, nil
	}
	buf := text.New()
	nb := ctx.Bufs.Add(name, buf)
	nb.Scratch = true
	nb.Listed = false

	fresh := window.New(gitWindowID(p.s), buf, ctx.Opt.GW)
	fresh.View.Width = cur.View.Width
	fresh.View.Height = cur.View.Height
	fresh.Opt = cur.Opt
	fresh.SetHeight(cur.View.Height)
	if err := tab.Split(cur, fresh, dir, before, size); err != nil {
		ctx.Bufs.Remove(nb)
		p.say(err.Error())
		return nil, nil, nil
	}
	tab.Cur = fresh
	ctx.Bufs.Cur = nb
	p.show(nb)
	p.relayout()
	p.setLines(nb, lines)
	return buf, nb, fresh
}

// gitWindowID is the next free window id, which internal/ex works out with an
// unexported function of its own.
//
// Written again here rather than shared with cmd/pvim/finder.go's
// nextWindowID because a call across the two files would make each one's
// build depend on the other. It is eight lines and it goes the day internal/ex
// exports one.
func gitWindowID(s *session) int {
	n := 0
	for _, t := range s.ctx.Tabs.Pages {
		for _, w := range t.Windows() {
			if w.ID > n {
				n = w.ID
			}
		}
	}
	return n + 1
}

// closeWin takes a window and its scratch buffer away.
func (p *gitPlugin) closeWin(w *window.Window, b *ex.Buf) {
	ctx := p.s.ctx
	if w == nil {
		return
	}
	if tab := ctx.Tabs.Current(); tab != nil && len(tab.Windows()) > 1 {
		tab.Cur = w
		ctx.Bufs.Cur = b
		if err := ctx.RunLine("close"); err != nil {
			p.say(err.Error())
		}
	}
	if b != nil {
		ctx.Bufs.Remove(b)
	}
	p.relayout()
	p.s.sync()
}

// alive reports whether a window is still in the current tab page.
//
// The windows this file keeps pointers to can be closed by ":q" or ":only"
// like any others, and a pointer to a closed one is a tab.Cur that is not in
// the layout tree. Every use of a remembered window goes through this.
func (p *gitPlugin) alive(w *window.Window) bool {
	if w == nil {
		return false
	}
	tab := p.s.ctx.Tabs.Current()
	if tab == nil {
		return false
	}
	for _, other := range tab.Windows() {
		if other == w {
			return true
		}
	}
	return false
}

// forget drops the windows that have been closed since the last command, so
// that a ":q" on the status buffer means the next ":Gstatus" opens a new one
// rather than writing into a window that is not on the screen.
func (p *gitPlugin) forget() {
	if !p.alive(p.statusWin) {
		p.statusBuf, p.statusWin = nil, nil
	}
	if !p.alive(p.blameWin) || !p.alive(p.blameOn) {
		p.blameBuf, p.blameWin, p.blameOn, p.blame = nil, nil, nil, nil
		p.blameFile = ""
	}
	kept := p.diffs[:0]
	for _, d := range p.diffs {
		if p.alive(d.win[0]) && p.alive(d.win[1]) {
			kept = append(kept, d)
		}
	}
	p.diffs = kept
}

// focus makes a window and its buffer the current ones.
func (p *gitPlugin) focus(w *window.Window, b *ex.Buf) {
	if tab := p.s.ctx.Tabs.Current(); tab != nil && w != nil {
		tab.Cur = w
	}
	if b != nil {
		p.s.ctx.Bufs.Cur = b
		p.show(b)
	}
}

// setLines replaces a scratch buffer's whole contents.
func (p *gitPlugin) setLines(b *ex.Buf, lines [][]byte) {
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

// clampCursor keeps the cursor inside a buffer that just got shorter.
func (p *gitPlugin) clampCursor(n int) {
	line := p.s.ed.Cursor().Line
	if line > n {
		line = n
	}
	if line < 1 {
		line = 1
	}
	p.s.ed.SetCursor(text.Pos{Line: line, Col: 0})
	p.s.sync()
}

// --------------------------------------------------------------------- bits

// splitArgs splits a command's arguments the way a shell would split them for
// quoting, and no further: single and double quotes group, a backslash
// escapes the next character, and nothing is expanded.
func splitArgs(s string) []string {
	var out []string
	var cur []byte
	var quote byte
	started := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
				continue
			}
			cur = append(cur, c)
		case c == '\'' || c == '"':
			quote = c
			started = true
		case c == '\\' && i+1 < len(s):
			i++
			cur = append(cur, s[i])
			started = true
		case c == ' ' || c == '\t':
			if len(cur) > 0 || started {
				out = append(out, string(cur))
				cur, started = nil, false
			}
		default:
			cur = append(cur, c)
		}
	}
	if len(cur) > 0 || started {
		out = append(out, string(cur))
	}
	return out
}

// splitLines cuts command output into buffer lines, with the trailing newline
// dropped so that a scratch buffer does not end in a blank line git did not
// print.
func splitLines(out []byte) [][]byte {
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return nil
	}
	return toBytes(strings.Split(s, "\n"))
}

// firstLine is the first line of some output, for the message line.
func firstLine(out []byte) string {
	s := strings.TrimSpace(string(out))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// gitLastLine is the last line with anything on it. Named for this file
// because cmd/pvim/recover.go already has a lastLine of its own with a
// different signature.
func gitLastLine(out []byte) string {
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

func toBytes(ss []string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

// itoa is strconv.Itoa without the import, which this file would otherwise
// take for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
