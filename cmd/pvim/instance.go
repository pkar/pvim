package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkar/pvim/internal/server"
	"github.com/pkar/pvim/internal/text"
)

// One instance, and every shell talking to it.
//
// This is the fourth of the four deliberate departures from gvim, and
// the rule is short: "pvim file" typed anywhere opens a tab in the editor that
// is already up, "pvim --wait file" stays alive until that tab closes, and
// "pvim --new file" starts its own. There is no --remote-tab, no --servername
// and no +clientserver; one unix socket in ~/.cache/vim does the one thing
// that whole family was for.
//
// What it buys, and it is the gate: EDITOR="pvim --wait" git commit
// opens the commit message as a tab in the window on the screen and the commit
// lands when the tab closes.
//
// # Which side of the socket a process ends up on
//
// Startup order, in main: try to attach, and become the instance only if there
// was nobody to attach to. That way two shells racing on a cold machine end
// with one editor and one client rather than two editors, and a stale socket
// left behind by a kill -9 is taken over rather than being a file somebody has
// to delete before the editor will start (internal/server.Listen owns that
// part).
//
// # Both frontends serve, and each hands the work to its own editor goroutine
//
// The listening half needs to hand a request to the goroutine that owns the
// buffers, at a moment nobody chose. The window has exactly that: the pump in
// window.go is a goroutine sitting on a channel, and a socket request goes down
// it like an event. The terminal's editor goroutine is whoever called
// tui.Terminal.Loop, and the way in is tui.Terminal.Post, which puts an event
// on the same channel the keyboard's events arrive on; terminal.go queues the
// work and posts a tui.WakeEvent to have it run. Two goroutines, one shape, and
// this file does not know which it is talking to: everything below takes a
// dispatch and calls it.
//
// That is what makes "pvim file" from a second tmux pane open a tab in the
// terminal instance already running in the first, which terminal vim cannot do
// at all.

// socketPath is where this process looks for the instance, or nothing when
// there is no cache directory to put one in.
//
// The directory is ~/.cache/vim, which cmd/pvim already creates on every
// launch because the vimrc points 'undodir' at it and vim will not create it
// itself. A home directory that cannot be found is not fatal: the editor still
// runs, alone, which is what it did before this file existed.
func socketPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir, err := cacheDir(home)
	if err != nil {
		return ""
	}
	return server.SocketPath(dir)
}

// attach hands the file to a running instance, and reports whether it took it.
//
// A false with no error means there is nobody listening and this process
// should become the editor. An error is a socket that answered and refused, or
// one this build cannot talk to, and it is reported rather than swallowed:
// starting a second editor because the first one said no would leave two, and
// the second would be the one holding the file.
func attach(sock string, c config) (bool, error) {
	if sock == "" || c.newInstance || c.file == "" {
		// No file means nothing to hand over. "pvim" on its own from a second
		// shell starts its own editor, the same as gvim with no arguments,
		// and it is Listen that then finds the socket taken and runs without
		// one.
		return false, nil
	}
	abs, err := filepath.Abs(c.file)
	if err != nil {
		return false, err
	}
	cwd, _ := os.Getwd()
	err = server.Open(sock, server.Request{
		Op:   server.OpOpen,
		Path: abs,
		Cwd:  cwd,
		Wait: c.wait,
	})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, server.ErrNoInstance):
		return false, nil
	default:
		return false, err
	}
}

// dispatch is how a socket request reaches the goroutine that owns the
// buffers: the window's pump, or the terminal's loop through a wake event.
//
// It is a func value rather than an interface because it is one call and
// because the two implementations have nothing else in common. Both answer
// exactly once and both run the work on the editor goroutine, which is the
// whole of the contract; a dispatch that cannot get there -- the frontend is
// still starting, or already gone -- returns an error rather than blocking, so
// the shell on the other end of the socket is told rather than parked.
type dispatch func(work func(client) error) error

// serveSocket binds the socket and answers requests on it for as long as the
// window is up. It is the window frontend's call; the terminal's is in
// terminal.go and goes through serve with a dispatch of its own.
func (e *editor) serveSocket(sock string, c config) func() {
	if e.pump == nil {
		return func() {}
	}
	return e.serve(sock, c, e.handleRequest)
}

// serve binds the socket and answers requests on it for as long as the editor
// is up.
//
// It never fails the startup. A socket that cannot be bound -- another
// instance has it, the directory is gone, this build cannot listen -- means
// this editor runs alone, which is a whole editor and not a broken one, and
// the reason goes on the message line where it can be read rather than to
// stderr where the window would hide it.
func (e *editor) serve(sock string, c config, h server.Handler) func() {
	if sock == "" || c.newInstance || h == nil {
		return func() {}
	}
	ln, err := server.Listen(sock)
	if err != nil {
		if !errors.Is(err, server.ErrInUse) {
			e.ed.Say("pvim: no instance socket: " + err.Error())
		}
		return func() {}
	}
	go func() {
		// Serve returns when the listener is closed, which is the editor on
		// its way out, so there is nothing to report and nowhere left to
		// report it.
		_ = ln.Serve(h)
	}()
	return func() { _ = ln.Close() }
}

