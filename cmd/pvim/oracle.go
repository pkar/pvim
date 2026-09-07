package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// The two artifacts pvim writes that the buffer file is not. The names are
// fixed by cmd/oracle and duplicated here rather than shared, because both are
// package main and neither can import the other. Nothing checks the two copies
// agree, so a rename on that side is a whole run of exit code 5 on this one;
// the names have not moved since the harness was written and this comment is
// the warning.
const (
	oracleStateFile    = "state.txt"
	oracleMessagesFile = "msgs.txt"
)

// oracleTrailer is the marker that starts the vimscript cmd/oracle appends to
// every keys file. Everything from it onwards dumps state and writes the
// buffer, in vim's language, and pvim has no vimscript to run it with: it
// recognises the marker instead and dumps the same state natively, in the same
// format, which is what the contract at the top of cmd/oracle/main.go says.
//
// Matched on the four Escapes and the :redir END together, because either alone
// occurs in ordinary keystrokes and the pair does not.
var oracleTrailer = []byte("\x1b\x1b\x1b\x1b:redir END\r")

// oracleScript is a keys file taken apart.
type oracleScript struct {
	// Opts is the :set line the harness put in front of the case, without the
	// ":set " and without the carriage return. Empty when the profile was
	// vanilla and the case had no .opts.
	Opts string
	// Messages is the file the harness redirected vim's message line into,
	// which is the file pvim has to write the same messages to.
	Messages string
	// Keys is the case's own keystrokes, with the prologue and the trailer
	// taken off.
	Keys []byte
	// Trailer says the state-dumping trailer was there. Without it the script
	// is somebody's own keys file rather than a harness run, and pvim writes
	// no state.txt, because writing one nobody asked for would leave a file in
	// a directory the user is sitting in.
	Trailer bool
}

// splitScript takes a keys file apart into the three pieces the harness built
// it from.
//
// It is a recogniser and not a parser: the shapes it knows are exactly the ones
// cmd/oracle/script.go writes, an optional ":set OPTS\r" then a
// ":redir! > FILE\r", and the trailer at the end. Anything it does not
// recognise is left in Keys, so a hand-written script with a colon command in
// it is fed through unchanged rather than being silently eaten.
func splitScript(b []byte) oracleScript {
	var s oracleScript

	if i := bytes.LastIndex(b, oracleTrailer); i >= 0 {
		s.Trailer = true
		b = b[:i]
	}
	if rest, opts, ok := cutCommand(b, ":set "); ok {
		s.Opts, b = opts, rest
	}
	if rest, file, ok := cutCommand(b, ":redir! > "); ok {
		s.Messages, b = file, rest
	}
	s.Keys = b
	return s
}

// cutCommand takes a "PREFIX ARG\r" off the front of b, returning the rest of
// b and the argument.
func cutCommand(b []byte, prefix string) (rest []byte, arg string, ok bool) {
	if !bytes.HasPrefix(b, []byte(prefix)) {
		return b, "", false
	}
	i := bytes.IndexByte(b, '\r')
	if i < 0 {
		return b, "", false
	}
	return b[i+1:], string(b[len(prefix):i]), true
}

// scriptKeys decodes a keystroke file into keys.
//
// Flush and not Next, on every call, because a file has no timing: the
// question a terminal answers with 'ttimeoutlen' -- is this 0x1b an Escape or
// the first byte of an arrow key -- cannot arise when every byte that is ever
// coming has already arrived. So a trailing Escape is an Escape, which is how
// every case in testdata/keys ends.
func scriptKeys(b []byte) []key.Key {
	var d key.Decoder
	var keys []key.Key
	for len(b) > 0 {
		k, n, st := d.Flush(b)
		if st != key.StatusKey || n == 0 {
			break
		}
		keys = append(keys, k)
		b = b[n:]
	}
	return keys
}

