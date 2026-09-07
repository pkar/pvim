package undofile

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// E325, the message vim prints when it finds a swap file, and the prompt under
// it.
//
// Every string here was taken out of vim 9.2.0321 rather than out of the
// documentation, twice: once by running the real thing on a pseudo-terminal
// with a swap file left behind by a kill -9, and once with `strings` over
// /opt/homebrew/Cellar/macvim/9.2.0321/MacVim.app/Contents/MacOS/Vim, which is
// where the leading spaces came from -- a terminal wraps at column 80 and eats
// them, and " owned by: " has ten of them. TestAttentionMatchesVim
// holds the whole message against the transcript of that run, and the run is
// reproducible:
//
//	printf ':sleep 60\r' > sk
//	script -q /dev/null vim -u NONE -i NONE --cmd 'set directory=DIR//' -s sk f.txt &
//	kill -9 $(pgrep -f 'MacOS/Vim -u NONE') # the crash
//	printf 'q' > qk
//	script -q e325.txt vim -u NONE -i NONE --cmd 'set directory=DIR//' -s qk f.txt
//
// The reason for the fidelity is not nostalgia. This message is the only thing
// standing between a person and a day's lost work, it is read once a year under
// stress, and every word of it is a word they have read before.

// The prompt's choices. Vim offers six, and five when the process that made the
// swap file is still running: deleting the swap file of a live editor would
// take that editor's recovery away, so the option is not on the menu.
type SwapChoice int

// The choices, in the order vim lists them.
const (
	SwapReadOnly SwapChoice = iota
	SwapEdit
	SwapRecover
	SwapDelete
	SwapQuit
	SwapAbort
)

// swapChoiceNames are vim's button labels with the '&' that marks the shortcut,
// which is how vim stores them and how the prompt below is rendered.
var swapChoiceNames = map[SwapChoice]string{
	SwapReadOnly: "&Open Read-Only",
	SwapEdit:     "&Edit anyway",
	SwapRecover:  "&Recover",
	SwapDelete:   "&Delete it",
	SwapQuit:     "&Quit",
	SwapAbort:    "&Abort",
}

// SwapChoices is the list offered for a swap file, with "(D)elete it" left out
// when the process that wrote it is still alive.
func SwapChoices(stillRunning bool) []SwapChoice {
	if stillRunning {
		return []SwapChoice{SwapReadOnly, SwapEdit, SwapRecover, SwapQuit, SwapAbort}
	}
	return []SwapChoice{SwapReadOnly, SwapEdit, SwapRecover, SwapDelete, SwapQuit, SwapAbort}
}

// SwapPrompt is the line the person answers, rendered the way vim's do_dialog
// renders a dialog on a console: the default choice in square brackets, the
// rest in round ones, comma-separated, and a trailing space after the colon.
//
//	[O]pen Read-Only, (E)dit anyway, (R)ecover, (D)elete it, (Q)uit, (A)bort:
//
// The default is the first, which is what vim passes as dfltbutton, and it is
// the safe one: a person who holds down Enter to get past a message they did
// not read ends up with a read-only buffer and no way to lose anything.
func SwapPrompt(stillRunning bool) string {
	var b strings.Builder
	for i, c := range SwapChoices(stillRunning) {
		if i > 0 {
			b.WriteString(", ")
		}
		name := swapChoiceNames[c]
		at := strings.IndexByte(name, '&')
		l, r := "(", ")"
		if i == 0 {
			l, r = "[", "]"
		}
		b.WriteString(name[:at])
		b.WriteString(l)
		b.WriteString(name[at+1 : at+2])
		b.WriteString(r)
		b.WriteString(name[at+2:])
	}
	b.WriteString(": ")
	return b.String()
}

// ParseSwapChoice reads one keystroke as an answer to the prompt.
//
// Case-insensitive, as vim's dialog is. Enter takes the default, which is the
// first choice; Escape aborts, which is what vim's do_dialog does with a
// cancelled dialog. A key that is not a choice is not an answer and the prompt
// stays up, which is also vim.
func ParseSwapChoice(b byte, stillRunning bool) (SwapChoice, bool) {
	switch b {
	case '\r', '\n':
		return SwapReadOnly, true
	case 0x1b:
		return SwapAbort, true
	}
	for _, c := range SwapChoices(stillRunning) {
		name := swapChoiceNames[c]
		key := name[strings.IndexByte(name, '&')+1]
		if b == key || b == key+('a'-'A') {
			return c, true
		}
	}
	return 0, false
}

