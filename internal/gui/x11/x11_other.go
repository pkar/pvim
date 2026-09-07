//go:build !linux

package x11

import (
	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/raster"
)

// Everywhere that is not Linux, which for this project means macOS.
//
// This file exists for the same reason internal/gui/gui_other.go does, with the
// arrow the other way round: every package has to build on both platforms so
// that the tree cross-compiles and so that a failure in one frontend costs a
// window rather than the editor. It imports nothing but the event vocabulary,
// so a darwin build of this package is the platform-independent half -- the
// keysym table, the font search, the pixel conversion, the chunking -- and none
// of xgb at all.
//
// That is also what makes the platform-independent half testable here. Those
// four files carry no build tag on purpose: they are the part of an X11
// frontend that can be checked on a machine with no X server, and on this
// machine there is no X server, so being able to check them is the difference
// between tested code and code that compiled.

// run refuses: there is no X server on this platform and no backend compiled
// in to talk to one. cmd/pvim reads it as "try the other frontend".
func run(h gui.Handler) error { return ErrNoBackend }

// available is false. Nothing here can open a window.
func available() bool { return false }

// onMain and postMain refuse rather than panicking, so a clipboard call that
// reached here on the wrong platform is an error a long way nearer its cause
// than a nil dereference would be.
func onMain(fn func()) error   { return ErrNoEventLoop }
func postMain(fn func()) error { return ErrNoEventLoop }

// setFont and setLinespace refuse, so a `:set guifont=` reports what it did.
func setFont(f raster.GUIFont) error { return ErrNoBackend }
func setLinespace(n int) error       { return ErrNoBackend }

// inForce has no window to answer for.
func inForce() (raster.GUIFont, bool) { return raster.GUIFont{}, false }
