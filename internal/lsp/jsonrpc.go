// Package lsp is the language server client: JSON-RPC 2.0 over a child
// process's stdin and stdout, and the dozen requests an editor needs out of a
// protocol with two hundred of them.
//
// encoding/json and nothing else, and the reason is the static gate: every
// JSON library worth reaching for either carries a cgo file somewhere in its
// graph or is one dependency away from one, and this is the package where that
// would sneak in without anybody noticing until `make static-check` went red.
// There is no code generator here either. The protocol structs in protocol.go
// are hand-written and hold exactly the fields this editor reads or sends,
// which is about sixty of the several thousand in the specification.
//
// This package imports nothing else in this module. A language server client
// is a process, a pipe and a wire format; what a completion item does to a
// buffer is cmd/pvim's, and keeping the arrow pointing that way is what lets
// every test in here run against a fake server over an in-memory pipe with no
// editor anywhere near it.
package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// The wire format, from the base protocol: each message is a header block of
// "Name: value" lines ending in a blank line, then that many bytes of JSON.
// Only Content-Length is required and only Content-Length is written here;
// Content-Type is read and ignored, which is what the specification says to do
// with it (its one legal value names a JSON charset that has been UTF-8 since
// the header was deprecated).
const (
	contentLength = "content-length"
	// maxMessage caps a single message. gopls's biggest reply on a large
	// package is a completion list of a few hundred kilobytes; 32 MB is far
	// past anything real and is here so that a desynchronised stream, where a
	// Content-Length is read out of the middle of a JSON body, fails with a
	// message instead of allocating until the machine swaps.
	maxMessage = 32 << 20
)

// Message is one JSON-RPC 2.0 frame in either direction.
//
// Request, response and notification are one struct because they are one
// struct on the wire: a message with an ID and a Method is a request, one with
// an ID and no Method is a response, and one with a Method and no ID is a
// notification. Three Go types would mean sniffing the JSON twice, once to
// decide which type to unmarshal into and once to unmarshal.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// ResponseError is the error object a server answers a request with.
type ResponseError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *ResponseError) Error() string {
	return "lsp: " + e.Message + " (" + strconv.Itoa(e.Code) + ")"
}

// ErrClosed is what a call on a connection whose read loop has stopped
// answers. It is a sentinel rather than a string so that cmd/pvim can tell a
// server that died from a server that said no: the first means the editor
// stops offering completion, the second means this one completion failed.
var ErrClosed = errors.New("lsp: connection closed")

// Handler answers a request or a notification the server sent us.
//
// The protocol is bidirectional and a client that ignores the server's half of
// it hangs. gopls sends client/registerCapability, workspace/configuration and
// window/workDoneProgress/create as REQUESTS, each of which it waits for an
// answer to before carrying on, so an editor that only ever reads replies to
// its own calls gets one completion and then silence. See Client.handle in client.go.
//
// A nil result is a null result, which is the right answer to most of them. An
// error becomes a JSON-RPC error object. For a notification both are dropped.
type Handler func(method string, params json.RawMessage) (any, error)

// Conn is one JSON-RPC connection: a reader, a writer and the calls in flight.
//
// It is safe for concurrent use, which it has to be: the coalescer goroutine
// sends didChange while the editor goroutine is blocked in a completion, and
// the read loop is a third. Writes are serialised by one mutex because the
// framing has no way to interleave two messages, and the pending table by
// another so that a slow write cannot hold up a reply being delivered.
type Conn struct {
	w  io.Writer
	rc io.Closer
	r  *bufio.Reader

	// wmu serialises whole messages onto the writer. Header and body have to
	// arrive together or the other end resynchronises on the wrong byte.
	wmu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan Message
	closed  bool
	err     error

	handler Handler

	// done closes when the read loop stops, which is how Close waits for it
	// and how a call in flight learns the server is gone.
	done chan struct{}
}

// NewConn starts a connection over r and w and runs its read loop.
//
// closer is closed by Close before the read loop is waited for; for a child
// process it is the process's stdin, and closing it is what makes a
// well-behaved server exit on its own. Nil is allowed and means there is
// nothing to close, which is what the in-memory pipes in the tests pass.
func NewConn(r io.Reader, w io.Writer, closer io.Closer, h Handler) *Conn {
	c := &Conn{
		w:       w,
		rc:      closer,
		r:       bufio.NewReaderSize(r, 64<<10),
		pending: map[int64]chan Message{},
		handler: h,
		done:    make(chan struct{}),
	}
	go c.read()
	return c
}

// Done is closed when the read loop has stopped, whether because the far end
// went away or because Close was called.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the read loop stopped, or nil while it is running. io.EOF is
// reported as ErrClosed: a server that exited on request and a pipe that broke
// look identical from here and the difference is the caller's to know.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Notify sends a notification, which is a message with no ID and no reply.
func (c *Conn) Notify(method string, params any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	return c.write(Message{JSONRPC: "2.0", Method: method, Params: raw})
}

