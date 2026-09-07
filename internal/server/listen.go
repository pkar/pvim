package server

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// The listening half: the running instance.

// socketMode is what the socket is chmodded to the moment after it is bound.
// User only. See perm.go for why this is a belt and the directory is the
// braces.
const socketMode os.FileMode = 0o600

// probeTimeout is how long a dial at a socket that may be a corpse is given to
// answer before it is called alive.
//
// A connect to a unix socket somebody has bound but is not accepting on does
// not fail, it waits in the listen backlog, so a timeout here means "somebody
// bound this and is too busy to accept", which is a live instance under load
// and not a corpse. Erring that way is deliberate: calling a live socket dead
// unlinks it and leaves the running editor listening on a path nothing can
// reach, and the cost of the other mistake is one error message telling a
// person to try again.
const probeTimeout = 250 * time.Millisecond

// probeAttempts and probeRetryDelay are the retry behind that "does not fail",
// which is only true while the backlog has room.
//
// Measured rather than assumed, because the answer decides whether the
// paragraph above is a description or a wish: on this machine, macOS 26 with
// kern.ipc.somaxconn at 128, a Go net.Listener on a unix socket that nobody
// accepts from takes 128 connections and answers the 129th with ECONNREFUSED,
// not EAGAIN and not a timeout. So a full backlog says exactly what a corpse
// says, and one dial cannot tell them apart: it would unlink the socket of a
// running editor and leave it listening where nothing could reach it.
// TestAFullBacklogIsNotACorpse takes the measurement again on whatever box it
// runs on, so a kernel that answers differently fails there rather than here.
//
// Three dials 10ms apart instead of one. It does not make the two cases
// distinguishable, because a listener that stays 128 deep for 20ms still reads
// as dead; what it does is require the backlog to be full for the whole 20ms
// rather than for the microsecond the probe landed in, and Serve's accept loop
// drains a burst in far less than that. Reaching it at all needs 129 shells
// running "pvim file" inside one turn of that loop, which is why this is a
// cheap retry and not a lock file.
//
// That last sentence was too comfortable and the correction is worth having in
// writing, because it is what decides whether this is an edge case or a matter
// of time. It holds only while something is accepting. Measured on this box:
// a queued connection whose client went away does NOT give its backlog slot
// back, so on a listener nobody is accepting from the 128 are cumulative rather
// than concurrent -- 129 connects ever, not 129 at once -- and once they have
// been made the socket reads as a corpse permanently. So the estimate is a
// statement about the accept loop being up, which is why Serve retries an
// accept that failed for a reason that will pass, and why the shape of that
// retry belongs to this paragraph as much as to Serve.
//
// The other half of getting it wrong is contained rather than prevented: an
// instance whose socket is taken over is unreachable, but Close no longer
// unlinks by name, so the instance that displaced it keeps its socket when the
// loser quits and the shell after that reaches the editor that is up.
//
// The cost lands on the corpse, which is the common case: every launch after a
// crash pays two extra refused connects and 20ms before it takes the socket
// over. A refused connect on a unix socket is a syscall that does not leave the
// kernel, so the 20ms is the sleeps and nothing else.
const (
	probeAttempts   = 3
	probeRetryDelay = 10 * time.Millisecond
)

// takeoverAttempts bounds the loop in Listen.
//
// Each turn is one bind, and a turn is only taken again when the file at the
// path changed under us, which is another process winning the same race. Three
// is not tuned, it is "more than the one retry the algorithm needs, few enough
// that a path being rewritten in a loop by something else reports ErrInUse in
// milliseconds instead of spinning".
const takeoverAttempts = 3

// hookAfterProbe runs the instant the probe has an answer and before anything
// is done about it. It is nil in every build and set only by the three tests
// that reproduce the races this loop exists for, which all live in the same
// window: the file at the path changing under us between the probe and what the
// probe's answer told us to do. There is no other way to hit that window
// deterministically, and a race that is only tested by running the thing a
// thousand times is not tested.
//
// live is what the probe answered, because the two halves of the window are
// different bugs. With live false the danger is unlinking a socket somebody has
// just bound; with live true it is reporting ErrInUse about an instance that
// died a microsecond later.
var hookAfterProbe func(path string, live bool)

