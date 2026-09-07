//go:build unix

package lsp

import (
	"os/exec"
	"syscall"
)

// The child process's group, on the platforms that have one.
//
// syscall and not golang.org/x/sys/unix: Setpgid and Kill are in the standard
// library on every unix, they are two calls, and this package is the wrong
// place to grow a dependency the static gate would then have to reason about.
// Nothing here is behind cgo.

// setProcessGroup puts the server in a process group of its own.
//
// Without it the server is in the editor's group, and a CTRL-C typed in the
// terminal pvim was started from goes to both. What that looks like from the
// editor is every later request failing with a broken pipe and no message,
// because the signal killed a process the client has no reason to think is
// dead. With it, the interrupt reaches the editor alone and the server is
// taken down by Close, which is the one path that should ever end it.
//
// It is also what makes killGroup safe: killing a negative pid kills a group,
// and doing that to a group the editor is itself in would kill the editor.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup is the last resort in Close: SIGKILL to the server's whole group,
// so that a gopls which has spawned a "go list" of its own does not leave the
// child behind when the parent goes.
func killGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		// No group, which happens when setProcessGroup did not run or the
		// child had already been reaped. The process alone is still worth a
		// signal.
		_ = cmd.Process.Kill()
	}
}
