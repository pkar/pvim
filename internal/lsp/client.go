package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// Client is one language server: the child process, the connection to it, and
// the documents it has been told about.
//
// One server per editor, not one per buffer. gopls holds a whole module in
// memory and starting a second one for the second file opened would double
// four hundred megabytes to save nothing; the workspace root in Root is what
// makes one server enough. A second Client would be a second LANGUAGE, and
// nothing in this vimrc has one.
//
// Every method is safe to call from any goroutine. It has to be: the
// coalescer's timer fires on its own, the connection's read loop delivers
// diagnostics on another, and the editor calls in from a third.
type Client struct {
	cmd  *exec.Cmd
	conn *Conn
	caps ServerCapabilities
	root string
	bin  string

	// stderr is the tail of what the server wrote to its standard error,
	// which is where gopls puts a panic. It is bounded because a server in a
	// loop can produce megabytes a second and the only use for it is the last
	// twenty lines in an error message.
	stderr *tailBuffer

	// sendMu serialises a flush of pending changes with the request that
	// follows it, which is what keeps a completion from being answered
	// against text the server has not been sent yet.
	sendMu sync.Mutex

	mu      sync.Mutex
	docs    map[string]*doc
	diags   map[string][]Diagnostic
	pending map[string][]ContentChange
	order   []string
	timer   *time.Timer
	closed  bool

	updates chan struct{}
	opts    Options

	closeOnce sync.Once
	closeErr  error
}

// doc is one document the server has been told about.
type doc struct {
	uri     string
	langID  string
	version int
}

// Options is what a Client is started with.
type Options struct {
	// Root is the workspace root. Empty means the server is started with no
	// root at all, which every server treats as "single file mode" and which
	// is not what this editor ever wants; Root(path) is how to fill it.
	Root string

	// Coalesce is how long a burst of edits is held before textDocument/
	// didChange is sent, which at about 50 ms and which is the
	// default when this is zero.
	//
	// The number is a trade and both ends of it are real. Shorter and every
	// keystroke is a JSON message and a re-parse in the server, which on a
	// 4000-line file is measurable while typing a sentence. Longer and the
	// completion after a "." is answered against a document that is one
	// character behind, which gopls reports as "no completions" rather than
	// as an error. 50 ms is below the gap between two keystrokes of ordinary
	// typing and far below the 200 ms the popup gate allows.
	Coalesce time.Duration

	// OnMessage receives window/showMessage and window/logMessage. It is
	// called on a goroutine of this package's, never on the caller's, so an
	// implementation that touches editor state has to hand over first.
	OnMessage func(ShowMessageParams)

	// Env is the child's environment, or nil for this process's.
	Env []string

	// Args are the arguments the server binary is started with. Nil means
	// none, which is how gopls is meant to be run: with no subcommand it
	// serves the protocol on its standard input and output.
	Args []string
}

// defaultCoalesce is the 50 ms.
const defaultCoalesce = 50 * time.Millisecond

