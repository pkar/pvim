package mode

import (
	"bytes"
	"strconv"

	"github.com/pkar/pvim/internal/text"
)

// CTRL-N and CTRL-P: keyword completion out of the words already in the
// buffer.
//
// 'completeopt' decides what the first press does and it is the whole reason
// this is a state machine rather than a lookup. The vimrc sets
// "menu,menuone,noselect,noinsert", which means the first CTRL-N shows a menu
// and inserts nothing at all; vim's own default is "menu,preview", where the
// first CTRL-N inserts the first match. An implementation that only does the
// second is an editor that types a word you did not ask for every time you
// reach for the menu.
//
// There is no menu here. internal/screen draws one and reads the
// state through Completion; this file decides what is in it and what is in the
// buffer.

// completeSubmode is what showmode() prints instead of "-- INSERT --" while a
// keyword completion is running. It is vim's edit_submode, with the leading
// space vim puts there, and it is deliberately not "-- INSERT --" plus
// something: the redirect holds "-- Keyword completion (^N^P) match 1 of 2"
// with no trailing " --", which is the shape showmode takes when edit_submode
// is set.
const completeSubmode = "-- Keyword completion (^N^P)"

// The four things vim says about where a completion has got to, its
// edit_submode_extra. Measured one at a time; "Back at original" is what
// 'noselect' leaves on the first press and what cycling past the last match
// comes back to.
const (
	completeSearching = "-- Searching..."
	completeOnly      = "The only match"
	completeOriginal  = "Back at original"
	completeNoMatch   = "Pattern not found"
	// completeScanning is an ordinary message and not part of showmode: it
	// gets a newline of its own in the redirect. vim prints it once per
	// completion because 'complete' contains "t" and it scans the tag files
	// whether or not the buffer already answered.
	completeScanning = "Scanning tags."
)

// completion is the CTRL-N session in progress.
type completion struct {
	active bool
	// extra is the edit_submode_extra showmode is to print after the submode
	// name, empty for the bare "-- Keyword completion (^N^P)" vim shows for
	// the instant between one candidate and the next.
	extra string
	// words are the candidates, in the order vim offers them: forward from
	// the cursor and then round the top of the buffer.
	words [][]byte
	// idx is the selected candidate, or -1 for none, which is what
	// 'noselect' leaves it at until the second press.
	idx int
	// at is where the word being completed starts, and prefix is what had been
	// typed of it.
	at     text.Pos
	prefix []byte
	// shown is how many bytes of a candidate are currently in the buffer past
	// the prefix, so that moving to the next one knows what to take out.
	shown int
}

// Completion is the completion in progress: the candidates and which one is
// selected, or nil when there is none. The popup menu reads it; nothing else
// does.
func (e *Editor) Completion() (words [][]byte, selected int, active bool) {
	return e.comp.words, e.comp.idx, e.comp.active
}

// message is what showmode prints while a completion is running.
func (c completion) message() string {
	if c.extra == "" {
		return completeSubmode
	}
	return completeSubmode + " " + c.extra
}

// complete is CTRL-N and CTRL-P.
func (e *Editor) complete(forward bool) error { return e.completeFrom(forward, false) }

// Omni is CTRL-X CTRL-O: the same completion machinery over the source
// SetOmniFunc installed rather than over the buffer's own words.
func (e *Editor) Omni() error { return e.completeFrom(true, true) }

// completeFrom is the body of both. omni picks the source and is remembered for
// the life of the completion, so a CTRL-N after a CTRL-X CTRL-O steps through
// the candidates the server gave rather than starting a buffer completion.
func (e *Editor) completeFrom(forward, omni bool) error {
	if e.mode != Insert && e.mode != Replace {
		return e.beep()
	}
	if !e.comp.active {
		return e.startCompletion(forward, omni)
	}
	// Every press after the first shows the submode with nothing after it
	// before it shows where it got to. Measured: two CTRL-N over two
	// candidates leave "match 1 of 2", then a bare submode, then
	// "match 2 of 2" in the redirect.
	e.comp.extra = ""
	e.showMode()
	return e.stepCompletion(forward)
}

// endCompletion is what any key that is not CTRL-N or CTRL-P does to a
// completion in progress: vim says where it got to one more time and then goes
// back to saying "-- INSERT --". Measured on CTRL-N then X, where the redirect
// holds "The only match" twice and then the mode message, before the X is even
// inserted.
func (e *Editor) endCompletion() {
	if !e.comp.active {
		return
	}
	e.showMode()
	e.comp = completion{}
	e.showMode()
}

// startCompletion gathers the candidates and, unless 'completeopt' says
// otherwise, inserts the first of them.
func (e *Editor) startCompletion(forward, omni bool) error {
	line := e.buf.Line(e.cur.Line)
	start := e.cur.Col
	for start > 0 && isKeywordByte(line[prevRune(line, start)], e.opt.IsKeyword) {
		start = prevRune(line, start)
	}
	prefix := append([]byte(nil), line[start:e.cur.Col]...)

	e.comp = completion{
		active: true,
		idx:    -1,
		at:     text.Pos{Line: e.cur.Line, Col: start},
		prefix: prefix,
		extra:  completeSearching,
	}
	e.showMode()
	e.msg.say(completeScanning)

	// vim's ins_compl_new_leader takes the word being completed out and puts
	// it back whatever it then finds, so a completion saves an undo header and
	// lands in the changelist even when there is no match at all: measured, a
	// CTRL-N over "zq" with nothing to complete leaves the buffer alone with
	// undotree().seq_cur at 1 and getchangelist() naming the last byte of the
	// word.
	e.rewriteWord(prefix)

	// The omnifunc first when one is installed and this is an omni completion,
	// then the buffer. internal/lsp fills the slot with gopls's answer, which
	// is what makes the vimrc's "au filetype go inoremap <buffer> . .<C-x><C-o>"
	// mean anything: without a feed the key opened an empty menu.
	if e.omni != nil && omni {
		e.comp.words = e.omni(prefix)
	} else {
		e.comp.words = e.bufferWords(prefix, forward)
	}
	if len(e.comp.words) == 0 {
		e.comp.extra = completeNoMatch
		e.showMode()
		return nil
	}
	if hasFlag(e.opt.CompleteOpt, "noinsert") || hasFlag(e.opt.CompleteOpt, "noselect") {
		e.comp.extra = e.completeStatus()
		e.showMode()
		return nil
	}
	return e.stepCompletion(forward)
}

