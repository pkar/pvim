package tui

// The control sequences this package writes, in one place, each with the name
// the terminal documentation gives it.
//
// Every one of these was read off a real vim rather than out of a terminfo
// database: `TERM=xterm-256color script -q log vim --clean -s keys file` and
// then `cat -v log` shows vim 9.2.0321 opening with
//
//	\x1b[?1006;1000h \x1b[?1002h \x1b[?1049h ... \x1b[?2004h ... \x1b[22;2t
//
// and closing with
//
//	\x1b[?1006;1000l \x1b[?1002l \x1b[?2004l \x1b[23;2t ... \x1b[?1049l \x1b[?25h
//
// so the set is vim's set and the order out is vim's order. It also shows vim
// bracketing each repaint in \x1b[?25l and \x1b[?25h, which is what Render
// does and why the cursor never trails a frame across the screen.
const (
	// seqAltScreenOn switches to the alternate screen buffer so that the
	// shell's scrollback is still there after:q.
	seqAltScreenOn  = "\x1b[?1049h"
	seqAltScreenOff = "\x1b[?1049l"

	// seqPasteOn turns on bracketed paste. Without it a pasted "jj" leaves
	// insert mode and a pasted tab triggers completion.
	seqPasteOn  = "\x1b[?2004h"
	seqPasteOff = "\x1b[?2004l"

	// seqMouseOn is the three-part incantation vim sends: 1000 is click
	// reporting, 1002 adds drag, 1006 asks for the SGR encoding, which is the
	// only one that can report a column past 223.
	seqMouseOn  = "\x1b[?1006;1000h\x1b[?1002h"
	seqMouseOff = "\x1b[?1006;1000l\x1b[?1002l"

	// seqTitlePush and seqTitlePop save and restore the terminal's window and
	// icon titles, so that a shell whose tab said "~/work" still says it
	// after:q rather than saying "main.go (internal/tui) - pvim" forever.
	// The capture shows vim sending both halves, 2 for the window title and
	// 1 for the icon, on the way in and on the way out.
	seqTitlePush = "\x1b[22;2t\x1b[22;1t"
	seqTitlePop  = "\x1b[23;2t\x1b[23;1t"

	seqCursorHide = "\x1b[?25l"
	seqCursorShow = "\x1b[?25h"

	// seqWrapOff turns autowrap off for as long as the editor owns the
	// screen. Every cell this package writes is preceded by an absolute
	// cursor position, so wrapping can only ever do harm: writing the
	// bottom-right cell with autowrap on scrolls the whole screen up by one
	// line on most terminals, which is a frame of garbage and a lost row.
	// Vim solves the same problem by never writing that cell; this editor
	// draws a full grid and turns the feature off instead.
	seqWrapOff = "\x1b[?7l"
	seqWrapOn  = "\x1b[?7h"

	// seqReset is SGR 0: back to the terminal's own colours and no
	// attributes.
	seqReset = "\x1b[0m"

	// seqCursorDefault is DECSCUSR 0, the shape the terminal started with.
	seqCursorDefault = "\x1b[0 q"

	// seqClear homes the cursor and erases the screen, which Start does once
	// so the first frame is painted onto a known background rather than onto
	// whatever the alternate screen last held.
	seqClear = "\x1b[H\x1b[2J"

	// seqTitleStart and seqTitleEnd bracket OSC 2. The terminator is BEL
	// rather than ST because every terminal accepts BEL and a few old ones
	// mishandle ST.
	seqTitleStart = "\x1b]2;"
	seqTitleEnd   = "\a"
)

// pasteStart and pasteEnd bracket a bracketed paste. They are not decoded by
// internal/key: "\x1b[200~" is a well-formed CSI with no key behind it, so the
// decoder would hand back <Ignore> and the pasted text would arrive as
// keystrokes with mappings applied. The reader takes them out of the stream
// before the decoder ever sees them.
// csiPrivate opens a private-mode CSI: a set, a reset, or a report answering
// one. Everything this package writes with it is a set or a reset; what comes
// back the other way is filtered out of the input in input.go.
var csiPrivate = []byte("\x1b[?")

var (
	pasteStart = []byte("\x1b[200~")
	pasteEnd   = []byte("\x1b[201~")
)
