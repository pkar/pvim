//go:build !darwin

package main

import (
	"errors"
	"os"
)

// openPTY is not implemented away from darwin. The reference vim this harness
// diffs against lives at /opt/homebrew/bin/vim, so the oracle has never run
// anywhere else; the darwin file says why a terminal is needed at all, and a
// linux version is /dev/ptmx plus TIOCSPTLCK and TIOCGPTN, both of which
// x/sys/unix already wraps without unsafe. It is not written because it has
// never been run, and a pty helper nobody has exercised is worse than an error
// that names itself.
func openPTY() (master, slave *os.File, err error) {
	return nil, nil, errors.New("the oracle needs a pty and only darwin is implemented")
}
