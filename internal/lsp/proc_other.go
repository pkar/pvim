//go:build !unix

package lsp

import "os/exec"

// A platform with no process groups. Neither backend of this editor runs on
// one; the file exists so that "GOOS=windows go build ./..." says
// nothing rather than failing on two missing functions, which is one less
// thing between here and a port nobody has asked for.

func setProcessGroup(cmd *exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
