//go:build !darwin && !linux

package tui

import "os"

// noDriver is the driver on an operating system this editor has no terminal
// layer for. It exists so that the package builds everywhere the rest of the
// module does: everything above the Driver seam is portable Go and only the
// termios call is not, so a platform with no implementation loses the terminal
// frontend and nothing else.
type noDriver struct{}

func newDriver(in, out *os.File) Driver { return noDriver{} }

func (noDriver) Raw() (func() error, error) { return nil, ErrNoTerminal }

func (noDriver) Size() (rows, cols int, err error) { return 0, 0, ErrNoTerminal }

func (noDriver) Resized() (<-chan struct{}, func()) {
	return make(chan struct{}), func() {}
}
