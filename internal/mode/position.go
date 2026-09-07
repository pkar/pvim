package mode

import (
	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/operator"
	"github.com/pkar/pvim/internal/text"
)

// A position handed in as a motion.
//
// Every other way into this package is a key: Key takes one, the motion table
// turns it into a walk, and the walk produces the destination an operator
// takes its span from. A mouse click has no key and no walk. It has the answer
// already -- a line and a column -- and vim uses it as a motion anyway: "d"
// and then a click three lines down deletes from the cursor to the pointer.
// This file is that entry, and it is deliberately the only thing in the
// package that takes a destination from outside.
//
// # Measured
//
// vim 9.2.0321 decodes SGR mouse reports out of a "-s" keystroke file, so a
// click is as gradeable as any other key: the report is "\x1b[<0;COL;ROWM"
// followed by the same with a lower-case "m", and vim needs a pty under it and
// 'ttymouse' set to sgr. On "alpha bravo charlie / delta echo foxtrot / golf
// hotel india / juliet kilo lima / mike november oscar / papa quebec romeo"
// with the cursor at line 3 column 3,:
//
//	d and a click at 5,5 deletes "lf hotel india\njuliet kilo lima\nmike",
//	 cursor left at 3,3
//	d and a click at 2,2 deletes "elta echo foxtrot\ngo", cursor at 2,2
//	d and a click at 3,3 deletes nothing and writes no register
//	c, y and > over the same three positions take the same text: y leaves the
//	cursor at the start, > shifts lines 3 to 5, and c opens insert mode --
//	including on the click that lands on the cursor's own cell, where the
//	empty region deletes nothing and c starts inserting anyway
//
// which is charwise EXCLUSIVE, both directions, with no rule of its own: the
// same span the same two positions would make if a key had produced them. So
// what this package needs is not a mouse, it is a destination.
//
// Three more, because each one decides a line of code:
//
// - The count is thrown away. "2d" and a click, and "d3" and a click, delete
// exactly what "d" and the same click delete. Nothing here multiplies.
// - A click past the end of a line lands one column PAST the last character
// when an operator is pending and ON the last character when one is not:
// with the cursor at 3,3, "d" and a click at column 60 of "juliet kilo
// lima" takes the whole of "lima", where a click with no operator leaves
// the cursor on the final "a". That is the operator-pending cursor
// allowance and it is the caller's to apply; text.Buffer.Clamp already
// lets a column sit at len(line), and this does not pull it back.
// - A click below the last line clamps to the last line and keeps the
// column.
//
// # What "." does with one, and what it does here
//
// vim replays the CLICK: after a "d" and a click at screen row 5 deleted three
// lines, a "gg" and a "." deleted from line 1 to screen row 5 again, which by
// then was a different part of a shorter buffer. The redo record holds the
// mouse report and re-decodes it against the screen as it is now.
//
// This editor's redo record is keys (see dot.go), a screen position is not a
// key, and inventing one would put a report into a register that a macro could
// then replay into a window of another size. So a command completed by a
// position records nothing and leaves the previous change as the one "."
// repeats. Registered here rather than guessed at: it is the one thing on this
// path that does not match vim.

// PendingOperator reports whether an operator has been typed and is waiting
// for the motion that says how much of the buffer it takes.
//
// A frontend asks because a mouse click means two different things either side
// of the answer: with nothing pending it moves the cursor, and with an
// operator pending it is that operator's motion. See MotionToPos.
func (e *Editor) PendingOperator() bool { return e.pend.op != operator.OpNone }

// MotionToPos completes the pending operator with a position instead of a key.
//
// kind is the motion kind the position stands for, which for a mouse click is
// motion.KindCharExclusive; the linewise and inclusive kinds are here because
// motion.Result carries them and an operator cannot be given a span without
// one, not because anything reaches this with them.
//
// It beeps and throws the command away when there is no operator to complete
// or when the editor is holding the next keystroke for something else, which
// is what vim does with a key that means nothing where it was typed.
func (e *Editor) MotionToPos(to text.Pos, kind motion.Kind) error {
	return e.command(func() error {
		if e.wait != nil || e.pend.op == operator.OpNone {
			return e.beep()
		}
		// The keys that made this command cannot replay it. See the note on
		// "." above; recordDot reads the flag and finish clears it.
		e.fromPos = true
		// No motion key, because there is no key: the ten motions that force a
		// short delete into "1 are named by their own character and a position
		// is none of them. See register.MotionForcesNumbered.
		return e.operateOverMotion(motion.Result{Ok: true, To: to, Kind: kind}, 0)
	})
}

// ClearOperator throws a half-typed normal-mode command away without a beep:
// the count, the register prefix, the operator and the keys typed towards one.
//
// It is vim's clearop(), and the comment above the call in do_mouse is the
// reason it is exported: "when jumping to another window, clear a pending
// operator. That's a bit friendlier than beeping and not jumping to that
// window." Measured with a ":split", a "d" typed in the top window and a click
// in the bottom one: the buffer is untouched, the cursor is where the pointer
// was, and the "w" typed after it moves a word rather than deleting one.
//
// Measured too on the prefixes, all in one window: "3" then a click then "x"
// deletes one character and not three, and so does "g" then a click then "x".
// A click discards whatever was half typed.
//
// Insert, replace and command-line mode are left alone. There is nothing to
// clear in them and the keys typed towards the insert in progress are what its
// own dot record is made of, so throwing them away would leave "." repeating
// the insert before last.
func (e *Editor) ClearOperator() {
	switch e.mode {
	case Insert, Replace, Cmdline:
		return
	}
	_ = e.abandon()
}
