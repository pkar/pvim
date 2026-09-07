//go:build darwin || linux

package tui

import (
	"os"
	"os/signal"
	"sync"

	"golang.org/x/sys/unix"
)

// fatalSignals are the ones that kill the process while it owns the terminal.
//
// SIGINT and SIGQUIT are not here: raw mode turns ISIG off, so CTRL-C and
// CTRL-\ arrive as the bytes 0x03 and 0x1c and become keystrokes, which is
// what vim does and what the vimrc's terminal mappings assume. SIGTERM is a
// kill from another process and SIGHUP is the terminal itself going away, and
// both leave the tty in raw mode unless something puts it back.
var fatalSignals = []os.Signal{unix.SIGTERM, unix.SIGHUP}

// onFatalSignal arranges for restore to run before the process dies of one of
// those, and returns the call that unsubscribes.
//
// It restores and then re-raises with the handler removed, rather than calling
// os.Exit: a process killed by SIGTERM should be reported as killed by
// SIGTERM, because that is what the shell, the supervisor and "make" all read,
// and exiting with a status of our own invention loses that.
func onFatalSignal(restore func()) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, fatalSignals...)
	done := make(chan struct{})

	go func() {
		select {
		case sig := <-ch:
			restore()
			signal.Stop(ch)
			signal.Reset(sig)
			_ = unix.Kill(unix.Getpid(), sig.(unix.Signal))
		case <-done:
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			signal.Stop(ch)
			close(done)
		})
	}
}
