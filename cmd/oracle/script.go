package main

import "bytes"

// The three artifacts every side of a run has to produce. The buffer is the
// file the editor was pointed at; the other two are written by the trailer and
// the redirect below.
const (
	bufferName   = "buf.txt"
	stateName    = "state.txt"
	messagesName = "msgs.txt"
)

// The terminal the harness hands both sides. Fixed, because whether vim wraps a
// message and therefore whether it stops for a hit-enter prompt that eats the
// next keystroke depends on the width, and a case that passes on a 200-column
// terminal and fails on an 80-column one is not a case, it is a coin.
const (
	ptyRows = 40
	ptyCols = 120
)

// script assembles the bytes that go into the keys file: the options line, the
// message redirect, the case's own keystrokes, and the trailer.
//
// Both sides get byte-identical input. That is the whole contract: pvim
// --oracle -s is handed the same file vim -s is handed, and is expected to
// leave the same three artifacts in its working directory.
func script(opts string, keys []byte) []byte {
	var b bytes.Buffer

	// Options first, message redirect second, so that a broken :set line
	// complains on the terminal instead of into every case's message file.
	if opts != "" {
		b.WriteString(":set " + opts + "\r")
	}
	b.WriteString(":redir! > " + messagesName + "\r")

	b.Write(keys)

	b.Write(trailer)
	return b.Bytes()
}

// trailer is appended to every case. It dumps the editor state vim's own
// functions can see into state.txt, because a buffer that matches with the
// wrong registers is a bug that has not been noticed yet.
//
// The four leading Escapes are slack, not superstition. One leaves insert or
// replace mode, one cancels a half-typed operator or command line, and one is
// eaten by the hit-enter prompt that any error message during the case leaves
// pending. Extra Escapes in normal mode do nothing, so the count is cheap.
//
// The function definition works because vim reads the body of a:function from
// whatever it is currently reading commands from, which under -s is the script
// itself. It exists so the quoting rule is written once: a register holding a
// newline has to survive writefile(), which splits on newlines, so backslash,
// NL, CR and tab are escaped and everything else goes through as itself.
var trailer = []byte("\x1b\x1b\x1b\x1b" +
	":redir END\r" +

	":function! PvimQuote(s) abort\r" +
	`let r = substitute(a:s, '\\', '\\\\', 'g')` + "\r" +
	`let r = substitute(r, '\n', '\\n', 'g')` + "\r" +
	`let r = substitute(r, '\r', '\\r', 'g')` + "\r" +
	`let r = substitute(r, '\t', '\\t', 'g')` + "\r" +
	"return r\r" +
	"endfunction\r" +

	":let g:PvimState = []\r" +
	`:call add(g:PvimState, "cursor\t" . line('.') . ' ' . col('.') . ' ' . virtcol('.'))` + "\r" +

	// The unnamed register, the last search pattern, the numbered shift and
	// the named registers, each with its type: a yank that lands linewise
	// where vim made it characterwise is exactly the diff worth catching.
	`:let g:PvimRegs = ['"', '/'] + map(range(10), 'string(v:val)') + map(range(26), 'nr2char(97 + v:val)')` + "\r" +
	`:for r in g:PvimRegs | call add(g:PvimState, 'reg ' . r . "\t" . getregtype(r) . "\t" . PvimQuote(getreg(r))) | endfor` + "\r" +

	`:for m in map(range(26), 'nr2char(97 + v:val)') | call add(g:PvimState, 'mark ' . m . "\t" . getpos("'" . m)[1] . ' ' . getpos("'" . m)[2]) | endfor` + "\r" +

	`:call add(g:PvimState, "changelist\t" . string(getchangelist()))` + "\r" +
	`:call add(g:PvimState, "undoseq\t" . undotree().seq_cur)` + "\r" +

	":call writefile(g:PvimState, '" + stateName + "')\r" +
	":wq!\r")
