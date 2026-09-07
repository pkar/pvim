package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkar/pvim/internal/ex"
	pvfmt "github.com/pkar/pvim/internal/fmt"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/lsp"
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/quickfix"
	"github.com/pkar/pvim/internal/screen"
	"github.com/pkar/pvim/internal/text"
)

// The language server, format on save, and the four ":Go" commands.
//
// This is the whole of what vim-go was, for the eleven "g:go_*" lines in the
// vimrc and the two keys behind them. What it replaces is 15,000 lines of
// vimscript driving a job channel; what it is is a client in internal/lsp, a
// table in internal/fmt, and this file joining the two to a buffer.
//
// The import of internal/fmt is aliased because the package is called fmt and
// so is the standard library's. See that package's own comment for why the
// directory keeps the name.
//
// WHERE THIS IS PLUGGED IN, and what is not plugged in yet.
//
// Every entry point below is a method on *editor and every one of them is
// reached from exactly one place, listed here so that the wiring is one thing
// to read rather than nine greps. Those call sites are not wired up yet, so
// the lines are written here as comments and the functions are tested
// directly. cmd/pvim/lsp_test.go drives the same sequence a live editor would.
//
//	cmd/pvim/window.go, newFrontendEditor, after ed.bufferOpened:
//	 ed.lspBufferOpened()
//	cmd/pvim/editor.go, editor.open, at the end:
//	 e.lspBufferOpened()
//	cmd/pvim/window.go and terminal.go, when Run returns:
//	 ed.lspStop()
//	internal/ex, exWrite, before c.fileBytes: a new Context hook
//	 PreWrite func(b *Buf) error
//	 which cmd/pvim sets to editor.lspBeforeWrite. editor.write() in
//	 cmd/pvim/editor.go needs the same call after its BufWritePre and before
//	 fileBytes; only ZZ goes through that function and ":w" goes
//	 through internal/ex, which fires no BufWritePre at all.
//	internal/ex, exWrite, after the bytes are down:
//	 editor.lspAfterWrite
//	cmd/pvim/session.go, session.pass, at the top, for K and CTRL-]:
//	 if done, err:= s.editor().lspKey(k); done { return err }
//	 session has no back pointer to its editor, so this is either that
//	 field or a func value set where the session is built.
//	cmd/pvim/session.go, the "g" table beside gt, gT and gf, for gd:
//	 editor.lspDefinition
//	 gd is two keys and the "g" of it is held by session.pendingG, so it
//	 cannot go through lspKey with the other two.
//	internal/ex/table.go, beside "cclose", so that ":copen" is reachable at
//	all: the handler exists in quickfixcmd.go and the table has no row for it.
//	 {Name: "copen", Range: RangeCount},
//	internal/ex, a hook for a command implemented outside the package, so that
//	":GoBuild", ":GoTest", ":GoDef" and ":GoInfo" can be typed:
//	 Plugin func(name, args string) (handled bool, err error)
//	 which cmd/pvim sets to editor.lspExCommand.
//	internal/mode, insert mode, CTRL-X CTRL-O:
//	 a way to hand the existing completion state machine a candidate list.
//	 See editor.lspOmniComplete for the exact shape asked for.
//	internal/screen, a third MatchSource slot on Frame, so that a diagnostic
//	range is underlined in the text. See lspMatches below.
//
// --oracle reaches none of it. Every call site above is behind the frontend or
// behind the vimrc, and oracle.go builds its editor with newEditor directly:
// a graded run has no autocommands, no vimrc, no frontend and therefore no
// server. cmd/pvim/lsp_test.go asserts that with a process check.

// lspState is everything the language server adds to an editor.
//
// It is in a side table keyed by *editor rather than a field on the struct so
// that none of this has to touch cmd/pvim/editor.go. The field is where it
// belongs the day somebody moves it, and the map goes away with it; nothing
// else here changes.
type lspState struct {
	// clients is one server per workspace root. One root is the normal case
	// and the map is what happens when a session opens a file in a second
	// repository: gopls is rooted at a directory and a second root is a
	// second workspace, not a second request.
	clients map[string]*lsp.Client

	// table is the format-on-save table, holding the first client as its
	// formatter.
	table *pvfmt.Table

	// docs is what the server has been told, per file: the exact bytes, so
	// that the next sync can work out what changed without asking the buffer
	// to remember anything.
	docs map[string]*lspDoc

	// lastSync is when a didChange last went out, which is what the
	// coalescing window is measured from.
	lastSync time.Time

	// coalesce is the window. See internal/lsp's Options.Coalesce.
	coalesce time.Duration

	// said is the one-line complaints already made, so that a session with no
	// gopls says so once and not on every keystroke.
	said map[string]bool

	// diagID is the id of the quickfix list this file filled last, so that a
	// second batch of diagnostics replaces it instead of pushing a tenth list
	// and dropping the ":grep" from before lunch.
	diagID int
}

// lspDoc is one open document as the server last saw it.
type lspDoc struct {
	client *lsp.Client
	// text is the bytes the server has. The whole document and not a hash:
	// the next sync needs to know WHERE the change was, and a hash answers
	// only whether there was one.
	text []byte
	ft   string
	path string
}

