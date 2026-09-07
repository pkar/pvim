//go:build !darwin && !linux

package clip

// Everywhere else: no clipboard, and this file is what keeps the package
// compiling on a platform nobody has thought about. The linux file says what
// filling one in would take; on a platform this editor has never been built
// for there is nothing to say beyond the refusal.

// New always fails here.
func New(run Runner) (Clipboard, error) {
	return nil, ErrUnsupported
}
