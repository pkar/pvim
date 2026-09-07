package lsp

import "encoding/json"

// The protocol structs, hand-written, holding the fields this editor sends or
// reads and no others.
//
// Two things about them are worth knowing before reading any of the code that
// uses them.
//
// A Position's Character is NOT a byte column. It counts UTF-16 code units by
// default, which is the one part of this protocol that came from the editor it
// was designed for rather than from anything sensible, and it means an "e" with
// an acute accent is one unit and two bytes and an emoji is two units and four.
// Everything crossing this boundary goes through UTF16Column and ByteColumn
// in edit.go. Getting it wrong shows up as a completion inserted one byte to the
// left on a line with a comment in Norwegian and nowhere else, which is the
// kind of bug that survives a year.
//
// A URI is not a path. It is "file://" plus a percent-encoded absolute path,
// and a Go source file under a directory with a space in it round-trips
// through url.URL and not through string concatenation. See URI in find.go.

// Position is a place in a document: a zero-based line, and a zero-based
// column in UTF-16 code units.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a half-open span, End exclusive.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a range in a named document, which is what a definition comes
// back as.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// LocationLink is the other shape a definition can come back as, when the
// client said it understood one. This client does not say so, and the type is
// here because gopls sends it anyway for some results and dropping it silently
// would make "gd" do nothing rather than say why.
type LocationLink struct {
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

// TextEdit is a replacement of one range with new text. A formatting reply is
// a list of these.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// TextDocumentIdentifier names a document.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// VersionedTextDocumentIdentifier names a document and which revision of it
// the message is about. The version is the client's own counter and the server
// only ever compares it with the last one it saw.
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// TextDocumentItem is a document being opened: its whole content, once.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// DidOpenTextDocumentParams is textDocument/didOpen.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// ContentChange is one incremental edit. A nil Range means the whole document
// is being replaced by Text, which is the full-sync form and what this client
// falls back to when a server says it cannot do incremental.
type ContentChange struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

// DidChangeTextDocumentParams is textDocument/didChange.
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier `json:"textDocument"`
	ContentChanges []ContentChange                 `json:"contentChanges"`
}

// DidSaveTextDocumentParams is textDocument/didSave. Text is sent only when
// the server asked for it in its save options, which gopls does not.
type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

// DidCloseTextDocumentParams is textDocument/didClose.
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// TextDocumentPositionParams is the shape every "at the cursor" request takes.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// CompletionContext says what set the completion off. gopls reads it: a
// completion triggered by "." is a member completion and is ranked
// differently from one the user asked for with a keystroke.
type CompletionContext struct {
	TriggerKind      int    `json:"triggerKind"`
	TriggerCharacter string `json:"triggerCharacter,omitempty"`
}

// The two trigger kinds this client sends. 1 is "the user invoked it", which
// is a bare CTRL-X CTRL-O, and 2 is "a trigger character was typed", which is
// the dot the vimrc's own mapping puts in front of it.
const (
	TriggerInvoked   = 1
	TriggerCharacter = 2
)

// CompletionParams is textDocument/completion.
type CompletionParams struct {
	TextDocumentPositionParams
	Context CompletionContext `json:"context"`
}

// CompletionItem is one candidate.
//
// InsertText and TextEdit are both optional and both mean "what to put in the
// buffer"; Label is what the menu shows and is the fallback for both. gopls
// sends a TextEdit for nearly everything, because the range it wants to
// replace is not always the word the client thinks is being completed.
type CompletionItem struct {
	Label         string    `json:"label"`
	Kind          int       `json:"kind,omitempty"`
	Detail        string    `json:"detail,omitempty"`
	SortText      string    `json:"sortText,omitempty"`
	FilterText    string    `json:"filterText,omitempty"`
	InsertText    string    `json:"insertText,omitempty"`
	TextEdit      *TextEdit `json:"textEdit,omitempty"`
	Documentation any       `json:"documentation,omitempty"`
}

// CompletionList is the reply to a completion. The server may answer with a
// bare array of items instead, which is why Completion in client.go sniffs the
// raw JSON before unmarshalling.
type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