// rewriteWord deletes the word being completed and puts word back in its
// place, which is what vim does on every step of a completion and is where the
// undo header and the changelist entry come from. The entry names the last
// byte of what is now there, because vim inserts one character at a time and
// each one reports its own column.
func (e *Editor) rewriteWord(word []byte) {
	e.insUndo()
	at := e.comp.at
	line := e.buf.Line(at.Line)
	end := at.Col + len(e.comp.prefix) + e.comp.shown
	if end > len(line) {
		end = len(line)
	}
	e.buf.Delete(text.Range{Start: at, End: text.Pos{Line: at.Line, Col: end}})
	e.cur = e.buf.Insert(at, word)
	if len(word) > 0 {
		e.buf.ChangedAt(text.Pos{Line: at.Line, Col: at.Col + len(word) - 1})
	}
}

// completeStatus is vim's edit_submode_extra for where the completion has got
// to: which of how many, or that it is showing the typed text again.
func (e *Editor) completeStatus() string {
	switch {
	case e.comp.idx < 0:
		return completeOriginal
	case len(e.comp.words) == 1:
		return completeOnly
	default:
		return "match " + strconv.Itoa(e.comp.idx+1) + " of " + strconv.Itoa(len(e.comp.words))
	}
}

// stepCompletion moves to the next or previous candidate and puts it in the
// buffer in place of the one before it.
func (e *Editor) stepCompletion(forward bool) error {
	n := len(e.comp.words)
	switch {
	case forward:
		e.comp.idx++
		if e.comp.idx >= n {
			e.comp.idx = -1
		}
	default:
		e.comp.idx--
		if e.comp.idx < -1 {
			e.comp.idx = n - 1
		}
	}

	// Out with whatever the last candidate put in, and in with the new one.
	// It is one rewrite of the whole word and not an edit of its tail, because
	// that is what vim does and what the changelist column reports.
	word := e.comp.prefix
	if e.comp.idx >= 0 {
		word = e.comp.words[e.comp.idx]
	}
	if e.comp.shown > 0 {
		e.trimTyped(e.comp.shown)
	}
	// rewriteWord reads shown to know how much of the last candidate is in the
	// buffer, so it is cleared after the rewrite and not before it.
	e.rewriteWord(word)
	e.comp.shown = 0
	if e.comp.idx >= 0 {
		add := word[len(e.comp.prefix):]
		e.ins.text = append(e.ins.text, add...)
		e.comp.shown = len(add)
	}
	e.comp.extra = e.completeStatus()
	e.showMode()
	return nil
}

// bufferWords finds every keyword in the buffer that starts with the prefix,
// in the order vim offers them: from the cursor forward and round the end for
// CTRL-N, backward and round the start for CTRL-P. Duplicates are dropped,
// keeping the first one offered.
func (e *Editor) bufferWords(prefix []byte, forward bool) [][]byte {
	type found struct {
		word []byte
		at   text.Pos
	}
	var all []found
	for ln := 1; ln <= e.buf.LineCount(); ln++ {
		line := e.buf.Line(ln)
		for i := 0; i < len(line); {
			if !isKeywordByte(line[i], e.opt.IsKeyword) {
				i = nextRune(line, i)
				continue
			}
			j := i
			for j < len(line) && isKeywordByte(line[j], e.opt.IsKeyword) {
				j = nextRune(line, j)
			}
			w := line[i:j]
			if len(w) > len(prefix) && bytes.HasPrefix(w, prefix) {
				all = append(all, found{word: append([]byte(nil), w...), at: text.Pos{Line: ln, Col: i}})
			}
			i = j
		}
	}

	var ordered []found
	if forward {
		for _, f := range all {
			if f.at.Compare(e.cur) > 0 {
				ordered = append(ordered, f)
			}
		}
		for _, f := range all {
			if f.at.Compare(e.cur) <= 0 {
				ordered = append(ordered, f)
			}
		}
	} else {
		for i := len(all) - 1; i >= 0; i-- {
			if all[i].at.Compare(e.cur) < 0 {
				ordered = append(ordered, all[i])
			}
		}
		for i := len(all) - 1; i >= 0; i-- {
			if all[i].at.Compare(e.cur) >= 0 {
				ordered = append(ordered, all[i])
			}
		}
	}

	var out [][]byte
	seen := map[string]bool{}
	for _, f := range ordered {
		if seen[string(f.word)] {
			continue
		}
		seen[string(f.word)] = true
		out = append(out, f.word)
	}
	return out
}

// SetOmniFunc installs the source CTRL-X CTRL-O completes from.
//
// It is a func rather than an interface because there is exactly one caller and
// exactly one thing it returns: the candidate words for a prefix, in the order
// the menu shows them. internal/lsp fills it from gopls; nothing else does, and
// a nil slot means CTRL-X CTRL-O falls back to the buffer's own words, which is
// vim's behaviour with 'omnifunc' empty.
func (e *Editor) SetOmniFunc(f func(prefix []byte) [][]byte) { e.omni = f }