// The side table. A mutex because a test may build two editors and because
// internal/lsp's own goroutines never touch it -- everything here runs on the
// editor's goroutine.
var (
	lspMu    sync.Mutex
	lspTable = map[*editor]*lspState{}
)

// lsp returns this editor's language-server state, making it on first use.
func (e *editor) lsp() *lspState {
	lspMu.Lock()
	defer lspMu.Unlock()
	s := lspTable[e]
	if s == nil {
		s = &lspState{
			clients:  map[string]*lsp.Client{},
			docs:     map[string]*lspDoc{},
			said:     map[string]bool{},
			coalesce: 50 * time.Millisecond,
		}
		s.table = pvfmt.New(nil)
		lspTable[e] = s
	}
	return s
}

// lspStop shuts every server down and forgets this editor.
//
// It is the half of the pgrep gate that this file owns: internal/lsp's
// Close asks the server to leave, closes its standard input and kills its
// process group if it will not, and does not return until the process is
// gone. What is left for the call site is calling this at all, and calling it
// on every way out -- ":q", ZZ, a quit from the socket, and the window's
// Cmd-Q -- which is why the line goes where the frontend's Run returns and
// not next to any one command.
func (e *editor) lspStop() {
	lspMu.Lock()
	s := lspTable[e]
	delete(lspTable, e)
	lspMu.Unlock()
	if s == nil {
		return
	}
	for _, c := range s.clients {
		_ = c.Close()
	}
}

// lspRunning reports whether this editor has a server at all, which is what a
// test asks before it asserts that a graded run started none.
func (e *editor) lspRunning() bool {
	lspMu.Lock()
	defer lspMu.Unlock()
	s := lspTable[e]
	return s != nil && len(s.clients) > 0
}

// lspLanguages are the filetypes this editor starts a server for.
//
// Go and its three metadata files, because gopls is the only server on this
// machine and the vimrc's only language configuration is Go's. Terraform is
// deliberately absent: terraform-ls is not installed here, and the vimrc's
// Terraform support was one line, "g:terraform_fmt_on_save=1", which is a
// formatter and not a server. internal/fmt runs that one through the CLI.
var lspLanguages = map[string]bool{
	"go": true, "gomod": true, "gowork": true,
}

// lspBufferOpened is what a buffer arriving does: work out whether it wants a
// server, start one if it is the first, and tell it about the file.
//
// Called for every buffer, including the ones with no server, because the
// format-on-save table has to know about a .tf file too.
func (e *editor) lspBufferOpened() {
	b := e.cur()
	if b == nil || b.Name == "" || b.Scratch {
		return
	}
	e.lspReadVars()
	ft := ""
	if e.opt != nil {
		ft = e.opt.B.FileType
	}
	if !lspLanguages[ft] {
		return
	}
	c, err := e.lspClient(b.Name)
	if err != nil {
		e.lspSayOnce("gopls", err.Error())
		return
	}
	s := e.lsp()
	if s.docs[b.Name] != nil {
		return
	}
	src := b.Text.Bytes()
	if err := c.DidOpen(b.Name, ft, src); err != nil {
		e.lspSayOnce("didOpen", err.Error())
		return
	}
	s.docs[b.Name] = &lspDoc{client: c, text: src, ft: ft, path: b.Name}
}

// lspReadVars takes the two format-on-save switches out of the vimrc.
//
// vim-go's g:go_fmt_autosave defaults to 1 and this vimrc does not set it, so
// its absence means on, which is why the default here is on and this only ever
// turns it off. g:terraform_fmt_on_save the vimrc sets to 1 by name, so the
// value is read rather than assumed: the day it becomes 0 the row goes away
// and nothing else changes.
//
// Read on every buffer opening rather than once, because ":so $MYVIMRC" is one
// of this vimrc's own autocommands and a "let" that changed after startup
// should mean something.
func (e *editor) lspReadVars() {
	s := e.lsp()
	s.table.GoEnabled = e.lspVarOn("g:go_fmt_autosave", true)
	s.table.TerraformEnabled = e.lspVarOn("g:terraform_fmt_on_save", true)
}

// lspVarOn reads a g: variable as a boolean, falling back to def when the
// vimrc never mentioned it.
//
// Vim's own truthiness, through vimrc.Val.Truthy, so that "let g:x = 'no'" is
// false for the reason vim says it is -- a string converts to its leading
// digits -- rather than for a reason invented here.
func (e *editor) lspVarOn(name string, def bool) bool {
	if e.vars == nil {
		return def
	}
	let, ok := e.vars[name]
	if !ok || let.Unlet {
		return def
	}
	return let.Val.Truthy()
}

// lspBufferClosed is textDocument/didClose, for a buffer leaving the list.
func (e *editor) lspBufferClosed(path string) {
	s := e.lsp()
	d := s.docs[path]
	if d == nil {
		return
	}
	delete(s.docs, path)
	_ = d.client.DidClose(path)
}

