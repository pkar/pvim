package server

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Everything here runs against a real unix socket in a real directory, because
// the two things this package has to get right -- a stale socket taken over and
// a live one refused -- are both about a file on disk and neither of them
// exists in a fake.

// socketDir is a directory to put a socket in, short enough for the kernel.
//
// A unix socket path is limited to 104 bytes on macOS, NUL included, and
// t.TempDir() under $TMPDIR is already 88 of them before the test's own name is
// added: /var/folders/ys/rcjnkysd00d4g1z77bj2pqz80000gn/T/TestName123/001. A
// test that goes over the limit fails at bind with "invalid argument", which
// reads like a permissions problem and is not one, so this measures and falls
// back to a short directory under /tmp instead. /tmp itself is 1777 and would
// be refused by checkDir; the directory made inside it is 0700 and is not.
func socketDir(t *testing.T) string {
	t.Helper()
	if dir := t.TempDir(); len(SocketPath(dir)) < 100 {
		return dir
	}
	dir, err := os.MkdirTemp("/tmp", "pvim")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// instance is a running editor as this package sees one: a listener, a
// goroutine serving it, and a handler that records what it was asked to open
// and hands back a channel the test closes when it decides the buffer is gone.
type instance struct {
	path   string
	reqs   chan Request
	closed chan struct{}
	ln     *Listener
	served chan error
}

func newInstance(t *testing.T) *instance {
	t.Helper()
	return newInstanceAt(t, SocketPath(socketDir(t)))
}

func newInstanceAt(t *testing.T, path string) *instance {
	t.Helper()
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen(%s): %v", path, err)
	}
	i := &instance{
		path:   path,
		reqs:   make(chan Request, 8),
		closed: make(chan struct{}),
		ln:     ln,
		served: make(chan error, 1),
	}
	go func() { i.served <- ln.Serve(i.handle) }()
	t.Cleanup(func() {
		ln.Close()
		select {
		case err := <-i.served:
			if err != nil {
				t.Errorf("Serve returned %v, want nil after Close", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after the listener was closed")
		}
	})
	return i
}

// handle is the editor's side of Handler: take the request, hand back the
// channel that says when the buffer went away. The send is buffered so that a
// test which never reads reqs does not wedge the server.
func (i *instance) handle(req Request) (<-chan struct{}, error) {
	select {
	case i.reqs <- req:
	default:
	}
	return i.closed, nil
}

// took blocks for the request the instance was asked to open.
func (i *instance) took(t *testing.T) Request {
	t.Helper()
	select {
	case req := <-i.reqs:
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("the running instance was never asked to open anything")
		return Request{}
	}
}

// TestListenTakesTheSocket is the ordinary startup: nothing there, bind it,
// and leave it user-only.
func TestListenTakesTheSocket(t *testing.T) {
	i := newInstance(t)
	if i.ln.Path() != i.path {
		t.Errorf("Path() is %q, want %q", i.ln.Path(), i.path)
	}
	fi, err := os.Lstat(i.path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 {
		t.Errorf("%s is %v, want a socket", i.path, fi.Mode())
	}
	if perm := fi.Mode().Perm(); perm != socketMode {
		t.Errorf("socket mode is %04o, want %04o: anybody who can reach the directory can talk to it", perm, socketMode)
	}
}

// TestCloseUnlinks, because a socket left behind is the next launch's stale
// socket and the whole of the dance below.
func TestCloseUnlinks(t *testing.T) {
	path := SocketPath(socketDir(t))
	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("after Close, Lstat says %v, want the file to be gone", err)
	}
}

// TestLiveSocketIsRefused. Two editors must never both be listening, and the
// second one has to be told which of the two it is.
func TestLiveSocketIsRefused(t *testing.T) {
	i := newInstance(t)
	second, err := Listen(i.path)
	if !errors.Is(err, ErrInUse) {
		second.Close()
		t.Fatalf("second Listen returned %v, want ErrInUse", err)
	}
	if !strings.Contains(err.Error(), i.path) {
		t.Errorf("error %q does not name the socket", err)
	}
	// And the live one is untouched: the refusal must not have unlinked it.
	if _, err := os.Lstat(i.path); err != nil {
		t.Fatalf("the live socket is gone after the refusal: %v", err)
	}
	if err := Open(i.path, Request{Path: "/tmp/after-refusal.txt", Cwd: "/tmp"}); err != nil {
		t.Fatalf("the live instance stopped answering after a refused Listen: %v", err)
	}
}

// stale leaves a socket file at path with nothing behind it, which is what a
// SIGKILL or a lost power supply leaves in ~/.cache/vim.
func stale(t *testing.T, path string) {
	t.Helper()
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("the corpse is not on disk: %v", err)
	}
}

// TestStaleSocketIsTakenOver is the case an editor is unusable without: the
// last run was killed, the file is still there, and this run has to start
// anyway.
func TestStaleSocketIsTakenOver(t *testing.T) {
	path := SocketPath(socketDir(t))
	stale(t, path)
	i := newInstanceAt(t, path)
	if err := Open(i.path, Request{Path: "/tmp/f.txt", Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open after the takeover: %v", err)
	}
	if got := i.took(t).Path; got != "/tmp/f.txt" {
		t.Errorf("opened %q", got)
	}
}

// TestSomethingElseOnThePathIsNotUnlinked. The stale takeover is a licence to
// delete one specific thing, a socket file with nobody behind it, and it stops
// there: ~/.cache/vim also holds undo history, and a bug here would be the
// editor eating a file it did not create.
func TestSomethingElseOnThePathIsNotUnlinked(t *testing.T) {
	path := SocketPath(socketDir(t))
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(path)
	if err == nil {
		ln.Close()
		t.Fatal("Listen took over a path that was not a socket")
	}
	if !strings.Contains(err.Error(), "not a socket") {
		t.Errorf("Listen said %q, which does not say why", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the file was removed anyway: %v", err)
	}
}

// TestHolderRebindsBetweenTheDialAndTheUnlink is the race in Listen's doc
// comment, driven rather than hoped for: the socket is a corpse when we probe
// it and a live one by the time we would have unlinked it. Unlinking there
// leaves the winner listening on a path nothing can reach, so the answer has to
// be ErrInUse and the winner's socket has to still be on disk and still
// answering.
func TestHolderRebindsBetweenTheDialAndTheUnlink(t *testing.T) {
	path := SocketPath(socketDir(t))
	stale(t, path)

	var once sync.Once
	var winner *instance
	hookAfterProbe = func(p string, live bool) {
		if live {
			// A later turn of the loop probing the socket this hook bound
			// itself. Only the first answer, the corpse, is this test's.
			return
		}
		once.Do(func() {
			if err := os.Remove(p); err != nil {
				t.Error(err)
				return
			}
			winner = newInstanceAt(t, p)
		})
	}
	t.Cleanup(func() { hookAfterProbe = nil })

	ln, err := Listen(path)
	if !errors.Is(err, ErrInUse) {
		ln.Close()
		t.Fatalf("Listen returned %v, want ErrInUse: it unlinked a socket that had just been bound", err)
	}
	if winner == nil {
		t.Fatal("the hook never ran, so this test proved nothing")
	}
	if err := Open(path, Request{Path: "/tmp/winner.txt", Cwd: "/tmp"}); err != nil {
		t.Fatalf("the winner's socket is unreachable: %v", err)
	}
	if got := winner.took(t).Path; got != "/tmp/winner.txt" {
		t.Errorf("the winner was asked for %q", got)
	}
}

// TestHolderVanishesBetweenTheDialAndTheUnlink is the same window with the
// other outcome: the corpse was cleaned up by somebody else while we were
// dialling it, so there is nothing to unlink and the bind should just work.
func TestHolderVanishesBetweenTheDialAndTheUnlink(t *testing.T) {
	path := SocketPath(socketDir(t))
	stale(t, path)

	var once sync.Once
	hookAfterProbe = func(p string, live bool) {
		if live {
			// A later turn of the loop probing the socket this hook bound
			// itself. Only the first answer, the corpse, is this test's.
			return
		}
		once.Do(func() {
			if err := os.Remove(p); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(func() { hookAfterProbe = nil })

	i := newInstanceAt(t, path)
	if err := Open(i.path, Request{Path: "/tmp/f.txt", Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open after the takeover: %v", err)
	}
}

// TestAFullBacklogIsNotACorpse. The probe asks the kernel to connect, and a
// listener whose accept queue is full answers that with ECONNREFUSED, which is
// the same word a socket with nobody behind it says. Measured on this machine:
// a Go net.Listener on a unix socket nobody accepts from takes 128 connections
// and refuses the 129th. Believing that one refusal would unlink the socket of
// a running editor and leave it listening where nothing can reach it.
//
// What the retry buys is the burst draining, which is the only version of this
// that can happen to an editor: Serve accepts in a loop and 129 shells would
// have to run "pvim file" inside one turn of it. So that is what this drives --
// the queue is full when the first probe lands and has room by the second.
func TestAFullBacklogIsNotACorpse(t *testing.T) {
	path := SocketPath(socketDir(t))
	// A running instance that has not reached Accept yet, which is what being
	// busy looks like from out here.
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := os.Chmod(path, socketMode); err != nil {
		t.Fatal(err)
	}

	var held []net.Conn
	t.Cleanup(func() {
		for _, c := range held {
			c.Close()
		}
	})
	for len(held) < 1000 {
		c, err := net.DialTimeout("unix", path, probeTimeout)
		if err != nil {
			if !errors.Is(err, syscall.ECONNREFUSED) {
				t.Fatalf("a full backlog answered %v, not ECONNREFUSED: the retry in alive is guarding the wrong thing", err)
			}
			break
		}
		held = append(held, c)
	}
	if len(held) == 0 || len(held) >= 1000 {
		t.Fatalf("filled %d connections without a refusal", len(held))
	}
	t.Logf("the backlog took %d connections and refused the next", len(held))

	// The instance gets round to accepting, between one probe and the next.
	var once sync.Once
	hookProbeRetry = func(int) {
		once.Do(func() {
			c, err := ln.Accept()
			if err != nil {
				t.Error(err)
				return
			}
			t.Cleanup(func() { c.Close() })
		})
	}
	t.Cleanup(func() { hookProbeRetry = nil })

	second, err := Listen(path)
	if !errors.Is(err, ErrInUse) {
		second.Close()
		t.Fatalf("Listen returned %v, want ErrInUse: it called a running instance a corpse because its accept queue was full", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("the running instance's socket was unlinked anyway: %v", err)
	}
}

// TestTheHolderDiesRightAfterTheProbe is the other half of the same window and
// the one the loop does NOT close, pinned here so it is a decision on the
// record rather than a surprise in six months. The probe finds a live instance,
// that instance exits a microsecond later, and Listen has already committed to
// its answer: ErrInUse, about an editor that is no longer running.
//
// The point of the test is the second half. A wrong answer that costs one
// message and then goes away is a different thing from a wrong answer that
// wedges the path, so what is asserted is that the socket is gone with the
// instance that owned it and the very next Listen takes it.
func TestTheHolderDiesRightAfterTheProbe(t *testing.T) {
	path := SocketPath(socketDir(t))
	holder := newInstanceAt(t, path)

	var once sync.Once
	var died bool
	hookAfterProbe = func(p string, live bool) {
		once.Do(func() {
			if !live {
				t.Errorf("the probe called a running instance a corpse")
				return
			}
			// The editor exits here, between the probe and the return. Its own
			// Close unlinks the socket, which is what an ordinary quit does and
			// what makes the next launch clean.
			if err := holder.ln.Close(); err != nil {
				t.Error(err)
				return
			}
			died = true
		})
	}
	t.Cleanup(func() { hookAfterProbe = nil })

	ln, err := Listen(path)
	if !errors.Is(err, ErrInUse) {
		ln.Close()
		t.Fatalf("Listen returned %v, want ErrInUse", err)
	}
	if !died {
		t.Fatal("the hook never ran, so this test proved nothing")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("Lstat says %v, want the dead instance to have taken its socket with it", err)
	}
	second, err := Listen(path)
	if err != nil {
		t.Fatalf("the launch after the one that lost the race also failed: %v", err)
	}
	second.Close()
}

// TestDialWithNoSocketAndNoDirectory. Both are "start one yourself" and the
// caller must not have to tell them apart.
func TestDialWithNoSocketAndNoDirectory(t *testing.T) {
	dir := socketDir(t)
	if _, err := Dial(SocketPath(dir)); !errors.Is(err, ErrNoInstance) {
		t.Errorf("Dial with no socket: %v, want ErrNoInstance", err)
	}
	if _, err := Dial(filepath.Join(dir, "nope", SocketName)); !errors.Is(err, ErrNoInstance) {
		t.Errorf("Dial with no directory: %v, want ErrNoInstance", err)
	}
	stale(t, SocketPath(dir))
	if _, err := Dial(SocketPath(dir)); !errors.Is(err, ErrNoInstance) {
		t.Errorf("Dial at a corpse: %v, want ErrNoInstance", err)
	}
}

// TestWorldWritableDirectoryIsRefused, on both halves, which refuse the same
// thing and then do two different things about it. See perm.go: a socket
// anybody can replace is a socket anybody can be sent an absolute path by, and
// with --wait, a shell they can hold open. Neither half puts a socket there and
// neither half dials one.
//
// What the dialling half must not do is refuse to run. Its error is fatal in
// cmd/pvim, and the condition is a directory mode rather than anything about a
// socket -- there is no socket in the directory here at all -- so an editor
// that will not start over it is the whole editor lost to a umask of 002 on a
// directory somebody made by hand. Dial answers ErrNoInstance, which is true,
// the process becomes the instance itself, and the sentence with the mode in it
// lands on the message line from Listen, which is the half that can print.
// 0775 is in the table because it is the one that turns up in real life.
func TestWorldWritableDirectoryIsRefused(t *testing.T) {
	for _, mode := range []os.FileMode{0o777, 0o775} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			dir := socketDir(t)
			path := SocketPath(dir)
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(dir, 0o700) })

			ln, err := Listen(path)
			if err == nil {
				ln.Close()
				t.Fatal("Listen bound a socket in a group- or world-writable directory")
			}
			if !strings.Contains(err.Error(), "writable by group or other") {
				t.Errorf("Listen said %q, which does not say why", err)
			}
			_, err = Dial(path)
			if !errors.Is(err, ErrNoInstance) {
				t.Errorf("Dial said %v; cmd/pvim treats anything else as fatal, so this is an editor that will not start over a directory mode", err)
			}
			if !strings.Contains(err.Error(), "writable by group or other") {
				t.Errorf("Dial said %q, and a caller that prints it learns nothing", err)
			}
		})
	}
}

// TestASymlinkAtTheSocketPathIsRefused. The owner check is an Lstat and the
// dial follows the path, so without this a link this user owns is a link to
// anywhere: every check passes on the link and the connection lands on whatever
// socket is at the other end, in a directory nothing has looked at. Measured
// before the check went in, this test sent /tmp/f.txt down a symlink to an
// instance in a directory Dial never stat'd.
func TestASymlinkAtTheSocketPathIsRefused(t *testing.T) {
	target := newInstance(t)
	link := SocketPath(socketDir(t))
	if err := os.Symlink(target.path, link); err != nil {
		t.Fatal(err)
	}

	err := Open(link, Request{Path: "/tmp/f.txt", Cwd: "/tmp"})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Open through a symlink returned %v, want a refusal that says why", err)
	}
	if errors.Is(err, ErrNoInstance) {
		t.Error("the refusal reads as ErrNoInstance, which would start a second editor rather than saying what is wrong")
	}
	select {
	case req := <-target.reqs:
		t.Errorf("the instance at the other end of the link was handed %+v", req)
	default:
	}

	// And Listen will not take the path over either: a symlink is not a socket,
	// so the takeover refuses to remove it rather than deleting a file it did
	// not create.
	ln, err := Listen(link)
	if err == nil {
		ln.Close()
		t.Fatal("Listen bound over a symlink")
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("the link was removed anyway: %v", err)
	}
}

// TestOpenDoesNotWait: one reply and the process is done. This is "pvim file"
// from a shell, which is the common case and the one that must not block.
func TestOpenDoesNotWait(t *testing.T) {
	i := newInstance(t)
	done := make(chan error, 1)
	go func() { done <- Open(i.path, Request{Path: "/tmp/f.txt", Cwd: "/home/pkar"}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Open blocked even though Wait was not set")
	}
	req := i.took(t)
	if req.Path != "/tmp/f.txt" || req.Cwd != "/home/pkar" || req.Wait {
		t.Errorf("the instance was asked for %+v", req)
	}
}

// TestOpenRefusesARelativePath, in the client, before anything is sent. The
// running instance has 'autochdir' on and its cwd is wherever its current
// buffer lives, so a relative path resolved there opens a file nobody asked
// for.
func TestOpenRefusesARelativePath(t *testing.T) {
	i := newInstance(t)
	err := Open(i.path, Request{Path: "main.go", Cwd: "/tmp"})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("Open with a relative path returned %v", err)
	}
}

// TestTheInstanceRefusesToo. The client checks first, so this is the server
// keeping its own contract against a peer that is not this build.
func TestTheInstanceRefusesToo(t *testing.T) {
	i := newInstance(t)
	for _, tc := range []struct {
		name string
		req  Request
		says string
	}{
		{"relative path", Request{Op: OpOpen, Path: "main.go"}, "absolute"},
		{"no path", Request{Op: OpOpen}, "no path"},
		{"unknown op", Request{Op: "detonate", Path: "/tmp/f.txt"}, "unknown op"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Dial(i.path)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err := c.Send(tc.req); err != nil {
				t.Fatal(err)
			}
			rep, err := c.Next()
			if err != nil {
				t.Fatal(err)
			}
			if rep.Kind != ReplyError || !strings.Contains(rep.Error, tc.says) {
				t.Fatalf("reply is %+v, want an error saying %q", rep, tc.says)
			}
		})
	}
}

// TestHandlerRefusal: the editor itself saying no, which the client sees as
// ErrRefused with the editor's own sentence attached.
func TestHandlerRefusal(t *testing.T) {
	path := SocketPath(socketDir(t))
	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go ln.Serve(func(Request) (<-chan struct{}, error) {
		return nil, errors.New("E212: cannot open file for writing")
	})

	err = Open(path, Request{Path: "/tmp/f.txt", Cwd: "/tmp"})
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("Open returned %v, want ErrRefused", err)
	}
	if !strings.Contains(err.Error(), "E212") {
		t.Errorf("the editor's own message did not survive: %q", err)
	}
}

