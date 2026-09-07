package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// A fake server on the other end of two in-memory pipes.
//
// Every test in this file that is not about a real gopls runs against this,
// and that is deliberate: the wire format, the coalescing window, the
// bidirectional half of the protocol and the shapes a reply can take are all
// things a fake can be made to do on demand and a real server cannot. The
// tests against the real gopls are at the bottom and they check the things
// only it can answer.
type fakeServer struct {
	t    *testing.T
	conn *Conn

	mu       sync.Mutex
	received []Message
	replies  map[string]func(Message) (any, error)
}

// newFake returns a client wired to a fake server over two pipes.
func newFake(t *testing.T, opts Options) (*Client, *fakeServer) {
	t.Helper()
	cr, sw := io.Pipe() // server writes, client reads
	sr, cw := io.Pipe() // client writes, server reads

	f := &fakeServer{t: t, replies: map[string]func(Message) (any, error){}}
	f.reply("initialize", func(Message) (any, error) {
		return InitializeResult{Capabilities: ServerCapabilities{
			TextDocumentSync: json.RawMessage("2"),
			CompletionProvider: &struct {
				TriggerCharacters []string `json:"triggerCharacters"`
			}{TriggerCharacters: []string{"."}},
		}}, nil
	})
	f.reply("shutdown", func(Message) (any, error) { return nil, nil })

	// The server's writer is closed when its loop ends, which is what turns
	// the client's Close into an EOF on the read loop instead of a wait with
	// no end. A fake that only closes one direction hangs every test in this
	// file in t.Cleanup and looks like a bug in the client.
	f.conn = &Conn{w: sw}
	go func() {
		defer sw.Close()
		f.serve(sr)
	}()
	c := NewClient(cr, cw, cw, opts)
	t.Cleanup(func() { _ = c.Close() })
	return c, f
}

func (f *fakeServer) reply(method string, fn func(Message) (any, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[method] = fn
}

func (f *fakeServer) serve(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		m, err := readMessage(br)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.received = append(f.received, m)
		fn := f.replies[m.Method]
		f.mu.Unlock()
		if len(m.ID) == 0 {
			continue // a notification
		}
		var reply Message
		reply.JSONRPC = "2.0"
		reply.ID = m.ID
		if fn == nil {
			reply.Result = json.RawMessage("null")
		} else {
			res, err := fn(m)
			if err != nil {
				reply.Error = &ResponseError{Code: -32603, Message: err.Error()}
			} else {
				raw, _ := json.Marshal(res)
				reply.Result = raw
			}
		}
		_ = f.conn.write(reply)
	}
}

// push sends a notification from the server to the client.
func (f *fakeServer) push(method string, params any) {
	raw, _ := json.Marshal(params)
	_ = f.conn.write(Message{JSONRPC: "2.0", Method: method, Params: raw})
}

// seen returns every message of one method the server has been sent.
func (f *fakeServer) seen(method string) []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Message
	for _, m := range f.received {
		if m.Method == method {
			out = append(out, m)
		}
	}
	return out
}

// waitFor spins until cond is true or the deadline passes. Polling and not a
// channel because what is being waited for is a message crossing three
// goroutines, and a test that hangs when it breaks is worse than one that
// fails in half a second.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestFramingRoundTrip is the base protocol: a header block, a blank line and
// that many bytes of JSON, read back into the message that was written.
func TestFramingRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	c := &Conn{w: &buf}
	want := Message{JSONRPC: "2.0", ID: json.RawMessage("7"), Method: "textDocument/hover", Params: json.RawMessage(`{"a":1}`)}
	if err := c.write(want); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "Content-Length: ") {
		t.Fatalf("no Content-Length header: %q", buf.String())
	}
	got, err := readMessage(bufio.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != want.Method || string(got.ID) != string(want.ID) || string(got.Params) != string(want.Params) {
		t.Fatalf("round trip: got %+v want %+v", got, want)
	}
}

