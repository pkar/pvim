package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Who is allowed to reach the socket, checked before it is bound and before it
// is dialled.
//
// A unix socket is a file, and a file is reachable by anyone who can write the
// directory it sits in. Put one in a world-writable directory and any local
// user can delete it and bind their own in its place; the next "pvim file"
// from this account then hands them an absolute path, a working directory, and
// -- with --wait -- a shell that sits and waits for them to say the buffer
// closed. That is a local privilege problem and it is cheap to refuse, so this
// package refuses rather than proceeding.
//
// The two checks are on the DIRECTORY and not on the socket, which is the part
// worth being clear about. The socket's own mode is set to 0600 after bind, and
// that is a belt: Linux checks a unix socket's permissions at connect, and the
// BSDs have historically not, so on macOS the socket's mode is close to
// decoration. What actually keeps another user out is that they cannot write
// the directory, which is what checkDir insists on.

// checkDir refuses a socket path whose directory anybody else can write.
//
// A missing directory is returned as the os.Stat error it is, unwrapped, so
// that a caller can tell it apart with errors.Is(err, fs.ErrNotExist): to Dial
// that means there is no instance, and to Listen it means cmd/pvim has not
// made ~/.cache/vim yet, which are two different sentences.
func checkDir(path string) error {
	dir := filepath.Dir(path)
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("server: %s is not a directory", dir)
	}
	// Group and other write, and the sticky bit does not save it: sticky stops
	// another user deleting the socket, and does nothing at all about them
	// creating one at a path where none exists yet, which is the case that
	// matters on a machine where pvim is not running.
	if m := fi.Mode().Perm(); m&0o022 != 0 {
		return fmt.Errorf("server: %s is writable by group or other (mode %04o), refusing to put a socket there", dir, m)
	}
	return ownedByCaller(dir, fi)
}

// errSymlinkAtSocket is the one refusal below that Dial keeps as a hard error.
// A sentinel because the caller has to tell it from the others: everything else
// here means "there is no instance at this path, become one", and a symlink
// means somebody put a link where this package only ever puts a socket.
var errSymlinkAtSocket = errors.New("server: a symlink at the socket path")

// checkSocketFile refuses a socket path holding something this process cannot
// or must not dial: a symlink, a file that is not a socket at all, or a socket
// belonging to somebody else.
//
// checkDir has already said that only this user can create a file in that
// directory, so a user half can only fail on a directory whose mode changed
// under a socket that was already there, or on one this user does not own at
// all. It is here because the cost is a stat and the failure it prevents is
// dialling a stranger's socket and telling it what file to open.
//
// The type half is the one that was missing, and its absence was measured
// rather than reasoned about: a regular file this user owns passed both other
// checks, connect answered ENOTSOCK, and the editor exited 1 on every launch
// until somebody deleted a file they had never heard of. Answering here rather
// than at the connect is only for the sentence, which names the mode.
//
// The symlink half is the asymmetry between the two calls this function sits
// between. It is Lstat, so what it looks at is the link; net.Dial follows the
// link, so what gets connected to is whatever is at the other end, in a
// directory nothing here has checked at all. Every check above would pass on a
// link this user owns pointing at another user's socket. Nothing this package
// does ever creates one, so refusing costs nothing and closes the gap between
// the thing that was checked and the thing that is dialled.
func checkSocketFile(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s, and this package never makes one; refusing to dial through it", errSymlinkAtSocket, path)
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("server: %s is not a socket (mode %v), so there is nothing here to talk to", path, fi.Mode())
	}
	return ownedByCaller(path, fi)
}