// handleRequest is internal/server's Handler for the window frontend.
func (e *editor) handleRequest(req server.Request) (<-chan struct{}, error) {
	return e.request(e.pump.do, req)
}

// request is one request from another shell, whichever frontend is up.
//
// It runs on a goroutine of Serve's making and touches no editor state itself.
// Everything it wants doing goes down the dispatch, which lands on the
// goroutine that owns the buffers, and it waits for the answer so that the
// reply the client gets is the truth rather than a hope.
func (e *editor) request(do dispatch, req server.Request) (<-chan struct{}, error) {
	if req.Op != server.OpOpen {
		return nil, fmt.Errorf("pvim: unknown request %q", req.Op)
	}
	if req.Path == "" {
		return nil, errors.New("pvim: the request named no file")
	}
	var closed <-chan struct{}
	err := do(func(cl client) error {
		ch, err := e.openRemote(req.Path, req.Wait)
		if err != nil {
			return err
		}
		closed = ch
		return e.repaint(cl)
	})
	if err != nil {
		return nil, err
	}
	return closed, nil
}

// openRemote opens path in a new tab, and returns a channel that closes when
// the buffer it opened is closed again.
//
// ":tabedit" and not a hand-built tab, because internal/ex already knows how
// to load a file into a buffer, put a window over it, insert the tab after the
// current one and swap the editor onto it, and every one of those is a rule
// this file would otherwise be guessing at a second time. What is here instead
// is the escaping, because the argument is a path from another shell and
// internal/ex's expandName reads a backslash as an escape and "%" and "#" as
// the current and alternate file.
//
// wait is what the client asked for: with no wait there is nothing to watch
// for and the channel is nil, which internal/server answers as "already gone"
// and which is right -- the client is not waiting for it.
func (e *editor) openRemote(path string, wait bool) (<-chan struct{}, error) {
	if e.ctx == nil {
		return nil, errors.New("pvim: no editor")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	e.ed.NewMessageLine()
	if err := e.ctx.RunLine("tabedit " + escapeExArg(abs)); err != nil {
		if msg := errorMessage(err, "tabedit"); msg != "" {
			e.ed.Say(msg)
		}
		return nil, err
	}
	e.sess.sync()
	e.ed.Redisplay()
	if !wait {
		return nil, nil
	}
	b := e.cur()
	if b == nil || b.Text == nil {
		return nil, errors.New("pvim: the tab opened no buffer")
	}
	ch := make(chan struct{})
	e.waiting = append(e.waiting, waiter{buf: b.Text, done: ch})
	return ch, nil
}

// waiter is one "pvim --wait" client and the buffer it is waiting on.
type waiter struct {
	buf  *text.Buffer
	done chan struct{}
}

// releaseClosed answers every waiting client whose buffer has gone off the
// screen, and every one of them when the editor is quitting.
//
// "Closed" is "no window in any tab is showing it", which is what ":q" on the
// tab, ":tabclose" and ":bd" all produce and which is the event
// EDITOR="pvim --wait" is waiting for. It is deliberately not "the buffer left
// the buffer list": vim keeps a hidden buffer in the list after its window is
// gone, and a git commit that only unblocked on a ":bwipe" would hang on the
// ordinary ":wq".
func (e *editor) releaseClosed(quitting bool) {
	if len(e.waiting) == 0 {
		return
	}
	shown := map[*text.Buffer]bool{}
	if !quitting && e.tabs != nil {
		for _, t := range e.tabs.Pages {
			for _, w := range t.Windows() {
				shown[w.Buf] = true
			}
		}
	}
	keep := e.waiting[:0]
	for _, w := range e.waiting {
		if shown[w.buf] {
			keep = append(keep, w)
			continue
		}
		close(w.done)
	}
	e.waiting = keep
}

// escapeExArg makes a path safe to hand to internal/ex as a command argument.
//
// Five characters, and each one for its own reason. A backslash is expandName's
// escape and has to survive as itself. A space would end the argument. "%" and
// "#" expand to the current and alternate file names. A bar separates one ex
// command from the next, so a file called "a|b" would run "b" as a command.
// Everything else goes through as it is, including a double quote, which is a
// comment only where a command takes no file name.
func escapeExArg(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\', ' ', '\t', '%', '#', '|':
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