// TestWaitOnABufferThatIsAlreadyGone. A nil channel from the handler is a tab
// that closed in the same breath it opened, and a --wait client that blocked
// forever on it would be git hanging with no window to close.
func TestWaitOnABufferThatIsAlreadyGone(t *testing.T) {
	path := SocketPath(socketDir(t))
	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go ln.Serve(func(Request) (<-chan struct{}, error) { return nil, nil })

	done := make(chan error, 1)
	go func() { done <- Open(path, Request{Path: "/tmp/f.txt", Cwd: "/tmp", Wait: true}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a --wait client blocked on a buffer that was already gone")
	}
}

// TestWaitBlocksUntilTheBufferCloses, in this process. The subprocess version
// below is the real thing; this one is here because it can say exactly when the
// client was still blocked.
func TestWaitBlocksUntilTheBufferCloses(t *testing.T) {
	i := newInstance(t)
	done := make(chan error, 1)
	go func() { done <- Open(i.path, Request{Path: "/tmp/COMMIT_EDITMSG", Cwd: "/tmp", Wait: true}) }()

	if req := i.took(t); !req.Wait {
		t.Fatal("the request did not carry Wait")
	}
	select {
	case err := <-done:
		t.Fatalf("Open returned %v while the buffer was still open", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(i.closed)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Open returned %v after the buffer closed, want nil: git reads that as a refused commit", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Open did not return after the buffer closed")
	}
}

// TestTheInstanceGoingAwayIsNotSuccess. Exit 0 from a --wait client means "the
// person saved it", and an editor that was killed while git held the commit
// message did not say that.
//
// The instance here is a raw listener rather than a Listener, because what has
// to happen is the connection dying under a waiting client and there is no way
// to make a real one do that on purpose: closing the listener does not drop the
// connections it accepted, and killing the process is what this is standing in
// for.
func TestTheInstanceGoingAwayIsNotSuccess(t *testing.T) {
	path := SocketPath(socketDir(t))
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := os.Chmod(path, socketMode); err != nil {
		t.Fatal(err)
	}
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		NewReader(c).ReadSlice('\n')
		WriteReply(c, Reply{Kind: ReplyOpened})
		c.Close() // and then the editor was killed
	}()

	done := make(chan error, 1)
	go func() { done <- Open(path, Request{Path: "/tmp/COMMIT_EDITMSG", Cwd: "/tmp", Wait: true}) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Open returned nil when the instance died mid-wait: git reads that as a commit message the person saved")
		}
		if !strings.Contains(err.Error(), "closed the connection") {
			t.Errorf("Open said %q, which does not say what happened", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Open hung after the instance went away")
	}
}

// TestVersionMismatchFromTheClientSide is the failure the version field exists
// for, in the direction that happens: a running instance from before a rebuild,
// and a client that has to say so rather than hang.
func TestVersionMismatchFromTheClientSide(t *testing.T) {
	dir := socketDir(t)
	path := SocketPath(dir)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := os.Chmod(path, socketMode); err != nil {
		t.Fatal(err)
	}
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		NewReader(c).ReadSlice('\n')
		fmt.Fprintf(c, "{\"version\":%d,\"kind\":\"opened\"}\n", Version+98)
	}()

	done := make(chan error, 1)
	go func() { done <- Open(path, Request{Path: "/tmp/f.txt", Cwd: "/tmp", Wait: true}) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrVersion) {
			t.Fatalf("Open returned %v, want ErrVersion", err)
		}
		for _, want := range []string{fmt.Sprint(Version + 98), fmt.Sprint(Version)} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %s", err, want)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a client talking to an instance that speaks another version hung")
	}
}