// TestFramingIgnoresOtherHeaders: Content-Type is in the specification, is
// deprecated, and is still sent by at least one server. It has to be skipped
// and not choked on.
func TestFramingIgnoresOtherHeaders(t *testing.T) {
	raw := "Content-Type: application/vscode-jsonrpc; charset=utf-8\r\nContent-Length: 13\r\n\r\n" + `{"id":1}` + "     "
	m, err := readMessage(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if string(m.ID) != "1" {
		t.Fatalf("id: got %q", m.ID)
	}
}

// TestFramingRefusesMissingLength: a header block with no Content-Length is
// not a message and must not be guessed at.
func TestFramingRefusesMissingLength(t *testing.T) {
	if _, err := readMessage(bufio.NewReader(strings.NewReader("X: 1\r\n\r\n{}"))); err == nil {
		t.Fatal("a message with no Content-Length was accepted")
	}
}

// TestUTF16Columns is the conversion the protocol forces, in the three cases
// that tell a right implementation from one that only works in English.
func TestUTF16Columns(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		byteCol int
		u16     int
	}{
		{"ascii", "http.Get", 5, 5},
		{"ascii end", "http.Get", 8, 8},
		// "é" is two bytes and one UTF-16 unit.
		{"two byte rune", "café.Get", 5, 4},
		// A rune outside the basic plane is four bytes and TWO units, which is
		// the case that catches a client counting runes.
		{"astral rune", "x\U0001F600y", 5, 3},
		{"astral start", "x\U0001F600y", 1, 1},
		{"past the end", "abc", 99, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UTF16Column([]byte(tc.line), tc.byteCol); got != tc.u16 {
				t.Errorf("UTF16Column(%q, %d) = %d, want %d", tc.line, tc.byteCol, got, tc.u16)
			}
			if got := ByteColumn([]byte(tc.line), tc.u16); got != min(tc.byteCol, len(tc.line)) {
				t.Errorf("ByteColumn(%q, %d) = %d, want %d", tc.line, tc.u16, got, min(tc.byteCol, len(tc.line)))
			}
		})
	}
}

// TestByteColumnInsideSurrogatePair: a column that lands between the two units
// of an astral rune is not a place in the text, and rounding to the start of
// the rune is the only answer that leaves valid UTF-8 behind.
func TestByteColumnInsideSurrogatePair(t *testing.T) {
	line := []byte("x\U0001F600y")
	if got := ByteColumn(line, 2); got != 1 {
		t.Fatalf("ByteColumn inside a surrogate pair = %d, want 1", got)
	}
}

// TestOffsetAndPosition are inverses over a document with a blank line, a
// trailing newline and a multi-byte rune in it.
func TestOffsetAndPosition(t *testing.T) {
	src := []byte("package main\n\nimport \"café\"\n")
	for off := 0; off <= len(src); off++ {
		p := PositionAt(src, off)
		back, err := Offset(src, p)
		if err != nil {
			t.Fatalf("offset %d -> %+v: %v", off, p, err)
		}
		// A byte in the middle of a rune has no position of its own and comes
		// back at the start of that rune, which is the only round trip that
		// can hold.
		if back > off {
			t.Fatalf("offset %d round-tripped forward to %d", off, back)
		}
	}
	if got, _ := Offset(src, Position{Line: 2, Character: 0}); got != 14 {
		t.Fatalf("start of line 2 = %d, want 14", got)
	}
}