// hookProbeRetry runs between two refused probes, and exists for the same
// reason: the case the retry is there for is a backlog that drains in that
// window, and a test that waited 10ms and hoped would be a test that fails on a
// loaded box. Nil in every build.
var hookProbeRetry func(attempt int)

// Listener is the running instance's socket.
type Listener struct {
	ln   net.Listener
	path string

	// bound is the socket this listener created, kept so that Close can unlink
	// by identity rather than by name. See Close.
	bound os.FileInfo
}

// Listen takes the socket at path, or reports that another instance has it.
//
// # Stale sockets, and why this is not just net.Listen
//
// A unix socket is a file and it outlives the process that bound it. A pvim
// killed with SIGKILL, a laptop that lost power, a crash: all three leave
// ~/.cache/vim/pvim.sock on disk with nothing behind it, and net.Listen on a
// path that exists fails with EADDRINUSE whether or not anybody is home. An
// editor that refuses to start after one crash until somebody deletes a file
// they have never heard of is not an editor anybody keeps using.
//
// So the algorithm, and it is the classic one:
//
// 1. net.Listen("unix", path). If it succeeds, this process is the instance.
// 2. If it fails and the path exists, dial it. A dial that CONNECTS means a
// live process is listening: return ErrInUse and do not touch the file. A
// dial refused (ECONNREFUSED) every time, over the few attempts
// probeAttempts describes, means the file is a corpse.
// 3. Unlink the corpse and go back to 1.
//
// Step 3 is where the race lives and it is a real one: two shells running
// "pvim file" at the same second after a crash both find the stale socket, both
// unlink, and both listen. One of them unlinks the other's fresh socket and
// then both are listening, one of them on a path nothing can reach.
//
// What closes it here is that the unlink is guarded by identity rather than by
// name. The file is stat'd before the probe and again after it, and it is only
// removed when os.SameFile says it is the same inode both times; a different
// inode means somebody rebound the path while we were dialling, so the loop
// starts again and the next bind's failure sends us to the probe, which now
// finds a live socket and answers ErrInUse. The window that is left is between
// that second stat and the unlink itself, which is one syscall wide rather than
// a dial's round trip wide, and closing it entirely needs a lock file taken
// with O_EXCL beside the socket, which is a second piece of state that can also
// go stale. vim's own --remote has the same race with worse consequences.
//
// A Listener must be Closed, which unlinks the socket: Go's net package does
// that for a unix listener it created.
func Listen(path string) (*Listener, error) {
	if err := checkDir(path); err != nil {
		return nil, fmt.Errorf("server: cannot listen on %s: %w", path, err)
	}
	for attempt := 0; attempt < takeoverAttempts; attempt++ {
		ln, err := net.Listen("unix", path)
		if err == nil {
			// Between bind and here the socket carries whatever the umask
			// allowed. The directory check is what makes that window safe; see
			// perm.go.
			if err := os.Chmod(path, socketMode); err != nil {
				ln.Close()
				return nil, fmt.Errorf("server: cannot set the mode of %s: %w", path, err)
			}
			l := &Listener{ln: ln, path: path}
			// The inode this process bound, for Close. Taking Go's unlink away
			// is only safe once we have something to put in its place, so the
			// two lines go together and a stat that failed leaves both undone:
			// unlinking by name is wrong in one rare case and not unlinking at
			// all is wrong on every ordinary quit.
			if fi, err := os.Lstat(path); err == nil {
				if ul, ok := ln.(*net.UnixListener); ok {
					ul.SetUnlinkOnClose(false)
					l.bound = fi
				}
			}
			return l, nil
		}
		// The bind failed, and EADDRINUSE is the only answer this loop can do
		// anything about: it is the kernel saying there is a file in the way,
		// which may be a corpse. Anything else is this bind's own problem --
		// a path longer than sun_path, a directory gone read-only, a full disk
		// -- and reporting it as ErrInUse would be a lie twice over. It says
		// "another instance is listening" when nothing is, and cmd/pvim prints
		// nothing at all for ErrInUse because a second shell finding the first
		// one is the ordinary case, so the reason was not merely wrong, it was
		// silent: on a home directory that cannot hold a unix socket, single
		// instance never worked and nothing anywhere said why. Measured on a
		// 146-byte path: "another instance is already listening", with nothing
		// at the path at all.
		if !errors.Is(err, syscall.EADDRINUSE) {
			return nil, fmt.Errorf("server: cannot listen on %s: %w", path, err)
		}
		before, statErr := os.Lstat(path)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue // it went away under us; try the bind again
			}
			// The bind says the path is taken and the stat cannot see it. That
			// is the stat's failure and not the bind's, so it is the one to
			// report.
			return nil, fmt.Errorf("server: cannot listen on %s: %w", path, statErr)
		}
		// Not a socket: something else is sitting on the path. Unlinking it
		// would be this package deleting a file it did not create, in a
		// directory where the editor also keeps undo history, and no
		// arrangement of the socket is worth that.
		if before.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("server: %s is not a socket (mode %v), refusing to remove it", path, before.Mode())
		}
		live, err := alive(path)
		if err != nil {
			return nil, err
		}
		if hookAfterProbe != nil {
			hookAfterProbe(path, live)
		}
		if live {
			// The other half of the window, answered honestly rather than
			// cleverly. The instance that just answered the probe can be dead
			// by the time this line runs, and there is no way to find that out
			// that does not have the same race inside it: re-probing asks the
			// same question again, and a stat cannot tell a live socket from
			// the corpse of the same inode.
			//
			// So an instance that dies in this window costs one "another
			// instance is already listening" and nothing else. The launch after
			// this one finds either an empty path, because the instance's own
			// Close unlinked the socket, or the corpse a SIGKILL left, which is
			// the takeover above. Erring this way is the same choice alive()
			// makes: calling a dead instance live costs a message, and the
			// opposite unlinks a socket a running editor is still listening on.
			return nil, fmt.Errorf("%w at %s", ErrInUse, path)
		}
		after, statErr := os.Lstat(path)
		if statErr != nil || !os.SameFile(before, after) {
			continue // somebody rebound it while we were dialling
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("server: cannot remove the stale socket at %s: %w", path, err)
		}
	}
	return nil, fmt.Errorf("%w at %s", ErrInUse, path)
}

