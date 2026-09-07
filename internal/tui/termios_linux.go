package tui

import "golang.org/x/sys/unix"

// See termios_darwin.go. Linux spells the same two ioctls TCGETS and TCSETS.
const (
	ioctlReadTermios  = unix.TCGETS
	ioctlWriteTermios = unix.TCSETS
)