// TestApplyEditsIsGofmt is the shape a formatting reply takes: several edits,
// out of order, each measured against the ORIGINAL document. Applied
// front-to-back they corrupt the file; applied back-to-front they do not, and
// this is the test that says which one this package does.
func TestApplyEditsIsGofmt(t *testing.T) {
	src := []byte("package main\nimport \"os\"\nimport \"fmt\"\nfunc main() {}\n")
	edits := []TextEdit{
		// Deliberately not in document order.
		{Range: Range{Position{2, 0}, Position{3, 0}}, NewText: ""},
		{Range: Range{Position{1, 0}, Position{1, 0}}, NewText: "\n"},
		{Range: Range{Position{1, 7}, Position{1, 11}}, NewText: "(\n\t\"fmt\"\n\t\"os\"\n)"},
	}
	got, err := ApplyEdits(src, edits)
	if err != nil {
		t.Fatal(err)
	}
	want := "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\nfunc main() {}\n"
	if string(got) != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// TestApplyEditsEmptyIsIdentity: a file gopls has nothing to say about comes
// back with an empty array, and that is not an error.
func TestApplyEditsEmptyIsIdentity(t *testing.T) {
	src := []byte("package main\n")
	got, err := ApplyEdits(src, nil)
	if err != nil || string(got) != string(src) {
		t.Fatalf("got %q, %v", got, err)
	}
}

// TestApplyEditsRefusesOverlap: overlapping edits are a server bug and
// applying them is a silently corrupted file.
func TestApplyEditsRefusesOverlap(t *testing.T) {
	src := []byte("abcdef\n")
	edits := []TextEdit{
		{Range: Range{Position{0, 0}, Position{0, 4}}, NewText: "X"},
		{Range: Range{Position{0, 2}, Position{0, 6}}, NewText: "Y"},
	}
	if _, err := ApplyEdits(src, edits); err == nil {
		t.Fatal("overlapping edits were applied")
	}
}

// TestCoalesceOneMessagePerBurst is the 50 ms window: a burst of edits
// leaves the client as ONE didChange holding all of them, not one message per
// keystroke.
func TestCoalesceOneMessagePerBurst(t *testing.T) {
	c, f := newFake(t, Options{Root: "/tmp/x", Coalesce: 30 * time.Millisecond})
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.DidOpen("/tmp/x/a.go", "go", []byte("package main\n")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if err := c.Change("/tmp/x/a.go", ContentChange{
			Range: &Range{Position{0, 12}, Position{0, 12}},
			Text:  "x",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Nothing has gone out yet: the window has not closed.
	if got := len(f.seen("textDocument/didChange")); got != 0 {
		t.Fatalf("%d didChange before the window closed, want 0", got)
	}
	waitFor(t, "the coalesced didChange", func() bool { return len(f.seen("textDocument/didChange")) == 1 })

	var p DidChangeTextDocumentParams
	if err := json.Unmarshal(f.seen("textDocument/didChange")[0].Params, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.ContentChanges) != 8 {
		t.Fatalf("%d content changes in the batch, want 8", len(p.ContentChanges))
	}
	if p.TextDocument.Version != 2 {
		t.Fatalf("version %d, want 2 (didOpen was 1)", p.TextDocument.Version)
	}
}

// TestRequestFlushesFirst is the half of the window that makes it safe: a
// request goes out AFTER the changes queued before it, whatever the timer was
// doing, so a completion is never answered against text the server has not
// been sent.
func TestRequestFlushesFirst(t *testing.T) {
	c, f := newFake(t, Options{Root: "/tmp/x", Coalesce: time.Hour})
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.reply("textDocument/completion", func(Message) (any, error) {
		return CompletionList{Items: []CompletionItem{{Label: "Get"}}}, nil
	})
	if err := c.DidOpen("/tmp/x/a.go", "go", []byte("package main\n")); err != nil {
		t.Fatal(err)
	}
	if err := c.Change("/tmp/x/a.go", ContentChange{Range: &Range{Position{0, 12}, Position{0, 12}}, Text: "\nhttp."}); err != nil {
		t.Fatal(err)
	}
	items, err := c.Complete(context.Background(), "/tmp/x/a.go", Position{Line: 1, Character: 5}, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Label != "Get" {
		t.Fatalf("items = %+v", items)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var changeAt, completeAt = -1, -1
	for i, m := range f.received {
		switch m.Method {
		case "textDocument/didChange":
			changeAt = i
		case "textDocument/completion":
			completeAt = i
		}
	}
	if changeAt < 0 || completeAt < 0 || changeAt > completeAt {
		t.Fatalf("didChange at %d, completion at %d: the change must go first", changeAt, completeAt)
	}
}

// TestServerRequestsAreAnswered is the bidirectional half, and it is the one
// that hangs an editor when it is missing: gopls sends workspace/configuration
// and client/registerCapability as requests and waits for a reply.
func TestServerRequestsAreAnswered(t *testing.T) {
	c, f := newFake(t, Options{Root: "/tmp/x"})
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The fake asks the client a question and reads its answer off the same
	// stream the client's own replies come back on, so this needs its own
	// reader; instead, drive it through the client's connection directly by
	// calling the handler, which is what the read loop does.
	res, err := c.handle("workspace/configuration", json.RawMessage(`{"items":[{"section":"gopls"},{"section":"gopls"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	settings, ok := res.([]map[string]any)
	if !ok || len(settings) != 2 {
		t.Fatalf("workspace/configuration answered %#v; gopls wants one object per item", res)
	}
	if res, err := c.handle("client/registerCapability", json.RawMessage(`{"registrations":[]}`)); err != nil || res != nil {
		t.Fatalf("client/registerCapability answered %#v, %v; want null", res, err)
	}
	if res, _ := c.handle("workspace/applyEdit", json.RawMessage(`{"edit":{}}`)); res == nil {
		t.Fatal("workspace/applyEdit must be refused explicitly, not ignored")
	}
	_ = f
}

// TestDiagnosticsArrive: a publishDiagnostics notification lands in the store
// and wakes the update channel.
func TestDiagnosticsArrive(t *testing.T) {
	c, f := newFake(t, Options{Root: "/tmp/x"})
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.push("textDocument/publishDiagnostics", PublishDiagnosticsParams{
		URI: URI("/tmp/x/a.go"),
		Diagnostics: []Diagnostic{{
			Range:    Range{Position{3, 1}, Position{3, 6}},
			Severity: SeverityError,
			Message:  "undefined: nope",
		}},
	})
	select {
	case <-c.Updates():
	case <-time.After(2 * time.Second):
		t.Fatal("no update signalled")
	}
	ds := c.Diagnostics("/tmp/x/a.go")
	if len(ds) != 1 || ds[0].Message != "undefined: nope" {
		t.Fatalf("diagnostics = %+v", ds)
	}
	if all := c.AllDiagnostics(); len(all["/tmp/x/a.go"]) != 1 {
		t.Fatalf("AllDiagnostics = %+v", all)
	}

	// An empty list is how a server says a file is clean, and it has to clear
	// the store rather than leave the last errors on the screen.
	f.push("textDocument/publishDiagnostics", PublishDiagnosticsParams{URI: URI("/tmp/x/a.go")})
	waitFor(t, "the diagnostics to clear", func() bool { return len(c.Diagnostics("/tmp/x/a.go")) == 0 })
}

// TestCompletionReplyShapes: the protocol allows a bare array as well as a
// list object, and servers use both.
func TestCompletionReplyShapes(t *testing.T) {
	items, err := parseCompletion(json.RawMessage(`[{"label":"B"},{"label":"A"}]`))
	if err != nil || len(items) != 2 || items[0].Label != "A" {
		t.Fatalf("array shape: %+v, %v", items, err)
	}
	items, err = parseCompletion(json.RawMessage(`{"isIncomplete":true,"items":[{"label":"Z","sortText":"00"},{"label":"A","sortText":"99"}]}`))
	if err != nil || len(items) != 2 || items[0].Label != "Z" {
		t.Fatalf("sortText must beat the label: %+v, %v", items, err)
	}
	if items, err := parseCompletion(json.RawMessage(`null`)); err != nil || items != nil {
		t.Fatalf("null: %+v, %v", items, err)
	}
}

// TestDefinitionReplyShapes: Location, []Location and []LocationLink.
func TestDefinitionReplyShapes(t *testing.T) {
	one, err := parseLocations(json.RawMessage(`{"uri":"file:///a.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}}`))
	if err != nil || len(one) != 1 || one[0].URI != "file:///a.go" {
		t.Fatalf("single: %+v, %v", one, err)
	}
	many, err := parseLocations(json.RawMessage(`[{"uri":"file:///a.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}}}]`))
	if err != nil || len(many) != 1 {
		t.Fatalf("array: %+v, %v", many, err)
	}
	links, err := parseLocations(json.RawMessage(`[{"targetUri":"file:///b.go","targetSelectionRange":{"start":{"line":9,"character":5},"end":{"line":9,"character":8}}}]`))
	if err != nil || len(links) != 1 || links[0].URI != "file:///b.go" || links[0].Range.Start.Line != 9 {
		t.Fatalf("links: %+v, %v", links, err)
	}
}

// TestHoverShapes: a string, a MarkupContent and an array of both.
func TestHoverShapes(t *testing.T) {
	if got := hoverText(json.RawMessage(`"plain"`)); got != "plain" {
		t.Errorf("string hover = %q", got)
	}
	if got := hoverText(json.RawMessage(`{"kind":"plaintext","value":"func Get(url string)"}`)); got != "func Get(url string)" {
		t.Errorf("markup hover = %q", got)
	}
	if got := hoverText(json.RawMessage(`[{"value":"a"},"b"]`)); got != "a\nb" {
		t.Errorf("array hover = %q", got)
	}
	if got := hoverText(json.RawMessage(`null`)); got != "" {
		t.Errorf("null hover = %q", got)
	}
}

// TestURIRoundTrip: a path with a space in it is not a URI until it has been
// escaped, and gopls compares URIs as strings.
func TestURIRoundTrip(t *testing.T) {
	for _, p := range []string{"/tmp/a.go", "/tmp/a dir/b.go", "/tmp/100% sure/c.go", "/tmp/a#b/d.go"} {
		u := URI(p)
		if !strings.HasPrefix(u, "file:///") {
			t.Errorf("URI(%q) = %q", p, u)
		}
		if got := Path(u); got != p {
			t.Errorf("Path(URI(%q)) = %q", p, got)
		}
	}
	if got := Path("http://example.com/x"); got != "" {
		t.Errorf("a non-file URI came back as a path: %q", got)
	}
}

// TestLanguageID: vim's 'filetype' is the protocol's languageId except for the
// go.mod family.
func TestLanguageID(t *testing.T) {
	for ft, want := range map[string]string{"go": "go", "gomod": "go.mod", "terraform": "terraform", "markdown": "markdown"} {
		if got := LanguageID(ft); got != want {
			t.Errorf("LanguageID(%q) = %q, want %q", ft, got, want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestDiffIsIncremental: an edit in the middle of a document comes out as the
// bytes that moved, not as the whole file.
func TestDiffIsIncremental(t *testing.T) {
	old := []byte("package main\n\nfunc main() {\n\tx := 1\n\t_ = x\n}\n")
	next := []byte("package main\n\nfunc main() {\n\tx := 12\n\t_ = x\n}\n")
	ch, ok := Diff(old, next)
	if !ok {
		t.Fatal("no change reported for a changed document")
	}
	if ch.Range == nil {
		t.Fatal("the change has no range; that is a whole-document resend")
	}
	if ch.Text != "2" {
		t.Fatalf("the change carries %q, want the one byte that was typed", ch.Text)
	}
	if ch.Range.Start.Line != 3 || ch.Range.End.Line != 3 {
		t.Fatalf("the change is on lines %d..%d, want 3", ch.Range.Start.Line, ch.Range.End.Line)
	}
	if ch.Range.Start.Character != 7 || ch.Range.End.Character != 7 {
		t.Fatalf("the change spans columns %d..%d, want an insert at 7", ch.Range.Start.Character, ch.Range.End.Character)
	}
	if _, ok := Diff(old, old); ok {
		t.Error("an unchanged document reported a change")
	}
}

// TestDiffRoundTrips is the contract with the server in one sentence: applying
// what Diff sends to what the server has gives what the buffer holds.
//
// The cases are the boundaries. Each of the first four is a place where the
// prefix or the suffix runs to the end of one of the two documents, and each
// of them is one missing bounds check away from a panic on a keystroke.
func TestDiffRoundTrips(t *testing.T) {
	cases := []struct{ name, old, next string }{
		{"append at the end", "package main\n", "package main\nx"},
		{"append after a multi-byte rune", "// café", "// café!"},
		{"delete everything", "package main\n", ""},
		{"fill an empty document", "", "package main\n"},
		{"prepend", "b\n", "a\nb\n"},
		{"replace the only line", "one\n", "two\n"},
		{"edit beside an astral rune", "x\U0001F600y\n", "x\U0001F600z\n"},
		{"delete inside a word", "// café au lait\n", "// café\n"},
		{"insert a whole function", "package a\n", "package a\n\nfunc f() {}\n"},
		{"crlf line", "a\r\nb\r\n", "a\r\nbc\r\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch, ok := Diff([]byte(c.old), []byte(c.next))
			if !ok {
				t.Fatal("no change reported")
			}
			got, err := ApplyEdits([]byte(c.old), []TextEdit{{Range: *ch.Range, NewText: ch.Text}})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.next {
				t.Fatalf("applying %+v %q gave %q, want %q", *ch.Range, ch.Text, got, c.next)
			}
		})
	}
}
