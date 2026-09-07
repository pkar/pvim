//go:build darwin

package main

import (
	"bytes"
	"testing"

	"golang.org/x/sys/unix"
)

// TestPTYRoundTrip checks the one assumption the darwin pty helper makes: that
// the slave named by the master's minor number is the master's slave. If the
// mapping is ever wrong the open still succeeds, on somebody else's terminal,
// and every run afterwards is a mystery. Bytes written to one end coming out of
// the other is the only proof that means anything.
func TestPTYRoundTrip(t *testing.T) {
	master, slave, err := openPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()

	// A newline is not decoration: the line discipline starts in canonical
	// mode and a read on the slave blocks until it sees one.
	const msg = "dw\n"
	if _, err := master.WriteString(msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := slave.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	// The line discipline echoes and translates, so this is a containment check
	// and not equality. Containment is enough: it can only hold if the two ends
	// are the same terminal.
	if !bytes.Contains(buf[:n], []byte("dw")) {
		t.Errorf("wrote %q to the master, read %q from the slave", msg, buf[:n])
	}
}

// TestPTYHasTheFixedWindowSize pins the terminal both editors are handed.
// Whether vim wraps a message, and so whether it stops for a hit-enter prompt
// that eats the next keystroke, depends on the width.
func TestPTYHasTheFixedWindowSize(t *testing.T) {
	_, slave, err := openPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer slave.Close()

	ws, err := unix.IoctlGetWinsize(int(slave.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Row != ptyRows || ws.Col != ptyCols {
		t.Errorf("pty is %dx%d, want %dx%d", ws.Col, ws.Row, ptyCols, ptyRows)
	}
}
