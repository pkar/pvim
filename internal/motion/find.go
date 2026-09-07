package motion

import (
	"unicode/utf8"

	"github.com/pkar/pvim/internal/text"
)

// f, F, t, T and their repeats, which are one function in vim and one here:
// searchc.
//
// The repeat is where vim changed and where memory is wrong. Before vim 8, ;
// after a t did nothing at all: the cursor was already in front of the target,
// the search found that same target and backed up onto the position it started
// from. Now, unless 'cpo' contains ';', a; that repeats a t with no count
// skips the target it is sitting in front of and stops before the next one,
// which is what makes; walk a line of separators. The mechanism is one flag
// -- the first candidate is not allowed to be a match -- and it applies to,
// as well.

// searchChar is vim's searchc: find the count'th char on this line, in one
// direction, optionally stopping one character short.
//
// cmd is 'f', 'F', 't' or 'T' for a new find, or ';' or ',' for a repeat. The
// last find lives in the Context, so that a macro that runs an f leaves the
// same state behind as the same keys typed.
func searchChar(r Request, cmd byte) Result {
	ctx := r.Ctx
	var target rune
	var dir int
	var till bool
	stop := true

	switch cmd {
	case 'f', 'F', 't', 'T':
		if r.Arg == 0 {
			return fail()
		}
		target = rune(r.Arg)
		dir = 1
		if cmd == 'F' || cmd == 'T' {
			dir = -1
		}
		till = cmd == 't' || cmd == 'T'
		if ctx != nil {
			ctx.Find = Find{Cmd: cmd, Char: target}
		}
	case ';', ',':
		// Nothing to say. vim's nv_csearch calls clearopbeep() when searchc()
		// finds no last_csearch, which beeps and leaves the message line
		// alone. E35 is "No previous regular expression", it belongs to n, N
		// and a bare //, and internal/search already prints it there.
		if ctx == nil || ctx.Find.Cmd == 0 {
			return fail()
		}
		last := ctx.Find
		target = last.Char
		dir = 1
		if last.Cmd == 'F' || last.Cmd == 'T' {
			dir = -1
		}
		if cmd == ',' {
			dir = -dir
		}
		till = last.Cmd == 't' || last.Cmd == 'T'
		// Force a move of at least one character, so that; and, move even
		// when the cursor is already sitting where a t left it.
		if r.Count1() == 1 && till {
			stop = false
		}
	}

	b := r.Buf
	line := b.Line(r.From.Line)
	col := r.From.Col
	for n := r.Count1(); n > 0; n-- {
		for {
			if dir > 0 {
				col += charLen(line, col)
				if col >= len(line) {
					return fail()
				}
			} else {
				if col <= 0 {
					return fail()
				}
				col = prevChar(line, col)
			}
			c, _ := utf8.DecodeRune(line[col:])
			if c == target && stop {
				break
			}
			stop = true
		}
	}
	if till {
		if dir > 0 {
			col = prevChar(line, col)
		} else {
			col += charLen(line, col)
		}
	}

	p := text.Pos{Line: r.From.Line, Col: col}
	kind := KindCharInclusive
	if dir < 0 {
		kind = KindCharExclusive
	}
	return Result{
		To: p, Kind: kind, Ok: true,
		Curswant: dispCol(b, p, r.Opt.TabStop), Count: r.Count1(),
	}
}

// motionFind is f, F, t, T, ";" and ",".
func motionFind(cmd byte) Func {
	return func(r Request) Result { return searchChar(r, cmd) }
}