// TestVersionMismatchFromTheInstanceSide: the other direction, and the reason
// serve answers a bad request line instead of hanging up. The reply is in this
// build's version, so the older client fails its own check with both numbers in
// it; here the reply is readable and carries the sentence.
func TestVersionMismatchFromTheInstanceSide(t *testing.T) {
	i := newInstance(t)
	c, err := Dial(i.path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c.c, "{\"version\":%d,\"op\":\"open\",\"path\":\"/tmp/f.txt\"}\n", Version+98)

	rep, err := c.Next()
	if err != nil {
		t.Fatalf("the instance hung up on a version it did not know: %v", err)
	}
	if rep.Kind != ReplyError {
		t.Fatalf("reply is %+v, want an error", rep)
	}
	for _, want := range []string{fmt.Sprint(Version + 98), fmt.Sprint(Version)} {
		if !strings.Contains(rep.Error, want) {
			t.Errorf("reply %q does not name %s", rep.Error, want)
		}
	}
	select {
	case req := <-i.reqs:
		t.Errorf("the editor was handed %+v from a peer it could not parse", req)
	default:
	}
}

// TestAPeerThatSaysNothingIsDroppedIsWhatTheDeadlineIsFor. A connection that is
// opened and then goes quiet holds a goroutine and a file descriptor in the
// editor for as long as the editor runs, and the thing on the other end need
// not be malicious: a shell that starts "pvim file" and is suspended between
// the connect and the write looks exactly like this.
func TestAPeerThatSaysNothingIsDroppedIsWhatTheDeadlineIsFor(t *testing.T) {
	old := requestDeadline
	requestDeadline = 50 * time.Millisecond
	t.Cleanup(func() { requestDeadline = old })

	i := newInstance(t)
	c, err := net.Dial("unix", i.path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// The instance answers rather than hanging up without a word, so that the
	// person who typed the command gets a sentence instead of "unexpected EOF".
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	rep, err := ReadReply(NewReader(c))
	if err != nil {
		t.Fatalf("reading the instance's answer to a peer that said nothing: %v", err)
	}
	if rep.Kind != ReplyError {
		t.Fatalf("reply is %+v, want an error", rep)
	}
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Error("the connection is still open after the deadline")
	}
	select {
	case req := <-i.reqs:
		t.Errorf("the editor was handed %+v by a peer that never sent a request", req)
	default:
	}
}

