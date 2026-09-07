package clip

import (
	"bytes"

	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// The four answers to "what type is the text on the clipboard", which is the
// one question a pasteboard string cannot answer on its own.
//
// The first three are vim's own MCHAR, MLINE and MBLOCK, which is what it puts
// in the first element of the VimPboardType plist. They are spelled out here
// rather than derived from register.Type because they are a wire format: the
// day register.Type grows a fourth constant or changes order, these numbers
// must not move, because MacVim on the other side of the pasteboard is still
// writing 0, 1 and 2.
const (
	motionChar  = 0 // vim's MCHAR
	motionLine  = 1 // vim's MLINE
	motionBlock = 2 // vim's MBLOCK

	// motionNone is not vim's: it is this package's way of saying the
	// pasteboard carried no VimPboardType at all, which is every copy from
	// every application that is not vim. vim calls the same case MAUTO and
	// reaches it two ways, the second being a VimPboardType whose number is
	// none of the three above. Measured: a plist holding 7 and "alpha" reads
	// back charwise and 7 and "alpha\n" reads back linewise, which is the
	// trailing-newline rule and not a refusal, so an unknown number lands here.
	motionNone = -1
)

// motionOf is the number that goes on the pasteboard for a register value.
//
// There is no case for a $ block. A blockwise yank made with $ has no right
// edge and register.Value carries that as ToEOL, and vim's own pasteboard
// write does not carry it either: measured, CTRL-V j $ y in vim over "alpha"
// and "beta" puts 2 and "alpha\nbeta\n" on the board, the same plist a fixed
// width block of the same text would put there. So a $ block pasted back is an
// ordinary block, in vim and here, and matching vim is the whole point.
func motionOf(t register.Type) int {
	switch t {
	case register.TypeLine:
		return motionLine
	case register.TypeBlock:
		return motionBlock
	default:
		return motionChar
	}
}

// valueOfMotion turns clipboard text plus vim's motion number into a register
// value.
//
// Every rule below was measured, not read: this package's own put() put a plist
// of [motion, text] on the general pasteboard and /opt/homebrew/bin/vim 9.2.0321
// was asked for getregtype('*') and getreg('*') through writefile(). What came
// back:
//
//	motion text getregtype getreg
//	none "alpha" v "alpha"
//	none "alpha\n" V "alpha\n"
//	none "alpha\nbeta" v "alpha\nbeta"
//	0 "alpha\n" v "alpha\n"
//	0 "a\nb\n" v "a\nb\n"
//	1 "alpha" V "alpha\n"
//	1 "alpha\nbeta" V "alpha\nbeta\n"
//	2 "abcd\nef\n" ^V4 "abcd\nef"
//	2 "ab\ncdef" ^V4 "ab\ncdef"
//	2 "a\n\nb\n" ^V1 "a\n\nb"
//	7 "alpha" v "alpha"
//	7 "alpha\n" V "alpha\n"
//	-7 "alpha" v "alpha"
//
// So: the number wins over the trailing newline when it is one vim knows, a
// linewise value keeps its lines whether or not the text ended in a newline, a
// blockwise one drops one trailing newline and recomputes its width, and
// anything else falls back to the newline rule ValueOf implements.
//
// Empty text is an empty register whatever the number says, which is one place
// this does not follow vim: vim answers V and a single blank line for an empty
// string carrying MLINE. It is left as it is because an empty string with a vim
// plist beside it is a pasteboard nobody can produce by copying anything, and
// because "nothing has been copied" reading as an empty register is what makes
// "+p on a fresh login do nothing rather than insert a blank line.
func valueOfMotion(b []byte, motion int) register.Value {
	if len(b) == 0 {
		return register.Value{}
	}
	switch motion {
	case motionChar:
		return register.Char(split(b)...)
	case motionLine:
		return register.LineValue(split(bytes.TrimSuffix(b, []byte("\n")))...)
	case motionBlock:
		lines := split(bytes.TrimSuffix(b, []byte("\n")))
		return register.BlockValue(blockWidth(lines), lines...)
	default:
		return ValueOf(b)
	}
}

// bytesFor is the text that goes on the pasteboard for a register value.
//
// It is Bytes with one addition, and the addition is vim's: a blockwise value
// goes on the board with a newline after every line, including the last.
// Measured, CTRL-V j l "*y over "alpha" and "beta" puts "lp\net\n" on the
// pasteboard, where Bytes of the same register is "lp\net". Other applications
// see the trailing newline and pvim on the way back drops it again, which is
// the round trip vim has.
func bytesFor(v register.Value) []byte {
	b := Bytes(v)
	if v.Type == register.TypeBlock && len(v.Lines) > 0 {
		b = append(b, '\n')
	}
	return b
}

// blockWidth is the width vim gives a block that arrived from the clipboard:
// the widest line in it, in the number getregtype() prints after the CTRL-V.
//
// vim recomputes this on every read because the plist does not carry it, so a
// block that came from another vim is measured here and not remembered from
// there. Measured through the same probe as the table above: "abcd\nef\n"
// reads ^V4 and "a\n\nb\n" reads ^V1, so a blank line contributes nothing and
// the answer is a maximum and not a sum.
func blockWidth(lines [][]byte) int {
	w := 0
	for _, line := range lines {
		if n := cells(line); n > w {
			w = n
		}
	}
	return w
}

// cells is how many screen columns a line of a pasted block occupies, which is
// vim's mb_string2cells over the line: utf_ptr2cells per character, with the
// combining marks absorbed into the character in front of them, and not the
// display-column rule a buffer is drawn with. A tab counts one and a control
// character counts one, because str_to_reg measures the text as characters,
// never looks at 'tabstop', and utf_ptr2cells answers 1 for every byte below
// 0x80 whether or not it is printable.
//
// Measured, by calling setreg('z', text . "\nx", 'b') under
// /opt/homebrew/bin/vim 9.2.0321 and reading getregtype('z') back. setreg with
// 'b' is the same str_to_reg the pasteboard read goes through, so it is the
// same measurement the plist probe in valueOfMotion above took, one process
// cheaper:
//
//	text vim
//	"abcd" ^V4
//	"a\tb" ^V3
//	"\x01\x02" ^V2
//	"\x7f" ^V1
//	"é" ^V1
//	"日本" ^V4
//	"日本語" ^V6
//	"\u0085" ^V4
//	"\u200b" ^V6
//	"e\u0301" ^V1
//	"\u0301" ^V1
//	"🙂" ^V2
//	"ａｂ" ^V4
//
// So the answer is text.DisplayWidth at a 'tabstop' of one, minus a column for
// every C0 control and DEL in the line. A tabstop of one already makes a tab
// one cell wherever it falls; internal/text is the only place that disagrees
// with vim here, because it draws a control as the two cells its ^X takes on
// the screen and a block width counts characters instead. Every byte below 0x20
// and 0x7f is its own base character -- a continuation byte and a combining
// mark are both above 0x7f -- so the correction is a count of bytes and cannot
// double-subtract.
//
// This used to be utf8.RuneCount, which gave a wide character one column and so
// pasted a block of CJK from MacVim two columns narrow for every 日 in its
// widest line, because Width is what pads the short lines of a block. The
// argument for the rune count was that internal/text and internal/screen hold a
// width table each and this package should not hold a third. That was the wrong
// half to give up: internal/text imports nothing else in the module, so the
// arrow to it costs one leaf package and no table at all, and register.Value
// documents Width as display columns, which is a contract this package was the
// only writer to break.
func cells(line []byte) int {
	// A tabstop of one so a tab is one cell wherever it starts.
	n := text.DisplayWidth(line, 1)
	for _, b := range line {
		if b == '\t' {
			continue
		}
		if b < 0x20 || b == 0x7f {
			n--
		}
	}
	return n
}

// clone copies a value's lines so that the caller and this package's cache
// cannot write through each other.
//
// register.File does not clone what a Clipboard hands back -- it clones only
// the values in its own map -- so without this the slice behind the last
// pasteboard read is the same slice the editor is about to put, and a put that
// modified it in place would change what the next Read answers.
func clone(v register.Value) register.Value {
	if v.Lines == nil {
		return v
	}
	lines := make([][]byte, len(v.Lines))
	for i, line := range v.Lines {
		lines[i] = append([]byte(nil), line...)
	}
	v.Lines = lines
	return v
}