// Call sends a request and waits for its reply.
//
// result is unmarshalled into when the server answers, and may be nil for a
// call whose result is not read. cancel is a channel that abandons the wait --
// context.Context's Done, in practice -- and abandoning it leaves the pending
// entry behind for the read loop to drop, because a reply that arrives after
// the caller has gone still has to be read off the wire and thrown away.
func (c *Conn) Call(cancel <-chan struct{}, method string, params, result any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.closed {
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = ErrClosed
		}
		return err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan Message, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	drop := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}

	if err := c.write(Message{JSONRPC: "2.0", ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: raw}); err != nil {
		drop()
		return err
	}

	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if result == nil || len(m.Result) == 0 {
			return nil
		}
		return json.Unmarshal(m.Result, result)
	case <-cancel:
		drop()
		// The server is not told. $/cancelRequest exists and gopls honours
		// it, and it is deliberately not sent: the only thing this editor
		// abandons is a completion whose 200 ms budget ran out, the reply is
		// on its way by then, and a cancel notification would cost a write on
		// the hot path to save the server a few microseconds of work it has
		// already done.
		return errCancelled
	case <-c.done:
		drop()
		if err := c.Err(); err != nil {
			return err
		}
		return ErrClosed
	}
}

// errCancelled is a Call whose caller gave up waiting.
var errCancelled = errors.New("lsp: request abandoned")

// Cancelled reports whether err is a request this client stopped waiting for,
// which is not a failure of the server and not something to say on the message
// line.
func Cancelled(err error) bool { return errors.Is(err, errCancelled) }

// Close shuts the connection down: the writer's closer first, so a server
// watching for EOF on its stdin exits, then a wait for the read loop.
func (c *Conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		<-c.done
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	var err error
	if c.rc != nil {
		err = c.rc.Close()
	}
	<-c.done
	return err
}

// write frames one message onto the writer.
func (c *Conn) write(m Message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := io.WriteString(c.w, "Content-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

// read is the read loop: one message at a time until the stream ends.
func (c *Conn) read() {
	defer close(c.done)
	for {
		m, err := readMessage(c.r)
		if err != nil {
			c.fail(err)
			return
		}
		c.dispatch(m)
	}
}

// fail records why the loop stopped and wakes everything waiting on it.
func (c *Conn) fail(err error) {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		err = ErrClosed
	}
	c.mu.Lock()
	c.closed = true
	if c.err == nil {
		c.err = err
	}
	pending := c.pending
	c.pending = map[int64]chan Message{}
	c.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
}

// dispatch routes one incoming message: a reply to a waiting call, or a
// request or notification into the handler.
func (c *Conn) dispatch(m Message) {
	if m.Method == "" {
		// A reply. An ID that is not an integer is not one of ours -- every
		// ID this client sends is a decimal integer -- and is dropped.
		id, err := strconv.ParseInt(strings.TrimSpace(string(m.ID)), 10, 64)
		if err != nil {
			return
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
			close(ch)
		}
		return
	}

	if len(m.ID) == 0 {
		// A notification, handled inline. Not on a goroutine, and that is the
		// point: two publishDiagnostics for one file arrive in order and have
		// to be APPLIED in order, and a goroutine per notification loses that
		// -- the second one wins the race about half the time and the file
		// keeps the errors it no longer has. Nothing this client does in a
		// notification handler blocks, so the read loop is never held up.
		c.serve(m)
		return
	}
	// A request. On a goroutine of its own so that a handler which calls back
	// into the server -- nothing here does, and a workspace/applyEdit handler
	// would -- cannot deadlock the read loop it is waiting on.
	go c.serve(m)
}

// serve runs the handler for one server-sent message and replies if it was a
// request.
func (c *Conn) serve(m Message) {
	var (
		result any
		err    error
	)
	if c.handler != nil {
		result, err = c.handler(m.Method, m.Params)
	}
	if len(m.ID) == 0 {
		return // a notification: nothing to answer
	}
	reply := Message{JSONRPC: "2.0", ID: m.ID}
	if err != nil {
		reply.Error = &ResponseError{Code: -32603, Message: err.Error()}
	} else {
		raw, merr := json.Marshal(result)
		if merr != nil {
			reply.Error = &ResponseError{Code: -32603, Message: merr.Error()}
		} else {
			reply.Result = raw
		}
	}
	_ = c.write(reply)
}

// readMessage reads one framed message.
func readMessage(r *bufio.Reader) (Message, error) {
	n := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return Message{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of the header block
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return Message{}, fmt.Errorf("lsp: bad header line %q", line)
		}
		if strings.ToLower(strings.TrimSpace(name)) != contentLength {
			continue
		}
		n, err = strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return Message{}, fmt.Errorf("lsp: bad Content-Length %q", value)
		}
	}
	if n < 0 {
		return Message{}, errors.New("lsp: message with no Content-Length")
	}
	if n > maxMessage {
		return Message{}, fmt.Errorf("lsp: message of %d bytes is past the %d byte cap", n, maxMessage)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal(body, &m); err != nil {
		return Message{}, fmt.Errorf("lsp: %w", err)
	}
	return m, nil
}

// marshalParams turns a params value into raw JSON, with nil meaning absent.
func marshalParams(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return raw, nil
}