// TestAKilledClientDoesNotLeaveTheInstanceWaiting. The tab stays open, which is
// the right answer -- a terminal window closing is not a reason to throw
// somebody's file away -- but the goroutine holding the connection has to go.
func TestAKilledClientDoesNotLeaveTheInstanceWaiting(t *testing.T) {
	i := newInstance(t)
	runtime.GC()
	before := runtime.NumGoroutine()

	c, err := Dial(i.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Send(Request{Op: OpOpen, Path: "/tmp/f.txt", Cwd: "/tmp", Wait: true}); err != nil {
		t.Fatal(err)
	}
	if rep, err := c.Next(); err != nil || rep.Kind != ReplyOpened {
		t.Fatalf("first reply %+v, %v", rep, err)
	}
	i.took(t)
	c.Close() // the client was killed; nobody said the buffer closed

	// Two goroutines went with that connection, serve and its hangup watcher,
	// and both have to be gone without anything closing i.closed. Polling
	// rather than sleeping: the count is the only thing this can see from the
	// outside, and this package's tests are the only thing running.
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.Gosched()
		if runtime.NumGoroutine() <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines went from %d to %d and stayed there after the client was killed", before, runtime.NumGoroutine())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The subprocess half. Everything above drives the client from the same process
// as the instance, which is not what this package is for: the whole point is a
// second pvim, started from a second shell, that talks to the first and exits.
// These two run a real one.

const (
	clientEnv = "PVIM_SERVER_TEST_CLIENT_SOCKET"
	fileEnv   = "PVIM_SERVER_TEST_CLIENT_FILE"
	waitEnv   = "PVIM_SERVER_TEST_CLIENT_WAIT"
)

// TestHelperClient is not a test. It is the second process: run with clientEnv
// set, it does exactly what "pvim file" does when an instance is up, and its
// exit code is the whole result. Skipped in every ordinary run.
func TestHelperClient(t *testing.T) {
	path := os.Getenv(clientEnv)
	if path == "" {
		t.Skip("not the helper process")
	}
	req := Request{
		Path: os.Getenv(fileEnv),
		Cwd:  "/tmp",
		Wait: os.Getenv(waitEnv) == "1",
	}
	if err := Open(path, req); err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		os.Exit(3)
	}
	os.Exit(0)
}

// client starts a second process pointed at path and returns a channel carrying
// its exit error.
func client(t *testing.T, path, file string, wait bool) (<-chan error, *exec.Cmd) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperClient$")
	cmd.Env = append(os.Environ(),
		clientEnv+"="+path,
		fileEnv+"="+file,
		waitEnv+"="+map[bool]string{true: "1", false: "0"}[wait],
	)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Nothing this package starts is allowed to outlive the test that started
	// it, whatever the test does on its way out.
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if err != nil {
			err = fmt.Errorf("%w: %s", err, out.String())
		}
		done <- err
	}()
	return done, cmd
}

