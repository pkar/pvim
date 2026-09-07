package ex

import (
	"strings"

	"github.com/pkar/pvim/internal/substitute"
	"github.com/pkar/pvim/internal/text"
)

// ":s", ":&", ":~", ":g" and ":v": the ex layer's half of internal/substitute.
//
// internal/substitute owns the pattern, the replacement grammar and the
// counting, and deliberately cannot run an ex command because doing so would
// mean importing this package. So the split is: it says which lines a ":g"
// selected and what one ":s" did, and this file supplies the buffer, the undo
// block, the cursor, the message line and, for ":g", the loop that runs a
// command over each marked line.

// subState is the remembered-pattern state, made on first use.
//
// A Context built by a parser test has none and does not want one; the two
// repeat forms and an empty pattern are the only things that read it, and they
// answer E33 or E35 against a fresh one, which is the right answer for an
// editor that has not substituted yet.
func (c *Context) subState() *substitute.State {
	if c.Sub == nil {
		c.Sub = &substitute.State{}
	}
	return c.Sub
}

// subConfirm is the "c" flag's prompt, routed to whatever the frontend put on
// Context.Prompt.
//
// A Context with no Prompt returns nil, which internal/substitute reads as
// "yes to everything". That is what a test wants and it is NOT what the oracle
// wants: cmd/pvim supplies a Prompt that reads the next key out of the script,
// which is how ":%s/aaa/X/gc" followed by "yynq" answers four prompts.
//
// The choices are in vim's order with "q" last, because Context.Prompt answers
// with the last choice when the input runs out and quitting is the answer that
// keeps what was already replaced. CTRL-E and CTRL-Y are not here: they scroll
// and ask again, and nothing scrolls during an ex command yet.
func (c *Context) subConfirm() substitute.ConfirmFunc {
	ask := c.PromptQuiet
	if ask == nil {
		ask = c.Prompt
	}
	if ask == nil {
		return nil
	}
	return func(p substitute.Prompt) substitute.Answer {
		ans, err := ask(p.Text, "ynalq")
		if err != nil {
			return substitute.Quit
		}
		switch ans {
		case 'y':
			return substitute.Yes
		case 'n':
			return substitute.No
		case 'a':
			return substitute.All
		case 'l':
			return substitute.Last
		}
		return substitute.Quit
	}
}

// noteSearch copies whatever "/", "?", "*" or "#" last looked for into the
// substitute state, so that ":s//X/" and ":g//d" find it.
//
// internal/search owns that pattern and internal/substitute may not import it,
// so this package carries it across. The guard is what makes the copy safe to
// do on every command: LastIsSearch decides where an empty pattern comes from,
// and setting it unconditionally would make ":s/a/b/" followed by ":s//c/"
// resolve to the last SEARCH rather than to "a". Only a pattern that has
// actually changed since the last look moves it.
func (c *Context) noteSearch() {
	if c.Ed == nil {
		return
	}
	p := c.Ed.Search().Pattern
	if p == "" || p == c.searchSeen {
		return
	}
	c.searchSeen = p
	// It is also the search slot itself, which is the half "\/" and "\?"
	// read: a pattern in the mode machine that this layer did not publish came
	// from a "/", "?", "*" or "#", and nothing else writes RE_SEARCH.
	c.searchPat = p
	c.subState().NoteSearch(p)
}

// publishPattern makes the pattern a ":s" or ":g" just used the one "n"
// repeats and the contents of "/, which is what vim does.
func (c *Context) publishPattern(p string) {
	if p == "" || c.Ed == nil {
		return
	}
	c.searchSeen = p
	c.Ed.SetSearchPattern(p)
}

// exSubstitute is ":s", ":&" and ":~", which differ only in where an absent
// pattern comes from.
func exSubstitute(kind substitute.Kind) Handler {
	return func(c *Context, cmd Cmd) error {
		c.noteSearch()
		parsed, err := substitute.Parse(kind, cmd.Args, c.subState(), c.opts())
		if err != nil {
			return err
		}

		b := c.buffer()
		req := substitute.Request{
			Buf:     b,
			First:   cmd.Lines.First,
			Last:    cmd.Lines.Last,
			Cmd:     parsed,
			Cursor:  c.Ed.Cursor(),
			Opt:     c.opts(),
			State:   c.subState(),
			Confirm: c.subConfirm(),
		}

		// One undo block for the whole command however many lines it touches,
		// which is why this is not a c.edit per line: ":%s/a/X/g" then "u"
		// gives the whole file back.
		var res substitute.Result
		c.editIfChanged(text.Pos{Line: cmd.Lines.First}, func(b *text.Buffer) {
			res, err = substitute.Do(req)
			from := res.FirstLine
			if res.DeferredFrom > 0 {
				from = res.DeferredFrom
			}
			if from > 0 && (!res.Asked || res.AnsweredAll) {
				// One report for the whole run, from the first line it
				// touched: vim's do_sub replaces the lines and calls
				// changed_lines() once at the end, so the changelist entry is
				// the top of the range and not the last line rewritten.
				//
				// Not while the "c" flag is asking. There the screen is
				// redrawn between prompts, so every line is reported as it
				// goes and the last one wins: ":%s/aaa/X/gc" answered "yynq"
				// leaves the changelist on line 3 and the same command
				// answered "a" leaves it on line 1.
				b.ChangedAt(text.Pos{Line: from})
			}
		})
		// The pattern reaches "/ even when the substitution failed, because
		// resolve() recorded it before the matching began.
		c.publishPattern(c.subState().SubPattern)
		if err != nil {
			return err
		}
		c.reportSub(res)
		return nil
	}
}

