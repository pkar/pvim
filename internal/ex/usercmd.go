package ex

import (
	"errors"
	"strconv"
	"strings"
)

// User commands: ":command", ":delcommand" and running one.
//
// The vimrc defines exactly one, on line 130:
//
//	command! -range -nargs=0 -bar JsonPretty <line1>,<line2>!jq --sort-keys '.'
//
// which is the whole of the feature that has to work: a bang to allow
// redefinition, -range to accept one, -nargs=0 to refuse arguments, -bar to
// let a "|" end it, and <line1> and <line2> substituted into the replacement.
// Everything else here is there because it costs a line each and because a
// half-implemented <q-args> is worse than one that says what it does.

// The errors ":command" answers with.
var (
	// ErrCommandExists is E174, a redefinition with no bang. It is what
	// catches a vimrc sourced twice, and the reason the vimrc's own
	// ":command!" has the bang.
	ErrCommandExists = errors.New("E174: Command already exists: add ! to replace it")
	// ErrBadCommandName is E183: a user command has to start with an
	// uppercase letter.
	ErrBadCommandName = errors.New("E183: User defined commands must start with an uppercase letter")
	// ErrNoSuchUserCommand is E184.
	ErrNoSuchUserCommand = errors.New("E184: No such user-defined command")
	// ErrNoRangeAllowedUser is E481 for a user command defined without
	// -range.
	ErrNoRangeAllowedUser = ErrNoRangeAllowed
	// ErrTooManyArgs is E488 for a -nargs=0 command given some.
	ErrTooManyArgs = errors.New("E488: Trailing characters")
	// ErrArgRequiredUser is E471 for a -nargs=1 or -nargs=+ command given
	// none.
	ErrArgRequiredUser = ErrArgumentRequired
)

// Define adds or replaces a user command.
func (u UserCommands) Define(c *UserCommand, bang bool) error {
	if c.Name == "" || c.Name[0] < 'A' || c.Name[0] > 'Z' {
		return ErrBadCommandName
	}
	if _, ok := u[c.Name]; ok && !bang {
		return withName(ErrCommandExists, c.Name)
	}
	u[c.Name] = c
	return nil
}

// exCommand is ":command": define one, or list them all when given nothing.
func exCommand(c *Context, cmd Cmd) error {
	arg := trimArgs(cmd.Args)
	if arg == "" {
		return listUserCommands(c)
	}
	uc, err := ParseUserCommand(arg)
	if err != nil {
		return err
	}
	if c.Cmds == nil {
		c.Cmds = UserCommands{}
	}
	return c.Cmds.Define(uc, cmd.Bang)
}

// listUserCommands is ":command" with no argument, whose header vim spells
// " Name Args Address Complete Definition".
func listUserCommands(c *Context) error {
	c.say("    Name              Args Address Complete Definition")
	names := make([]string, 0, len(c.Cmds))
	for name := range c.Cmds {
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		u := c.Cmds[name]
		c.say("    " + padRight(name, 18) + padRight(nargsLabel(u.NArgs), 5) +
			padRight(addressLabel(u.Range), 8) + padRight(u.Complete, 9) + u.Repl)
	}
	return nil
}

// nargsLabel and addressLabel are how ":command" spells the two attributes it
// prints: "0" is a blank column and "-range" prints ".".
func nargsLabel(n string) string {
	if n == "" || n == "0" {
		return "0"
	}
	return n
}

func addressLabel(r string) string {
	switch r {
	case "":
		return ""
	case ".":
		return "."
	case "%":
		return "%"
	default:
		return r
	}
}

// ParseUserCommand reads a ":command" definition: the attributes, the name and
// the replacement.
func ParseUserCommand(arg string) (*UserCommand, error) {
	u := &UserCommand{NArgs: "0"}
	rest := arg
	for {
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, "-") {
			break
		}
		end := strings.IndexAny(rest, " \t")
		attr := rest
		if end >= 0 {
			attr = rest[:end]
			rest = rest[end:]
		} else {
			rest = ""
		}
		if err := applyAttr(u, attr); err != nil {
			return nil, err
		}
	}
	rest = strings.TrimLeft(rest, " \t")
	end := strings.IndexAny(rest, " \t")
	if end < 0 {
		u.Name = rest
		return u, u.check()
	}
	u.Name = rest[:end]
	u.Repl = strings.TrimLeft(rest[end:], " \t")
	return u, u.check()
}

// check is the one rule vim enforces on a name.
func (u *UserCommand) check() error {
	if u.Name == "" || u.Name[0] < 'A' || u.Name[0] > 'Z' {
		return ErrBadCommandName
	}
	return nil
}

