package main

import (
	"fmt"
	"strings"

	"github.com/pkar/pvim/internal/ex"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/mode"
)

// The ":" prompt.
//
// internal/mode owns "/" and "?" because a search is a motion and has to reach
// the same operator path a w does. It does not own ":", and it should not: a
// colon line is an ex command, and internal/ex is the layer that runs one.
// Neither of them can own the prompt itself, so the frontend does, which is
// this file.
//
// TODO: the editing keys below are the ones a line needs to
// be usable, and internal/ex.Line.Key is where they belong -- it is the type
// that already holds the text, the cursor and the history index, and it is the
// one place wildmenu completion can hang off. When that method exists this
// loop becomes a call to it and the history and CTRL-D completion arrive with
// it. It is written here rather than left waiting because a frontend with no
// colon line cannot be typed into at all, and the gate is a working day
// of typing into it.

// startCmdline opens the prompt, with whatever prefix the count or the visual
// selection puts in front of it.
//
// The two prefixes are vim's and are not cosmetic. In visual mode ":" gives
// ":'<,'>", which is how the vimrc's own JsonPretty is reached from a
// selection. A count gives ":.,.+{count}-1", so "3:" is three lines starting
// here.
func (s *session) startCmdline() error {
	prefix := ""
	visual := false
	switch s.ed.Mode() {
	case mode.VisualChar, mode.VisualLine, mode.VisualBlock:
		prefix, visual = "'<,'>", true
	default:
		if n := showcmdCount(s.ed.Showcmd()); n > 1 {
			prefix = fmt.Sprintf(".,.+%d", n-1)
		} else if n == 1 {
			prefix = "."
		}
	}
	// The mode machine has to leave visual mode and drop the count, and
	// Escape is what does both. Unconditionally in visual mode and not only
	// when something is half typed: leaving is what sets the '< and '> the
	// prefix above has just promised, and a prompt opened over a selection
	// still live resolves that range against marks nobody has written and
	// answers E20.
	if visual {
		if err := s.ed.Key(key.Key{Special: key.KeyEsc}); err != nil {
			return err
		}
	}
	s.clearCount()
	s.line = &ex.Line{Prefix: ':', Text: []byte(prefix), Pos: len(prefix), HistIdx: -1}
	return nil
}

// cmdlineKey feeds one keystroke to the open prompt.
func (s *session) cmdlineKey(k key.Key) error {
	l := s.line
	switch {
	case k.Special == key.KeyEsc:
		s.line = nil
		return nil
	case k.Special == key.KeyCR || k.Special == key.KeyNL:
		line := l.String()
		s.line = nil
		return s.runLine(line)
	case k.Special == key.KeyBS || k == key.Ctrl('h'):
		if len(l.Text) == 0 {
			// Backspace on an empty line abandons it. This is the one that
			// surprises people and it is vim's.
			s.line = nil
			return nil
		}
		i := prevRune(l.Text, l.Pos)
		l.Text = append(l.Text[:i], l.Text[l.Pos:]...)
		l.Pos = i
		return nil
	case k == key.Ctrl('u'):
		l.Text, l.Pos = l.Text[:0], 0
		return nil
	case k == key.Ctrl('w'):
		i := wordStart(l.Text, l.Pos)
		l.Text = append(l.Text[:i], l.Text[l.Pos:]...)
		l.Pos = i
		return nil
	case k == key.Ctrl('r'):
		// CTRL-R takes a register name next. The vimrc's insert-mode paste is
		// CTRL-R CTRL-O +, and the CTRL-O form is not here: it changes how the
		// text is inserted and not which register it comes from, and on a
		// command line the two are the same thing.
		r, ok := s.next()
		if !ok || !r.IsRune() || r.Rune > 0x7f {
			return nil
		}
		v, err := s.ed.Registers().Get(byte(r.Rune))
		if err != nil {
			return nil
		}
		// A register holding line breaks pastes them as carriage returns,
		// which is what vim shows on the command line and what keeps one
		// command on one line.
		text := strings.ReplaceAll(string(v.Bytes()), "\n", "\r")
		l.Text = append(l.Text[:l.Pos], append([]byte(text), l.Text[l.Pos:]...)...)
		l.Pos += len(text)
		return nil
	case k.IsRune():
		b := []byte(string(k.Rune))
		l.Text = append(l.Text[:l.Pos], append(b, l.Text[l.Pos:]...)...)
		l.Pos += len(b)
		return nil
	}
	return nil
}

