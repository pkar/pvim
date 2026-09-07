//go:build darwin

package main

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// openPTY returns a connected pseudo-terminal pair.
//
// The oracle needs one because vim refuses to run a -s script when its standard
// input is not a terminal: it prints "Press ENTER or type command to continue"
// to stderr before the script starts, that prompt swallows the first keystrokes
// of the script, and the run ends with "Vim: Error reading input, exiting..."
// and a file that was never edited. Measured on 9.2.321 with stdin at
// /dev/null, at a regular file and at a pipe; all three fail the same way, and
// the same script with a terminal on stdin passes. Nothing on the vim command
// line turns this off (--not-a-term suppresses the warning, not the prompt), so
// the harness hands it a terminal.
//
// Pure Go and no unsafe. Three of the four steps macOS wants for a pty are
// argument-free ioctls, and the fourth, TIOCPTYGNAME, is the only one that
// needs a pointer, so it is skipped: on darwin /dev/ptmx is a cloning device
// whose minor number is the slave's index, and fstat gives that. The result is
// checked against the slave's own device number in openPTY, so a kernel that
// ever stops numbering them that way fails here by name rather than by opening
// somebody else's terminal.
func openPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}
	defer func() {
		if err != nil {
			master.Close()
		}
	}()

	fd := master.Fd()
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(unix.TIOCPTYGRANT), 0); e != 0 {
		return nil, nil, fmt.Errorf("TIOCPTYGRANT: %w", e)
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(unix.TIOCPTYUNLK), 0); e != 0 {
		return nil, nil, fmt.Errorf("TIOCPTYUNLK: %w", e)
	}

	var mst unix.Stat_t
	if err = unix.Fstat(int(fd), &mst); err != nil {
		return nil, nil, fmt.Errorf("fstat /dev/ptmx: %w", err)
	}
	minor := unix.Minor(uint64(mst.Rdev))
	name := fmt.Sprintf("/dev/ttys%03d", minor)

	slave, err = os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open pty slave %s: %w", name, err)
	}
	var sst unix.Stat_t
	if err = unix.Fstat(int(slave.Fd()), &sst); err != nil {
		slave.Close()
		return nil, nil, fmt.Errorf("fstat %s: %w", name, err)
	}
	if unix.Minor(uint64(sst.Rdev)) != minor {
		slave.Close()
		return nil, nil, fmt.Errorf("%s is minor %d, wanted %d: /dev/ptmx no longer numbers slaves by minor", name, unix.Minor(uint64(sst.Rdev)), minor)
	}

	// A fixed window size, so that where vim wraps a message and therefore
	// whether it stops for a hit-enter prompt is a property of the case and not
	// of whoever ran the oracle.
	ws := unix.Winsize{Row: ptyRows, Col: ptyCols}
	if err = unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &ws); err != nil {
		slave.Close()
		return nil, nil, fmt.Errorf("TIOCSWINSZ: %w", err)
	}
	return master, slave, nil
}
