package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// The wire: one JSON object per line, in both directions.
//
// This file is the whole format and it is implemented rather than stubbed,
// because the format is the contract five other pieces of work are written
// against and a stub would let two of them disagree about it silently. What is
// stubbed is the socket either side of it.

// Op is what a request asks for. There is one; the type exists so that a
// second one is an added constant and not a bool that means something else.
type Op string

// OpOpen asks the running instance to open a file in a new tab.
const OpOpen Op = "open"

// Request is one line from the second process to the running instance.
//
// Path is absolute and Cwd is absolute, both resolved by the client before it
// sends: the running instance has 'autochdir' on, so its own working directory
// is wherever its current buffer lives and a relative path resolved there would
// open the wrong file, or none.
type Request struct {
	Version int    `json:"version"`
	Op      Op     `json:"op"`
	Path    string `json:"path"`
	Cwd     string `json:"cwd"`
	// Wait asks the running instance to hold the connection open until the
	// buffer it opened is closed again. It is --wait, and it is what makes
	// EDITOR="pvim --wait" work: git blocks on the client process, the client
	// blocks on this reply, and the commit lands when the tab closes.
	Wait bool `json:"wait"`
}

// Kind is what a reply says happened.
type Kind string

// The reply kinds. A client that did not ask to wait sees exactly one of these
// and closes; a client that did sees ReplyOpened and then, later, ReplyClosed.
const (
	// ReplyOpened means the file is open in the running instance. For a client
	// that is not waiting this is the end of the exchange.
	ReplyOpened Kind = "opened"
	// ReplyClosed means the buffer the request opened has been closed. Only a
	// waiting client ever sees it, and seeing it is what makes it exit 0.
	ReplyClosed Kind = "closed"
	// ReplyError means the request was refused, with the reason in Error.
	ReplyError Kind = "error"
)

// Reply is one line from the running instance back to the second process.
type Reply struct {
	Version int    `json:"version"`
	Kind    Kind   `json:"kind"`
	Error   string `json:"error,omitempty"`
}

// Final reports whether this reply ends the exchange, which is every kind but
// ReplyOpened to a client that asked to wait. The client decides that with its
// own Wait flag; this is the half that does not depend on it.
func (r Reply) Final() bool { return r.Kind != ReplyOpened }

// WriteRequest writes one request and the newline that terminates it.
func WriteRequest(w io.Writer, r Request) error {
	r.Version = Version
	return writeLine(w, r)
}

// WriteReply writes one reply and the newline that terminates it.
func WriteReply(w io.Writer, r Reply) error {
	r.Version = Version
	return writeLine(w, r)
}

// writeLine marshals v and writes it followed by a newline, in one Write.
//
// One Write and not two, because two writes on a unix socket are two packets a
// reader can be woken for separately, and a reader that gets a JSON object with
// no newline yet blocks holding a line the sender considers sent. Not a
// correctness bug with a line reader on the other side, and still the kind of
// thing that turns into a mysterious pause under load.
func writeLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// ReadRequest reads one request line.
//
// It takes a *bufio.Reader rather than an io.Reader because the caller reads
// more than one line from the same connection and a reader that buffered the
// second line and then went away would lose it. The caller keeps the buffered
// reader for the life of the connection.
func ReadRequest(r *bufio.Reader) (Request, error) {
	var req Request
	line, err := readLine(r)
	if err != nil {
		return req, err
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return req, fmt.Errorf("server: bad request line: %w", err)
	}
	if req.Version != Version {
		return req, fmt.Errorf("%w: peer speaks %d, this build speaks %d", ErrVersion, req.Version, Version)
	}
	return req, nil
}

// ReadReply reads one reply line.
func ReadReply(r *bufio.Reader) (Reply, error) {
	var rep Reply
	line, err := readLine(r)
	if err != nil {
		return rep, err
	}
	if err := json.Unmarshal(line, &rep); err != nil {
		return rep, fmt.Errorf("server: bad reply line: %w", err)
	}
	if rep.Version != Version {
		return rep, fmt.Errorf("%w: peer speaks %d, this build speaks %d", ErrVersion, rep.Version, Version)
	}
	return rep, nil
}

// NewReader wraps a connection in the buffered reader both ends read their
// lines from.
//
// The size is not a detail: bufio.Reader.ReadSlice cannot return a line longer
// than its buffer, so a reader made with bufio.NewReader would refuse a
// perfectly legal request the moment somebody opened a file with a long enough
// path. Sizing it at maxLine makes the refusal mean what it says.
func NewReader(r io.Reader) *bufio.Reader { return bufio.NewReaderSize(r, maxLine) }

// maxLine is what a line is allowed to be. A request is a path, a working
// directory and two small fields; anything larger is a peer that is not this
// editor, and reading it into memory unbounded is the one way a socket in the
// home directory could be turned into a denial of service.
const maxLine = 64 << 10

// readLine reads up to and including the next newline, refusing a line that
// runs past maxLine.
//
// io.EOF passes through unwrapped: a client closing after its last reply is the
// normal end of an exchange and the caller tests for it with errors.Is.
func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, fmt.Errorf("server: line longer than %d bytes", maxLine)
	}
	return line, err
}
