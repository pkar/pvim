// Package server is the single instance: one pvim per desktop, and every shell
// that types "pvim file" talks to it over a unix socket.
//
// This is the fourth of the four deliberate departures from gvim. There
// is no --remote-tab, no --servername, no +clientserver and no X11 property
// protocol; there is one socket in ~/.cache/vim and three things it does.
// "pvim file" from any shell opens a tab in the window that is already up and
// the second process exits. "pvim --wait file" stays alive until that tab
// closes and then exits 0, which is the whole of what EDITOR=pvim --wait needs
// for git commit. "pvim --new file" skips the socket and starts its own
// instance. In the terminal frontend the same socket means a file opened from a
// second tmux pane lands in the first, which terminal vim cannot do at all.
//
// # The wire format is deliberately boring
//
// One JSON object per line, a version field on every object, and nothing
// clever: no length prefix, no handshake, no capability negotiation, no
// streaming. Both ends of this protocol are the same binary, usually the same
// build, and the interesting failure is not a malicious peer but a stale
// running instance from before a rebuild -- so the version check is the whole
// of the compatibility story, and it fails with a sentence that names both
// numbers rather than a decode error twenty bytes in.
//
// The client writes exactly one Request and then reads Reply lines until one
// says the exchange is over. A client that did not ask to wait gets one reply
// and closes. A client that did gets ReplyOpened as soon as the running
// instance has the file, and ReplyClosed when the buffer goes away, which may
// be an hour later, so the socket has to survive being idle and the client has
// to be a process doing nothing but reading it.
//
// # Who can reach the socket
//
// The socket is bound user-only and, more to the point, this package refuses to
// bind or dial one in a directory that group or other can write. A socket is a
// file; a file in a directory anybody can write is a file anybody can replace,
// and the process on the other end of the replacement is handed an absolute
// path, a working directory and, under --wait, a shell that sits and waits for
// it. ~/.cache/vim is 0700 on this machine and 0755 would pass too. See
// perm.go, which is where the check and the reasoning live.
//
// # What is not in the protocol, on purpose
//
// No line or column to open at, no ex command to run, no reply carrying the
// buffer's contents. Each of those is a feature that would have to be measured
// against something, and the list of what not to build has gvim's whole
// remote family on it. The one field beyond the path is the client's working
// directory, and it earns its place: the running instance has 'autochdir' on
// and its cwd follows whatever buffer it is showing, so a relative path is
// resolved by the client before it is sent and the cwd travels only so the
// running instance can say where the file came from.
package server

import (
	"errors"
	"path/filepath"
)

// Version is the protocol version. It goes on every object in both directions
// and it is compared for equality, not for "at least": two builds of this
// editor that disagree about the format have no business guessing.
const Version = 1

// SocketName is the socket's name inside the cache directory. The directory is
// ~/.cache/vim, which cmd/pvim already creates on every launch because the
// vimrc points 'undodir' at it and vim will not create it itself.
const SocketName = "pvim.sock"

// SocketPath is where the instance listens, given the cache directory.
//
// A function and not a constant because the directory is worked out from
// $HOME at run time, and because a test needs a socket somewhere else: a unix
// socket path is limited to about 100 bytes by the kernel, so a test that puts
// one under a deeply nested t.TempDir() fails with a bind error that reads like
// a permissions problem. Anything calling this in a test should keep the
// directory short.
func SocketPath(cacheDir string) string { return filepath.Join(cacheDir, SocketName) }

// The errors both ends can produce. Sentinels, because the caller's decision
// hangs on which one it is: ErrNoInstance means start one, ErrInUse means do
// not, and ErrVersion means say so and start a separate one.
var (
	// ErrNoInstance is a dial that found no instance to talk to. That is
	// wider than "nothing is listening" and deliberately so: no socket at the
	// path, a socket with no live process behind it, something on the path
	// that is not a socket at all, a socket belonging to another uid, a
	// directory the socket rules refuse, a path the kernel will not connect
	// to. cmd/pvim answers every one of them the same way, by becoming the
	// instance itself, and the reason is wrapped in here for whoever prints
	// it. See Dial for what that width is worth and what it cost to learn.
	ErrNoInstance = errors.New("server: no running instance")

	// ErrInUse is Listen finding a socket that a live process is already
	// answering on. That process is the instance and this one is not; the
	// caller either sends its file over and exits, or was told --new and
	// starts up with no socket at all.
	//
	// It is the one error cmd/pvim prints nothing for, which is right for what
	// it means and is why Listen is careful never to say it about a bind that
	// failed for its own reason: a silent wrong answer leaves single instance
	// not working with nothing anywhere to read.
	ErrInUse = errors.New("server: another instance is already listening")

	// ErrVersion is a peer speaking a different protocol version. The message
	// names both numbers; this is the sentinel behind it.
	ErrVersion = errors.New("server: protocol version mismatch")

	// ErrRefused is the running instance declining a request -- a path it
	// cannot read, a request it does not understand. The reason travels as
	// text in the reply and this is what the client matches on.
	ErrRefused = errors.New("server: the running instance refused the request")
)