// runOracle is the headless path: no terminal, no window, no clock, and the
// three artifacts cmd/oracle diffs left in the working directory.
//
// It writes them whatever the editor did. A run that stopped on an
// unimplemented key still has a buffer and a cursor, and the oracle's job is to
// say how far that is from vim's; a run that writes nothing is exit code 5,
// which means "the candidate did not start" and is a different and much worse
// report than "the candidate got it wrong".
func runOracle(c config) error {
	script, err := os.ReadFile(c.keys)
	if err != nil {
		return err
	}
	s := splitScript(script)

	data, err := os.ReadFile(c.file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Give the editor a window. Without one H, M and L fail with Ok false and
	// every scrolling key is refused by the mode machine, which is 211 of the
	// 270 differences the last fuzz run reported. The size comes from --rows
	// and --cols, whose defaults are what vim reports on the harness's own
	// pseudo-terminal: 40 by 120, so 39 rows of text under a one-row command
	// line.
	ed, err := newEditor(data, c.file, c.rows, c.cols, s.Opts)
	if err != nil {
		return err
	}
	e := ed.ed

	// The mode machine's own option struct is filled a second time, from the
	// same :set line, by the reader in cmd/pvim/setline.go. It is not redundant:
	// that reader is what 608 graded runs were measured through, and
	// ex.ModeOptions is newer and has not been. When the two agree this costs
	// nothing; where they do not, the measured one wins and the disagreement is
	// a bug in the new one worth finding on its own.
	opts := e.Options()
	if err := applySet(&opts, s.Opts); err != nil {
		return err
	}
	// 'scrolloff' comes from internal/options and not from the flat mode
	// struct, because only one of the two was measured against a real vim:
	// "vim --clean" sources defaults.vim and comes up with 'scrolloff' 5,
	// while mode.DefaultOptions() carries the documented 0 instead. Five lines
	// is exactly where H and L land, so a headless run that took the 0 would
	// answer both wrong by five on every file. A .opts line that names scrolloff has already overwritten this
	// through applySet, so this only fills in the default.
	if !strings.Contains(s.Opts, "scrolloff") && !strings.Contains(s.Opts, "so=") {
		opts.ScrollOff = ed.opt.ScrollOffValue()
	}
	e.SetOptions(opts)
	// The keys came out of a file, so vim's may_sync_undo() never runs and the
	// whole script is one undo step: "xxx" then "u" puts all three characters
	// back, measured against vim 9.2.0321. Without this the editor closes an
	// undo block per command and every case with two changes and a u in it
	// differs from vim on the buffer, the changelist and the sequence number.
	e.SetScriptInput(true)

	runErr := endOfRun(ed.sess.Run(scriptKeys(s.Keys)))
	if s.Trailer && runErr == nil {
		// The trailer the harness appends starts with four Escapes, which vim
		// reads before it dumps anything: a case that runs off the end still
		// inside insert mode leaves it there, and leaving it moves the cursor
		// left and puts a newline in the message log. pvim never sees the
		// trailer, so it types the same Escapes itself. In normal mode they do
		// nothing, which is why the count is not worth being clever about.
		runErr = endOfRun(ed.sess.Run(scriptKeys([]byte("\x1b\x1b\x1b\x1b"))))
	}

	// The artifacts describe the buffer this run was pointed at, through
	// whichever mode machine is on it at the end.
	//
	// Two things make that a choice rather than "e". A ":e", a ":b" or a ":sp"
	// swaps the mode machine for a new one, and reading the pointer captured
	// before the run put "cursor 1 1 1" and an empty unnamed register in
	// state.txt after a script whose x had plainly deleted a character. And
	// pvim ends some scripts on a buffer that is not the file at all, which is
	// D-003: "K" and ":!" put their output in a scratch split where vim puts
	// it on the command line, so the mode machine that is current after a "K"
	// is over the output and not over the file the harness is grading, and
	// dumping that one describes the wrong buffer.
	//
	// Comparing buffers rather than names says both in one line.
	target := e
	if ed.ed.Buffer() == e.Buffer() {
		target = ed.ed
	}
	// The buffer and the state come from target, for the reason above; the
	// MESSAGE stream comes from the live editor whatever buffer it is over.
	// A buffer swap builds a new mode.Editor and AdoptMessages hands the stream
	// across, so the live one holds the whole run: dumping target's would lose
	// everything a ":new" or a ":e" said after the swap.
	if err := writeArtifacts(c, s, target, ed.ed); err != nil {
		return err
	}
	return runErr
}

// endOfRun maps what the mode machine stopped with onto what --oracle exits
// with.
//
// A quit is a zero. ZZ, :q and :wq stop the editor and vim exits 0 when they
// do, the keys after one are dropped the same way vim drops the rest of a -s
// file once it has quit, and the artifacts are written either way because the
// harness reads them off the disk and not out of a live editor. Anything else
// is a one, and cmd/oracle reads a candidate that exited non-zero as a
// difference even when the buffer came out right, which is what keeps a key the
// mode machine has not implemented from passing by luck.
func endOfRun(err error) error {
	if errors.Is(err, mode.ErrQuit) {
		return nil
	}
	return err
}

// writeArtifacts leaves the buffer, the state dump and the message log where
// cmd/oracle looks for them, which is beside the file that was edited.
//
// An empty file name is pvim started on no file at all. The harness never does
// that, and the state and the messages are still written, because a run with
// nothing to save still has registers and a cursor worth diffing.
//
// The buffer goes back to the file this run was pointed at, which is what the
// trailer's ":wq!" writes in the reference for every script that ends on the
// buffer it started on. A script that ends somewhere else writes that other
// file in both editors and leaves this one alone, and copying the buffer back
// over it is still the same bytes: vim refuses a ":e" over a modified buffer
// with E37, so a script that got to another file left this one as it was read.
func writeArtifacts(c config, s oracleScript, e, msgSrc *mode.Editor) error {
	if msgSrc == nil {
		msgSrc = e
	}
	dir := "."
	if c.file != "" {
		dir = filepath.Dir(c.file)
		if err := os.WriteFile(c.file, fileBytes(e), 0o644); err != nil {
			return err
		}
	}
	msgs := s.Messages
	if msgs == "" {
		msgs = oracleMessagesFile
	}
	if err := os.WriteFile(filepath.Join(dir, msgs), messageLog(msgSrc), 0o644); err != nil {
		return err
	}
	if !s.Trailer {
		return nil
	}
	return os.WriteFile(filepath.Join(dir, oracleStateFile), stateDump(e), 0o644)
}

// fileBytes is the buffer as it goes to disk, which is not always the buffer
// as it was read.
//
// 'fixendofline' is on by default and it means what it says: a file whose last
// line arrived with no separator is written back with one. Only 'nofixeol'
// keeps the file as it was. internal/text records what it read in NoEOL and
// says nothing about what to write, which is the right split -- the buffer is
// the file's contents and this is an option about saving.
func fileBytes(e *mode.Editor) []byte {
	if e.Options().FixEndOfLine {
		// The write is what fixes it, so the buffer stops being one that came
		// from a file with no separator. Clearing the flag rather than
		// appending a byte keeps the two shapes that both render as no bytes
		// apart: a buffer of one empty line writes a separator and a buffer
		// every line of which was deleted writes nothing at all.
		e.Buffer().SetNoEOL(false)
	}
	return e.Buffer().Bytes()
}

// messageLog is what vim's:redir caught: the editor's message stream, then
// the newline `:redir END` itself writes.
//
// The framing of the stream is internal/mode's business and the comment on its
// messages type says where each byte comes from. The trailing newline is this
// function's: it is the redirect being closed and not something the editor
// said, which is why a case that prints nothing still leaves a msgs.txt of
// exactly one byte.
func messageLog(e *mode.Editor) []byte {
	return append(e.Redirect(), '\n')
}

// stateDump writes the editor state in the format cmd/oracle/script.go's
// trailer makes vim write: one entry per line, tab separated, in this order.
//
//	cursor LINE COL VIRTCOL, all 1-based, as line(), col() and virtcol()
//	 report them
//	reg R the register type as getregtype() prints it, then the contents
//	 quoted, for the unnamed register, then "/, then 0-9, then a-z
//	mark M LINE COL for a-z, 0 0 for a mark that is not set
//	changelist string(getchangelist()), which is a vimscript list literal
//	undoseq undotree().seq_cur
func stateDump(e *mode.Editor) []byte {
	var b bytes.Buffer
	buf := e.Buffer()
	cur := e.Cursor()
	tab := e.Options().TabStop

	fmt.Fprintf(&b, "cursor\t%d %d %d\n",
		cur.Line, cur.Col+1, text.VirtCol(buf.Line(cur.Line), cur.Col, tab))

	for _, name := range register.Names() {
		v, err := e.Registers().Get(name)
		if err != nil {
			v = register.Value{}
		}
		fmt.Fprintf(&b, "reg %c\t%s\t%s\n", name, v.RegType(), quoteState(v.Bytes()))
	}

	// getpos() reports a 1-based column, and an unset mark as a flat 0 0
	// rather than as line 0 column 1. Both halves of that are the format and
	// neither is negotiable.
	for name := byte('a'); name <= 'z'; name++ {
		line, col := 0, 0
		if p, ok := buf.Mark(name); ok {
			line, col = p.Line, p.Col+1
		}
		fmt.Fprintf(&b, "mark %c\t%d %d\n", name, line, col)
	}

	fmt.Fprintf(&b, "changelist\t%s\n", changeList(buf))
	fmt.Fprintf(&b, "undoseq\t%d\n", buf.UndoSeq())
	return b.Bytes()
}

// changeList renders vim's getchangelist() the way string() prints it: a list
// holding a list of dictionaries and the current index.
//
// Two things about the format, both measured against vim 9.2.0321 through the
// harness rather than reasoned about, because a guess here is a diff on every
// case that changes anything.
//
// The column is zero-based, where getpos() and col() are one-based. `$~` on an
// eight-character line leaves "cursor 1 8 8" and
// [[{'lnum': 1, 'col': 7, 'coladd': 0}], 1] in the same dump, and `3lX` leaves
// "cursor 1 3 3" with col 2, so it is the base and not an off-by-one in one
// case.
//
// The list holds one entry, wherever the last change was, however many changes
// the script made and however far apart. `x` then `G` then `x` over five lines
// gives lnum 5; `x` `j` `x` `G` `x` gives lnum 5; `x...` gives lnum 1. vim adds
// an entry only when it starts a new change list and updates that entry
// otherwise, so a script that never syncs one has exactly one. A buffer model
// that appends a position per edit renders several here and differs from vim on
// every case with two changes in it, which is most of them.
func changeList(b *text.Buffer) string {
	var out strings.Builder
	out.WriteString("[[")
	for i, p := range b.ChangeList() {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(&out, "{'lnum': %d, 'col': %d, 'coladd': 0}", p.Line, p.Col)
	}
	fmt.Fprintf(&out, "], %d]", len(b.ChangeList()))
	return out.String()
}

// quoteState escapes a register's contents the way the trailer's PvimQuote
// does: backslash, newline, carriage return and tab, and nothing else. The
// escaping exists because writefile() splits on newlines and a register holding
// one would otherwise become two lines of the dump.
func quoteState(v []byte) string {
	var out strings.Builder
	for _, c := range v {
		switch c {
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}