// lspClient is the server for a file's workspace, started on first use.
//
// Started lazily and not at startup, and rooted at the FILE and not at the
// working directory. Both halves matter. Lazily, because a session spent in
// markdown should not have 400 MB of gopls behind it and because the startup
// budget is 50 ms, which is less than gopls takes to answer
// initialize. Rooted at the file, because 'autochdir' is on in this vimrc: the
// working directory follows the buffer, so a cwd-rooted server would open a
// fresh workspace for every directory visited and reload the module each time.
// lsp.Root is the ".git" walk, and internal/finder wants the
// same one.
func (e *editor) lspClient(path string) (*lsp.Client, error) {
	s := e.lsp()
	root := lsp.Root(path)
	if c := s.clients[root]; c != nil {
		return c, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	bin, err := lsp.Find(home, "gopls")
	if err != nil {
		return nil, errors.New("gopls is in neither ~/.vimgo nor $PATH; no completion, no format on save")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := lsp.Start(ctx, bin, lsp.Options{
		Root:     root,
		Coalesce: s.coalesce,
		// window/showMessage arrives on one of internal/lsp's goroutines and
		// the message line belongs to the editor's, so it is dropped rather
		// than raced on. What gopls says there is "no packages found" and
		// build failures, both of which turn up again as diagnostics.
		OnMessage: nil,
	})
	if err != nil {
		return nil, err
	}
	s.clients[root] = c
	if s.table.Server == nil {
		s.table.Server = c
	}
	return c, nil
}

// lspSayOnce puts a line on the message area the first time a given thing goes
// wrong, and never again. An editor that says "gopls not found" on every
// keystroke of an insert is worse than one that says it once.
func (e *editor) lspSayOnce(key, msg string) {
	s := e.lsp()
	if s.said[key] {
		return
	}
	s.said[key] = true
	e.ed.Say(msg)
}

// lspSync sends what has changed in the current buffer since the server last
// heard, as an incremental didChange.
//
// force is a request or a save about to go out: those always sync, whatever
// the coalescing window says, which is what makes the window a latency
// optimisation and never a correctness problem. Without force the sync is
// skipped inside the window, so a burst of typing costs one diff and one
// message rather than one per keystroke.
//
// The change is worked out by comparing the bytes with the ones the server
// has: a common prefix, a common suffix, and one replacement in the middle.
// That is a real incremental change and not a whole-document resend -- typing
// a character in a 4,000-line file sends about twenty bytes -- and it needs
// nothing from internal/text, which has no change feed and should not grow one
// for this. What it costs is one copy of the buffer per window, which at 50 ms
// and a 200 KB Go file is 4 MB a second of garbage while typing and is not
// measurable next to the JSON.
func (e *editor) lspSync(force bool) {
	s := e.lsp()
	b := e.cur()
	if b == nil || b.Name == "" {
		return
	}
	d := s.docs[b.Name]
	if d == nil {
		return
	}
	if !force && time.Since(s.lastSync) < s.coalesce {
		return
	}
	s.lastSync = time.Now()

	next := b.Text.Bytes()
	change, ok := lsp.Diff(d.text, next)
	if !ok {
		return // nothing moved
	}
	if err := d.client.Change(b.Name, change); err != nil {
		return
	}
	d.text = next
	if force {
		_ = d.client.Flush()
	}
}

// lspPos is the cursor, as the protocol wants it: zero-based lines, and a
// column in UTF-16 code units rather than bytes.
func (e *editor) lspPos() lsp.Position {
	cur := e.ed.Cursor()
	line := e.buf.Line(cur.Line)
	return lsp.Position{Line: cur.Line - 1, Character: lsp.UTF16Column(line, cur.Col)}
}

// lspRequestTimeout is how long a keystroke will wait for a server.
//
// The popup gate is 200 ms and the measured warm answer for "http." on
// this machine is 3.6 ms, so this is not a budget, it is a deadlock guard: a
// gopls still loading a cold workspace would otherwise hold the editor for as
// long as it took. A completion that times out shows no popup, which is what
// vim does when 'omnifunc' returns nothing, and the next one usually works.
const lspRequestTimeout = 2 * time.Second

// lspOmniComplete is 'omnifunc': the candidates at the cursor.
//
// The vimrc's own mapping is what fires it:
//
//	autocmd FileType go inoremap <buffer> . .<C-x><C-o>
//
// so every "." typed in a Go file types the dot and then asks for this. The
// trigger character is passed on, which is what makes gopls rank the members
// of the thing to the left of the dot first rather than every symbol in the
// module.
//
// It returns words and inserts nothing. That is the other half of the gate and
// it is not this function's doing: 'completeopt' is "menu,menuone,noselect,
// noinsert" in this vimrc, and internal/mode's own completion state machine
// already honours it -- see startCompletion in internal/mode/complete.go,
// which shows the menu and returns without a rewrite when either flag is set.
//
// TODO: internal/mode has no CTRL-X CTRL-O. What it wants is a
// hook of this shape on mode.Editor, called when insert mode reads CTRL-X
// CTRL-O, with the result going into the existing "completion" struct exactly
// as bufferWords does:
//
//	// SetOmniFunc installs 'omnifunc'. base is what has been typed of the
//	// word being completed and the result is the candidates, in the order
//	// the popup is to show them.
//	func (e *Editor) SetOmniFunc(f func(base []byte) [][]byte)
//
// cmd/pvim would then call ed.SetOmniFunc(e.lspOmniCompleteWords) once, and
// the popup, 'completeopt', CTRL-N, CTRL-P, CTRL-Y and CTRL-E all work
// already. Until that exists this function is reachable from a test and from
// nothing a person can type.
func (e *editor) lspOmniComplete(trigger string) ([]lsp.CompletionItem, error) {
	b := e.cur()
	if b == nil || b.Name == "" {
		return nil, nil
	}
	s := e.lsp()
	d := s.docs[b.Name]
	if d == nil {
		return nil, nil
	}
	e.lspSync(true)
	ctx, cancel := context.WithTimeout(context.Background(), lspRequestTimeout)
	defer cancel()
	return d.client.Complete(ctx, b.Name, e.lspPos(), trigger)
}

// lspOmniCompleteWords is lspOmniComplete in the shape a popup menu wants:
// the text each candidate would insert, in the server's own order.
//
// The label is what the menu shows and what gets inserted, and they are the
// same string here because SnippetSupport is off in internal/lsp's
// capabilities. Where gopls sends a textEdit -- which it does for most items --
// its new text is used instead, because the range it wants to replace is not
// always the word the editor thinks is being completed.
func (e *editor) lspOmniCompleteWords(trigger string) [][]byte {
	items, err := e.lspOmniComplete(trigger)
	if err != nil {
		if !lsp.Cancelled(err) {
			e.lspSayOnce("complete", "omnifunc: "+err.Error())
		}
		return nil
	}
	out := make([][]byte, 0, len(items))
	seen := map[string]bool{}
	for _, it := range items {
		w := it.Label
		if it.TextEdit != nil && it.TextEdit.NewText != "" {
			w = it.TextEdit.NewText
		} else if it.InsertText != "" {
			w = it.InsertText
		}
		// A candidate carrying a newline is not a word a popup can insert.
		// gopls sends one for a "fill switch" style code action item.
		if w == "" || strings.ContainsAny(w, "\n\r") || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, []byte(w))
	}
	return out
}

// lspDefinition is gd, CTRL-] and ":GoDef": open where the symbol under the
// cursor is defined.
//
// The file is opened read-only when it is not under the workspace root, which
// is what makes the "gd on os.ReadFile opens the stdlib file read-only"
// true and what stops a stray "x" in $GOROOT from becoming a write to the
// standard library. A definition inside the project opens writable, which is
// the whole point of the key.
func (e *editor) lspDefinition() error {
	b := e.cur()
	if b == nil || b.Name == "" {
		return nil
	}
	s := e.lsp()
	d := s.docs[b.Name]
	if d == nil {
		return errors.New("E426: tag not found: no language server for this buffer")
	}
	e.lspSync(true)
	ctx, cancel := context.WithTimeout(context.Background(), lspRequestTimeout)
	defer cancel()
	locs, err := d.client.Definition(ctx, b.Name, e.lspPos())
	if err != nil {
		return err
	}
	if len(locs) == 0 {
		// vim's own message for a tag jump that found nothing, which is what
		// the muscle memory behind CTRL-] expects to see.
		return errors.New("E433: no tags file")
	}
	return e.lspJump(locs[0], d.client.Root())
}

// lspJump opens a location and puts the cursor on it.
func (e *editor) lspJump(loc lsp.Location, root string) error {
	path := lsp.Path(loc.URI)
	if path == "" {
		return errors.New("E433: the server answered with a location that is not a file")
	}
	if path != e.cur().Name {
		if err := e.ctx.RunLine("edit " + escapeExArg(path)); err != nil {
			return err
		}
	}
	// Read-only for anything outside the workspace: the standard library, the
	// module cache, a vendored dependency. All three are files a person means
	// to read and none of them is a file they mean to change, and the module
	// cache is mode 0444 on disk anyway, so a write would fail with a worse
	// message later.
	if nb := e.cur(); nb != nil && !lspUnderRoot(path, root) {
		nb.ReadOnly = true
	}
	e.lspPlaceCursor(loc.Range.Start)
	return nil
}

// lspPlaceCursor moves the cursor to a protocol position in the current
// buffer, converting the UTF-16 column back to a byte one.
func (e *editor) lspPlaceCursor(p lsp.Position) {
	line := p.Line + 1
	if line < 1 {
		line = 1
	}
	if n := e.buf.LineCount(); line > n {
		line = n
	}
	col := lsp.ByteColumn(e.buf.Line(line), p.Character)
	e.ed.SetCursor(text.Pos{Line: line, Col: col})
	e.sess.sync()
}

// lspUnderRoot reports whether a path is inside a directory.
func lspUnderRoot(path, root string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// lspHover is K and ":GoInfo": what the symbol under the cursor is, in a
// scratch split.
//
// A split and not the message line, because gopls's answer for a function is
// its whole signature plus its doc comment and 'cmdheight' is 1 in this vimrc:
// on the message line that is a press-enter prompt after every K.
func (e *editor) lspHover() error {
	b := e.cur()
	if b == nil || b.Name == "" {
		return nil
	}
	s := e.lsp()
	d := s.docs[b.Name]
	if d == nil {
		return errors.New("E349: no language server for this buffer")
	}
	e.lspSync(true)
	ctx, cancel := context.WithTimeout(context.Background(), lspRequestTimeout)
	defer cancel()
	body, err := d.client.Hover(ctx, b.Name, e.lspPos())
	if err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return errors.New("E349: No identifier under cursor")
	}
	return e.lspScratch("[Hover]", strings.Split(strings.TrimRight(body, "\n"), "\n"))
}

// lspScratch puts lines in a scratch split.
//
// Through ":new" and then a rewrite of the buffer it made, rather than through
// internal/ex's own openScratch, which is unexported. The two end up in the
// same place: a listed=false, buftype=nofile buffer in a horizontal split,
// which ":q" closes without a prompt.
func (e *editor) lspScratch(name string, lines []string) error {
	if err := e.ctx.RunLine("new"); err != nil {
		return err
	}
	b := e.cur()
	if b == nil || b.Text == nil {
		return errors.New("E444: no window for the scratch buffer")
	}
	rows := make([][]byte, 0, len(lines))
	for _, l := range lines {
		rows = append(rows, []byte(l))
	}
	if len(rows) > 0 {
		b.Text.InsertLines(0, rows)
		b.Text.DeleteLines(len(rows)+1, len(rows)+1)
	}
	b.Name = name
	b.Scratch = true
	b.Listed = false
	b.ReadOnly = true
	b.MarkSaved()
	e.file = name
	e.sess.sync()
	return nil
}

// lspBeforeWrite is format on save, and it runs BEFORE the bytes are taken.
//
// The order is the plugins' order and it is not an accident. The vimrc has
//
//	autocmd BufWritePre *.py,*.java,*.js,*.go silent! %s/\s\+$//e
//
// and vim runs BufWritePre autocommands before a plugin's own write hook, so
// the whitespace strip goes first and gofmt sees the stripped buffer. The
// caller therefore does its BufWritePre and then calls this; doing it the
// other way round would strip whitespace out of text gofmt had just laid out
// and write something neither tool would produce.
//
// The formatted text goes into the BUFFER and the buffer is then written,
// rather than the formatted text being written past the buffer. That is what
// makes undo work over a format -- one "u" after a save that reformatted puts
// the file back the way it was typed -- and it is why internal/fmt refuses to
// let a formatter write a file itself.
//
// A formatter that fails is a message and not a refusal to save. gopls answers
// a formatting request over a file with a syntax error in it with an error,
// and that file is exactly the one most likely to be being saved.
func (e *editor) lspBeforeWrite() error {
	b := e.cur()
	if b == nil || b.Name == "" || b.Scratch {
		return nil
	}
	ft := ""
	if e.opt != nil {
		ft = e.opt.B.FileType
	}
	s := e.lsp()
	if !s.table.Formats(ft) {
		return nil
	}
	// The server has to have the pre-format bytes before it is asked to
	// format them, which for a buffer edited since the last request it has
	// not.
	e.lspSync(true)

	src := b.Text.Bytes()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := s.table.Format(ctx, ft, b.Name, src)
	if err != nil {
		if errors.Is(err, pvfmt.ErrNoFormatter) {
			return nil
		}
		e.ed.Say(err.Error())
		return nil
	}
	if string(out) == string(src) {
		return nil
	}
	e.lspReplaceBuffer(out)
	// And the server hears about the reformat as an edit, because it is one.
	e.lspSync(true)
	return nil
}

// lspReplaceBuffer puts new bytes in the current buffer as one undo step, with
// the cursor kept on the line it was on.
//
// Line by line and not a whole-buffer Replace, because a format that changed
// three import lines should leave the marks, the jumplist and the fold state
// on the other four thousand alone. The cursor is clamped rather than tracked:
// gofmt does not move code between lines, so the line a person was on is the
// line they stay on, and the column is the one thing that can end up in a
// different place after an indent changed.
func (e *editor) lspReplaceBuffer(out []byte) {
	b := e.cur()
	cur := e.ed.Cursor()
	next := text.Read(out)

	b.Text.OpenUndoBlock(cur)
	old := b.Text.LineCount()
	n := next.LineCount()
	for i := 1; i <= n && i <= old; i++ {
		if string(b.Text.Line(i)) != string(next.Line(i)) {
			b.Text.SetLine(i, next.Line(i))
		}
	}
	switch {
	case n > old:
		add := make([][]byte, 0, n-old)
		for i := old + 1; i <= n; i++ {
			add = append(add, next.Line(i))
		}
		b.Text.InsertLines(old, add)
	case n < old:
		b.Text.DeleteLines(n+1, old)
	}
	b.Text.SetNoEOL(next.NoEOL())
	b.Text.CloseUndoBlock()

	e.ed.SetCursor(b.Text.Clamp(cur))
	e.sess.sync()
}

// lspAfterWrite is textDocument/didSave, and it is what sets the diagnostics
// for the file that was just written going.
//
// gopls re-analyses on didChange as well, so the diagnostics behind the
// one-second gate are usually already on their way before this; the save is
// what makes them cover the whole package rather than the open file.
func (e *editor) lspAfterWrite() {
	b := e.cur()
	if b == nil || b.Name == "" {
		return
	}
	s := e.lsp()
	d := s.docs[b.Name]
	if d == nil {
		return
	}
	e.lspSync(true)
	_ = d.client.DidSave(b.Name)
}

// lspDiagnostics fills the quickfix list from what the servers have published,
// and reports whether anything changed.
//
// Every file the server has an opinion about, not only the one that was
// written: a type error in a.go usually shows up as a diagnostic in b.go, and
// a list holding only the buffer that was saved sends a person looking in the
// wrong file. That is also why the entries are sorted by file and line rather
// than left in map order, which is random.
//
// The list replaces the one this function pushed last rather than pushing a
// new one every time, so that a ":grep" from before lunch is still one
// ":colder" away after twenty saves.
func (e *editor) lspDiagnostics() bool {
	s := e.lsp()
	if len(s.clients) == 0 {
		return false
	}
	var entries []quickfix.Entry
	for _, c := range s.clients {
		for path, ds := range c.AllDiagnostics() {
			// The file once and not once per diagnostic: a package that does
			// not build has twenty errors in one file as often as one, and the
			// columns all need the same bytes.
			src, _ := os.ReadFile(path)
			for _, d := range ds {
				entries = append(entries, lspDiagEntry(path, src, d))
			}
		}
	}
	lspSortEntries(entries)

	if e.ctx == nil || e.ctx.QF == nil {
		return false
	}
	cur := e.ctx.QF.Current()
	if cur != nil && cur.ID == s.diagID && s.diagID != 0 {
		if lspSameEntries(cur.Entries, entries) {
			return false
		}
		cur.Entries = entries
		if cur.Idx >= len(entries) {
			cur.Idx = -1
		}
		return true
	}
	if len(entries) == 0 {
		return false
	}
	s.diagID++
	l := &quickfix.List{Title: "Diagnostics", Entries: entries, Idx: -1, ID: s.diagID}
	lspPushList(e, l)
	return true
}

// lspDiagEntry turns one diagnostic into one quickfix entry.
//
// The column is converted out of UTF-16 by reading the file, because that is
// the only place the line's bytes are: the diagnostic may be for a file no
// buffer holds. A file that cannot be read keeps the protocol's column, which
// is right for the ASCII line every Go compiler error is on and wrong by a
// byte or two on a line with a comment in another language, and that is a
// better answer than no entry at all.
func lspDiagEntry(path string, src []byte, d lsp.Diagnostic) quickfix.Entry {
	e := quickfix.Entry{
		FileName: path,
		LNum:     d.Range.Start.Line + 1,
		Col:      d.Range.Start.Character + 1,
		EndLNum:  d.Range.End.Line + 1,
		EndCol:   d.Range.End.Character + 1,
		Text:     d.Message,
		Valid:    true,
		Kind:     diagKind(d.Severity),
	}
	if line := lspFileLine(src, d.Range.Start.Line); line != nil {
		e.Col = lsp.ByteColumn(line, d.Range.Start.Character) + 1
	}
	if line := lspFileLine(src, d.Range.End.Line); line != nil {
		e.EndCol = lsp.ByteColumn(line, d.Range.End.Character) + 1
	}
	return e
}

// lspFileLine is line n, zero-based, of src, or nil.
func lspFileLine(src []byte, n int) []byte {
	if len(src) == 0 || n < 0 {
		return nil
	}
	for i := 0; ; i++ {
		j := bytes.IndexByte(src, '\n')
		if i == n {
			if j < 0 {
				return src
			}
			return src[:j]
		}
		if j < 0 {
			return nil
		}
		src = src[j+1:]
	}
}

// diagKind maps a protocol severity onto vim's quickfix type character.
func diagKind(sev int) quickfix.Kind {
	switch sev {
	case lsp.SeverityError:
		return quickfix.KindError
	case lsp.SeverityWarning:
		return quickfix.KindWarning
	case lsp.SeverityInfo:
		return quickfix.KindInfo
	case lsp.SeverityHint:
		return quickfix.KindNote
	default:
		return quickfix.KindNone
	}
}

// lspSortEntries puts a quickfix list in the order ":cn" walks it: by file, then
// by line, then by column.
//
// Sorted at all because the diagnostics arrive out of a map, and a list whose
// order changed every time it was rebuilt would move ":cn" to a different
// error on every save.
func lspSortEntries(entries []quickfix.Entry) {
	sort.SliceStable(entries, func(i, j int) bool { return lspLessEntry(entries[i], entries[j]) })
}

// lspLessEntry orders two entries.
func lspLessEntry(a, b quickfix.Entry) bool {
	if a.FileName != b.FileName {
		return a.FileName < b.FileName
	}
	if a.LNum != b.LNum {
		return a.LNum < b.LNum
	}
	if a.Col != b.Col {
		return a.Col < b.Col
	}
	return a.Text < b.Text
}

// lspSameEntries reports whether two lists say the same thing, so that a
// republish of identical diagnostics does not redraw the quickfix window.
func lspSameEntries(a, b []quickfix.Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// lspMatches is the diagnostics as internal/screen's MatchSource, so that a
// range with an error on it is underlined in the text.
//
// TODO: nothing draws this. screen.Frame has a Search and an
// IncSearch MatchSource and no third slot, so what this wants is
//
//	// Diagnostics are the ranges a language server reported a problem on,
//	// drawn in SpellBad, under 'hlsearch' and over the syntax colour.
//	Diagnostics MatchSource
//
// on screen.Frame, the same one line in the renderer that Search already has,
// and one line in cmd/pvim/draw.go setting it to e.lspMatches(). The group is
// already in the table: GroupSpellBad is an undercurl in red, which is what
// vim's own SpellBad is and what an error range should look like.
type lspMatches struct {
	// byLine is the ranges on each buffer line, 1-based, already in byte
	// columns.
	byLine map[int][]screen.Match
}

// MatchesOn is screen.MatchSource: the matches on one buffer line.
func (m *lspMatches) MatchesOn(line int) []screen.Match {
	if m == nil {
		return nil
	}
	return m.byLine[line]
}

// lspMatchSource builds the underline source for the current buffer.
func (e *editor) lspMatchSource() *lspMatches {
	b := e.cur()
	if b == nil || b.Name == "" {
		return nil
	}
	s := e.lsp()
	d := s.docs[b.Name]
	if d == nil {
		return nil
	}
	m := &lspMatches{byLine: map[int][]screen.Match{}}
	for _, diag := range d.client.Diagnostics(b.Name) {
		start := diag.Range.Start
		end := diag.Range.End
		for ln := start.Line; ln <= end.Line; ln++ {
			n := ln + 1
			if n < 1 || n > b.Text.LineCount() {
				continue
			}
			lineText := b.Text.Line(n)
			from, to := 0, len(lineText)
			if ln == start.Line {
				from = lsp.ByteColumn(lineText, start.Character)
			}
			if ln == end.Line {
				to = lsp.ByteColumn(lineText, end.Character)
			}
			if to <= from {
				// A zero-width range still has to be visible, which is what
				// vim does with an empty match: one cell.
				to = from + 1
			}
			m.byLine[n] = append(m.byLine[n], screen.Match{Line: n, Start: from, End: to})
		}
	}
	return m
}

// The ":Go" commands.
//
// ":GoBuild" and ":GoTest" are "go build" and "go test" into the quickfix
// list; ":GoDef" and ":GoInfo" are gd and K under the names vim-go gave them,
// which is what makes the muscle memory survive the plugin going away.
//
// TODO: internal/ex has no way to register a Go-backed command.
// UserCommand carries a replacement STRING, so ":command! GoBuild ..." can
// only expand to another ex command line, and these four need to run code.
// What this wants is one hook on ex.Context, in the same shape as Open and
// Map:
//
//	// Plugin runs a command implemented outside this package. It is tried
//	// after the built-in table and before E492, and reports whether it took
//	// the command.
//	Plugin func(name, args string) (handled bool, err error)
//
// with cmd/pvim setting it to editor.lspExCommand. Until then these are
// reachable from a test and from nothing a person can type.

// lspExCommand runs one of the four ":Go" commands, reporting whether the name
// was one of them.
func (e *editor) lspExCommand(name, args string) (bool, error) {
	switch name {
	case "GoBuild":
		return true, e.goCommand("build", args)
	case "GoTest":
		return true, e.goCommand("test", args)
	case "GoDef":
		return true, e.lspDefinition()
	case "GoInfo":
		return true, e.lspHover()
	}
	return false, nil
}

// goCommand runs "go build" or "go test" over the workspace and puts what it
// said in the quickfix list.
//
// From the workspace root and not the working directory, for the reason
// 'autochdir' makes everything in this file take that shape: the cwd follows
// the buffer, so "go build ./..." run from it builds one package and calls it
// the project.
func (e *editor) goCommand(sub, args string) error {
	b := e.cur()
	if b == nil || b.Name == "" {
		return errors.New("E32: No file name")
	}
	root := lsp.Root(b.Name)
	argv := []string{sub}
	if strings.TrimSpace(args) != "" {
		argv = append(argv, strings.Fields(args)...)
	} else {
		argv = append(argv, "./...")
	}
	cmd := exec.Command("go", argv...)
	cmd.Dir = root
	// Both streams: "go build" writes errors to standard error and "go test"
	// writes a failing test's output to standard output, and a quickfix list
	// that had one and not the other would be empty for half the failures.
	out, _ := cmd.CombinedOutput()

	entries := lspParseGoOutput(root, string(out))
	title := "go " + strings.Join(argv, " ")
	if e.ctx == nil || e.ctx.QF == nil {
		return nil
	}
	lspPushList(e, &quickfix.List{Title: title, Entries: entries, Idx: -1})
	if len(entries) == 0 {
		e.ed.Say(title + ": no errors")
	}
	return nil
}

// lspParseGoOutput turns the go tool's output into quickfix entries.
//
// This is the 'errorformat' vim-go uses, written as a scan rather than as a
// pattern, and the reason it is here rather than in internal/quickfix is that
// internal/quickfix's Format.Parse is still a stub. When the general parser
// lands this function becomes the
// two-line errorformat string vim-go carries:
//
//	%-G#\ %.%#,%A%f:%l:%c:\ %m,%A%f:%l:\ %m,%C%*\s%m,%-G%.%#
//
// and nothing else here changes, because the entries are the same entries.
//
// What the shapes are, measured against go1.27.1:
//
//	# example.com/probe a package header, dropped
//	internal/x/a.go:5:2: undefined: y a build error, relative to the root
//	--- FAIL: TestThing (0.00s) a failed test, kept with no location
//	 a_test.go:12: want 1, got 2 a test failure, relative to the
//	 package directory, resolved by the
//	 "FAIL <import path>" line that closes
//	 the package's output
func lspParseGoOutput(root, out string) []quickfix.Entry {
	var entries []quickfix.Entry
	// pending are the entries whose file name is relative to a package
	// directory that has not been named yet.
	var pending []int
	modulePath := lspModulePath(root)

	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# "):
			// A package header. Dropped, as vim-go's %-G does.
			continue

		case strings.HasPrefix(line, "--- FAIL:"), strings.HasPrefix(line, "--- SKIP:"):
			entries = append(entries, quickfix.Entry{Text: strings.TrimSpace(line)})
			continue

		case strings.HasPrefix(line, "FAIL\t"), strings.HasPrefix(line, "ok  \t"):
			// The line that closes a package's output and names it. Every
			// entry still waiting for a directory gets this one.
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			pkg := strings.TrimSpace(fields[1])
			dir := lspPackageDir(root, modulePath, pkg)
			for _, i := range pending {
				entries[i].FileName = filepath.Join(dir, entries[i].FileName)
			}
			pending = pending[:0]
			continue
		}

		indented := line[0] == ' ' || line[0] == '\t'
		file, lnum, col, msg, ok := lspSplitGoError(strings.TrimSpace(line))
		if !ok {
			// A continuation of the entry before it: the go tool indents the
			// second and later lines of a multi-line error.
			if indented && len(entries) > 0 {
				last := &entries[len(entries)-1]
				last.Text += "\n" + strings.TrimSpace(line)
			}
			continue
		}
		entry := quickfix.Entry{
			FileName: file,
			LNum:     lnum,
			Col:      col,
			Text:     msg,
			Kind:     quickfix.KindError,
			Valid:    true,
		}
		switch {
		case filepath.IsAbs(file):
		case indented:
			// A test failure: relative to the package directory, which the
			// "FAIL <import path>" line below it will name.
			pending = append(pending, len(entries))
		default:
			// A build error: relative to the module root, which is where the
			// command was run.
			entry.FileName = filepath.Join(root, file)
		}
		entries = append(entries, entry)
	}
	return entries
}

