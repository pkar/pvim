//go:build darwin || linux

package tui

import (
	"fmt"
	"os"
	"os/signal"
	"sync"

	"golang.org/x/sys/unix"
)

// sysDriver is the real operating system: termios through
// golang.org/x/sys/unix, the window size through an ioctl, and SIGWINCH
// through os/signal.
//
// No cgo, no shell out to stty, no third-party terminal library. x/sys is a
// pinned dependency of this module and it is here for this.
type sysDriver struct {
	in  *os.File
	out *os.File
}

// newDriver is what NewDriver calls on darwin and linux.
func newDriver(in, out *os.File) Driver { return &sysDriver{in: in, out: out} }

// Raw puts the input descriptor into raw mode and returns the call that puts
// it back.
//
// The flag work is cfmakeraw(3), spelled out rather than delegated because the
// two flags this editor cares most about are easy to lose in a library call:
// ISIG off is what makes CTRL-C reach the editor as a keystroke instead of
// killing it, and IXON off is what makes CTRL-S and CTRL-Q reach it instead of
// stopping the terminal. OPOST off is why every line this package writes ends
// with an explicit cursor position and never with a bare newline.
//
// The restore closure is idempotent, because it is called from Stop and Stop
// is called from a defer, from a signal handler and from the error path of
// Start, and a terminal restored twice must not be an error the second time.
func (d *sysDriver) Raw() (func() error, error) {
	fd := int(d.in.Fd())
	old, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoTerminal, err)
	}

	raw := *old
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	// One byte is a read, and no timer: the escape timeout is the editor's and
	// lives in pump, where it can be told what 'ttimeoutlen' is. A VTIME here
	// would be a second timeout underneath it with no way to configure it.
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0

	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoTerminal, err)
	}

	var once sync.Once
	return func() error {
		var err error
		once.Do(func() { err = unix.IoctlSetTermios(fd, ioctlWriteTermios, old) })
		return err
	}, nil
}

// Size asks the output descriptor how big it is. The output and not the input,
// because a pipe on stdin with a terminal on stdout is a thing that happens
// and the size that matters is the one being drawn on.
func (d *sysDriver) Size() (rows, cols int, err error) {
	ws, err := unix.IoctlGetWinsize(int(d.out.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, err
	}
	return int(ws.Row), int(ws.Col), nil
}

// Resized subscribes to SIGWINCH.
//
// The channel it returns carries no value and holds at most one pending
// notification: dragging a window edge delivers a stream of signals and only
// the last size matters, so a full channel drops the notification rather than
// queueing a resize per pixel column.
func (d *sysDriver) Resized() (<-chan struct{}, func()) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, unix.SIGWINCH)

	out := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sig:
				select {
				case out <- struct{}{}:
				default:
				}
			case <-done:
				return
			}
		}
	}()

	var once sync.Once
	return out, func() {
		once.Do(func() {
			signal.Stop(sig)
			close(done)
		})
	}
}