// Start spawns a server binary and completes the handshake.
//
// ctx bounds the handshake alone, not the server's life: gopls answers
// initialize in about 30 ms on this machine and then goes on loading the
// workspace in the background, so a context with a second on it is generous
// and a context that is cancelled later does not kill the server. Close does
// that.
func Start(ctx context.Context, bin string, opts Options) (*Client, error) {
	cmd := exec.Command(bin, opts.Args...)
	cmd.Env = opts.Env
	// The server's working directory is the workspace root, not the editor's.
	// With 'autochdir' on, the editor's cwd follows the buffer, and a server
	// inheriting it would resolve a relative build path differently depending
	// on which file happened to be open when it started.
	if opts.Root != "" {
		cmd.Dir = opts.Root
	}
	// Its own process group, so that a CTRL-C in the terminal the editor was
	// started from goes to the editor and not to the server as well: a gopls
	// killed out from under a live client leaves every later request failing
	// with a broken pipe and no explanation.
	setProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	tail := newTailBuffer(8 << 10)
	cmd.Stderr = tail

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("lsp: %s: %w", bin, err)
	}

	c := &Client{
		cmd:     cmd,
		root:    opts.Root,
		bin:     bin,
		stderr:  tail,
		docs:    map[string]*doc{},
		diags:   map[string][]Diagnostic{},
		pending: map[string][]ContentChange{},
		updates: make(chan struct{}, 1),
		opts:    opts,
	}
	if c.opts.Coalesce <= 0 {
		c.opts.Coalesce = defaultCoalesce
	}
	c.conn = NewConn(stdout, stdin, stdin, c.handle)

	if err := c.Initialize(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// NewClient wraps an already-connected server, which is what the tests use: a
// pair of in-memory pipes and a fake server on the other end of them, with no
// process anywhere. The handshake is the caller's to do with Initialize.
func NewClient(r io.Reader, w io.Writer, closer io.Closer, opts Options) *Client {
	c := &Client{
		root:    opts.Root,
		docs:    map[string]*doc{},
		diags:   map[string][]Diagnostic{},
		pending: map[string][]ContentChange{},
		updates: make(chan struct{}, 1),
		opts:    opts,
	}
	if c.opts.Coalesce <= 0 {
		c.opts.Coalesce = defaultCoalesce
	}
	c.conn = NewConn(r, w, closer, c.handle)
	return c
}

// Root is the workspace this server was started for.
func (c *Client) Root() string { return c.root }

// Binary is the server executable this client is talking to.
func (c *Client) Binary() string { return c.bin }

// PID is the server process's id, or 0 when there is no process. The gate that
// says gopls exits with the editor is a pgrep against this number.
func (c *Client) PID() int {
	if c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

// Updates is signalled when diagnostics have changed. It is a channel of
// capacity one with the send dropped when it is full, which is exactly the
// semantics wanted: a frontend that has not looked yet does not need to be
// told twice, and a server that republishes diagnostics for forty files in a
// burst must not block its own read loop on an editor that is busy.
func (c *Client) Updates() <-chan struct{} { return c.updates }

// Initialize sends initialize and initialized.
func (c *Client) Initialize(ctx context.Context) error {
	params := InitializeParams{
		ProcessID: os.Getpid(),
		Capabilities: ClientCapabilities{
			TextDocument: TextDocumentClientCapabilities{
				Synchronization: SyncCapability{DidSave: true},
				Completion: CompletionCapability{
					ContextSupport: true,
					CompletionItem: CompletionItemCapability{
						// See CompletionItemCapability: false here is what
						// makes gopls answer "Get" instead of "Get(${1:url})".
						SnippetSupport:      false,
						DocumentationFormat: []string{"plaintext", "markdown"},
					},
				},
				Hover:              HoverCapability{ContentFormat: []string{"plaintext", "markdown"}},
				PublishDiagnostics: DiagnosticCapability{VersionSupport: true},
			},
			Workspace: WorkspaceClientCapabilities{
				Configuration:    true,
				WorkspaceFolders: true,
			},
			General: GeneralClientCapabilities{PositionEncodings: []string{"utf-16"}},
		},
	}
	if c.root != "" {
		params.RootURI = URI(c.root)
		params.WorkspaceFolders = []WorkspaceFolder{{URI: params.RootURI, Name: baseName(c.root)}}
	}

	var res InitializeResult
	if err := c.conn.Call(ctx.Done(), "initialize", params, &res); err != nil {
		return c.wrap("initialize", err)
	}
	c.caps = res.Capabilities
	if err := c.conn.Notify("initialized", struct{}{}); err != nil {
		return c.wrap("initialized", err)
	}
	return nil
}

// wrap puts the server's name and, when it has died, the tail of its standard
// error onto an error. A gopls that exits during initialize because the module
// does not build says why on stderr and says nothing at all on the wire.
func (c *Client) wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	msg := fmt.Sprintf("lsp: %s: %v", what, err)
	if errors.Is(err, ErrClosed) && c.stderr != nil {
		if tail := strings.TrimSpace(c.stderr.String()); tail != "" {
			msg += "\n" + tail
		}
	}
	return errors.New(msg)
}

// Incremental reports whether the server asked for ranged didChange. A server
// that did not gets whole documents, which is correct and slower; nothing
// this editor talks to says no.
func (c *Client) Incremental() bool { return c.caps.syncKind() == SyncIncremental }

// TriggerCharacters are the characters the server wants a completion request
// after. gopls answers ["."], which is exactly the character the vimrc's own
// mapping puts CTRL-X CTRL-O behind.
func (c *Client) TriggerCharacters() []string {
	if c.caps.CompletionProvider == nil {
		return nil
	}
	return c.caps.CompletionProvider.TriggerCharacters
}

// DidOpen tells the server about a file and its contents.
func (c *Client) DidOpen(path, filetype string, text []byte) error {
	uri := URI(path)
	c.mu.Lock()
	if c.docs[uri] != nil {
		c.mu.Unlock()
		return nil // already open; a second didOpen is a protocol error
	}
	d := &doc{uri: uri, langID: LanguageID(filetype), version: 1}
	c.docs[uri] = d
	c.mu.Unlock()

	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.conn.Notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uri, LanguageID: d.langID, Version: 1, Text: string(text)},
	})
}

