package mode

import (
	"strings"

	"github.com/pkar/pvim/internal/operator"
)

// The message line, and the byte stream vim's `:redir` catches off it.
//
// A frontend wants the last thing said. cmd/oracle wants something harder: the
// exact bytes `:redir! > file` collected while the script ran, because that is
// what it diffs against pvim's msgs.txt. Those are not the same object. vim's
// redirect is a stream of what was written to the message line, and the
// framing of that stream is not "one message per line":
//
// - an ordinary message writes a newline and then its text, because msg_start
// moves to a fresh line when anything has been written already, and
// something always has: vim announces the file it opened before the
// redirect is even on;
// - showmode() writes its text with no newline at all, which is why
// `i CTRL-O dw` leaves "-- INSERT ---- (insert) ---- INSERT --" run
// together in the redirect;
// - leaving insert or replace mode writes a bare newline, because ins_esc
// ends with msg("") to wipe the mode message, and so does the q that stops
// a recording;
// - a message vim marked as worth keeping is written a second time, because
// the redraw after the command redisplays keep_msg and then clears it. Only
// some messages are: "3 fewer lines" is, "3 lines yanked" is not, and an
// error clears the kept message rather than setting one.
//
// All four were measured against vim 9.2.0321 through cmd/oracle rather than
// read out of the source, and each one of them is a byte on 130 of the 504
// runs.
type messages struct {
	// redir is the stream, exactly as :redir would have caught it.
	redir strings.Builder
	// log is one entry per message, which is what a statusline wants and what
	// a test reads.
	log []string
	// keep is the message a redraw would put back on the line, empty when the
	// last thing said was an error or an ordinary message. vim's keep_msg.
	keep string
	// scrolled is vim's msg_scroll: a prompt has written where the message
	// line was and the next message goes under it rather than over it. While
	// it is set nothing is kept across the redraw, because msgmore() and the
	// substitute report both test it before calling set_keep_msg. It is why
	// ":4,2d" answered "y" says "3 fewer lines" once where ":2,4d" says it
	// twice.
	scrolled bool
	// writes counts everything that reached the line. Editor.Key compares it
	// across a command to answer "did this command say anything", which is one
	// of the three things that make vim redisplay the mode message.
	writes int
}

// say is an ordinary message: vim's msg(). It clears the kept message, because
// msg_start() sets keep_msg to NULL before anything is printed.
func (m *messages) say(text string) {
	m.redir.WriteByte('\n')
	m.redir.WriteString(text)
	m.log = append(m.log, text)
	m.keep = ""
	m.writes++
}

// sayKeep is a message the redraw after the command puts back: vim's
// set_keep_msg, which msgmore(), the undo report, the shift report and
// give_warning() all call and which nothing else does.
func (m *messages) sayKeep(text string) {
	m.say(text)
	if !m.scrolled {
		m.keep = text
	}
}

// raw writes text into the stream with no framing at all: no leading newline,
// no trailing one. It is what a message that is not a line needs -- the ":!"
// echo, which vim ends with a carriage return rather than a newline -- and it
// clears the kept message the same way an ordinary one does, because vim's
// msg_start does that before anything reaches the screen.
func (m *messages) raw(text string) {
	m.redir.WriteString(text)
	m.log = append(m.log, text)
	m.keep = ""
	m.writes++
}

// prompt is a question written on the message line: the text goes out like an
// ordinary message and the line is marked as scrolled, so that whatever the
// command goes on to say is not kept for a redraw that would put it under the
// answer.
func (m *messages) prompt(text string) {
	m.say(text)
	m.scrolled = true
}

// answer is the key that answered a prompt, echoed where the cursor is with no
// newline of its own. vim prints it as part of the prompt line, which is why
// the redirect holds "(y/n)?y" and not the two on separate lines.
func (m *messages) answer(c byte) {
	m.redir.WriteByte(c)
	m.writes++
}

// empty is vim's msg(""), which writes nothing but the newline that moves off
// the line it is wiping. ins_esc calls it on the way out of insert mode and
// do_record calls it when q stops a recording.
func (m *messages) empty() {
	m.redir.WriteByte('\n')
	m.keep = ""
	m.scrolled = false
	m.writes++
}

// mode is showmode(): the mode message and the recording indicator, written
// where the cursor already is and with no newline of their own. It is
// deliberately not counted as a write and does not touch the kept message:
// showmode is the redraw, not something the redraw is reacting to.
func (m *messages) mode(text string) {
	m.redir.WriteString(text)
	m.log = append(m.log, text)
}