// runLine runs an accepted command line and puts whatever it said on the
// message line.
//
// An error is a message and not a stop. That is vim: a bad command prints its
// E-code and the editor carries on, and an editor that exited because of a typo
// on the colon line would be unusable. The one thing that does stop the loop is
// a quit, and that arrives through ex.Context's Quit callback and not through
// here.
func (s *session) runLine(line string) error {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	if s.ctx == nil {
		return nil
	}
	// The newline the accepted command line leaves behind, before anything the
	// command says. vim writes it because the message about to be printed
	// cannot share the line the command was typed on, and :redir catches it:
	// every ":" case in testdata/keys has it as its first byte.
	s.ed.NewMessageLine()
	// The two command lines cmd/pvim/finder.go answers before the ex layer
	// sees them: ":NERDTreeToggle", which internal/ex has no way to define a
	// Go body for, and ":e <dir>", which internal/ex answers with E484
	// because openFile reads the name. Both are reported there; this is three
	// lines and it comes out when the hook exists.
	if took, perr := pluginCommand(s, line); took {
		s.sync()
		if perr != nil {
			s.ed.Say(perr.Error())
		}
		s.ed.Redisplay()
		return nil
	}
	// The ":G" family, which cmd/pvim/git.go answers before the ex layer sees
	// the line, for the same reason the two above are answered there:
	// internal/ex's user command table holds a replacement string and has no
	// way to give a command a Go body. One field on ex.UserCommand would
	// retire both hooks and it is reported rather than written.
	if took, gerr := gitCommand(s, line); took {
		s.sync()
		if gerr != nil {
			s.ed.Say(gerr.Error())
		}
		s.ed.Redisplay()
		return nil
	}
	// The tag commands, answered in front of the ex layer for the reason the
	// two hooks above give: internal/ex's ":tag" row has no handler and one
	// there would need the tag stack, which is per window and lives on the
	// session. See cmd/pvim/tags.go.
	if took, terr := tagCommandLine(s, line); took {
		s.sync()
		if terr != nil {
			s.ed.Say(terr.Error())
		}
		s.ed.Redisplay()
		return nil
	}
	err := s.ctx.RunLine(line)
	s.sync()
	if err != nil {
		// An empty message is a command that has already reported, or one the
		// person cancelled at a prompt. vim prints nothing for those and a
		// bare Say would leave an empty line in the message log.
		if msg := errorMessage(err, line); msg != "" {
			s.ed.Say(msg)
		}
	}
	// The redraw between one command and the next, which is where a kept
	// message goes out a second time. Editor.Key does this for a normal-mode
	// command; a colon command runs outside it, so it is done here.
	s.ed.Redisplay()
	return nil
}

// errorMessage renders an ex error the way vim's message line does: the E-code
// and its sentence, and for the unknown-command case the command that was not
// recognised, because "E492: Not an editor command: Xyzzy" is the message and
// the name is half of it.
func errorMessage(err error, line string) string {
	msg := err.Error()
	if err == ex.ErrNotAnEditorCommand {
		return msg + ": " + strings.TrimLeft(line, " \t:")
	}
	return msg
}

// prevRune is the byte index of the rune before i, which is where a backspace
// on a command line lands.
func prevRune(b []byte, i int) int {
	if i <= 0 {
		return 0
	}
	i--
	for i > 0 && b[i]&0xc0 == 0x80 {
		i--
	}
	return i
}

// wordStart is where CTRL-W deletes back to: over the white space before the
// cursor and then over the word before that.
func wordStart(b []byte, i int) int {
	for i > 0 && (b[i-1] == ' ' || b[i-1] == '\t') {
		i--
	}
	for i > 0 && b[i-1] != ' ' && b[i-1] != '\t' {
		i--
	}
	return i
}
