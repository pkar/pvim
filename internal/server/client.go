package server

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

// The dialling half: the second process, the one that exits.

// dialTimeout bounds the connect. A unix connect to a socket somebody is
// accepting on is immediate; this only bites when the running instance is
// wedged, and then it is better for "pvim file" to say so than to hang in a
// shell with no way to tell what it is waiting for. It is not a bound on the
// exchange: --wait blocks in Next for as long as the tab is open.
const dialTimeout = probeTimeout

// firstReplyDeadline bounds the wait for the FIRST reply, and nothing after it.
//
// dialTimeout does not cover this and cannot: a connect to a socket somebody
// bound and nobody is accepting from does not fail, it waits in the listen
// backlog, so the connect returns in microseconds and the reply never comes.
// Measured with the built binary against a socket in that state -- which is
// what a Serve that returned leaves behind -- "pvim --wait f.txt" printed
// nothing and was still waiting when it was killed at 15s. Under
// EDITOR="pvim --wait" that is git blocked forever with no message, which is
// the one failure this program must not have.
//
// The first reply only. ReplyOpened comes back as soon as the running instance
// has the file, so ten seconds is a peer that is stuck rather than one that is
// slow, and the deadline is cleared before the wait that follows: a --wait
// client sitting on an open tab for an hour is not affected by it at all. It is
// the mirror of requestDeadline at the other end, for the same reason and with
// the same number.
//
// A var rather than a const only so that the test for it can run in
// milliseconds instead of ten seconds. Nothing else writes it.
var firstReplyDeadline = 10 * time.Second

// Conn is a connection to the running instance.
//
// It is a type rather than a bare net.Conn so that the buffered reader lives
// with the connection: replies arrive one line at a time and a reader that was
// created per call would swallow whatever it buffered past the line it read.
type Conn struct {
	c     net.Conn
	r     *bufio.Reader
	path  string
	first bool // the next Next is the first reply, and it is the one with a deadline
}

// Dial connects to the instance listening at path.
//
// It answers ErrNoInstance for every reason there is no instance here to talk
// to, and that is a wider set than "the file is missing": a socket with nobody
// behind it, a regular file or a directory sitting on the path, a socket
// belonging to another uid, a directory the socket rules refuse, a path the
// kernel will not connect to at all. Those are one sentence to the caller --
// this process has to be the instance itself -- and there is nothing else it
// could do with any of them.
//
// That width was bought rather than assumed. Every one of those used to come
// back as a wrapped error, cmd/pvim treats an attach error as fatal, and an
// empty regular file at ~/.cache/vim/pvim.sock therefore stopped "pvim file"
// starting at all, on every launch, forever, because Listen deliberately
// refuses to remove a file it did not create and Dial exits before Listen is
// reached. A stale thing on the path that the editor will not start over is the
// exact outcome this package exists to prevent.
//
// The reason does not vanish with the error. It is wrapped into the
// ErrNoInstance, for a caller that prints it, and the same path is about to be
// refused by Listen in the same process, which puts its own sentence on the
// message line where a person reads it in an editor that started.
//
// Two things stay hard errors, because they are about this call rather than
// about the path. A symlink at the socket's path is one: nothing on this
// machine makes one there by accident, so it is a plant, and a plant should be
// named rather than quietly worked around. A connect that TIMES OUT is the
// other: that is an instance that is up and not answering, and starting a
// second editor beside a live one is worse than saying so.
func Dial(path string) (*Conn, error) {
	if err := checkDir(path); err != nil {
		return nil, noInstance(path, err)
	}
	if err := checkSocketFile(path); err != nil {
		if errors.Is(err, errSymlinkAtSocket) {
			return nil, fmt.Errorf("server: cannot dial %s: %w", path, err)
		}
		return nil, noInstance(path, err)
	}
	c, err := net.DialTimeout("unix", path, dialTimeout)
	if err != nil {
		if os.IsTimeout(err) {
			return nil, fmt.Errorf("server: %s did not answer a connect within %v; an instance is bound there and is not accepting", path, dialTimeout)
		}
		// Everything else the kernel can say here means the same thing.
		// ECONNREFUSED is the stale socket a crash left behind, and it is the
		// common case rather than an edge one: it is what every launch after a
		// SIGKILL sees. ENOENT is the file having gone away between the check
		// above and here, and ENOTSOCK is something that is not a socket having
		// taken its place in the same window. EINVAL is the kernel refusing the
		// address itself, which for a unix socket means a path longer than
		// sun_path -- 104 bytes on darwin, 108 on linux -- and a $HOME deep
		// enough to do that has no instance at that path and never will.
		return nil, noInstance(path, err)
	}
	return &Conn{c: c, r: NewReader(c), path: path, first: true}, nil
}

// noInstance says there is nobody to talk to and why.
//
// The wrap matters: cmd/pvim asks errors.Is(err, ErrNoInstance) and starts an
// editor, which is unaffected, and anything that prints the error still gets
// the reason instead of four words that explain nothing.
func noInstance(path string, why error) error {
	return fmt.Errorf("%w at %s: %w", ErrNoInstance, path, why)
}

// Send writes one request. A connection carries exactly one.
func (c *Conn) Send(req Request) error { return WriteRequest(c.c, req) }

// Next reads the next reply, blocking until it arrives. For a waiting client
// that is where the process spends its life.
//
// The first reply is the exception and carries firstReplyDeadline; see there.
func (c *Conn) Next() (Reply, error) {
	if c.first {
		c.first = false
		c.c.SetReadDeadline(time.Now().Add(firstReplyDeadline))
		defer c.c.SetReadDeadline(time.Time{})
	}
	rep, err := ReadReply(c.r)
	if err != nil && os.IsTimeout(err) {
		return rep, fmt.Errorf("server: the instance at %s took the connection and did not answer within %v", c.path, firstReplyDeadline)
	}
	return rep, err
}

// Close hangs up. A running instance sees the hangup and stops waiting on the
// buffer, which is what makes killing the client of a --wait not leave a
// goroutine in the editor forever.
func (c *Conn) Close() error {
	if c == nil || c.c == nil {
		return nil
	}
	return c.c.Close()
}

// Open is the whole of what "pvim file" does when an instance is already up:
// dial, send, read replies until the exchange is over, exit.
//
// It blocks until the running instance says the buffer closed when req.Wait is
// set, and returns as soon as the file is open when it is not. A returned
// ErrNoInstance means there is nobody to talk to and the caller should start up
// itself; ErrRefused means the instance said no and the reason is in the
// wrapped message.
//
// This is what EDITOR="pvim --wait" reduces to. git spawns the client, the
// client blocks in here, the tab opens in the window that was already on the
// screen, and the commit lands when the tab closes.
func Open(path string, req Request) error {
	if req.Op == "" {
		req.Op = OpOpen
	}
	if !filepath.IsAbs(req.Path) {
		return fmt.Errorf("server: %q is not an absolute path; resolve it before sending it", req.Path)
	}
	c, err := Dial(path)
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(req); err != nil {
		return fmt.Errorf("server: sending the request: %w", err)
	}
	for {
		rep, err := c.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// The instance went away mid-exchange. For a --wait client this
				// is the editor being killed while git holds the commit
				// message, and exiting 0 here would tell git the message was
				// saved on purpose.
				return errors.New("server: the running instance closed the connection before the buffer did")
			}
			return err
		}
		switch {
		case rep.Kind == ReplyError:
			return fmt.Errorf("%w: %s", ErrRefused, rep.Error)
		case rep.Kind == ReplyOpened && !req.Wait:
			return nil
		case rep.Final():
			return nil
		}
	}
}
