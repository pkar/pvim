package tui

import "os"

// Driver is the operating system underneath a Terminal: the three things this
// package cannot do without a real file descriptor.
//
// It is an interface for one reason. Raw mode cannot be tested in CI -- there
// is no tty under "go test" and there is no way to fake one without a pty --
// so everything that can be tested has to sit above a seam, and this is the
// seam. The frame diff, the escape-sequence writer, the key decoding, the
// escape timeout and the event loop are all above it and all run against a
// fake driver and a bytes.Buffer.
//
// The methods are what a terminal frontend needs and nothing else: put the
// terminal into raw mode and be able to put it back, ask how big it is, and be
// told when that changed.
type Driver interface {
	// Raw puts the terminal into raw mode and returns the call that puts it
	// back the way it was. The restore has to be safe to call from a signal
	// handler goroutine, because that is one of the places it is called from.
	Raw() (restore func() error, err error)

	// Size returns the terminal size in cells.
	Size() (rows, cols int, err error)

	// Resized returns a channel that receives once per SIGWINCH and the call
	// that stops the delivery. The channel carries no value because the size
	// is asked for again when one arrives: a size delivered with the signal
	// would already be stale by the time anyone read it.
	Resized() (resized <-chan struct{}, stop func())
}

// NewDriver returns the driver for this operating system, reading the size of
// out and putting in into raw mode.
//
// Both have to be the same terminal, which they are whenever they are the two
// ends of one process's tty. It takes *os.File rather than the interfaces
// Terminal holds because a file descriptor is exactly what this layer is for.
func NewDriver(in, out *os.File) Driver { return newDriver(in, out) }
