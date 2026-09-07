//go:build !darwin && !linux

package tui

// onFatalSignal does nothing where there is no terminal to restore. See
// signal_unix.go for what it does where there is one.
func onFatalSignal(restore func()) func() { return func() {} }
