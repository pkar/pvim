//go:build unix

package server

import (
	"fmt"
	"os"
	"syscall"
)

// ownedByCaller reports whether name belongs to the user running this process.
//
// Not "belongs to root or to us", which is the shape ssh uses for its config
// files: a directory this process cannot write is a directory it cannot bind a
// socket in either, so allowing root here would only move the failure to a
// worse error message.
func ownedByCaller(name string, fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("server: cannot tell who owns %s on this system", name)
	}
	if uid := os.Getuid(); int(st.Uid) != uid {
		return fmt.Errorf("server: %s is owned by uid %d and this process is uid %d", name, st.Uid, uid)
	}
	return nil
}
