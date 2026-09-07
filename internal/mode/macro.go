package mode

import (
	"bytes"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/register"
)

// Macros: q to record, @ to play.
//
// What is recorded is the raw keys, not the commands they meant, which is why
// a macro that recorded a search replays the search rather than the place it
// landed, and why a recorded macro whose search fails stops there. That
// stopping is not an error the caller sees: the beep sets e.aborted, playKeys
// checks it after every key, and the script that was feeding the editor
// carries on. Vim does the same thing by flushing its typeahead and leaving
// the script alone.

// recordMacro is q: start recording into the register named by the next key,
// or stop the recording that is running.
func (e *Editor) recordMacro() error {
	if e.rec.reg != 0 {
		name := e.rec.reg
		// The q that stopped the recording was appended by Key on its way in
		// and is not part of the macro; a register that replayed it would stop
		// a recording that was never started.
		keys := e.rec.keys
		if n := len(keys); n > 0 {
			keys = keys[:n-1]
		}
		e.rec = recorder{}
		v := register.Char(splitLines(key.Bytes(keys))...)
		if name == register.Unnamed {
			// q" records and shows the indicator, and then vim's stuff_yank()
			// hands the text to get_yank_register('"'), which resolves to
			// y_previous: the register the last yank or delete wrote, not the
			// unnamed one. Measured on a fresh vim, "q\"xq" leaves the x in
			// register 0 and leaves the unnamed register holding the "a" the x
			// deleted. internal/register has no y_previous, so this says so
			// rather than guessing at register 0, which would be right only
			// until the first yank.
			e.rec = recorder{reg: name}
			e.finish(false)
			return ErrNotImplemented
		}
		if err := e.regs.Set(appendTarget(name), appendRecording(e, name, v)); err != nil {
			e.Say("E354: Invalid register name")
		} else {
			// vim's do_record wipes the indicator with msg("") on the way out,
			// the same call ins_esc makes leaving insert mode.
			e.msg.empty()
		}
		e.finish(false)
		return nil
	}
	e.wait = func(k key.Key) error {
		if k == keyEsc || !k.IsRune() || k.Rune > 0x7f {
			return e.abandon()
		}
		name := byte(k.Rune)
		// vim's do_record takes "registers 0-9, a-z and \"" and nothing else,
		// and a name outside that beeps with nothing on the message line:
		// nv_record calls clearopbeep() on the FAIL and never emsg(). So "q+"
		// and "q_" are silence here even though + and _ are registers a yank
		// can name, and q" records into the unnamed one. Measured.
		if name == ':' || name == '/' || name == '?' {
			// vim opens the command-line window on these three, which is a
			// window and therefore out of scope here.
			e.finish(false)
			return ErrNotImplemented
		}
		if !isRecordName(name) {
			return e.beep()
		}
		if name == register.Unnamed {
			// See the stop half above: q" is loud rather than wrong.
			e.finish(false)
			return ErrNotImplemented
		}
		e.rec = recorder{reg: name}
		e.showMode()
		e.finish(false)
		return nil
	}
	return nil
}

// appendTarget is the register a recording is written to. An uppercase name
// appends, and the appending is done here rather than by register.Set, so the
// name handed over is the lowercase one.
func appendTarget(name byte) byte {
	if name >= 'A' && name <= 'Z' {
		return name + ('a' - 'A')
	}
	return name
}

// appendRecording is what q into an uppercase register leaves behind.
//
// It is a third append rule and neither of internal/register's two. A yank
// with "A takes the new text's type when that is linewise and merges only into
// a charwise register; :let @A and setreg('A') let the new type win outright.
// A recording does neither: it keeps the OLD register's type and width and
// always merges onto the last old line. Measured -- "ayy then qAxq leaves "a
// linewise holding "AAAx", and a blockwise "a stays CTRL-V 1 and holds
// "A"/"Bx" -- where the setreg rule gives charwise "AAA"/"x" for the first and
// the yank rule gives charwise for the second.
//
// A lowercase name, and an uppercase one whose register is empty, is a plain
// write of the keys as they were recorded.
func appendRecording(e *Editor, name byte, v register.Value) register.Value {
	if name < 'A' || name > 'Z' {
		return v
	}
	old, err := e.regs.Get(appendTarget(name))
	if err != nil || len(old.Lines) == 0 {
		return v
	}
	out := old
	out.Lines = append([][]byte(nil), old.Lines...)
	last := len(out.Lines) - 1
	joined := append(append([]byte(nil), out.Lines[last]...), v.Lines[0]...)
	out.Lines[last] = joined
	out.Lines = append(out.Lines, v.Lines[1:]...)
	return out
}