// TestASecondProcessAttaches. This is "pvim file" in the next tmux pane: the
// file lands in the instance that is already up and the second process is gone.
func TestASecondProcessAttaches(t *testing.T) {
	i := newInstance(t)
	done, _ := client(t, i.path, "/tmp/from-a-second-process.txt", false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the second process exited %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the second process never exited")
	}
	if got := i.took(t).Path; got != "/tmp/from-a-second-process.txt" {
		t.Errorf("the instance was asked for %q", got)
	}
}

// TestASecondProcessWaits is EDITOR="pvim --wait" reduced to its bones: git
// blocks on this process, this process blocks on the socket, and the commit
// lands the moment the tab closes.
func TestASecondProcessWaits(t *testing.T) {
	i := newInstance(t)
	done, _ := client(t, i.path, "/tmp/COMMIT_EDITMSG", true)
	if req := i.took(t); !req.Wait {
		t.Fatal("the request did not carry Wait")
	}
	select {
	case err := <-done:
		t.Fatalf("the client exited (%v) while the buffer was still open", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(i.closed)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the client exited %v after the buffer closed, want 0", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the client did not exit after the buffer closed")
	}
}

// TestARegularFileAtTheSocketPathStartsAnEditorAnyway is the worst thing a
// stale path can do and the one this whole package exists to prevent: not a
// socket that has to be taken over, but something at the socket's path that
// stops the editor starting at all.
//
// Measured before the fix, with the built binary and a HOME whose
// .cache/vim/pvim.sock was an empty regular file: "pvim: server: cannot dial
// .../pvim.sock: ... connect: socket operation on non-socket", exit 1, no
// editor, on every launch. Nothing clears it, because Listen deliberately
// refuses to remove a file it did not create and Dial exits before Listen is
// ever reached. So Dial has to answer ErrNoInstance here -- there is no
// instance, that is the plain truth of it -- and the sentence about the file
// reaches the person from the listening half, which runs next in the same
// process and can print.
func TestARegularFileAtTheSocketPathStartsAnEditorAnyway(t *testing.T) {
	path := SocketPath(socketDir(t))
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Dial(path); !errors.Is(err, ErrNoInstance) {
		t.Fatalf("Dial said %v; cmd/pvim treats anything but ErrNoInstance as fatal, so this is the editor refusing to start over a file nothing ever removes", err)
	}
	ln, err := Listen(path)
	if err == nil {
		ln.Close()
		t.Fatal("Listen took over a path that was not a socket")
	}
	if !strings.Contains(err.Error(), "not a socket") {
		t.Errorf("Listen said %q, which is the only half that can tell the person what is on the path", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the file was removed anyway: %v", err)
	}
}

// TestABindTheKernelRefusesIsNotAnotherInstance. ErrInUse is the one error
// cmd/pvim prints nothing for, because "somebody else is the instance" is the
// ordinary way for a second shell to start. Reporting a bind that failed for
// its own reason as ErrInUse therefore loses the reason completely: on a home
// directory that cannot hold a unix socket at all, single instance silently
// never works and nothing anywhere says why.
//
// Two errnos, because the misclassification was in the loop's shape rather
// than in one branch: a path longer than sun_path (about 104 bytes) and a
// directory this process cannot write. Both leave nothing at the path, which
// is what sent them down the "it went away under us, try again" branch and out
// of the bottom of the loop as ErrInUse.
func TestABindTheKernelRefusesIsNotAnotherInstance(t *testing.T) {
	dir := socketDir(t)
	cases := []struct {
		name string
		path string
		says string
		set  func(t *testing.T)
	}{
		{
			name: "a path longer than the kernel takes",
			path: filepath.Join(dir, strings.Repeat("x", 200)),
			says: "invalid argument",
		},
		{
			name: "a directory this process cannot write",
			path: SocketPath(dir),
			says: "permission denied",
			set: func(t *testing.T) {
				if os.Getuid() == 0 {
					t.Skip("root writes a 0500 directory anyway")
				}
				if err := os.Chmod(dir, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(dir, 0o700) })
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.set != nil {
				c.set(t)
			}
			ln, err := Listen(c.path)
			if err == nil {
				ln.Close()
				t.Fatal("Listen bound it")
			}
			if errors.Is(err, ErrInUse) {
				t.Errorf("Listen said %q about a path with nothing at it, which cmd/pvim prints nothing for", err)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("Listen said %q, which does not carry the bind error (%q)", err, c.says)
			}
			if _, err := os.Lstat(c.path); !os.IsNotExist(err) {
				t.Errorf("Lstat says %v, so the bind left something behind and this is not the branch it looks like", err)
			}
		})
	}
}