// alive reports whether something is accepting on the socket at path.
//
// The three answers that are not "yes" are worth separating. ECONNREFUSED is
// the corpse this whole dance is about -- the file is there, the inode has no
// listener -- and it is the one answer that is retried, because a full backlog
// says the same thing. ENOENT is the file having gone away between the stat and
// the dial, which is also not a live instance. Anything else -- EACCES on a
// socket in a directory this user owns, most obviously -- is unexpected enough
// that unlinking it would be the wrong guess, so it comes back as an error with
// the syscall in it.
func alive(path string) (bool, error) {
	for attempt := 0; ; attempt++ {
		c, err := net.DialTimeout("unix", path, probeTimeout)
		if err == nil {
			c.Close()
			return true, nil
		}
		switch {
		case errors.Is(err, syscall.ECONNREFUSED):
			// The corpse, or a listener whose backlog is momentarily full. See
			// probeAttempts: the two look identical from here and only the
			// corpse keeps saying so.
			if attempt+1 == probeAttempts {
				return false, nil
			}
		case os.IsNotExist(err):
			return false, nil
		case os.IsTimeout(err):
			return true, nil
		default:
			return false, fmt.Errorf("server: cannot tell whether %s is live: %w", path, err)
		}
		if hookProbeRetry != nil {
			hookProbeRetry(attempt)
		}
		time.Sleep(probeRetryDelay)
	}
}

