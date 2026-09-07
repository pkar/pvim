package tui

import "golang.org/x/sys/unix"

// The termios ioctls have different names on every unix, which is the one
// place a terminal library earns its keep and the one place this package
// cannot avoid a build tag. Two constants per platform is the whole cost.
const (
	ioctlReadTermios  = unix.TIOCGETA
	ioctlWriteTermios = unix.TIOCSETA
)