// Attention is the whole E325 message for a swap file, ready to print.
//
// opening is the file name as the user gave it, which is what vim prints in the
// "While opening file" line -- not the resolved absolute path, because a person
// who typed "pvim f.txt" is looking for "f.txt" in the message.
//
// The last line is the prompt from SwapPrompt. A caller that wants to put the
// prompt somewhere else -- a command line rather than the end of a scrolled
// message -- takes it off and asks for it separately.
func (i SwapInfo) Attention(opening string) string {
	var b strings.Builder
	// One newline between the two and not two: vim's emsg() prints the E-code
	// with no newline of its own and the line after it starts with one. The
	// transcript has them on consecutive lines and a blank line here would be
	// the first thing wrong with the message.
	b.WriteString("E325: ATTENTION")
	b.WriteString("\nFound a swap file by the name \"")
	b.WriteString(i.Path)
	b.WriteString("\"\n")
	i.info(&b)
	b.WriteString("While opening file \"")
	b.WriteString(opening)
	b.WriteString("\"\n")
	if st, err := os.Stat(opening); err != nil {
		b.WriteString("      CANNOT BE FOUND")
	} else {
		b.WriteString("             dated: ")
		b.WriteString(ctime(st.ModTime()))
		if !i.Mtime.IsZero() && st.ModTime().After(i.Mtime) {
			b.WriteString("      NEWER than swap file!\n")
		}
	}
	b.WriteString("\n(1) Another program may be editing the same file.  If this is the case,\n")
	b.WriteString("    be careful not to end up with two different instances of the same\n")
	b.WriteString("    file when making changes.  Quit, or continue with caution.\n")
	b.WriteString("(2) An edit session for this file crashed.\n")
	b.WriteString("    If this is the case, use \":recover\" or \"vim -r ")
	b.WriteString(opening)
	b.WriteString("\"\n    to recover the changes (see \":help recovery\").\n")
	b.WriteString("    If you did this already, delete the swap file \"")
	b.WriteString(i.Path)
	b.WriteString("\"\n    to avoid this message.\n")
	b.WriteString("\nSwap file \"")
	b.WriteString(i.Path)
	b.WriteString("\" already exists!\n")
	b.WriteString(SwapPrompt(i.Running))
	return b.String()
}

// info is vim's swapfile_info(): the block between the swap file's name and the
// "While opening file" line.
//
// The alignment is vim's, to the space. The "owned by" clause is dropped when
// a user cannot be named and the "dated:" that follows is re-indented from
// three spaces to thirteen, which is the sort of detail nobody would invent and
// which falls straight out of vim having two literals for it.
func (i SwapInfo) info(b *strings.Builder) {
	if i.User != "" {
		b.WriteString("          owned by: ")
		b.WriteString(i.User)
		b.WriteString("   dated: ")
	} else {
		b.WriteString("             dated: ")
	}
	b.WriteString(ctime(i.Mtime))
	if i.Unreadable != "" {
		b.WriteString("         ")
		b.WriteString(i.Unreadable)
		b.WriteString("\n")
		return
	}
	b.WriteString("         file name: ")
	if i.File == "" {
		b.WriteString("[No Name]")
	} else {
		b.WriteString(i.File)
	}
	b.WriteString("\n          modified: ")
	if i.Modified {
		b.WriteString("YES")
	} else {
		b.WriteString("no")
	}
	if i.User != "" {
		b.WriteString("\n         user name: ")
		b.WriteString(i.User)
		if i.Host != "" {
			b.WriteString("   host name: ")
			b.WriteString(i.Host)
		}
	} else if i.Host != "" {
		b.WriteString("\n         host name: ")
		b.WriteString(i.Host)
	}
	if i.PID != 0 {
		b.WriteString("\n        process ID: ")
		fmt.Fprintf(b, "%d", i.PID)
		if i.Running {
			b.WriteString(" (STILL RUNNING)")
		}
	}
	b.WriteString("\n")
}

// ctime is vim's get_ctime with add_newline set: strftime with
// "%a %b %d %H:%M:%S %Y" and a newline after it. Zero-padded day of the month,
// which C's ctime(3) pads with a space and vim does not.
//
// A zero time has no honest rendering and gets vim's answer for a stat that
// failed, which is nothing at all on that line.
func ctime(t time.Time) string {
	if t.IsZero() {
		return "\n"
	}
	return t.Format("Mon Jan 02 15:04:05 2006") + "\n"
}

// RecoveredMessage is what vim prints after a successful ":recover", which is
// three or four lines depending on whether the recovery changed anything.
//
// The "You may want to delete the .swp file now." line is the one that matters
// and it is the one people miss: recovery does not delete the swap file, so the
// next open of the same file raises E325 again and looks like the recovery
// failed.
func RecoveredMessage(swap, file string, sameAsFile bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Using swap file \"%s\"\n", swap)
	fmt.Fprintf(&b, "Original file \"%s\"\n", file)
	if sameAsFile {
		b.WriteString("Recovery completed. Buffer contents equals file contents.\n")
		b.WriteString("You may want to delete the .swp file now.\n")
		return b.String()
	}
	b.WriteString("Recovery completed. You should check if everything is OK.\n")
	b.WriteString("(You might want to write out this file under another name\n")
	b.WriteString("and run diff with the original file to check for changes)\n")
	b.WriteString("You may want to delete the .swp file now.\n")
	return b.String()
}
