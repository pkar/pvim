package undofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The transcript. This is what vim 9.2.0321 printed when it was
// opened over a file whose editor had been killed with a SIGKILL two minutes
// earlier, captured through `script` on an 80-column pseudo-terminal and
// unwrapped by hand -- the terminal broke the long paths across lines and ate
// the leading spaces at each break, which is why the indentation below comes
// from `strings` over the binary and not from the capture.
//
// The reproduction is in attention.go's comment. The two long paths and the
// clock are substituted in by the test so that the file it stats can be a
// temporary one; everything else is byte for byte what vim printed.
const vimAttention = `E325: ATTENTION
Found a swap file by the name "SWAP"
          owned by: pkar   dated: Sun Sep 06 05:44:04 2026
         file name: FILE
          modified: YES
         user name: pkar   host name: pkar-m.local
        process ID: 64477
While opening file "OPENING"
             dated: Sun Sep 06 05:42:43 2026

(1) Another program may be editing the same file.  If this is the case,
    be careful not to end up with two different instances of the same
    file when making changes.  Quit, or continue with caution.
(2) An edit session for this file crashed.
    If this is the case, use ":recover" or "vim -r OPENING"
    to recover the changes (see ":help recovery").
    If you did this already, delete the swap file "SWAP"
    to avoid this message.

Swap file "SWAP" already exists!
[O]pen Read-Only, (E)dit anyway, (R)ecover, (D)elete it, (Q)uit, (A)bort: `

func TestAttentionMatchesVim(t *testing.T) {
	dir := t.TempDir()
	opening := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(opening, []byte("alpha\nbravo\ncharlie\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileTime := time.Date(2026, 9, 6, 5, 42, 43, 0, time.Local)
	if err := os.Chtimes(opening, fileTime, fileTime); err != nil {
		t.Fatal(err)
	}
	const swap = "/private/tmp/swap/%private%tmp%work%f.txt.swp"
	info := SwapInfo{
		Path:     swap,
		Mtime:    time.Date(2026, 9, 6, 5, 44, 4, 0, time.Local),
		PID:      64477,
		Host:     "pkar-m.local",
		User:     "pkar",
		File:     "/private/tmp/work/f.txt",
		Modified: true,
	}

	want := vimAttention
	want = strings.ReplaceAll(want, "SWAP", swap)
	want = strings.ReplaceAll(want, "FILE", info.File)
	want = strings.ReplaceAll(want, "OPENING", opening)

	if got := info.Attention(opening); got != want {
		t.Errorf("the E325 message is not vim's.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// The prompt, on its own, because it is the line a person actually answers and
// because the choice list changes shape when the other editor is alive.
func TestSwapPromptMatchesVim(t *testing.T) {
	const dead = "[O]pen Read-Only, (E)dit anyway, (R)ecover, (D)elete it, (Q)uit, (A)bort: "
	const alive = "[O]pen Read-Only, (E)dit anyway, (R)ecover, (Q)uit, (A)bort: "
	if got := SwapPrompt(false); got != dead {
		t.Errorf("prompt for a crashed session:\n got %q\nwant %q", got, dead)
	}
	// Vim drops "&Delete it" from the button list when the process is still
	// running: deleting the swap file of a live editor takes that editor's
	// recovery away. Two literals in the binary, one with five buttons and one
	// with six.
	if got := SwapPrompt(true); got != alive {
		t.Errorf("prompt for a live editor:\n got %q\nwant %q", got, alive)
	}
}

func TestParseSwapChoice(t *testing.T) {
	cases := []struct {
		in      byte
		running bool
		want    SwapChoice
		ok      bool
	}{
		{'o', false, SwapReadOnly, true},
		{'O', false, SwapReadOnly, true},
		{'e', false, SwapEdit, true},
		{'r', false, SwapRecover, true},
		{'d', false, SwapDelete, true},
		{'q', false, SwapQuit, true},
		{'a', false, SwapAbort, true},
		{'\r', false, SwapReadOnly, true}, // the default is the safe one
		{0x1b, false, SwapAbort, true},    // Escape cancels the dialog
		{'d', true, 0, false},             // not offered while it is running
		{'x', false, 0, false},            // not a choice; the prompt stays up
		{' ', false, 0, false},            // nor is a space
	}
	for _, c := range cases {
		got, ok := ParseSwapChoice(c.in, c.running)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ParseSwapChoice(%q, running=%v) = %v, %v; want %v, %v",
				c.in, c.running, got, ok, c.want, c.ok)
		}
	}
}

// The three clauses that depend on what the file on the disk is doing.
func TestAttentionOnTheFileItself(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	swapTime := time.Now().Add(-time.Hour)

	t.Run("newer than the swap file", func(t *testing.T) {
		info := SwapInfo{Path: "/tmp/x.swp", Mtime: swapTime, User: "p"}
		if got := info.Attention(file); !strings.Contains(got, "      NEWER than swap file!") {
			t.Errorf("a file written after the crash is not called newer:\n%s", got)
		}
	})
	t.Run("cannot be found", func(t *testing.T) {
		info := SwapInfo{Path: "/tmp/x.swp", Mtime: swapTime, User: "p"}
		got := info.Attention(filepath.Join(dir, "gone.txt"))
		if !strings.Contains(got, "      CANNOT BE FOUND") {
			t.Errorf("a file that is not there is not called missing:\n%s", got)
		}
		if strings.Contains(got, "dated: ") && strings.Count(got, "dated: ") != 1 {
			t.Errorf("a missing file still got a date:\n%s", got)
		}
	})
	t.Run("still running", func(t *testing.T) {
		info := SwapInfo{Path: "/tmp/x.swp", Mtime: swapTime, User: "p", PID: 1, Running: true}
		got := info.Attention(file)
		if !strings.Contains(got, "        process ID: 1 (STILL RUNNING)") {
			t.Errorf("a live process is not marked:\n%s", got)
		}
		if strings.Contains(got, "(D)elete it") {
			t.Errorf("a live editor's swap file was offered for deletion:\n%s", got)
		}
	})
}

// The lines vim prints after a recovery, the last of which is the one people
// miss: recovery does not delete the swap file.
func TestRecoveredMessage(t *testing.T) {
	got := RecoveredMessage("/tmp/x.swp", "/tmp/f.txt", false)
	want := `Using swap file "/tmp/x.swp"
Original file "/tmp/f.txt"
Recovery completed. You should check if everything is OK.
(You might want to write out this file under another name
and run diff with the original file to check for changes)
You may want to delete the .swp file now.
`
	if got != want {
		t.Errorf("recovery message:\n got %q\nwant %q", got, want)
	}
	got = RecoveredMessage("/tmp/x.swp", "/tmp/f.txt", true)
	if !strings.Contains(got, "Recovery completed. Buffer contents equals file contents.") {
		t.Errorf("a recovery that changed nothing says the wrong thing:\n%s", got)
	}
	if !strings.Contains(got, "You may want to delete the .swp file now.") {
		t.Errorf("nothing told the person the swap file is still there:\n%s", got)
	}
}