// MarkupContent is a hover body: "plaintext" or "markdown" and the text.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Hover is the reply to textDocument/hover. Contents is typed as raw JSON
// because the specification allows three shapes for it -- a MarkupContent, a
// MarkedString, or an array of MarkedString -- and gopls has sent two of them
// across its releases.
type Hover struct {
	Contents json.RawMessage `json:"contents"`
	Range    *Range          `json:"range,omitempty"`
}

// FormattingOptions is what textDocument/formatting is told about the buffer's
// indentation. gopls ignores both fields -- gofmt's answer does not depend on
// what the editor thinks a tab is -- and they are sent because the protocol
// makes them required.
type FormattingOptions struct {
	TabSize      int  `json:"tabSize"`
	InsertSpaces bool `json:"insertSpaces"`
}

// DocumentFormattingParams is textDocument/formatting.
type DocumentFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Options      FormattingOptions      `json:"options"`
}

// Diagnostic is one problem the server found.
type Diagnostic struct {
	Range    Range           `json:"range"`
	Severity int             `json:"severity,omitempty"`
	Code     json.RawMessage `json:"code,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
}

// The severities, which are the protocol's and which map onto vim's quickfix
// type characters in cmd/pvim.
const (
	SeverityError   = 1
	SeverityWarning = 2
	SeverityInfo    = 3
	SeverityHint    = 4
)

// PublishDiagnosticsParams is the notification the server pushes when it has
// re-analysed a file. Version is the document version the diagnostics were
// computed against, and an editor that has typed since can tell they are
// stale by comparing it with its own counter.
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     int          `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// ShowMessageParams is window/showMessage and window/logMessage, which gopls
// uses to say things like "no packages found". They reach cmd/pvim's message
// line through Client.OnMessage.
type ShowMessageParams struct {
	Type    int    `json:"type"`
	Message string `json:"message"`
}

// InitializeParams is the handshake.
//
// ProcessID is the one field here that is load-bearing for something other
// than correctness: a server given the client's process id watches it and
// exits when it goes away, which is the difference between a gopls that dies
// with the editor and one that is still holding 400 MB an hour after a crash.
// The gate checks it with pgrep and this field is half of why it
// passes; Client.Close is the other half.
type InitializeParams struct {
	ProcessID             int                `json:"processId"`
	RootURI               string             `json:"rootUri"`
	Capabilities          ClientCapabilities `json:"capabilities"`
	WorkspaceFolders      []WorkspaceFolder  `json:"workspaceFolders,omitempty"`
	InitializationOptions any                `json:"initializationOptions,omitempty"`
}

// WorkspaceFolder is a root the server should watch.
type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// ClientCapabilities is what this client claims to understand. Everything
// absent is a "no", which is the point: a client that claims snippet support
// and does not implement it gets completion items with "${1:}" in them.
type ClientCapabilities struct {
	TextDocument TextDocumentClientCapabilities `json:"textDocument"`
	Workspace    WorkspaceClientCapabilities    `json:"workspace"`
	General      GeneralClientCapabilities      `json:"general,omitempty"`
}

// TextDocumentClientCapabilities is the per-document half.
type TextDocumentClientCapabilities struct {
	Synchronization    SyncCapability       `json:"synchronization"`
	Completion         CompletionCapability `json:"completion"`
	Hover              HoverCapability      `json:"hover"`
	Definition         DynamicCapability    `json:"definition"`
	Formatting         DynamicCapability    `json:"formatting"`
	PublishDiagnostics DiagnosticCapability `json:"publishDiagnostics"`
}

// SyncCapability says which of the sync notifications this client sends.
type SyncCapability struct {
	DidSave bool `json:"didSave"`
}

// CompletionCapability is the completion half.
type CompletionCapability struct {
	CompletionItem CompletionItemCapability `json:"completionItem"`
	ContextSupport bool                     `json:"contextSupport"`
}

// CompletionItemCapability is where snippets are turned down.
//
// SnippetSupport false is deliberate and is the whole reason this struct
// exists. With it true gopls answers a function completion with
// "Get(${1:url})" and expects the editor to run a snippet session over it;
// with it false the same completion is "Get", which is what a vim popup menu
// can insert. The gate says typing "http." shows a popup with "Get" in
// it, and this field is what makes the item say "Get".
type CompletionItemCapability struct {
	SnippetSupport          bool     `json:"snippetSupport"`
	DocumentationFormat     []string `json:"documentationFormat,omitempty"`
	InsertReplaceSupport    bool     `json:"insertReplaceSupport"`
	LabelDetailsSupport     bool     `json:"labelDetailsSupport"`
	DeprecatedSupport       bool     `json:"deprecatedSupport"`
	PreselectSupport        bool     `json:"preselectSupport"`
	CommitCharactersSupport bool     `json:"commitCharactersSupport"`
}

// HoverCapability says which markup a hover body may be written in. This
// client asks for plaintext first: the hover goes into a scratch split with no
// syntax highlighting, so markdown would only mean backticks on the screen.
type HoverCapability struct {
	ContentFormat []string `json:"contentFormat,omitempty"`
}

// DynamicCapability is the shape of a capability with nothing in it but the
// dynamic-registration flag, which this client turns down for everything: a
// server that registers a capability at run time expects the client to track
// registrations, and this one answers client/registerCapability with a null
// and forgets it.
type DynamicCapability struct {
	DynamicRegistration bool `json:"dynamicRegistration"`
}

// DiagnosticCapability is the publishDiagnostics half.
type DiagnosticCapability struct {
	RelatedInformation bool `json:"relatedInformation"`
	VersionSupport     bool `json:"versionSupport"`
}

// WorkspaceClientCapabilities is the workspace half. Configuration true is
// what lets gopls ask for its settings with workspace/configuration, which is
// a request and which this client answers; see Client.handle in client.go.
type WorkspaceClientCapabilities struct {
	Configuration    bool              `json:"configuration"`
	WorkspaceFolders bool              `json:"workspaceFolders"`
	DidChangeConfig  DynamicCapability `json:"didChangeConfiguration"`
}

// GeneralClientCapabilities carries the position encoding negotiation.
//
// This client offers utf-16 alone, which is the protocol's default and the one
// every server must support. Offering utf-8 as well would save the conversion
// in edit.go on every request, and it is not offered on purpose: a server that
// picks utf-8 and a client that quietly kept converting is a bug that only
// appears on non-ASCII lines, and one encoding with a tested converter beats
// two encodings and a branch.
type GeneralClientCapabilities struct {
	PositionEncodings []string `json:"positionEncodings,omitempty"`
}

// InitializeResult is the server's half of the handshake. Only the sync kind
// is read: it decides whether didChange sends ranges or whole documents.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

// ServerCapabilities is what came back. TextDocumentSync is raw because the
// protocol allows either a bare number or an options object in that slot and
// servers use both.
type ServerCapabilities struct {
	TextDocumentSync   json.RawMessage `json:"textDocumentSync"`
	CompletionProvider *struct {
		TriggerCharacters []string `json:"triggerCharacters"`
	} `json:"completionProvider"`
	HoverProvider              json.RawMessage `json:"hoverProvider"`
	DefinitionProvider         json.RawMessage `json:"definitionProvider"`
	DocumentFormattingProvider json.RawMessage `json:"documentFormattingProvider"`
	PositionEncoding           string          `json:"positionEncoding"`
}

// The document sync kinds.
const (
	SyncNone        = 0
	SyncFull        = 1
	SyncIncremental = 2
)

// syncKind reads the sync kind out of whichever of the two shapes the server
// sent, defaulting to none, which is what the protocol says an absent
// textDocumentSync means.
func (s ServerCapabilities) syncKind() int {
	if len(s.TextDocumentSync) == 0 {
		return SyncNone
	}
	var n int
	if err := json.Unmarshal(s.TextDocumentSync, &n); err == nil {
		return n
	}
	var opts struct {
		Change int `json:"change"`
	}
	if err := json.Unmarshal(s.TextDocumentSync, &opts); err == nil {
		return opts.Change
	}
	return SyncNone
}
