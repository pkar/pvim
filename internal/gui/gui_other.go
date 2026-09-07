//go:build !darwin

package gui

import "github.com/pkar/pvim/internal/raster"

// Everywhere that is not macOS. The window code in this package is AppKit and
// there is no second backend in it; the X11 one is a peer package under
// internal/gui/x11, and this file is what keeps GOOS=linux building here.
//
// That matters more than it looks. Every package below this one is meant to be
// testable on a box with no window server, and the way that stays true is that
// the whole tree cross-compiles for Linux, in CI or on a whim, without dragging
// purego and objc behind it. This file imports nothing at all, so a Linux build
// of internal/gui has an empty dependency graph beyond the shared api.go.

// run is the not-darwin implementation of Run: there is no backend, and the
// caller is expected to fall back to the terminal frontend, which is a peer of
// this package and not a lesser version of it.
func run(h Handler) error {
	return ErrNoBackend
}

// available is the not-darwin implementation of Available. There is no window
// to be had here, and cmd/pvim reads that as "use the terminal" rather than as
// a failure.
func available() bool { return false }

// onMain and postMain are the not-darwin main-thread marshaller: there is no
// run loop and nothing to marshal to. internal/clip's New refuses on this
// platform anyway, so nothing should ever reach these, and they answer rather
// than panicking because a nil-pointer crash on a Linux box would be a very
// long way from the mistake that caused it.
func onMain(fn func()) error   { return ErrNoMainThread }
func postMain(fn func()) error { return ErrNoMainThread }

// setFont and setLinespace are the not-darwin implementations of SetFont and
// SetLinespace. There is no window whose layout could change, and the editor
// on this platform is the terminal frontend, which gets its cell size from the
// terminal and not from a font. They refuse rather than pretending to succeed,
// so that a `set guifont=` in a vimrc on Linux reports what it did.
func setFont(f raster.GUIFont) error { return ErrNoBackend }
func setLinespace(n int) error       { return ErrNoBackend }
