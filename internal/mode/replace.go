package mode

import "github.com/pkar/pvim/internal/text"

// Replace mode: R, and the stack that makes its backspace put back what was
// overwritten rather than delete what was typed.
//
// The stack is the whole of why replace mode is not insert mode with a flag.
// Typing over the end of a line appends, and backspacing over that has to
// remove the character rather than restore one, so each entry records whether
// there was anything there. Measured: R over "ab" with XYZQ and three
// backspaces leaves "Xb", because Q and Z were appended and b was covered.

// replaceBytes overwrites at the cursor, one character at a time, remembering
// what each one covered.
func (e *Editor) replaceBytes(b []byte) {
	e.insUndo()
	for i := 0; i < len(b); {
		n := runeLen(b[i:])
		e.replaceOne(b[i : i+n])
		i += n
	}
}

// replaceOne overwrites the character at the cursor with one character.
func (e *Editor) replaceOne(r []byte) {
	if len(r) == 1 && r[0] == '\n' {
		e.ins.over = append(e.ins.over, overwritten{})
		e.cur = e.buf.Insert(e.cur, r)
		e.ins.text = append(e.ins.text, r...)
		return
	}
	// Replace mode never starts a run for the changelist, which reports every
	// character it covers, but an ordinary character put down here is still
	// the thing that stops endInsert saying the mode message twice: measured,
	// "R<CR>X" says it once where "RX<CR>" says it twice.
	e.ins.quiet = true
	e.ins.trimmed = false
	line := e.buf.Line(e.cur.Line)
	if e.cur.Col >= len(line) {
		e.ins.over = append(e.ins.over, overwritten{})
		e.cur = e.buf.Insert(e.cur, r)
		e.ins.text = append(e.ins.text, r...)
		return
	}
	end := nextRune(line, e.cur.Col)
	e.ins.over = append(e.ins.over, overwritten{
		text: append([]byte(nil), line[e.cur.Col:end]...),
		had:  true,
	})
	e.cur = e.buf.Replace(text.Range{
		Start: e.cur,
		End:   text.Pos{Line: e.cur.Line, Col: end},
	}, r)
	e.ins.text = append(e.ins.text, r...)
}

// replaceBackspace is BS in replace mode: the last character typed goes back
// to what it was covering, or disappears when it was covering nothing.
//
// With the stack empty the cursor moves left and the text is not touched at
// all, which is vim's rule and the reason backspacing past the start of an R
// does not eat the line.
func (e *Editor) replaceBackspace() error {
	// vim's ins_bs() returns before u_save() at the very start of the buffer,
	// so a backspace there leaves undotree().seq_cur at 0 where one anywhere
	// else leaves it at 1 over a buffer it did not change.
	if e.cur.Line == 1 && e.cur.Col == 0 {
		return nil
	}
	e.insUndo()
	// In the line the replace started on there is nothing to put back, and
	// vim's ins_bs() moves the cursor back over the line break rather than
	// joining: its column-zero branch ends in dec_cursor() whenever the cursor
	// is still on or above Insstart.lnum. Measured on a six-line file, "R<BS>"
	// on line 3 leaves the buffer alone and the cursor at the end of line 2,
	// "R<BS><BS>" one further left again, and "R<BS>X" appends the X to line 2.
	// The start of the replace moves back with the cursor, which is vim
	// decrementing Insstart in the same branch.
	if e.cur.Col == 0 && e.cur.Line <= e.ins.start.Line {
		if e.cur.Line == 1 {
			return nil
		}
		prev := e.buf.Line(e.cur.Line - 1)
		e.cur = text.Pos{Line: e.cur.Line - 1, Col: len(prev)}
		e.ins.start = e.cur
		return nil
	}
	if len(e.ins.over) == 0 {
		if e.cur.Col > 0 {
			e.cur.Col = prevRune(e.buf.Line(e.cur.Line), e.cur.Col)
		}
		return nil
	}
	top := e.ins.over[len(e.ins.over)-1]
	e.ins.over = e.ins.over[:len(e.ins.over)-1]

	if e.cur.Col == 0 {
		// The character being taken back was a line break.
		if e.cur.Line == 1 {
			return nil
		}
		gone := e.cur.Line
		prev := e.buf.Line(e.cur.Line - 1)
		e.cur = e.buf.Delete(text.Range{
			Start: text.Pos{Line: e.cur.Line - 1, Col: len(prev)},
			End:   text.Pos{Line: e.cur.Line},
		})
		// Same rule as J and as insert mode's joinBack: the changelist reports
		// the line that went and not the one it was joined onto.
		e.buf.ChangedAt(text.Pos{Line: gone})
		e.trimTyped(1)
		return nil
	}

	line := e.buf.Line(e.cur.Line)
	from := prevRune(line, e.cur.Col)
	r := text.Range{Start: text.Pos{Line: e.cur.Line, Col: from}, End: text.Pos{Line: e.cur.Line, Col: e.cur.Col}}
	e.trimTyped(e.cur.Col - from)
	if top.had {
		e.buf.Replace(r, top.text)
	} else {
		e.buf.Delete(r)
	}
	e.cur.Col = from
	return nil
}

// replaceLineBack is CTRL-U in replace mode: every character this replace put
// down on the line goes back to what it was covering, one at a time.
//
// vim has no separate CTRL-U: ins_bs() takes BACKSPACE_LINE and runs the same
// loop that BS runs once, and in replace mode that loop calls replace_do_bs().
// Measured: "Rab CTRL-U" leaves the line exactly as it was, where insert
// mode's CTRL-U would have deleted two characters, and "Ra<CR>b CTRL-U" puts
// back only the b because the loop stops in column zero rather than joining.
func (e *Editor) replaceLineBack() error {
	// Column zero is not the loop: vim's ins_bs() answers the column before it
	// asks which backspace this is, so CTRL-U there is one backspace and
	// nothing more.
	if e.cur.Col == 0 {
		return e.replaceBackspace()
	}
	if !e.canBackspaceOver("start") && e.cur.Line == e.ins.start.Line && e.cur.Col <= e.ins.start.Col {
		return nil
	}
	if !e.canBackspaceOver("indent") && e.ins.ai > 0 && e.cur.Col <= e.ins.ai {
		return nil
	}
	stop := 0
	if e.opt.AutoIndent {
		if nb := firstNonBlank(e.buf.Line(e.cur.Line)); nb < e.cur.Col {
			stop = nb
		}
	}
	if e.cur.Line == e.ins.start.Line && e.ins.start.Col > stop && e.ins.start.Col < e.cur.Col {
		stop = e.ins.start.Col
	}
	for e.cur.Col > stop {
		was := e.cur.Col
		if err := e.replaceBackspace(); err != nil {
			return err
		}
		// A stack that has run out moves the cursor and touches nothing, and
		// on the day it stops moving either the loop has to end or it never
		// does.
		if e.cur.Col >= was {
			break
		}
	}
	return nil
}
