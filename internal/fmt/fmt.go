// Package fmt is the format-on-save table: which filetypes are reformatted
// when a buffer is written, and by what.
//
// The name collides with the standard library's and the collision is
// deliberate, because the directory is named for what it does and because the
// two never meet: nothing in here needs Printf, and cmd/pvim imports it under
// a name of its own. A package called "format" in a directory called "fmt"
// would be worse -- the import path is what a person reads in a stack trace.
//
// What is in the table is what the vimrc's plugins did, and
// nothing else:
//
//	go vim-go, g:go_fmt_autosave, which defaults to 1 and
//	 which the vimrc therefore relies on without setting.
//	 vim-go's default formatter is gofmt; here it is the
//	 language server's textDocument/formatting, which is
//	 gopls running gofmt's own package on the same bytes.
//	tf, terraform, hcl vim-terraform, g:terraform_fmt_on_save=1, which the
//	 vimrc sets by name. That plugin shells out to
//	 "terraform fmt", and so does this.
//
// The ORDER matters and is not this package's to enforce. The vimrc has
//
//	autocmd BufWritePre *.py,*.java,*.js,*.go silent! %s/\s\+$//e
//
// and vim runs a BufWritePre autocommand before a plugin's own write hook, so
// the whitespace strip goes first and the formatter sees the stripped buffer.
// cmd/pvim keeps that order; this package is handed bytes and hands bytes back.
//
// Nothing here writes a file. A formatter that wrote the file itself -- which
// is what "gofmt -w" and "terraform fmt FILE" both do -- would put the
// formatted text on disk and leave the editor's buffer holding the text the
// person typed, so the next keystroke would mark the buffer modified against a
// file it now disagrees with. Both formatters here read standard input or the
// buffer and answer with bytes, and the caller puts them in the buffer BEFORE
// the write.
package fmt

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Formatter is something that can format a whole file. internal/lsp's Client
// satisfies it, which is the only implementation that exists; it is an
// interface so that this package does not import that one and so that a test
// can format with a function.
type Formatter interface {
	Format(ctx context.Context, path string, src []byte) ([]byte, error)
}

// FormatterFunc adapts a function to Formatter.
type FormatterFunc func(ctx context.Context, path string, src []byte) ([]byte, error)

// Format calls f.
func (f FormatterFunc) Format(ctx context.Context, path string, src []byte) ([]byte, error) {
	return f(ctx, path, src)
}

// Kind is how a filetype gets formatted.
type Kind int

const (
	// KindNone is a filetype with no formatter, which is every filetype not
	// in the table.
	KindNone Kind = iota
	// KindServer is the language server's textDocument/formatting.
	KindServer
	// KindCommand is an external program fed the buffer on standard input.
	KindCommand
)

// Rule is one row of the table.
type Rule struct {
	Kind Kind
	// Command and Args are the program for a KindCommand rule. The buffer
	// goes to its standard input and the formatted text comes back on its
	// standard output.
	Command string
	Args    []string
	// Why names the plugin and the setting this row reproduces, so that a
	// person reading a diff of a saved file can find out what changed it.
	Why string
	// Switch is which of the table's two flags turns this row off, matching
	// the "let" in the vimrc. A row with no switch is always on.
	//
	// A field and not a test on the command name, because a table overridden
	// for a test would otherwise inherit whichever rule the name happened to
	// match, and because the day a third formatter arrives the switch is
	// where the reader looks rather than a case in Rule.
	Switch Switch
}

// Switch names one of the vimrc's format-on-save flags.
type Switch int

// The two flags, and the absence of one.
const (
	SwitchNone Switch = iota
	// SwitchGoFmt is g:go_fmt_autosave, whose vim-go default is 1.
	SwitchGoFmt
	// SwitchTerraformFmt is g:terraform_fmt_on_save, which the vimrc sets.
	SwitchTerraformFmt
)

// ErrNoFormatter is what Format answers for a filetype the table has no row
// for. It is a sentinel and not an error message because a save of a markdown
// file reaches it on every write and must be silent.
var ErrNoFormatter = errors.New("fmt: no formatter for this filetype")

// ErrNoServer is a Go file written with no language server running. It is
// distinct from ErrNoFormatter on purpose: the first is normal and the second
// is a Go file quietly not being formatted, which is worth a line on the
// message area exactly once.
var ErrNoServer = errors.New("fmt: no language server to format with")

// Table is the format-on-save table and the two switches the vimrc has for it.
type Table struct {
	// Server formats the KindServer filetypes. Nil means there is none, and a
	// Go file written then comes back with ErrNoServer rather than unformatted
	// and silent.
	Server Formatter

	// GoEnabled is g:go_fmt_autosave. vim-go's default is 1 and the vimrc does
	// not set it, so the default here is also on; the field exists so that a
	// "let g:go_fmt_autosave = 0" in some future vimrc means something.
	GoEnabled bool

	// TerraformEnabled is g:terraform_fmt_on_save, which the vimrc sets to 1.
	TerraformEnabled bool

	// Run runs an external formatter. Nil means os/exec, and a test replaces
	// it to check the argument list without a terraform on the machine.
	Run func(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error)

	// Rules overrides the built-in table, for a test. Nil means Default.
	Rules map[string]Rule
}

