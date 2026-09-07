package ex

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/text"
)

// The shell commands: ":!cmd", ":{range}!cmd", ":r !cmd" and ":w !cmd".
//
// This path is load-bearing for this vimrc rather than a nice-to-have. Line
// 130 of it is
//
//	command! -range -nargs=0 -bar JsonPretty <line1>,<line2>!jq --sort-keys '.'
//
// and line 129 sets 'shell' to /opt/homebrew/bin/bash, so a filter that runs
// under the wrong shell or that loses the quoting round the jq program is a
// command the user runs every day that quietly does nothing.
//
// Two behaviours here are vim's and were measured rather than
// assumed. Standard error is merged into standard output, because vim's
// 'shellredir' defaults to ">%s 2>&1": ":2,3!nosuchcmd" puts
// "zsh:1: command not found: nosuchcmd" into the buffer and says "shell
// returned 127" on the message line. And the count message is
// "N lines filtered", printed only above 'report'.
//
// ":!" with no range is difference D-003: vim puts the output on the command
// line and waits for Enter, and this editor puts it in a scratch split and
// does not, because 'cmdheight=1' in the vimrc is there to get rid of the
// press-enter prompt.

// exFilter is ":{range}!cmd", ":!cmd" and the ":w !cmd" spelling that shares
// its plumbing.
func exFilter(c *Context, cmd Cmd) error {
	line := strings.TrimSpace(cmd.Args)
	if line == "" {
		return ErrArgumentRequired
	}
	if cmd.Lines.Given == 0 {
		return c.bangCommand(line)
	}
	return c.filterRange(cmd.Lines.First, cmd.Lines.Last, line)
}

// shellArgv is the command line as the shell will be given it: 'shell' and
// 'shellcmdflag', which the vimrc sets to homebrew's bash and leaves at "-c".
func (c *Context) shellArgv(line string) []string {
	o := c.opts()
	sh := o.G.Shell
	if sh == "" {
		sh = "/bin/sh"
	}
	flag := o.G.ShellCmdFlag
	if flag == "" {
		flag = "-c"
	}
	return []string{sh, flag, line}
}

// runShell runs one command line with input on standard input and returns
// everything it wrote, standard error included, and its exit status.
func (c *Context) runShell(line string, input []byte) ([]byte, int, error) {
	argv := c.shellArgv(line)
	cmd := exec.Command(argv[0], argv[1:]...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	// Merged, because vim's 'shellredir' is ">%s 2>&1" and the diagnostics of
	// a filter that failed are the most useful thing it produced.
	cmd.Stderr = &out
	err := cmd.Run()
	status := 0
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
		err = nil
	}
	return out.Bytes(), status, err
}

// filterRange replaces the lines with what the command wrote when given them.
func (c *Context) filterRange(first, last int, line string) error {
	var in bytes.Buffer
	b := c.buffer()
	for n := first; n <= last && n <= b.LineCount(); n++ {
		in.Write(b.Line(n))
		in.WriteByte('\n')
	}
	out, status, err := c.runShell(line, in.Bytes())
	if err != nil {
		return err
	}

	repl := splitLines(out)
	if len(out) == 0 {
		repl = nil
	}
	// Insert first and delete second, which is not the obvious order and is
	// the right one: deleting every line of the buffer leaves internal/text in
	// its "emptied" state, and inserting into that puts the replacement above
	// a trailing empty line that was never in the file. ":%!sort" is exactly
	// that case.
	c.edit(text.Pos{Line: first}, func(b *text.Buffer) {
		if len(repl) > 0 {
			b.InsertLines(last+1, repl)
		}
		b.DeleteLines(first, last)
	})
	c.moveTo(first)

	if status != 0 {
		c.say("shell returned " + strconv.Itoa(status))
	}
	// The count is the lines that went IN, not the lines that came back:
	// vim's do_filter reports line2 - line1 + 1 and reports nothing at all
	// when the range was smaller than 'report', however many lines the filter
	// produced. Measured with ":%!jq" over one line of JSON, which comes back
	// as nine and which vim says nothing about.
	n := last - first + 1
	if c.reportOver(n) {
		c.say(strconv.Itoa(n) + " " + plural(n, "line", "lines") + " filtered")
	}
	return nil
}