// Path is where the listener is bound, which a startup message prints.
func (l *Listener) Path() string { return l.path }

// Close stops listening and unlinks the socket -- if the socket on disk is
// still the one this listener bound.
//
// Go's own unlink is by name, and the name is the wrong question. After a
// takeover the path holds somebody else's inode: the instance that lost the
// path would delete the socket of the instance that has it, so a live editor
// stops being reachable the moment the one that displaced it quits, and the
// next "pvim file" finds nothing and starts a third editor. That is a worse
// outcome than the takeover itself and it has no guard on it at all, where the
// takeover at least has a probe.
//
// The identity check is the same one Listen makes and it leaves the same
// window: something could rebind the path between the Lstat and the Remove.
// That window is one syscall wide rather than a round trip wide, and the cost
// of losing it is a socket file left on disk, which the next launch takes over
// as the corpse it is.
func (l *Listener) Close() error {
	if l == nil || l.ln == nil {
		return nil
	}
	err := l.ln.Close()
	if l.bound != nil {
		if now, statErr := os.Lstat(l.path); statErr == nil && os.SameFile(l.bound, now) {
			os.Remove(l.path)
		}
	}
	return err
}

// Handler is the running editor, seen from the socket: one request in, and
// either an error or a channel that closes when the buffer the request opened
// is closed again.
//
// It runs on a goroutine of Serve's making and NOT on the editor's, which is
// the rule that keeps this package out of the editor's state: an implementation
// hands the request to the editor goroutine over a channel and returns. The
// editor is the only thing that touches a buffer, here as everywhere.
//
// The returned channel is only read when the request asked to wait. A nil
// channel is a buffer that is already gone, so a waiting client is answered
// immediately rather than blocking forever, which is what a request to open a
// file the editor closed again on the spot should do.
type Handler func(Request) (closed <-chan struct{}, err error)

// requestDeadline is how long a connection has to send its one request.
//
// A client that connects and says nothing would otherwise hold a goroutine for
// the life of the editor, and the only thing that ever connects here sends its
// line in the same breath. It is deliberately generous: this is a bound on a
// stuck peer, not a latency budget. The deadline is cleared before the wait, so
// a --wait client sitting idle for an hour is not affected by it at all.
//
// A var rather than a const only so that the test for it can run in
// milliseconds instead of ten seconds. Nothing else writes it.
var requestDeadline = 10 * time.Second

// Serve accepts connections until the listener is closed, handing each request
// to h.
//
// One connection is one request and however many replies that request calls
// for, and connections are served concurrently: a client that asked to wait
// holds its connection open for as long as its tab is open, which may be an
// hour, and the shell in the next pane must not be behind it in a queue.
//
// Serve returns nil when the listener is closed, which is how the editor shuts
// the socket down on its way out. An accept that failed because the machine is
// out of something is retried; anything else is returned.
//
// The retry is the shape http.Server has and it is here for the same reason,
// measured: with the file descriptor limit lowered under a dialler, Accept
// answered EMFILE and Serve returned. The editor kept running and the socket
// stayed bound, cmd/pvim discards this error, and from then on every client
// connected into a backlog nobody was accepting from -- which is not a connect
// failure, so it hung rather than falling back to its own editor. One transient
// errno retired the socket for the life of the editor, silently. Go's poll
// layer already retries EINTR, EAGAIN and ECONNABORTED; what reaches here is
// the resource pressure, and resource pressure passes.
func (l *Listener) Serve(h Handler) error {
	delay := acceptRetryMin
	for {
		c, err := l.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			if !retryableAccept(err) {
				return fmt.Errorf("server: accept on %s: %w", l.path, err)
			}
			time.Sleep(delay)
			if delay *= 2; delay > acceptRetryMax {
				delay = acceptRetryMax
			}
			continue
		}
		delay = acceptRetryMin
		go serve(c, h)
	}
}