// TestServeSurvivesATransientAcceptError. Serve used to return on any accept
// error that was not the listener closing, and EMFILE is one a machine hands
// out under fd pressure without anything being wrong with the socket. The
// editor kept running, the socket stayed bound, cmd/pvim discards Serve's
// error, and every later "pvim file" connected into a listen backlog nobody
// was accepting from -- which is not a dial failure, so it hung.
//
// The first Accept here fails the way that machine did and the second is
// ordinary. One client, which must be served.
func TestServeSurvivesATransientAcceptError(t *testing.T) {
	path := SocketPath(socketDir(t))
	raw, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, socketMode); err != nil {
		t.Fatal(err)
	}
	ln := &Listener{ln: &refuseOnce{Listener: raw}, path: path}
	served := make(chan error, 1)
	go func() { served <- ln.Serve(func(Request) (<-chan struct{}, error) { return nil, nil }) }()
	t.Cleanup(func() {
		ln.Close()
		<-served
	})

	done := make(chan error, 1)
	go func() { done <- Open(path, Request{Path: "/tmp/f.txt", Cwd: "/tmp"}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Open after one accept error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the client was never answered: one accept error retired the socket for the life of the editor")
	}
}

// refuseOnce is a listener whose first Accept fails the way a process out of
// file descriptors does, and which is ordinary after that. The error is built
// with the same layers the net package puts around an errno so that Serve sees
// what it would really see.
type refuseOnce struct {
	net.Listener
	once sync.Once
}