// reportSub moves the cursor and says what the substitution did.
//
// The cursor rule is internal/substitute's: a zero Line means nothing changed
// and the cursor stays where it is. Everything else is already the first
// non-blank of the last changed line, so this does not call moveTo, which
// would compute a first non-blank of its own and disagree on a line the "n"
// flag never touched.
func (c *Context) reportSub(res substitute.Result) {
	if res.Cursor.Line > 0 {
		c.Ed.SetCursor(res.Cursor)
		c.Sync()
	}
	if res.Message != "" {
		c.sayKeep(res.Message)
	} else if res.Asked && !c.subState().InGlobal() {
		// An interactive substitute that had nothing to report still wipes
		// the last prompt off the line: vim's do_sub ends in msg("") when the
		// count is under 'report', and a plain ":s" in the same position
		// prints nothing at all. Measured, ":%s/aaa/X/gc" answered "yynq".
		//
		// Not under a ":g". vim guards that msg("") with !global_busy, so
		// ":g/a/s/a/X/c" over three lines leaves prompt, blank, prompt, blank,
		// prompt where wiping per line leaves a doubled blank in front of every
		// prompt after the first. Measured, and the two controls that pin it to
		// the intersection -- the same global with no "c" flag, and the same
		// ":s" outside a global -- are byte-identical either way.
		c.Ed.NewMessageLine()
	}
	if res.Print != "" {
		c.say(res.Print)
	}
}