// IsOpen reports whether the server has been told about this file.
func (c *Client) IsOpen(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.docs[URI(path)] != nil
}

// Change queues one incremental edit. It is not sent immediately: edits are
// held for the coalescing window and go out as one didChange with several
// content changes in it, which is what the protocol's array is for.
func (c *Client) Change(path string, changes ...ContentChange) error {
	if len(changes) == 0 {
		return nil
	}
	uri := URI(path)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	if c.docs[uri] == nil {
		c.mu.Unlock()
		return nil // never opened: nothing to change
	}
	if _, seen := c.pending[uri]; !seen {
		c.order = append(c.order, uri)
	}
	c.pending[uri] = append(c.pending[uri], changes...)
	if c.timer == nil {
		c.timer = time.AfterFunc(c.opts.Coalesce, func() { _ = c.Flush() })
	} else {
		c.timer.Reset(c.opts.Coalesce)
	}
	c.mu.Unlock()
	return nil
}

// ChangeFull queues a whole-document replacement, which is the fallback for an
// edit whose range is not worth working out -- an undo of a hundred lines, a
// ":e!", a filter over the whole buffer -- and the only form a server without
// incremental sync understands.
func (c *Client) ChangeFull(path string, text []byte) error {
	return c.Change(path, ContentChange{Text: string(text)})
}

// Flush sends whatever the coalescer is holding, now.
//
// Called by the timer, and called by every request before it goes out. The
// second is what makes the window a latency optimisation rather than a
// correctness problem: the server never answers a completion against text it
// has not been sent, whatever the timer was doing.
func (c *Client) Flush() error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.flushLocked()
}