// applyAttr records one "-attr" or "-attr=value".
func applyAttr(u *UserCommand, attr string) error {
	name, value, has := strings.Cut(attr, "=")
	switch name {
	case "-nargs":
		if !has {
			return withName(errBadAttr, attr)
		}
		u.NArgs = value
	case "-range":
		u.Range = "."
		if has {
			u.Range = value
		}
	case "-count":
		u.Range = "count"
	case "-bar":
		u.Bar = true
	case "-bang":
		u.Bang = true
	case "-complete":
		u.Complete = value
	case "-buffer", "-register", "-keepscript":
		// Accepted and inert. None of them changes what the vimrc's one user
		// command does, and refusing them would fail a vimrc that carries one.
	default:
		return withName(errBadAttr, attr)
	}
	return nil
}

// errBadAttr is E181, an attribute ":command" does not know.
var errBadAttr = errors.New("E181: Invalid attribute")

// runUser runs a user command.
func (c *Context) runUser(cmd Cmd) error {
	u := c.Cmds[cmd.Typed]
	if u == nil {
		return withName(ErrNotAnEditorCommand, cmd.Typed)
	}
	if cmd.Range.Given > 0 && u.Range == "" {
		return ErrNoRangeAllowed
	}
	if cmd.Bang && !u.Bang {
		return ErrNoBangAllowed
	}
	args := trimArgs(cmd.Args)
	switch u.NArgs {
	case "", "0":
		if args != "" {
			return withName(ErrTrailing, args)
		}
	case "1", "+":
		if args == "" {
			return ErrArgumentRequired
		}
	}

	def := rangeCurrent
	if u.Range == "%" {
		def = rangeFile
	}
	first, last := c.cursorLine(), c.cursorLine()
	if u.Range != "" {
		f, l, err := Resolve(cmd.Range, c, def)
		if err != nil {
			return err
		}
		first, last = f, l
	}
	cmd.Lines = LineRange{First: first, Last: last, Given: cmd.Range.Given}

	// A Go command runs here, after the range and the argument checks and
	// before Repl is looked at, because a command with a Run has no Repl to
	// expand.
	if u.Run != nil {
		return u.Run(c, cmd)
	}

	repl, err := u.Expand(cmd)
	if err != nil {
		return err
	}
	return c.RunLine(repl)
}

// Expand substitutes the <...> placeholders into a user command's
// replacement.
//
// The seven that exist: <line1> and <line2> are the range, <count> is it as a
// number of lines, <bang> is "!" or nothing, <args> is the argument text,
// <q-args> is it quoted as one vimscript string, <f-args> is it split on white
// space and quoted one by one, and <lt> is a literal "<".
//
// <q-args> and <f-args> produce vimscript string literals, which this editor
// has no evaluator for. They are expanded anyway rather than refused, because
// a command that pastes them into a ":!" line -- which is the only thing they
// can usefully reach here -- gets a quoted argument out of them and that is
// what the quoting was for.
func (c *UserCommand) Expand(cmd Cmd) (string, error) {
	var out strings.Builder
	s := c.Repl
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			out.WriteByte(s[i])
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			out.WriteByte(s[i])
			continue
		}
		name := s[i : i+end+1]
		repl, ok := placeholder(name, cmd)
		if !ok {
			out.WriteByte(s[i])
			continue
		}
		out.WriteString(repl)
		i += end
	}
	return out.String(), nil
}

// placeholder resolves one <...> and says whether it was one at all.
func placeholder(name string, cmd Cmd) (string, bool) {
	args := trimArgs(cmd.Args)
	switch name {
	case "<line1>":
		return strconv.Itoa(cmd.Lines.First), true
	case "<line2>":
		return strconv.Itoa(cmd.Lines.Last), true
	case "<count>":
		return strconv.Itoa(cmd.Lines.Last - cmd.Lines.First + 1), true
	case "<bang>":
		if cmd.Bang {
			return "!", true
		}
		return "", true
	case "<args>":
		return args, true
	case "<q-args>":
		return quoteVim(args), true
	case "<f-args>":
		fields := strings.Fields(args)
		for i, f := range fields {
			fields[i] = quoteVim(f)
		}
		return strings.Join(fields, ", "), true
	case "<lt>":
		return "<", true
	case "<reg>", "<register>":
		if cmd.Reg == 0 {
			return "", true
		}
		return string(cmd.Reg), true
	}
	return "", false
}

// quoteVim wraps a string in double quotes and escapes what has to be, which
// is what <q-args> means.
func quoteVim(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('"')
	return b.String()
}

// exDelcommand is ":delcommand".
func exDelcommand(c *Context, cmd Cmd) error {
	name := trimArgs(cmd.Args)
	if cmd.Bang {
		c.Cmds = UserCommands{}
		return nil
	}
	if _, ok := c.Cmds[name]; !ok {
		return withName(ErrNoSuchUserCommand, name)
	}
	delete(c.Cmds, name)
	return nil
}

// padRight pads s on the right to n columns, which is what the ":command"
// listing's fixed columns are made of.
func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

// sortStrings is sort.Strings without the import, because this is the only
// place in the package that needs one and an insertion sort over a handful of
// user command names is not worth a dependency in a file that has none.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
