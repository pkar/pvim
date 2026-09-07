package operator

import (
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// Every expectation in this package's tests came out of
// /opt/homebrew/bin/vim 9.2 run headless over a pty:
//
//	vim --clean -i NONE --not-a-term -s KEYS FILE
//
// with a trailer that dumps line('.'), col('.'), getreg() and getregtype().
// The test names carry the keystrokes that were run, so a failure can be put
// back in front of the same vim without anybody having to reconstruct it.

// registerFileSatisfiesRegisters is the compile-time check that the interface
// this package asks for is the one internal/register offers. A method added on
// one side and not the other is a build failure here rather than a nil Regs
// somewhere in the mode machine.
var registerFileSatisfiesRegisters Registers = register.NewFile()

// recorder is a Registers that remembers what it was handed. Tests use it
// rather than a real register file because what is under test is which call an
// operator makes, not what the register file does with it.
type recorder struct {
	yankName   byte
	yanked     register.Value
	yanks      int
	delName    byte
	deleted    register.Value
	deletes    int
	useRegOne  bool
	failWith   error
	sawAnyCall bool
}

func (r *recorder) Yank(name byte, v register.Value) error {
	r.sawAnyCall = true
	r.yankName, r.yanked, r.yanks = name, v, r.yanks+1
	return r.failWith
}

func (r *recorder) Delete(name byte, v register.Value, useRegOne bool) error {
	r.sawAnyCall = true
	r.delName, r.deleted, r.deletes = name, v, r.deletes+1
	r.useRegOne = useRegOne
	return r.failWith
}

// buf builds a buffer from a string with \n between lines, the way the .in
// files the oracle uses are written.
func buf(s string) *text.Buffer { return text.Read([]byte(s)) }

// dump renders a buffer the way the test tables write it, so a failure prints
// something that can be compared by eye with the vim run.
func dump(b *text.Buffer) string {
	var sb strings.Builder
	for n := 1; n <= b.LineCount(); n++ {
		if n > 1 {
			sb.WriteByte('\n')
		}
		sb.Write(b.Line(n))
	}
	return sb.String()
}

// at turns vim's 1-based line and column into a text.Pos, so a test can write
// the numbers the state dump printed instead of subtracting one everywhere.
func at(line, col int) text.Pos { return text.Pos{Line: line, Col: col - 1} }

// vimPos prints a position the way the state dump does, 1-based.
func vimPos(p text.Pos) string {
	return itoa(p.Line) + " " + itoa(p.Col+1)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	if neg {
		return "-" + string(d)
	}
	return string(d)
}

// checkCursor compares a result's cursor against the 1-based pair vim printed.
func checkCursor(t *testing.T, got text.Pos, line, col int) {
	t.Helper()
	if want := at(line, col); got != want {
		t.Errorf("cursor %s, want %s (vim's line col)", vimPos(got), vimPos(want))
	}
}

// lineOpts is the vanilla profile: vim --clean's own defaults, which is what
// the oracle's first profile runs.
func lineOpts() Options { return DefaultOptions() }

// vimrcOpts is the second profile: the set lines from ~/.vimrc that this
// package can see. sw=4 ts=2 sts=2 shiftround, textwidth=0, no expandtab and
// no autoindent, which is the combination that catches an implementation that
// assumes shiftwidth and tabstop are the same number.
func vimrcOpts() Options {
	o := DefaultOptions()
	o.ShiftWidth, o.TabStop, o.SoftTabStop = 4, 2, 2
	o.ShiftRound = true
	return o
}

// motionAt is the motion result a span test hands SpanForMotion, written in
// vim's 1-based line and column. inclusive is the one bit that decides whether
// the character it lands on is part of the span, and it is spelled out at every
// call site because it is the difference between d$ and d0 on a blank line.
func motionAt(line, col int, inclusive bool) motion.Result {
	kind := motion.KindCharExclusive
	if inclusive {
		kind = motion.KindCharInclusive
	}
	return motion.Result{To: at(line, col), Kind: kind, Ok: true}
}