// readCommand is ":r !cmd": the output goes in after the range's last line,
// and nothing is taken out.
func (c *Context) readCommand(cmd Cmd, line string) error {
	if line == "" {
		return ErrArgumentRequired
	}
	out, status, err := c.runShell(line, nil)
	if err != nil {
		return err
	}
	lines := splitLines(out)
	if len(out) == 0 {
		lines = nil
	}
	if len(lines) > 0 {
		at := cmd.Lines.Last
		c.edit(text.Pos{Line: max(at, 1)}, func(b *text.Buffer) { b.InsertLines(at+1, lines) })
		c.moveTo(at + 1)
	}
	if status != 0 {
		c.say("shell returned " + strconv.Itoa(status))
	}
	if c.reportOver(len(lines)) {
		c.sayKeep(strconv.Itoa(len(lines)) + " more " + plural(len(lines), "line", "lines"))
	}
	return nil
}

// writeToCommand is ":w !cmd" and ":{range}w !cmd": the lines go to the
// command's standard input and the buffer is not touched.
func (c *Context) writeToCommand(cmd Cmd, line string) error {
	if line == "" {
		return ErrArgumentRequired
	}
	var in bytes.Buffer
	b := c.buffer()
	for n := cmd.Lines.First; n <= cmd.Lines.Last && n <= b.LineCount(); n++ {
		in.Write(b.Line(n))
		in.WriteByte('\n')
	}
	out, status, err := c.runShell(line, in.Bytes())
	if err != nil {
		return err
	}
	c.showOutput(line, out)
	if status != 0 {
		c.say("shell returned " + strconv.Itoa(status))
	}
	return nil
}

// bangCommand is ":!cmd" with no range: difference D-003.
//
// vim writes the output over the command line and waits for Enter. This puts
// it in a scratch buffer in a split and does not wait, because 'cmdheight=1'
// in the vimrc is set to get rid of exactly that prompt. When the split cannot
// be made -- one window too small to divide, or a Context with no tab pages,
// which is what a test builds -- the output goes on the message line instead,
// one line at a time, so that nothing is ever silently thrown away.
func (c *Context) bangCommand(line string) error {
	// The echo, and it is not a message line: vim writes ":!cmd" where the
	// command line was and ends it with a carriage return rather than a
	// newline, then writes a newline once the shell is done. Measured through
	// cmd/oracle, ":!true" leaves "\n:!true\r\n" in the redirect and
	// ":!false" leaves "\n:!false\r\n\nshell returned 1\n". The output
	// itself is in neither, because vim writes it straight to the terminal
	// and :redir never sees it, which is what makes difference D-003 invisible
	// here.
	c.sayRaw("\n:!" + line + "\r")
	// 'warn', on by default: a shell command over a buffer nobody has written
	// says so, every time and not once. Measured, "x" then ":!false" twice
	// leaves the warning in the redirect twice, and ":set nowarn" takes it
	// away.
	if c.opts().G.Warn {
		if b := c.current(); b != nil && b.Modified() && !b.Scratch {
			c.sayRaw("\n[No write since last change]")
		}
	}
	out, status, err := c.runShell(line, nil)
	if err != nil {
		return err
	}
	c.sayRaw("\n")
	c.showOutput(line, out)
	if status != 0 {
		c.sayRaw("\nshell returned " + strconv.Itoa(status) + "\n")
	}
	return nil
}

// showOutput puts a command's output where D-003 says it goes.
func (c *Context) showOutput(line string, out []byte) {
	if len(out) == 0 {
		return
	}
	lines := splitLines(out)
	if c.openScratch(":!"+line, lines) == nil {
		return
	}
	for _, l := range lines {
		c.say(string(l))
	}
}

// openScratch opens a split holding a buffer with no file behind it, which is
// where ":!" output and ":copen" both go.
//
// It answers an error rather than panicking when there is no window to split,
// so that every caller has somewhere else to put what it was going to show.
func (c *Context) openScratch(name string, lines [][]byte) error {
	if c.Tabs == nil {
		return errNoWindow
	}
	tab := c.tab()
	w := c.Window()
	if tab == nil || w == nil {
		return errNoWindow
	}
	b := text.New()
	if len(lines) > 0 {
		b.InsertLines(0, lines)
		b.DeleteLines(len(lines)+1, len(lines)+1)
	}
	nb := c.bufs().Add(name, b)
	nb.Scratch = true
	nb.Listed = false

	fresh := newWindow(c, nb)
	if err := tab.Split(w, fresh, splitDir(false), splitBefore(c, false), 0); err != nil {
		c.bufs().Remove(nb)
		return err
	}
	tab.Cur = fresh
	c.bufs().Cur = nb
	c.swapBuffer(nb)
	return nil
}