// New is the table this editor runs with: both switches in the position the
// vimrc leaves them, and the language server it was given.
func New(server Formatter) *Table {
	return &Table{Server: server, GoEnabled: true, TerraformEnabled: true}
}

// Default is the table, keyed by vim's 'filetype'.
//
// Three keys for Terraform because internal/filetype answers all three: a .tf
// file that looks like Terraform is "terraform", one that looks like the
// TinyFugue mud client's config is "tf", an empty one is "tf" as well, and a
// .hcl file is "hcl". vim-terraform's own autocommands cover the same set.
var Default = map[string]Rule{
	"go": {
		Kind:   KindServer,
		Switch: SwitchGoFmt,
		Why:    "vim-go g:go_fmt_autosave (default 1), through gopls textDocument/formatting",
	},
	"terraform": terraformRule,
	"tf":        terraformRule,
	"hcl":       terraformRule,
}

// terraformRule is "terraform fmt -", which reads standard input and writes
// the formatted configuration to standard output.
//
// The bare "-" and not a file name: "terraform fmt DIR" rewrites every file in
// a directory and "terraform fmt FILE" rewrites that file on disk, and both
// would format bytes other than the ones in the buffer. The stdin form is the
// only one that is a function from text to text.
var terraformRule = Rule{
	Kind:    KindCommand,
	Command: "terraform",
	Args:    []string{"fmt", "-"},
	Switch:  SwitchTerraformFmt,
	Why:     "vim-terraform g:terraform_fmt_on_save=1",
}

// Rule returns the row for a filetype, and whether there is one.
func (t *Table) Rule(filetype string) (Rule, bool) {
	rules := t.Rules
	if rules == nil {
		rules = Default
	}
	r, ok := rules[filetype]
	if !ok {
		return Rule{}, false
	}
	switch r.Switch {
	case SwitchGoFmt:
		if !t.GoEnabled {
			return Rule{}, false
		}
	case SwitchTerraformFmt:
		if !t.TerraformEnabled {
			return Rule{}, false
		}
	}
	return r, true
}

// Formats reports whether a filetype is formatted on save. cmd/pvim asks
// before it does any of the work around a format, so that a save of a
// markdown file costs one map lookup.
func (t *Table) Formats(filetype string) bool {
	_, ok := t.Rule(filetype)
	return ok
}

// Format formats src for a filetype and returns the result.
//
// path is the file the bytes belong to, needed by the server rule because
// textDocument/formatting names a document rather than sending one, and
// carried into the command rule's error messages.
//
// A formatter that fails leaves src alone and returns the error. That is the
// whole contract with the caller: a Go file with a syntax error in it is
// exactly the file a person is most likely to be saving, gopls answers a
// formatting request over one with an error, and an editor that responded by
// emptying the buffer would be unusable. The write goes ahead with the bytes
// that were there.
func (t *Table) Format(ctx context.Context, filetype, path string, src []byte) ([]byte, error) {
	r, ok := t.Rule(filetype)
	if !ok {
		return src, ErrNoFormatter
	}
	switch r.Kind {
	case KindServer:
		if t.Server == nil {
			return src, ErrNoServer
		}
		out, err := t.Server.Format(ctx, path, src)
		if err != nil {
			return src, err
		}
		return out, nil
	case KindCommand:
		out, err := t.runCommand(ctx, r, src)
		if err != nil {
			return src, err
		}
		return out, nil
	default:
		return src, ErrNoFormatter
	}
}

// runCommand feeds src to an external formatter and returns what it printed.
//
// An empty answer from a program that exited 0 is refused. That combination
// has one cause -- a formatter that decided the input was not its language and
// said nothing -- and applying it would empty the buffer, which is the one
// failure mode of format-on-save that loses work.
func (t *Table) runCommand(ctx context.Context, r Rule, src []byte) ([]byte, error) {
	run := t.Run
	if run == nil {
		run = execRun
	}
	out, err := run(ctx, r.Command, r.Args, src)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 && len(src) > 0 {
		return nil, errors.New(r.Command + ": formatted " + itoa(len(src)) + " bytes into nothing")
	}
	return out, nil
}

// execRun is the default Run: the program, the buffer on its standard input,
// its standard output back.
//
// The standard error is put on the returned error rather than dropped, because
// "terraform fmt" reports a syntax error there and on the message line that
// sentence is the whole of what a person needs.
func execRun(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errbuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errbuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errbuf.String())
		if msg == "" {
			return nil, errors.New(name + ": " + err.Error())
		}
		// One line. The message area is 'cmdheight' rows and that is 1 in this
		// vimrc, so a multi-line diagnostic would scroll the screen and put a
		// press-enter prompt where a person expected a saved file.
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		return nil, errors.New(name + ": " + msg)
	}
	return out.Bytes(), nil
}

// itoa is strconv.Itoa without the import, which this package does not
// otherwise need.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