// lspSplitGoError reads "file:line:col: message" and "file:line: message".
//
// By hand and not with a pattern, because "regexp" is internal/regex's alone
// in this tree and because a Windows-style "C:\x\a.go:5:2:" would need the
// same care from either. A file name is everything up to the first colon
// followed by a digit run and another colon, which is what makes
// "a:b.go:5:2: x" split at the right one.
func lspSplitGoError(line string) (file string, lnum, col int, msg string, ok bool) {
	// Find the colon that starts the line number.
	for i := 0; i < len(line); i++ {
		if line[i] != ':' {
			continue
		}
		rest := line[i+1:]
		n, after, ok2 := lspLeadingInt(rest)
		if !ok2 || after == "" || after[0] != ':' {
			continue
		}
		file = line[:i]
		lnum = n
		rest = after[1:]
		if c, after2, ok3 := lspLeadingInt(rest); ok3 && after2 != "" && after2[0] == ':' {
			col = c
			msg = strings.TrimSpace(after2[1:])
		} else {
			msg = strings.TrimSpace(rest)
		}
		if file == "" || msg == "" {
			return "", 0, 0, "", false
		}
		return file, lnum, col, msg, true
	}
	return "", 0, 0, "", false
}

// lspLeadingInt reads a decimal run off the front of s.
func lspLeadingInt(s string) (int, string, bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, s, false
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, s, false
	}
	return n, s[i:], true
}