// The backoff between a failed accept and the next one, doubling from the first
// to the second. The numbers are http.Server's. The small one is there so that
// a burst of one does not cost a client anything a person could notice, and the
// big one so that a machine that stays out of descriptors is retried at the
// rate of a heartbeat rather than in a spin.
const (
	acceptRetryMin = 5 * time.Millisecond
	acceptRetryMax = time.Second
)

// retryableAccept reports whether an accept failure is about the machine rather
// than about the socket.
//
// Named errnos and not net.Error's Temporary, which is deprecated and answers
// true for things that are not: the question here is narrow and the list is
// short enough to read.
func retryableAccept(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case syscall.EMFILE, syscall.ENFILE, syscall.ENOBUFS, syscall.ENOMEM,
		syscall.ECONNABORTED, syscall.EINTR, syscall.EAGAIN:
		return true
	}
	return false
}

// serve runs one connection to its end.
//
// Every path out of here writes a reply or is a peer that has already gone,
// because the client is a process blocking on this connection and a server that
// closes without a word turns into "pvim: unexpected EOF" in a git commit.
func serve(c net.Conn, h Handler) {
	defer c.Close()

	r := NewReader(c)
	c.SetReadDeadline(time.Now().Add(requestDeadline))
	req, err := ReadRequest(r)
	if err != nil {
		// A version mismatch lands here, and it is the reason this branch
		// answers instead of hanging up: the reply carries this build's
		// version, so the client's own ReadReply fails with ErrVersion and both
		// ends name both numbers. The text goes along for the person reading it.
		WriteReply(c, Reply{Kind: ReplyError, Error: err.Error()})
		return
	}
	c.SetReadDeadline(time.Time{})

	if err := checkRequest(req); err != nil {
		WriteReply(c, Reply{Kind: ReplyError, Error: err.Error()})
		return
	}

	closed, err := h(req)
	if err != nil {
		WriteReply(c, Reply{Kind: ReplyError, Error: err.Error()})
		return
	}
	if err := WriteReply(c, Reply{Kind: ReplyOpened}); err != nil {
		return
	}
	if !req.Wait {
		return
	}

	// A nil channel is a buffer that is already gone. Answering it immediately
	// is not a special case for tests: an editor that refuses the file and
	// closes the tab in the same breath would otherwise leave git blocked
	// forever on a tab that is not on the screen.
	if closed == nil {
		WriteReply(c, Reply{Kind: ReplyClosed})
		return
	}
	select {
	case <-closed:
		WriteReply(c, Reply{Kind: ReplyClosed})
	case <-hangup(r):
		// The client was killed, or the shell it was in went away. Nothing to
		// reply to. The editor is not told: the tab is the person's now, and
		// closing their file because a terminal window shut is worse than
		// leaving it open.
	}
}

// hangup returns a channel that closes when the peer does.
//
// It reads, because there is no other way to see a closed unix socket: the read
// is expected to block forever and return EOF exactly once, since the protocol
// says a connection carries one request and nothing more. The goroutine ends
// either at that EOF or when serve's deferred Close makes the read fail, so it
// does not outlive the connection.
func hangup(r *bufio.Reader) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		r.ReadByte()
	}()
	return ch
}

// checkRequest is what the running instance insists on before it hands a
// request to the editor.
//
// The path is absolute because the client resolved it against its own working
// directory: the running instance has 'autochdir' on, so its cwd is wherever
// its current buffer lives, and resolving "main.go" here would open a file in
// whatever directory the person happened to be looking at.
func checkRequest(req Request) error {
	if req.Op != OpOpen {
		return fmt.Errorf("server: unknown op %q", req.Op)
	}
	if req.Path == "" {
		return errors.New("server: the request has no path")
	}
	if !filepath.IsAbs(req.Path) {
		return fmt.Errorf("server: path %q is not absolute; the client resolves it, not the instance", req.Path)
	}
	return nil
}
