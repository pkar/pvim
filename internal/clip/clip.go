// Package clip is the system clipboard: what "* and "+ read and write when
// there is a desktop behind them.
//
// It exists because internal/register may not import anything
// platform-specific. register.Clipboard is the two-method interface that
// package already expects and this package fills it, three ways: a darwin
// implementation over NSPasteboard, a linux one that says X11 is not written,
// and a memory one for tests. Nothing here is reachable from the editor core
// except through register.File.SetClipboard, which cmd/pvim calls at startup
// and which a headless run never calls at all.
//
// # Which way the call goes, and why it goes that way
//
// The pasteboard is an AppKit object and every call to it goes on the thread
// AppKit owns. That thread belongs to internal/gui. The obvious arrangement,
// clip importing gui and calling a function there, is the wrong one: gui
// imports raster and screen and drags purego behind it, so a core package that
// wanted a clipboard would pull the whole window into its dependency graph, the
// linux build would stop being a matter of one directory, and internal/gui
// would have to grow an import of internal/register to hand a Value back.
//
// So the arrow is inverted. This package talks to NSPasteboard itself, through
// purego, the same way internal/gui talks to AppKit -- internal/deps_test.go
// has allowed purego and unsafe in exactly these two packages --
// and it takes the thread it must run on as a Runner, a plain func value.
// cmd/pvim passes gui.OnMain and the two packages never meet. On a box with no
// window there is no Runner to pass, no clipboard is installed, and "* and "+
// are ordinary registers, which is what every oracle run wants.
//
// # What the pasteboard carries, and what it loses
//
// A register is text plus a type: charwise, linewise, or a block with a width.
// A macOS pasteboard string is text. vim's answer, on macOS, is a second
// pasteboard type of its own beside the string, named "VimPboardType", holding
// a plist of [motion, text] where motion is 0, 1 or 2 for charwise, linewise
// and blockwise. That is not read out of a header: "VimPboardType" is in the
// strings of the same /opt/homebrew/bin/vim the oracle runs, and every rule
// this package follows was taken by putting a plist on the general pasteboard
// and asking that vim for getregtype('*'). motion.go carries the table.
//
// So a yank in pvim pastes into vim as the type it was yanked as, and back.
// What is still lost, both ways and in vim too:
//
// - The width of a blockwise value, which the plist does not carry. It is
// recomputed from the widest line on the way in, which is what vim does.
// - ToEOL, the mark of a block yanked with $. vim does not put it on the
// board either, so a $ block pasted back is an ordinary block in both.
// - Everything, when the copy came from an application that is not vim.
// Then there is no plist and the rule is the one vim uses for a foreign
// selection: text ending in a newline is linewise and anything else is
// charwise, which is why pbcopy of a whole line and a shell prompt's worth
// of output behave differently under p.
//
// # autoselect is a call and not a hook
//
// 'clipboard' contains "autoselect" in the vimrc, which means every cursor
// motion in visual mode writes the selection to the pasteboard. That is one
// Write per keystroke and it is what MacVim does. The write is an ordinary call
// the mode layer makes -- internal/mode's autoSelect calls
// register.File.SetSelection, which reaches Write here -- rather than a
// callback this package registers, so a test can hand the editor a Memory and
// count the writes without a window server anywhere near it.
package clip

import (
	"bytes"
	"errors"

	"github.com/pkar/pvim/internal/register"
)

// Clipboard is what this package provides. It is an alias rather than a second
// declaration so that the two can never drift: a caller holds one interface
// type whether it got it from here or named it in internal/register.
type Clipboard = register.Clipboard

// Runner runs fn on the thread the platform's clipboard has to be touched from
// and returns when fn has returned.
//
// It is a func value and not an interface so that this package needs no import
// to describe it. On darwin cmd/pvim passes gui.OnMain; a test passes
// func(fn func()) error { fn(); return nil }, which is correct because a test
// has no run loop to be on the wrong side of.
//
// Two rules, both of which a caller can get wrong quietly. It must not be
// called from the run loop thread itself: OnMain blocks until the work has run
// and the work cannot run while the thread that would run it is inside this
// call. And it must return an error rather than block forever when the run loop
// is gone, so that a clipboard read during shutdown fails instead of wedging
// the editor.
type Runner func(fn func()) error

