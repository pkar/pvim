module github.com/pkar/pvim

go 1.26

require (
	github.com/ebitengine/purego v0.11.0
	// The X11 backend. Pure Go, no dependencies of its own, and the
	// only package in the tree that speaks the X protocol: internal/gui/x11
	// for the window and internal/clip for the selections, both behind
	// //go:build linux, so the darwin build never compiles a line of it.
	github.com/jezek/xgb v1.3.1
	golang.org/x/image v0.45.0
	golang.org/x/sys v0.47.0
)

// Not imported by any file in this module. x/image/font/sfnt reads the cmap
// through x/text/encoding/charmap, so x/image's own require lands here and the
// build cannot resolve without it.
require golang.org/x/text v0.41.0 // indirect