// flushLocked sends the pending changes. The caller holds sendMu.
func (c *Client) flushLocked() error {
	c.mu.Lock()
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	if len(c.order) == 0 {
		c.mu.Unlock()
		return nil
	}
	type batch struct {
		uri     string
		version int
		changes []ContentChange
	}
	var batches []batch
	for _, uri := range c.order {
		changes := c.pending[uri]
		delete(c.pending, uri)
		d := c.docs[uri]
		if d == nil || len(changes) == 0 {
			continue
		}
		d.version++
		batches = append(batches, batch{uri: uri, version: d.version, changes: changes})
	}
	c.order = c.order[:0]
	full := !c.incrementalLocked()
	c.mu.Unlock()

	var first error
	for _, b := range batches {
		changes := b.changes
		if full {
			// A server with full sync only understands the last one, and only
			// if it has no range on it. A caller that used Change on such a
			// server has a bug; sending the tail is the least wrong thing and
			// ChangeFull is what it should have called.
			changes = changes[len(changes)-1:]
			changes[0].Range = nil
		}
		err := c.conn.Notify("textDocument/didChange", DidChangeTextDocumentParams{
			TextDocument:   VersionedTextDocumentIdentifier{URI: b.uri, Version: b.version},
			ContentChanges: changes,
		})
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// incrementalLocked is Incremental with c.mu already held.
func (c *Client) incrementalLocked() bool { return c.caps.syncKind() == SyncIncremental }

// DidSave tells the server the file is on disk.
func (c *Client) DidSave(path string) error {
	uri := URI(path)
	c.mu.Lock()
	open := c.docs[uri] != nil
	c.mu.Unlock()
	if !open {
		return nil
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := c.flushLocked(); err != nil {
		return err
	}
	return c.conn.Notify("textDocument/didSave", DidSaveTextDocumentParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	})
}

// DidClose tells the server the buffer is gone and drops its diagnostics.
func (c *Client) DidClose(path string) error {
	uri := URI(path)
	c.mu.Lock()
	open := c.docs[uri] != nil
	delete(c.docs, uri)
	delete(c.diags, uri)
	delete(c.pending, uri)
	c.mu.Unlock()
	if !open {
		return nil
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.conn.Notify("textDocument/didClose", DidCloseTextDocumentParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	})
}

// call flushes and then sends one request, which is the shape of every
// question this client asks.
func (c *Client) call(ctx context.Context, method string, params, result any) error {
	c.sendMu.Lock()
	if err := c.flushLocked(); err != nil {
		c.sendMu.Unlock()
		return err
	}
	c.sendMu.Unlock()
	return c.conn.Call(ctx.Done(), method, params, result)
}

// Complete asks for the candidates at a position.
//
// trigger is the character that set it off, empty for a completion the user
// asked for with a keystroke. The vimrc's mapping types a "." and then
// CTRL-X CTRL-O, so the editor passes "." and gopls ranks members of the
// package or the value to the left of it first.
func (c *Client) Complete(ctx context.Context, path string, pos Position, trigger string) ([]CompletionItem, error) {
	params := CompletionParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{URI: URI(path)},
			Position:     pos,
		},
		Context: CompletionContext{TriggerKind: TriggerInvoked},
	}
	if trigger != "" {
		params.Context.TriggerKind = TriggerCharacter
		params.Context.TriggerCharacter = trigger
	}
	var raw json.RawMessage
	if err := c.call(ctx, "textDocument/completion", params, &raw); err != nil {
		return nil, err
	}
	return parseCompletion(raw)
}

// parseCompletion reads either shape of a completion reply: a list object, or
// a bare array of items.
func parseCompletion(raw json.RawMessage) ([]CompletionItem, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '[' {
		var items []CompletionItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		return sortItems(items), nil
	}
	var list CompletionList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return sortItems(list.Items), nil
}

// sortItems puts the candidates in the order the popup menu shows them.
//
// By sortText and not by label, because sortText is the whole point of the
// field: gopls sorts an exported method above an unexported one and a
// same-package symbol above an imported one, and a client that ignores it
// shows "Get" somewhere in an alphabetical list of two hundred. An item with
// no sortText sorts by its label, which is the protocol's own rule.
func sortItems(items []CompletionItem) []CompletionItem {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].SortText, items[j].SortText
		if a == "" {
			a = items[i].Label
		}
		if b == "" {
			b = items[j].Label
		}
		return a < b
	})
	return items
}

// Definition asks where a symbol is defined. The reply comes back in either of
// the two shapes the protocol allows and both are read.
func (c *Client) Definition(ctx context.Context, path string, pos Position) ([]Location, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: URI(path)},
		Position:     pos,
	}
	var raw json.RawMessage
	if err := c.call(ctx, "textDocument/definition", params, &raw); err != nil {
		return nil, err
	}
	return parseLocations(raw)
}