// redisplay is the "display message after redraw" step of vim's main loop: the
// kept message goes out once more and is then forgotten, which is why
// "3 fewer lines" appears twice and "3 fewer lines" after another command does
// not appear a third time.
func (m *messages) redisplay() {
	if m.keep == "" {
		return
	}
	m.redir.WriteByte('\n')
	m.redir.WriteString(m.keep)
	m.keep = ""
	m.writes++
}

// Redirect is the message stream, which is what cmd/pvim's --oracle mode
// writes to msgs.txt. The trailing newline `:redir END` itself writes is not
// here: it belongs to the redirect being closed and not to anything the editor
// said.
func (e *Editor) Redirect() []byte { return []byte(e.msg.redir.String()) }

// showMode is vim's showmode(): whatever the message line should be saying
// about the mode right now, plus the recording indicator, or nothing at all
// when there is nothing to say.
//
// Nothing at all is the important half. In normal mode with no recording
// running it writes no bytes, which is why `v j d` leaves "-- VISUAL --" in
// the redirect and not "-- VISUAL --" followed by a blank line: the redraw
// after the d clears the mode message on the screen, and clearing a screen line
// is not something a redirect can see.
//
// It says nothing at all while a macro is replaying: that is vim's
// skip_showmode(), and see Editor.atMacro for what it costs to get wrong.
func (e *Editor) showMode() {
	if e.atMacro {
		return
	}
	s := e.modeMessage()
	if e.rec.reg != 0 {
		s += "recording @" + string(rune(e.rec.reg))
	}
	if s != "" {
		e.msg.mode(s)
	}
}

// modeMessage is the "-- SOMETHING --" half of showmode.
func (e *Editor) modeMessage() string {
	switch e.mode {
	case Insert:
		if e.comp.active {
			// vim's showmode prints edit_submode instead of the mode name
			// while a CTRL-X completion is running, which is why the redirect
			// holds "-- Keyword completion (^N^P) match 1 of 2" and not
			// "-- INSERT --" for those keystrokes.
			return e.comp.message()
		}
		return insertMessage
	case Replace:
		return replaceMessage
	case VisualChar, VisualLine, VisualBlock:
		if e.ins.oneShot {
			// A visual mode entered from insert mode's CTRL-O keeps saying so:
			// vim's showmode prints " (insert)" from restart_edit before the
			// visual name, giving "-- (insert) VISUAL --". Measured for all
			// three, LINE and BLOCK included.
			return "-- (insert) " + strings.TrimPrefix(visualMessage(e.mode), "-- ")
		}
		return visualMessage(e.mode)
	}
	// CTRL-O left insert mode for one command and vim keeps saying so: its
	// restart_edit is set, which showmode reports as this and not as normal
	// mode.
	if e.ins.oneShot {
		return oneShotMessage
	}
	return ""
}

// sayResult puts an operator's report on the message line, kept across the
// redraw when the operator said it was one of vim's set_keep_msg messages.
func (e *Editor) sayResult(res operator.Result) {
	switch {
	case res.Message == "":
	case res.Keep:
		e.msg.sayKeep(res.Message)
	default:
		e.msg.say(res.Message)
	}
}

// AdoptMessages takes over the message stream of the editor this one is
// replacing.
//
// A buffer swap builds a new Editor, and the redirect stream lives on the old
// one. Without this the stream splits in two: the ex layer's own Context.Msg
// stays bound to the editor the session started with while everything after the
// swap writes to the new one, so a ":new" followed by a ":q" loses the blank
// line vim leaves behind and cmd/oracle grades the difference.
//
// The builder's bytes are copied out rather than the builder itself: a
// strings.Builder panics if it is copied by value after anything has been
// written to it.
func (e *Editor) AdoptMessages(from *Editor) {
	if from == nil || from == e {
		return
	}
	// Reset and rewrite rather than build a new one and assign it: a
	// strings.Builder records the address it was first written at and panics
	// on the next write if it has been copied since, so "e.msg.redir = b" is a
	// panic one message later rather than an error here.
	if old := from.msg.redir.String(); old != "" {
		combined := old + e.msg.redir.String()
		e.msg.redir.Reset()
		e.msg.redir.WriteString(combined)
	}
	e.msg.log = append(append([]string(nil), from.msg.log...), e.msg.log...)
	e.msg.keep = from.msg.keep
	e.msg.scrolled = from.msg.scrolled
	e.msg.writes += from.msg.writes

	// The old editor keeps nothing: it is about to be dropped, and a second
	// writer to a stream that has been handed over is how a message ends up in
	// the artifact twice.
	from.msg = messages{}
}
