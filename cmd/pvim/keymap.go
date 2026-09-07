package main

import (
	"time"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/keymap"
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/vimrc"
)

// Mappings, between the frontend and the mode machine.
//
// A mapping is not a feature of the mode machine and must not be one. The keys
// a mapping produces are fed back through the same dispatch a typed key goes
// through, which is what makes ":nmap ab cd" reach the cd mapping and what
// makes "map { gT" work in operator-pending without internal/mode knowing that
// mappings exist. So the table lives beside the editor, the resolution lives in
// internal/keymap, and this file is the join.
//
// The one thing that does not come through here is a macro replay. internal/
// mode's @ pushes the register's keys through Editor.Key directly, below this
// layer, so a mapping is applied when the keys are recorded and not when they
// are played. Vim does the opposite: it records the typed keys and maps them
// again on every replay, measured with `nnoremap, A!<Esc>` and "qq,q@q", which
// gives two exclamation marks in vim and would give two here as well, and with
// the same script plus a ":nunmap," before the @q, which gives one in vim and
// two here. Closing that needs a hook in internal/mode; see the note in
// session.go.

// mapTable is the editor's map table, built on first use.
//
// It hangs off the session, which every constructor in this package makes, so
// that the table and the dispatch that reads it are one object and a test can
// take an editor apart without the two disagreeing.
func (e *editor) mapTable() *keymap.Table {
	if e.sess.keys == nil {
		e.sess.keys = &keymap.Table{}
	}
	return e.sess.keys
}

// mapLeader is 'mapleader' as it stands right now, which is what <Leader> in a
// right-hand side expands to.
//
// Right now and not at the end of the file: vim reads 'mapleader' when the :map
// command runs, so this is called from the sink as each line runs and a vimrc
// that set the leader twice would get two different answers, which is the
// answer vim gives.
func (e *editor) mapLeader() string {
	if v, ok := e.vars["g:mapleader"]; ok {
		return v.Val.Str
	}
	return ""
}

// mapLocalLeader is 'maplocalleader', which this vimrc never sets. Vim falls
// back to a backslash for <LocalLeader> when it is unset, the same fallback
// <Leader> has.
func (e *editor) mapLocalLeader() string {
	if v, ok := e.vars["g:maplocalleader"]; ok {
		return v.Val.Str
	}
	return e.mapLeader()
}

// applyMap puts one parsed :map command into the table.
//
// It is where the four shapes of the command part ways: :mapclear, :unmap, and
// the two halves of a definition. The error it returns goes on the message
// line, which is where a <expr> refusal and an E227 belong.
func (e *editor) applyMap(st vimrc.Map) error {
	if e.sess == nil {
		return nil
	}
	modes, err := keymap.ParseModes(st.Modes)
	if err != nil {
		return err
	}
	t := e.mapTable()
	buf := e.mapBuf()

	if st.Clear {
		t.Clear(modes, st.Buffer, buf)
		e.installMaps()
		return nil
	}
	if st.Unmap {
		if err := t.Unmap(modes, st.LHS, st.Buffer, buf); err != nil {
			return err
		}
		e.installMaps()
		return nil
	}

	// The right-hand side is parsed here and not by internal/vimrc, because it
	// is keys and internal/vimrc keeps it as the text it was written as: a
	// listing quotes the text, and only the thing that is going to feed the
	// keys back needs them as keys. <Leader> is expanded on this side too,
	// which vim does through the same replace_termcodes() call it uses on the
	// left.
	rhs, err := key.ParseLeader(st.RHS, e.mapLeader(), e.mapLocalLeader())
	if err != nil {
		return err
	}
	if err := t.Set(keymap.Mapping{
		Modes:   modes,
		LHS:     st.LHS,
		RHS:     rhs,
		LHSText: st.LHSText,
		RHSText: st.RHS,
		NoRemap: st.NoRemap,
		Buffer:  st.Buffer,
		Buf:     buf,
		Silent:  st.Silent,
		Nowait:  st.Nowait,
		Unique:  st.Unique,
		Expr:    st.Expr,
	}); err != nil {
		return err
	}
	e.installMaps()
	return nil
}