func (l *refuseOnce) Accept() (net.Conn, error) {
	var refused bool
	l.once.Do(func() { refused = true })
	if refused {
		return nil, &net.OpError{Op: "accept", Net: "unix", Err: os.NewSyscallError("accept", syscall.EMFILE)}
	}
	return l.Listener.Accept()
}

// TestTheClientDoesNotWaitForeverOnAnInstanceThatNeverAnswers is the other
// half of the same failure, and it is a fix on its own: a connect to a socket
// somebody bound and nobody accepts from does not fail, it waits in the
// backlog, so dialTimeout never fires. With no deadline on the reply the
// client blocks forever with nothing on stdout. Measured with the built binary
// against a bound-and-unaccepted socket: "pvim --wait f.txt" printed nothing
// and was still there when it was killed at 15s, which under EDITOR is git
// blocked with no message.
func TestTheClientDoesNotWaitForeverOnAnInstanceThatNeverAnswers(t *testing.T) {
	path := SocketPath(socketDir(t))
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := os.Chmod(path, socketMode); err != nil {
		t.Fatal(err)
	}
	defer func(d time.Duration) { firstReplyDeadline = d }(firstReplyDeadline)
	firstReplyDeadline = 100 * time.Millisecond

	done := make(chan error, 1)
	go func() { done <- Open(path, Request{Path: "/tmp/f.txt", Cwd: "/tmp"}) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Open reported the file open, and nothing ever accepted the connection")
		}
		if !strings.Contains(err.Error(), "did not answer") {
			t.Errorf("Open said %q, which does not say what happened", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Open never returned: the connect landed in the backlog, so the dial timeout does not cover it and the first reply has no deadline")
	}
}

// TestCloseDoesNotUnlinkASocketThisListenerDidNotBind. Go unlinks a unix
// listener's socket by NAME when it is closed, and the name is the wrong
// question: after a takeover the path holds somebody else's inode, so the
// instance that lost the path deletes the socket of the instance that has it,
// and the next "pvim file" finds nothing and starts a third editor while the
// second is still running and serving.
//
// The takeover is done by hand here rather than driven, because how the
// mistake is reached is TestAFullBacklogIsNotACorpse's subject and all that
// matters to Close is that the path holds an inode it did not bind.
func TestCloseDoesNotUnlinkASocketThisListenerDidNotBind(t *testing.T) {
	path := SocketPath(socketDir(t))
	first, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	second := newInstanceAt(t, path)

	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("the instance that lost the path took the winner's socket with it: %v", err)
	}
	if err := Open(second.path, Request{Path: "/tmp/still-here.txt", Cwd: "/tmp"}); err != nil {
		t.Fatalf("the instance that owns the path is unreachable after the other one quit: %v", err)
	}
	if got := second.took(t).Path; got != "/tmp/still-here.txt" {
		t.Errorf("the instance was asked for %q", got)
	}
}
