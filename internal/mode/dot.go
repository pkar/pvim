package mode

import (
	"strconv"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/operator"
)

// Dot repeat.
//
// The last change is kept as the keys that made it, with the count and the
// register prefix held apart. Keys, because that is the only representation
// that replays cwfoo<Esc>, a forced motion and a text object without a second
// implementation of each. Apart, because . with a count of its own replaces
// the original count rather than multiplying by it: 2d3w deletes six words and
// 2. after it deletes two, both measured.

// recordDot makes the command that just finished the one . repeats.
func (e *Editor) recordDot(count int, reg byte) {
	if e.inDot {
		return
	}
	// A command a mouse click completed. The keys it was made of are the
	// operator and nothing else, so recording them would leave "." waiting for
	// a motion that never comes; the change before this one stays the one "."
	// repeats. Consumed here rather than only in finish, because a c completed
	// by a click gets this far through endInsert instead.
	if e.fromPos {
		e.fromPos = false
		return
	}
	if e.dotFromVisual {
		e.dotFromVisual = false
		// A visual c: the shape is already recorded and this is the text that
		// was typed into the hole it made. The first key is the operator.
		if len(e.cmdKeys) > 1 {
			e.dot.vis.insert = append([]key.Key(nil), e.cmdKeys[1:]...)
		}
		return
	}
	if len(e.cmdKeys) == 0 {
		return
	}
	e.dot = dot{
		keys:  append([]key.Key(nil), e.cmdKeys...),
		count: count,
		reg:   reg,
	}
}

// recordVisualDot makes an operator on a selection the change . repeats. The
// selection is recorded as a size and not as the keys that made it, because
// that is what vim repeats: see visualRepeat.
func (e *Editor) recordVisualDot(op operator.Op, arg byte) {
	if e.inDot {
		return
	}
	e.dotFromVisual = true
	start, end := e.selection()
	e.dot = dot{
		count: e.pend.count(),
		reg:   e.pend.reg,
		vis: visualRepeat{
			ok:     true,
			mode:   e.mode,
			op:     op,
			arg:    arg,
			lines:  end.Line - start.Line,
			chars:  end.Col - start.Col,
			endCol: end.Col,
			toEOL:  e.mctx.Curswant == motion.CurswantEOL,
		},
	}
}

// recordVisualBlockInsert makes a blockwise I or A the change . repeats. It is
// recordVisualDot with no operator: what the repeat does with the block is
// type into it, and the text arrives later through recordDot's visual branch.
func (e *Editor) recordVisualBlockInsert(cmd byte) {
	if e.inDot {
		return
	}
	e.recordVisualDot(operator.OpNone, 0)
	e.dot.vis.ins = cmd
}

// repeatDot is the . command.
//
// A count typed on the . replaces the one the change was made with. A register
// typed on the . is thrown away and the original is used again, except that a
// numbered register is incremented, which is what :help redo-register
// describes and what "1dd. was measured doing.
func (e *Editor) repeatDot(count int) error {
	if e.dot.vis.ok {
		return e.repeatVisualDot(count)
	}
	if len(e.dot.keys) == 0 {
		return e.beep()
	}
	d := e.dot
	if count > 0 {
		d.count = count
	}
	reg := d.reg
	if reg >= '1' && reg <= '8' {
		reg++
		// Stored back, because :help redo-register increments on every repeat
		// and not only on the first: "1dd.. uses "1, then "2, then "3, and
		// leaves 1=CCC 2=BBB 3=AAA 4=CCC on AAA/BBB/CCC/DDD. Incrementing a
		// copy leaves the second . on "2 again, which "1dd. alone cannot see
		// because there is no second repeat in it.
		e.dot.reg = reg
	}

	var keys []key.Key
	if reg != 0 {
		keys = append(keys, key.Rune('"'), key.Rune(rune(reg)))
	}
	if d.count > 0 {
		for _, c := range strconv.Itoa(d.count) {
			keys = append(keys, key.Rune(c))
		}
	}
	keys = append(keys, d.keys...)

	e.pend = pending{}
	e.cmdKeys = nil

	wasDot, wasReplay := e.inDot, e.replaying
	e.inDot, e.replaying = true, true
	wasMapped := e.mapReplay
	e.mapReplay = false
	err := e.playKeys(keys)
	e.mapReplay = wasMapped
	e.inDot, e.replaying = wasDot, wasReplay
	return err
}

// repeatVisualDot applies the last visual operator to the same amount of text
// at the cursor: the same number of lines for a linewise selection, the same
// number of characters for a charwise one that was on a single line, and the
// same number of lines ending in the same column for one that was not. :help
// visual-repeat.
func (e *Editor) repeatVisualDot(count int) error {
	v := e.dot.vis
	start := e.cur
	end := start
	switch {
	case v.mode == VisualLine || v.lines > 0:
		end.Line += v.lines
		if n := e.buf.LineCount(); end.Line > n {
			end.Line = n
		}
		end.Col = v.endCol
	default:
		end.Col = start.Col + v.chars
	}
	if line := e.buf.Line(end.Line); end.Col > len(line) {
		end.Col = len(line)
		if end.Col > 0 {
			end.Col = prevRune(line, end.Col)
		}
	}

	was := e.mode
	e.visual, e.mode = start, v.mode
	if v.toEOL {
		e.mctx.Curswant = motion.CurswantEOL
	}
	e.cur = end

	wasDot := e.inDot
	e.inDot = true
	defer func() { e.inDot = wasDot }()

	if count > 0 {
		e.pend.count1 = count
	}
	if e.dot.reg != 0 {
		e.pend.reg = e.dot.reg
	}
	if v.ins != 0 {
		// A blockwise I or A: the same block at the cursor, and then the same
		// text typed into it. Without this the repeat replays the bare I or A
		// as a normal-mode command on one line and loses the block's height.
		if err := e.blockInsert(v.ins == 'A'); err != nil {
			return err
		}
		if e.mode == Insert || e.mode == Replace {
			return e.playKeys(v.insert)
		}
		return nil
	}
	if v.arg != 0 {
		return e.visualReplace(key.Rune(rune(v.arg)))
	}
	if v.op == operator.OpNone {
		e.mode = was
		return e.beep()
	}
	if err := e.visualOperator(v.op); err != nil {
		return err
	}
	// A visual c left insert mode running: type what was typed the first time.
	if e.mode == Insert || e.mode == Replace {
		return e.playKeys(v.insert)
	}
	return nil
}

// playKeys feeds keys back through the machine, stopping at the first one that
// beeped. It is what . and @ both replay through, so a failing search stops a
// macro and a failing motion stops a dot repeat, which is the same rule.
//
// A dot repeat goes straight into Key and a macro replay goes through the map
// layer first. That is vim's difference and not a convenience: do_pending_
// operator() stuffs the redo record into the STUFF buffer, which vgetorpeek()
// reads with no_mapping set, while do_execreg() pushes a register into the
// TYPEAHEAD, where a mapping applies exactly as it does to a typed key. See
// Editor.SetRemap.
func (e *Editor) playKeys(keys []key.Key) error {
	for _, k := range keys {
		if err := e.playKey(k); err != nil {
			return err
		}
		if e.aborted {
			return nil
		}
	}
	return nil
}

// playKey feeds one replayed key in, through the map layer when the replay is
// a macro and the frontend has installed one.
func (e *Editor) playKey(k key.Key) error {
	if e.mapReplay && e.remap != nil {
		return e.remap(k)
	}
	return e.Key(k)
}