// The errors this package returns. Both are sentinels: a caller decides
// whether to put an E-code on the message line by asking which one it got, not
// by matching a sentence.
var (
	// ErrUnsupported is a platform with no clipboard implementation. cmd/pvim
	// installs no clipboard on it and "* and "+ stay ordinary registers, which
	// is a working editor with no desktop integration rather than a broken one.
	ErrUnsupported = errors.New("clip: no clipboard on this platform")

	// ErrNoRunner is New called with a nil Runner on a platform that needs one.
	// It is separate from ErrUnsupported because the fixes are different: this
	// one is a wiring mistake in cmd/pvim and that one is the box.
	ErrNoRunner = errors.New("clip: this clipboard needs a main-thread runner")

	// ErrNoPasteboard is a macOS with no pasteboard to talk to. The general
	// pasteboard lives in another process that belongs to a login session, so
	// AppKit hands back nil for it in a session that has none: a build box, or
	// an ssh into a Mac whose console nobody is logged in at.
	//
	// It is an error rather than an empty register because messaging nil in
	// Objective-C is not a crash, it is nil back, so without this check every
	// yank would report success and every put would paste nothing, which is
	// the one shape of clipboard bug that takes a week to notice.
	ErrNoPasteboard = errors.New("clip: no general pasteboard in this session")

	// ErrNotUTF8 is a value the pasteboard cannot be given: text with a byte
	// sequence that is not valid UTF-8 in it.
	//
	// A macOS pasteboard string is an NSString and an NSString is not bytes, so
	// there is nothing to hand it. -[NSString initWithBytes:length:encoding:]
	// answers nil for input it cannot decode, and setString: with that nil
	// raises an Objective-C exception, which in a Go process with no
	// @catch anywhere is the editor gone. So the check is here, in Go, before
	// the trip, and the board is left holding whatever it held: a yank that
	// cannot reach the clipboard must not also destroy what was on it.
	//
	// vim does not meet this. Its 'encoding' is utf-8 and 'fileencodings' has
	// latin1 in it, so a latin-1 file is converted on the way into the buffer
	// and what reaches the pasteboard is always valid: measured, a file holding
	// the byte 0xe9 yanked with "*yy puts the bytes 63 61 66 c3 a9 on the
	// board. pvim's buffer is bytes, so it can hold what vim's cannot.
	ErrNotUTF8 = errors.New("clip: the text is not valid UTF-8")
)

// Bytes flattens a register value into what a pasteboard holds.
//
// It is register.Value.Bytes, named here because it is half of the conversion
// and a reader looking for the other half should find both in one place: a
// linewise value keeps its trailing newline, and that newline is the only thing
// that survives the round trip to say it was linewise.
func Bytes(v register.Value) []byte { return v.Bytes() }

// ValueOf turns flat clipboard bytes into a register value.
//
// Vim's rule for text arriving from outside, which is not a guess: a selection
// that ends in a newline is linewise and everything else is charwise. So
// "alpha\n" pastes as a whole line and "alpha" pastes into the middle of one,
// which is why pbcopy of a whole line and a shell prompt's worth of output
// behave differently under p and both behave the way they do in vim.
//
// Empty input is an empty value, which reads as a register nothing has written
// rather than as one holding an empty line.
func ValueOf(b []byte) register.Value {
	if len(b) == 0 {
		return register.Value{}
	}
	if b[len(b)-1] == '\n' {
		return register.LineValue(split(b[:len(b)-1])...)
	}
	return register.Char(split(b)...)
}

// split cuts text into the lines a register holds, which carry no terminator.
//
// A carriage return before a newline goes with it, and that is vim's behaviour
// and not a tidying-up of it. Measured: `printf 'alpha\r\nbeta\r\n' |
// pbcopy` leaves the CR bytes on the board -- `pbpaste | od -c` shows them --
// and /opt/homebrew/bin/vim then answers getreg('*') of "alpha\nbeta\n" and
// pastes two clean lines with no ^M in the buffer. So the stripping happens
// somewhere in vim and not in the pasteboard, and this is where it happens
// here.
func split(b []byte) [][]byte {
	lines := bytes.Split(b, []byte("\n"))
	for i, line := range lines {
		lines[i] = bytes.TrimSuffix(line, []byte("\r"))
	}
	return lines
}