// exGlobal is ":g", ":g!" and ":v".
//
// Two passes, and the second one is here: internal/substitute marks the lines
// the pattern selected and this runs the command over them. The marked lines
// move with the buffer as the commands change it, which is why ":g/^/m0"
// reverses a file instead of hanging.
func exGlobal(invert bool) Handler {
	return func(c *Context, cmd Cmd) error {
		c.noteSearch()
		g, err := substitute.ParseGlobal(cmd.Args, invert || cmd.Bang)
		if err != nil {
			return err
		}

		st := c.subState()
		b := c.buffer()
		// A ":g" inside a ":g" is allowed, and one with a range is E147 only
		// when the range is not the whole file. vim's test is
		//
		//	global_busy && (eap->line1 != 1 || eap->line2 != ml_line_count)
		//
		// on the RESOLVED lines, so ":g/a/%g/1/d" runs and ":g/a/1,2g/1/d" is
		// E147. Asking whether a range was typed refused the first of those.
		nested := st.InGlobal()
		partial := cmd.Lines.First > 1 || cmd.Lines.Last < b.LineCount()
		if err := st.EnterGlobal(partial); err != nil {
			return err
		}

		if nested {
			// The one-line form. vim's ex_global, when global_busy is set,
			// marks nothing: it tests the line the cursor is on and runs the
			// command once if it matches, and says nothing at all when it does
			// not. Its own source comment says why -- "When nesting the command
			// works on one line. This allows for :g/found/v/notfound/command" --
			// and a second full pass instead deleted lines the outer global had
			// never selected and printed a "Pattern not found" per outer line.
			// The depth still has to come back down. There is no message to
			// take from it at this depth: the outer global reports for both.
			defer st.LeaveGlobal(c.opts())
			n := c.cursorLine()
			marked, pattern, err := substitute.Marks(b, n, n, g, st, c.opts())
			c.publishPattern(pattern)
			if err != nil {
				return err
			}
			if marked.Len() == 0 {
				return nil
			}
			return c.runSourced(globalCommand(g))
		}

		// The line count the whole global changed, and the substitutions it
		// added up to, reported once at the end and never both. vim's
		// global_exe keeps old_lcount from before the loop and ends with
		//
		//	if (!do_sub_msg(FALSE)) msgmore(line_count - old_lcount);
		//
		// so ":g/a/s//X\rY/" over three lines says "3 substitutions on 3 lines"
		// and stops there; printing "3 more lines" after it also overwrote the
		// kept message, so the substitution report went out once where vim
		// keeps it twice. msgmore() itself does nothing while global_busy is
		// set, which is why ":g/aaa/d" over four lines says "4 fewer lines"
		// once and not "1 fewer line" four times.
		before := b.LineCount()
		defer func() {
			if msg := st.LeaveGlobal(c.opts()); msg != "" {
				c.sayKeep(msg)
				return
			}
			c.sayLineCount(b.LineCount() - before)
		}()
		marked, pattern, err := substitute.Marks(b, cmd.Lines.First, cmd.Lines.Last, g, st, c.opts())
		c.publishPattern(pattern)
		if err != nil {
			return err
		}
		if marked.Len() == 0 {
			c.say(notFoundMessage(pattern, g.Invert))
			return nil
		}

		// The lines to visit, taken out of Marked before anything runs. The ex
		// layer moves them itself: see shiftPending for why the whole-buffer
		// delta Marked carries is not vim's rule.
		pending := make([]int, 0, marked.Len())
		for {
			n, ok := marked.Next()
			if !ok {
				break
			}
			pending = append(pending, n)
		}

		command := globalCommand(g)

		// The whole global is one undo step. The mode machine may have a block
		// of its own open, so it is closed first and then the hold makes every
		// close underneath do nothing, leaving one step for the lot.
		//
		// Held and not opened: vim's ex_global calls no u_save of its own, so
		// a global whose command changes nothing numbers no undo header at
		// all. ":g/aaa/p" leaves undotree().seq_cur at 0, and opening a block
		// here left it at 1.
		//
		// The same is true one line at a time, and that is what undoHeld is
		// for. ":g/a/s/zzz/Q/" leaves seq_cur at 0 in vim because the inner
		// ":s" never reached its u_save, so the empty block editIfChanged
		// opens for it has to go rather than be numbered when the hold comes
		// off here. It cannot be dropped while the hold is on, so
		// editIfChanged takes the hold off for the length of its own block;
		// see the comment there for why that is safe.
		if c.Ed != nil && !c.scriptInput() {
			c.Ed.Sync()
		}
		b.HoldUndoBlock()
		wasHeld := c.undoHeld
		c.undoHeld = true
		defer func() {
			c.undoHeld = wasHeld
			b.ReleaseUndoBlock()
		}()

		for i, n := range pending {
			if n < 1 || n > b.LineCount() {
				continue
			}
			c.setCursorLine(n)
			lines := b.LineCount()
			err := c.runSourced(command)
			if delta := b.LineCount() - lines; delta != 0 {
				shiftPending(pending[i+1:], changedLine(b), delta)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
}

// runSourced runs one line the way a ":g" runs the command it was given: not
// typed, so the errors that come out of the parse quote it. See
// Context.sourcedError.
func (c *Context) runSourced(line string) error {
	was := c.sourced
	c.sourced = true
	defer func() { c.sourced = was }()
	return c.RunLine(line)
}

// globalCommand is the command a ":g" runs on each line it selected. An empty
// one is ":p", which is what makes ":g/foo/" a grep.
func globalCommand(g substitute.Global) string {
	if strings.TrimSpace(g.Command) == "" {
		return "print"
	}
	return g.Command
}

// notFoundMessage is what a ":g" or a ":v" says when it selected no line.
//
// Two sentences, and which one depends on which way round the test was. vim's
// ex_global ends its empty case with
//
//	if (type == 'v') smsg(_("Pattern found in every line: %s"), pat);
//	else		 smsg(_("Pattern not found: %s"), pat);
//
// because a ":v" that selected nothing means every line matched. Neither
// carries an E-code, which is the difference from ":s"'s own E486: ":g/zzz/d"
// says "Pattern not found: zzz" and ":s/zzz/x/" says "E486: Pattern not found:
// zzz". Measured, all three.
func notFoundMessage(pattern string, invert bool) string {
	if invert {
		return "Pattern found in every line: " + pattern
	}
	return substitute.NotFoundMessage(pattern)
}

// changedLine is the line the last command reported changing, which is the '.
// mark.
//
// For the commands that add or remove whole lines it is vim's own
// deleted_lines_mark and appended_lines_mark line -- the first line that went,
// or the line the new ones went in before -- because internal/text's
// DeleteLines and InsertLines report exactly that through ChangedAt.
func changedLine(b *text.Buffer) int {
	if p, ok := b.Mark(text.MarkLastChange); ok && p.Line > 0 {
		return p.Line
	}
	return 1
}

// shiftPending moves the lines a ":g" has still to visit over a command that
// added or removed lines.
//
// vim does no arithmetic here at all: ml_setmarked flags the LINES, and every
// buffer operation runs mark_adjust across them, so only lines added or removed
// ABOVE a marked line move it and a marked line inside a deleted range loses
// its mark rather than pointing at whatever moved into its place. Adding the
// whole-buffer delta to every line still to come -- which is what
// internal/substitute's Marked.Shift does -- moves the ones a change BELOW them
// left alone, and that is the difference between vim's ":g/^a/+3d" on
// a1,a2,a3,z4..z9, which deletes z4, z6 and z8, and a run of three deletes from
// the top of the z block.
//
// at is where the change was, taken from the '. mark. A command that made
// several changes reports the last of them and this then moves a line it should
// not have; every command a global is given in this editor -- :d, :s, :normal,
// :m, :t, :pu -- reports one.
func shiftPending(pending []int, at, delta int) {
	for i, n := range pending {
		switch {
		case delta > 0:
			// The new lines went in before line at, so everything from there
			// down moves by as many.
			if n >= at {
				pending[i] = n + delta
			}
		case n >= at-delta:
			// Below the lines that went, which were at .. at-delta-1.
			pending[i] = n + delta
		case n >= at:
			// Inside them, so the mark goes. Zero is what the loop skips.
			pending[i] = 0
		}
	}
}