// parseLocations reads a Location, an array of Location, or an array of
// LocationLink.
func parseLocations(raw json.RawMessage) ([]Location, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '{' {
		var loc Location
		if err := json.Unmarshal(raw, &loc); err != nil {
			return nil, err
		}
		return []Location{loc}, nil
	}
	var locs []Location
	if err := json.Unmarshal(raw, &locs); err == nil && len(locs) > 0 && locs[0].URI != "" {
		return locs, nil
	}
	var links []LocationLink
	if err := json.Unmarshal(raw, &links); err != nil {
		return nil, err
	}
	out := make([]Location, 0, len(links))
	for _, l := range links {
		out = append(out, Location{URI: l.TargetURI, Range: l.TargetSelectionRange})
	}
	return out, nil
}

// Hover asks what is under the cursor, as plain text.
//
// The markup is flattened here rather than in cmd/pvim because flattening it
// needs to know which of the three legal shapes came back, and that is this
// package's business. What comes out is the lines a scratch split shows.
func (c *Client) Hover(ctx context.Context, path string, pos Position) (string, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: URI(path)},
		Position:     pos,
	}
	var h Hover
	if err := c.call(ctx, "textDocument/hover", params, &h); err != nil {
		return "", err
	}
	return hoverText(h.Contents), nil
}

// hoverText flattens the three shapes of hover contents into plain text.
func hoverText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	case '{':
		var m MarkupContent
		if json.Unmarshal(raw, &m) == nil {
			return m.Value
		}
		// A MarkedString object: {"language": "go", "value": "..."}.
		var ms struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(raw, &ms) == nil {
			return ms.Value
		}
	case '[':
		var parts []json.RawMessage
		if json.Unmarshal(raw, &parts) == nil {
			var out []string
			for _, p := range parts {
				if s := hoverText(p); s != "" {
					out = append(out, s)
				}
			}
			return strings.Join(out, "\n")
		}
	}
	return ""
}

// Format asks the server to format a document and applies the reply to src.
//
// src has to be the bytes the server has, which after a Flush it is. The
// result is the formatted document; when the server has nothing to change it
// answers an empty array and src comes back unchanged, which is not the same
// as an error and must not be treated as one.
func (c *Client) Format(ctx context.Context, path string, src []byte) ([]byte, error) {
	params := DocumentFormattingParams{
		TextDocument: TextDocumentIdentifier{URI: URI(path)},
		Options:      FormattingOptions{TabSize: 8, InsertSpaces: false},
	}
	var edits []TextEdit
	if err := c.call(ctx, "textDocument/formatting", params, &edits); err != nil {
		return nil, err
	}
	return ApplyEdits(src, edits)
}

// Diagnostics is what the server last published for a file.
func (c *Client) Diagnostics(path string) []Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Diagnostic(nil), c.diags[URI(path)]...)
}

// AllDiagnostics is every file the server has published for, keyed by path.
//
// The whole map and not one file, because a type error in a.go usually shows
// up as diagnostics in b.go as well, and a quickfix list holding only the
// buffer that was written is a list that sends you looking in the wrong place.
func (c *Client) AllDiagnostics() map[string][]Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string][]Diagnostic, len(c.diags))
	for uri, ds := range c.diags {
		if p := Path(uri); p != "" {
			out[p] = append([]Diagnostic(nil), ds...)
		}
	}
	return out
}