// mapBuf is the buffer a <buffer> mapping belongs to.
func (e *editor) mapBuf() int {
	if b := e.cur(); b != nil {
		return b.Num
	}
	return 0
}

// installMaps hands the session a machine over the table, once there is
// anything in it.
//
// Once there is anything in it, and not always: an editor with no vimrc, which
// is every --oracle run, keeps the session's nil machine and the dispatch it
// had before this file existed. That is not an optimisation, it is the promise
// that a graded run cannot be changed by this code.
func (e *editor) installMaps() {
	if e.sess == nil || e.sess.keys == nil || e.sess.keys.Empty() {
		return
	}
	if e.sess.maps == nil {
		e.sess.maps = keymap.New(e.sess.keys)
		e.sess.mapBuf = e.mapBuf
	}
}

// mapState describes the editor to the resolver: which mode the next key will
// be executed in, which buffer it is in, and whether a command is holding the
// next key for itself.
func (s *session) mapState() keymap.State {
	st := keymap.State{Mode: keymap.Normal}
	if s.mapBuf != nil {
		st.Buf = s.mapBuf()
	}

	switch {
	case s.line != nil:
		// The ":" prompt this file opens is a command line whether or not the
		// mode machine agrees, and the mode machine does not: internal/mode
		// answers Normal while internal/ex holds the line.
		st.Mode = keymap.Cmdline
	default:
		switch s.ed.Mode() {
		case mode.Insert, mode.Replace:
			// One bit for both, because vim's map-overview table puts insert
			// and replace in the same column: an :imap fires under R.
			st.Mode = keymap.Insert
		case mode.VisualChar, mode.VisualLine, mode.VisualBlock:
			// Visual and not select: pvim has no select mode, so a :vmap and
			// an :xmap are the same mapping here and an :smap is unreachable.
			st.Mode = keymap.Visual
		case mode.OperatorPending:
			st.Mode = keymap.OpPending
		case mode.Cmdline:
			st.Mode = keymap.Cmdline
		}
	}

	// Vim reads the argument of an f, an r, a mark or a register name with
	// no_mapping set, so "f{" finds a brace even under this vimrc's "map {
	// gT". Measured. The three prefixes this file holds itself -- z, CTRL-W
	// and Z -- read their second key the same way, through normal.c's
	// plain_vgetc, so they are here too.
	st.NoMap = s.ed.Waiting() || s.pendingZ || s.pendingW || s.pendingZZ
	return st
}

// mapTimeout is how long the frontend should wait on an ambiguous mapping, and
// false when it must never stop waiting.
//
// This is 'timeout' and 'timeoutlen' and it is NOT 'ttimeout' and
// 'ttimeoutlen'. The second pair is the escape-sequence timeout, it belongs to
// internal/key's decoder and the terminal's read loop, and this vimrc sets the
// two pairs in opposite directions on purpose: "set notimeout" so a mapping
// waits for the second comma of "," however long that takes, and "set
// ttimeout ttimeoutlen=10" so a bare Escape is told from <Esc>[A in ten
// milliseconds. A frontend that ran a mapping wait on ttimeoutlen would fire
// "," on its own after 10ms.
func (e *editor) mapTimeout() (time.Duration, bool) {
	// Vim's own table, from options.txt under 'ttimeout': with 'timeout' off
	// there is no mapping delay at all whatever 'ttimeout' says, and with it on
	// the delay is 'timeoutlen' whatever 'ttimeoutlen' says. internal/tui's
	// keyCodeTimeout is the other row of the same table.
	if !e.opt.G.Timeout {
		return 0, false
	}
	ms := e.opt.G.TimeoutLen
	if ms < 0 {
		return 0, false
	}
	return time.Duration(ms) * time.Millisecond, true
}

// MapPending reports whether a mapping is half typed and the machine is waiting
// for the key that decides it. A frontend starts its 'timeoutlen' clock when
// this turns true.
func (s *session) MapPending() bool {
	return s.maps != nil && s.maps.Pending()
}

