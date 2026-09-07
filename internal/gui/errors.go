package gui

import "errors"

// ErrNoBackend is what Run returns on a platform this package has no window
// code for. It is a sentinel so that cmd/pvim can fall back to the terminal
// frontend on it and report anything else as a real failure.
var ErrNoBackend = errors.New("gui: no GUI backend on this platform")

// ErrAlreadyRunning is returned by a second call to Run in one process. There
// is one NSApplication, one main thread and one registered view class, and the
// class registration in particular is not repeatable.
var ErrAlreadyRunning = errors.New("gui: Run has already been called in this process")

// ErrNoMainThread is OnMain or PostMain called when there is no run loop to get
// onto: Run has not been called yet, or it has already returned. It is a
// sentinel rather than a blocked call because the caller is usually
// internal/clip on the editor goroutine, and an editor that hangs on a "+y
// during shutdown is worse than one that says the yank did not reach the
// pasteboard.
var ErrNoMainThread = errors.New("gui: no main thread to run on")

// errSourceCreate is CFRunLoopSourceCreate returning NULL, which means the
// context struct was rejected. It has no cause worth reporting beyond itself.
var errSourceCreate = errors.New("gui: CFRunLoopSourceCreate returned NULL")