// handle answers the server's half of the protocol.
//
// Every entry here is a message gopls sends unprompted, and the three that
// return a value are REQUESTS, which the server blocks on. That is the part
// that is easy to get wrong and hard to debug: a client that treats
// workspace/configuration as a notification gets one completion, then gopls
// waits forever for a reply and the editor looks like it has no language
// server at all.
func (c *Client) handle(method string, params json.RawMessage) (any, error) {
	switch method {
	case "workspace/configuration":
		// One settings object per item asked for. Empty objects mean "all
		// defaults", which is what this editor wants: gopls's defaults are
		// what vim-go has been running with on this machine.
		var req struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(params, &req)
		out := make([]map[string]any, len(req.Items))
		for i := range out {
			out[i] = map[string]any{}
		}
		return out, nil

	case "client/registerCapability", "client/unregisterCapability",
		"window/workDoneProgress/create":
		// Answered with null and forgotten. This client turns down dynamic
		// registration in its capabilities, so anything gopls registers here
		// is something it would have done anyway.
		return nil, nil

	case "workspace/applyEdit":
		// Refused rather than ignored. A server told an edit was applied when
		// it was not goes on to compute against a document that does not
		// exist; "applied": false is the protocol's way of saying no.
		return map[string]any{"applied": false}, nil

	case "textDocument/publishDiagnostics":
		var p PublishDiagnosticsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil
		}
		c.mu.Lock()
		if len(p.Diagnostics) == 0 {
			delete(c.diags, p.URI)
		} else {
			c.diags[p.URI] = p.Diagnostics
		}
		c.mu.Unlock()
		select {
		case c.updates <- struct{}{}:
		default:
		}
		return nil, nil

	case "window/showMessage", "window/logMessage":
		if c.opts.OnMessage != nil {
			var p ShowMessageParams
			if json.Unmarshal(params, &p) == nil {
				c.opts.OnMessage(p)
			}
		}
		return nil, nil
	}
	// Everything else -- $/progress, telemetry/event, and whatever a future
	// gopls invents -- is dropped. A request among them gets a null, which is
	// the answer for a capability this client never claimed.
	return nil, nil
}

// shutdownGrace is how long Close waits for the server to go away on its own
// before killing it.
//
// gopls answers shutdown and exits in a few milliseconds when it is idle and
// takes longer when it is in the middle of loading a workspace, which is
// exactly when an editor is most likely to be quit. Two seconds is past
// anything measured here (the worst on this machine, quitting during a cold
// load of a real module, was 180 ms) and short enough that a hung server does not
// hold the editor's exit.
const shutdownGrace = 2 * time.Second

// Close shuts the server down and does not return until it is gone.
//
// Four steps, and every one of them is needed for the gate that pgrep
// finds no gopls after the editor exits:
//
// 1. "shutdown", which asks the server to stop accepting work.
// 2. "exit", the notification that tells it to leave.
// 3. closing its standard input, which is what a server that ignored both
// notices.
// 4. waiting, and killing the process group if the grace period runs out.
//
// Steps 1 and 2 are the polite path and 3 and 4 are the one that actually
// holds. A gopls in a state where it answers neither -- which a panic in a
// goroutine puts it in -- is killed, and that is the difference between a
// clean exit and the 400 MB process the pgrep check exists to catch.
func (c *Client) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.closeOnce1() })
	return c.closeErr
}

func (c *Client) closeOnce1() error {
	c.mu.Lock()
	c.closed = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()

	if c.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		// The reply is not read for its content; what matters is that the
		// server got the message before "exit" arrives, because a server sent
		// "exit" without "shutdown" is required to exit with a non-zero
		// status and this editor would rather it did not.
		_ = c.conn.Call(ctx.Done(), "shutdown", nil, nil)
		cancel()
		_ = c.conn.Notify("exit", nil)
		_ = c.conn.Close()
	}

	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		return exitError(err)
	case <-time.After(shutdownGrace):
		killGroup(c.cmd)
		select {
		case err := <-done:
			return exitError(err)
		case <-time.After(shutdownGrace):
			return fmt.Errorf("lsp: %s would not exit", c.bin)
		}
	}
}

// exitError drops the status of a server that was killed, which is not a
// failure of the editor's and not something to print on the way out.
func exitError(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return nil
	}
	return err
}

// baseName is filepath.Base without the import, which this file does not
// otherwise need.
func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 && i+1 < len(p) {
		return p[i+1:]
	}
	return p
}

// tailBuffer keeps the last n bytes written to it and throws away the rest.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