// lspModulePath reads the module line out of a go.mod, or "".
func lspModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// lspPackageDir turns an import path into the directory it lives in, for a
// package inside this module.
func lspPackageDir(root, modulePath, pkg string) string {
	if modulePath == "" {
		return root
	}
	if pkg == modulePath {
		return root
	}
	if rel, ok := strings.CutPrefix(pkg, modulePath+"/"); ok {
		return filepath.Join(root, filepath.FromSlash(rel))
	}
	return root
}

// lspKey is gd, CTRL-] and K, taken before the mode machine sees them.
//
// CTRL-] is a tag jump and K is 'keywordprg', and both become the language
// server's here, which is what vim-go did with the same two keys.
//
// "gd" is NOT here and cannot be: it is two keys, and the "g" of it is held by
// session.pendingG in cmd/pvim/session.go along with gt, gT, gf and the rest.
// The call site for it is one case in that file's g table, calling
// editor.lspDefinition, and it is listed with the other wiring at the top of
// this file. ":GoDef" reaches the same function.
//
// It reports whether the key was taken. A buffer with no server takes none of
// them, so K over a word in a markdown file is still "man", which is what
// 'keywordprg' says and what the oracle grades.
//
// TODO: the call site is one line at the top of session.pass in
// cmd/pvim/session.go. It has to be there rather than in the mode machine
// because internal/mode has no buffer name, no window list and no way to open
// a file, and because a graded run must never reach it.
func (e *editor) lspKey(k key.Key) (bool, error) {
	b := e.cur()
	if b == nil || b.Name == "" {
		return false, nil
	}
	if e.lsp().docs[b.Name] == nil {
		return false, nil
	}
	if e.ed.Mode() != mode.Normal {
		return false, nil
	}
	switch k {
	case key.Rune('K'):
		return true, e.lspHover()
	case key.Ctrl(']'):
		return true, e.lspDefinition()
	}
	return false, nil
}

// lspPushList makes a list the current quickfix list.
//
// internal/ex's Push and not quickfix.Stack's own, which is a stub: the ex
// layer is where the ":colder" rule that Push encodes lives.
func lspPushList(e *editor, l *quickfix.List) {
	ex.Push(e.ctx.QF, l)
}