// playMacro is @x and @@: the keys in a register, fed back through the editor
// count times.
func (e *Editor) playMacro(k key.Key, count int) error {
	name, ok := atRegister(k)
	if !ok {
		return e.abandon()
	}
	if name == '@' {
		if e.lastAt == 0 {
			e.Say("E748: No previously used register")
			return e.beep()
		}
		name = e.lastAt
	}
	if !register.Valid(name) {
		e.Say("E354: Invalid register name: '" + transChar(name) + "'")
		return e.beep()
	}
	v, err := e.regs.Get(name)
	if err != nil {
		e.Say("E354: Invalid register name: '" + transChar(name) + "'")
		return e.beep()
	}
	e.lastAt = name
	e.finish(false)

	keys := decodeKeys(v.Bytes())
	if len(keys) == 0 {
		return nil
	}
	// A count on @ repeats the whole macro rather than each key in it, and a
	// macro that stops on a beep stops all of the repeats with it.
	was, wasMacro, wasMapped := e.replaying, e.atMacro, e.mapReplay
	e.replaying, e.atMacro, e.mapReplay = true, true, true
	defer func() { e.replaying, e.atMacro, e.mapReplay = was, wasMacro, wasMapped }()
	for i := 0; i < count; i++ {
		if err := e.playKeys(keys); err != nil {
			return err
		}
		if e.aborted {
			return nil
		}
	}
	return nil
}

// decodeKeys turns a register's bytes back into keys. It is the same decode a
// keystroke file gets, with no timing, so a trailing Escape is an Escape and
// not the start of something longer.
func decodeKeys(b []byte) []key.Key {
	var d key.Decoder
	var keys []key.Key
	for len(b) > 0 {
		k, n, st := d.Flush(b)
		if st != key.StatusKey || n == 0 {
			break
		}
		keys = append(keys, k)
		b = b[n:]
	}
	return keys
}

// splitLines cuts a register's bytes on newlines, which is the shape a
// register.Value holds.
func splitLines(b []byte) [][]byte {
	if len(b) == 0 {
		return [][]byte{nil}
	}
	return bytes.Split(b, []byte("\n"))
}

// isRecordName reports whether q may record into this register. vim's list is
// the alphanumerics and the unnamed register, which is narrower than the set of
// names a yank can use.
func isRecordName(c byte) bool {
	switch {
	case c >= '0' && c <= '9',
		c >= 'a' && c <= 'z',
		c >= 'A' && c <= 'Z',
		c == register.Unnamed:
		return true
	}
	return false
}

// atRegister is the register name "@" reads, which is a byte and not a rune.
//
// vim reads it with plain_vgetc() and complains about whatever byte comes
// back, so "@" followed by a Tab is E354 over '^I' and not the silence a key
// with no rune in it would otherwise get here. Escape cancels, and so does
// anything that is not one byte: a named key like <Up> arrives as a multi-byte
// terminal sequence and vim gets the first byte of it, which is the Escape.
func atRegister(k key.Key) (byte, bool) {
	if k == keyEsc {
		return 0, false
	}
	if k.IsRune() && k.Mod == 0 {
		if k.Rune > 0x7f {
			return 0, false
		}
		return byte(k.Rune), true
	}
	if b := k.ToBytes(); len(b) == 1 && b[0] < 0x80 {
		return b[0], true
	}
	return 0, false
}

// transChar renders a byte the way vim's transchar() does for a message: a
// control code as a caret and a letter, everything else as itself. It is here
// because E354 is the only message in this package that prints a byte the user
// typed rather than one out of the buffer.
func transChar(c byte) string {
	if c < 0x20 {
		return "^" + string(rune(c+0x40))
	}
	if c == 0x7f {
		return "^?"
	}
	return string(rune(c))
}