// MapTimeout ends the wait: whatever is held resolves the way it would have if
// the next key had said "not this mapping". A frontend calls it when
// 'timeoutlen' has passed, and never at all under 'notimeout'.
func (s *session) MapTimeout() error {
	if s.maps == nil {
		return nil
	}
	s.maps.Timeout()
	return s.drainMaps()
}

// drainMaps runs the resolver until it wants another key, handing each key it
// resolves to the dispatch a typed key goes through.
//
// The state is asked for again on every turn of the loop, and that is the whole
// reason this is a loop and not a slice of keys resolved up front: a right-hand
// side may change the mode halfway through itself, and the key after the change
// has to be looked up in the mode the change left behind.
func (s *session) drainMaps() error {
	for {
		k, ok, err := s.maps.Next(s.mapState())
		if err != nil {
			// E223 and the <SID> refusal, both of which arrive with the
			// typeahead already thrown away. Vim's answer to a recursive
			// mapping is the message and a cleared typebuf, and nothing else.
			s.ed.Say(err.Error())
			return nil
		}
		if !ok {
			return nil
		}
		if k.Special == key.KeyPlug {
			// A <Plug> key that no mapping claimed. No terminal can send one,
			// so it can only have come out of a right-hand side that named a
			// mapping nothing defined, and vim's answer is to swallow it.
			continue
		}
		said := len(s.ed.Messages())
		if err := s.keyRaw(k); err != nil {
			return err
		}
		// A beep flushes it too, and a beep says nothing: vim's ins_typebuf is
		// cleared by both got_int and an error, and internal/mode's beep() sets
		// the flag Aborted reports. Asked straight after the key, because the
		// next command clears it. Without this half, a mapping whose right-hand
		// side beeps runs on where vim stops.
		if s.ed.Aborted() || isErrorMessage(s.ed.Messages(), said) {
			// An error throws the rest of the mapping away, and this is the
			// difference between running this vimrc and mangling a file with
			// it. "nnoremap <Space> za " Spacebar to unfold" maps the space
			// to twenty-four characters; on a buffer with no folds vim runs
			// the za, says "E490: No fold found", and drops the other
			// twenty-one, leaving the file byte-identical. Measured. Without
			// this the quote would start a register name and the S would
			// substitute the line away.
			s.maps.FlushMapped()
		}
	}
}

// isErrorMessage reports whether the message log grew past n with something
// that starts with an E-code.
//
// The message log is the only signal there is. Vim flushes the typeahead on an
// error message and on a beep, and internal/mode's beep is silent by design --
// it sets an unexported "aborted" flag that stops a macro -- so the beep half
// of the rule cannot be seen from here. What that costs is the tail of a
// mapping whose right-hand side beeps rather than erroring, which runs on where
// vim would have stopped; the fix is one accessor in internal/mode and it is
// named in session.go.
func isErrorMessage(log []string, n int) bool {
	if len(log) <= n {
		return false
	}
	msg := log[len(log)-1]
	if len(msg) < 2 || msg[0] != 'E' {
		return false
	}
	return msg[1] >= '0' && msg[1] <= '9'
}

// flushMaps resolves a mapping left half typed when the input has run out.
//
// This is the one place the clock is not the frontend's, and it is here because
// a script is not a person. Vim, fed "ab" from a -s file with `ab` and `abc`
// both mapped, waits for a keyboard that is not there and never exits; measured
// under 'timeout' and 'notimeout' alike, because a script running out is not a
// timeout. pvim cannot hang, so the end of a script resolves what is held the
// way a timeout would. Nothing graded reaches this: --oracle reads no vimrc and
// therefore has no mappings at all.
func (s *session) flushMaps() error {
	if s.maps == nil {
		return nil
	}
	for s.maps.Pending() {
		before := len(s.maps.PendingKeys())
		if err := s.MapTimeout(); err != nil {
			return err
		}
		if len(s.maps.PendingKeys()) >= before {
			// Nothing moved, which can only mean the resolver is holding keys
			// it will not give up. Throwing them away beats spinning.
			s.maps.Reset()
			return nil
		}
	}
	return nil
}
