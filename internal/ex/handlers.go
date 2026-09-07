package ex

import (
	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/substitute"
)

// The table's Handler column, filled in one place.
//
// The handlers are attached here rather than written into the table literal
// for one reason: the table's order is the specification of what ":c" means
// and a reader checking it against vim's should see names and flags and
// nothing else. A handler added in the wrong row would be a bug that reads
// like a formatting choice.
//
// A name with no entry here keeps a nil Handler, which RunCmd answers with
// E319. That is deliberate and is not the same as E492: E492 means vim has no
// such command either, and E319 means pvim knows the command and has not
// written it yet. Today's E319 list is the commands that live in another
// package or are not written at all: ":let" and the rest of the vimrc
// language are internal/vimrc's; ":hi" and ":colorscheme" are
// internal/screen's; ":make", ":cfile" and ":vimgrep" are unimplemented.
func init() {
	handlers := map[string]Handler{
		// The text of a range.
		"delete": exDelete,
		"yank":   exYank,
		"put":    exPut,
		"copy":   exCopy,
		"t":      exCopy,
		"move":   exMove,
		"join":   exJoin,
		"normal": exNormal,
		"k":      exMark,
		"mark":   exMark,
		"print":  exPrint('p'),
		"number": exPrint('#'),
		"list":   exPrint('l'),
		"#":      exPrint('#'),
		"undo":   exUndo,
		"redo":   exRedo,

		// The pattern commands. internal/substitute does the matching and the
		// replacing; internal/ex/substitutecmd.go supplies the buffer, the
		// undo block and the ":g" loop.
		"substitute": exSubstitute(substitute.KindSubstitute),
		"&":          exSubstitute(substitute.KindAmpersand),
		"~":          exSubstitute(substitute.KindTilde),
		"global":     exGlobal(false),
		"vglobal":    exGlobal(true),

		// Files and quitting.
		"write": exWrite,
		"wall":  exWriteAll(false),
		"wq":    exWriteQuit(true),
		"xit":   exWriteQuit(false),
		"wqall": exWriteAll(true),
		"xall":  exWriteAll(true),
		"quit":  exQuit(false),
		"qall":  exQuit(true),
		"edit":  exEdit,
		"enew":  exEnew,
		"find":  exFind,
		"read":  exRead,
		"file":  exFile,
		"cquit": exQuit(true),

		// The buffer list.
		"ls":        exLs,
		"files":     exLs,
		"buffers":   exLs,
		"buffer":    exBuffer,
		"bnext":     exBufferStep(1, 0),
		"bprevious": exBufferStep(-1, 0),
		"bfirst":    exBufferStep(0, 1),
		"blast":     exBufferStep(0, -1),
		"bdelete":   exBdelete,

		// Windows and tabs.
		"split":       exSplit(false, false),
		"vsplit":      exSplit(true, false),
		"new":         exSplit(false, true),
		"vnew":        exSplit(true, true),
		"close":       exCloseWindow,
		"only":        exOnly,
		"tabnew":      exTabNew,
		"tabedit":     exTabNew,
		"tabclose":    exTabClose,
		"tabonly":     exTabOnly,
		"tabnext":     exTabStep(true),
		"tabprevious": exTabStep(false),
		"tabs":        exTabs,
		"wincmd":      exWincmd,

		// The shell.
		"!": exFilter,

		// Quickfix.
		"cc":        exCC,
		"cnext":     exCStep(1),
		"cprevious": exCStep(-1),
		"cfirst":    exCC,
		"clist":     exCList,
		"cwindow":   exCOpen,
		"copen":     exCOpen,
		"cclose":    exCClose,
		"cnfile":    exCNextFile,

		// State.
		"marks":      exMarks,
		"jumps":      exJumps,
		"clearjumps": exClearJumps,
		"changes":    exChanges,
		"registers":  exRegisters,
		"display":    exRegisters,
		"set":        exSet(options.Both),
		"setlocal":   exSet(options.SetLocal),
		"setglobal":  exSet(options.SetGlobal),
		"pwd":        exPwd,
		"cd":         exCd,
		"lcd":        exCd,
		"nohlsearch": exNohlsearch,
		"version":    exVersion,
		"help":       exHelp,

		// User commands.
		"command":    exCommand,
		"delcommand": exDelcommand,
	}
	for _, name := range mapCommands {
		handlers[name] = exMap
	}
	for i := range commands {
		if h, ok := handlers[commands[i].Name]; ok {
			commands[i].Handler = h
		}
	}
	// The shift commands are spelled with one to five angle brackets and the
	// table holds one entry each, so they are attached by name rather than
	// through the map above.
	for _, name := range []string{"<", ">"} {
		if cmd, ok := find(name); ok {
			cmd.Handler = exShift
		}
	}
	if cmd, ok := find("="); ok {
		cmd.Handler = exEquals
	}
}
