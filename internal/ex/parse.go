package ex

import "strings"

// The ex line is parsed in vim's own order, and the order is not cosmetic: a
// modifier may carry no range, a range binds to the command after the
// modifiers, and the command name decides what the rest of the line means. Any
// other order gets ":vertical 10split" or ":silent! 1,2d" wrong.
//
// Everything in this file is a scanner over one string with no editor in
// sight. A range parses the same whatever the buffer holds, which is what lets
// the table test cover it with no Context at all; turning "'a" and "/foo/"
// into line numbers is Resolve's job and lives in address.go.

// parser is the scanner. It holds the line and one index, and every method
// leaves i on the first byte it did not consume.
type parser struct {
	s string
	i int
}

// eof reports whether the scanner is at the end of the line.
func (p *parser) eof() bool { return p.i >= len(p.s) }

// peek returns the byte at the cursor, or zero at the end. Zero is the right
// answer rather than a special case: vim reads a NUL there too, and every
// guard in this file is written against a byte that is not one.
func (p *parser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.s[p.i]
}

// at returns the byte n further on, or zero past the end.
func (p *parser) at(n int) byte {
	if p.i+n >= len(p.s) {
		return 0
	}
	return p.s[p.i+n]
}

// skipWhite advances over spaces and tabs.
func (p *parser) skipWhite() {
	for !p.eof() && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

// rest returns everything the scanner has not consumed.
func (p *parser) rest() string { return p.s[p.i:] }

// isAlpha reports whether c is an ASCII letter, which is the only thing a
// built-in command name is made of.
func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// isDigit reports whether c is an ASCII digit.
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// Parse takes one ex command line apart.
//
// line is what was typed after the colon, with no leading colon and no
// trailing newline. Leading colons and blanks are skipped, then the command
// modifiers, then the range, then the name, then the bang, then whatever
// grammar the command's own entry says its arguments have.
//
// The name is resolved through Lookup, so a Cmd that comes back has Name set
// to the full name and Typed to what was written. An unrecognised name is
// ErrNotAnEditorCommand with Typed filled in, because the message quotes it.
func Parse(line string) (Cmd, error) {
	p := &parser{s: line}
	var c Cmd
	c.Line = line

	// Leading colons and blanks. ":d" is ":d", which is what a mapping that
	// starts with a colon inside another one produces.
	for {
		p.skipWhite()
		if p.peek() == ':' {
			p.i++
			continue
		}
		break
	}

	if err := parseMods(p, &c); err != nil {
		return c, err
	}

	var err error
	if c.Range, err = parseRange(p); err != nil {
		return c, err
	}

	p.skipWhite()
	c.Typed = scanName(p)
	if c.Typed == "" {
		// A range with no command is vim's bare ":", which moves the cursor
		// to the line the range named. It is spelled as the "goto" pseudo
		// command so that dispatch has one shape and not two.
		if c.Range.Given > 0 {
			c.Name = cmdGoto
			return c, nil
		}
		if p.eof() {
			c.Name = cmdNop
			return c, nil
		}
		return c, withName(ErrNotAnEditorCommand, strings.TrimSpace(p.rest()))
	}

	// A name starting with an uppercase letter is a user command, always, and
	// never a built-in. That is vim's rule and it is what keeps ":JsonPretty"
	// out of the table and ":W" an E492 rather than a mistyped ":w". The
	// definition is not visible from here, so the arguments are left whole and
	// RunCmd checks them against -nargs.
	if c.Typed[0] >= 'A' && c.Typed[0] <= 'Z' {
		c.Name = c.Typed
		if p.peek() == '!' {
			c.Bang = true
			p.i++
		}
		c.Args = strings.TrimSpace(p.rest())
		return c, nil
	}

	cmd, ok := Lookup(c.Typed)
	if !ok {
		return c, withName(ErrNotAnEditorCommand, c.Typed)
	}
	c.Name = cmd.Name

	// ":k" swallows the character after it as its argument, so ":ka" is the
	// name "k" and the argument "a". Lookup has already decided that; the
	// scanner has to give the character back.
	if cmd.Name == "k" && len(c.Typed) > 1 {
		p.i -= len(c.Typed) - 1
		c.Typed = "k"
	}
	// ":s" may carry its flag letters against the name. Same trick: the name
	// is one character and the flags go back to the argument scanner.
	if cmd.Name == "substitute" && c.Typed != "substitute" && !strings.HasPrefix("substitute", c.Typed) {
		p.i -= len(c.Typed) - 1
		c.Typed = "s"
	}

	if p.peek() == '!' && cmd.Bang {
		c.Bang = true
		p.i++
	} else if p.peek() == '!' && !cmd.Bang {
		return c, ErrNoBangAllowed
	}

	if c.Range.Given > 0 && cmd.Range == RangeNone {
		return c, ErrNoRangeAllowed
	}

	parseArgs(p, cmd, &c)
	return c, nil
}

// cmdGoto and cmdNop are the two commands that have no name in vim and need
// one here. A bare ":5" moves to line 5 and a bare ":" does nothing at all,
// and both have to reach dispatch as a Cmd like everything else.
const (
	cmdGoto = "\x00goto"
	cmdNop  = "\x00nop"
)

// scanName reads the command name off the front.
//
// Letters, or one of the punctuation commands. The punctuation set is vim's
// own: "!" is the shell filter, "<" and ">" shift (and repeat, ":>>" is two
// shiftwidths), "=" prints a line number, "&" and "~" repeat a substitute,
// "#" is ":number", "*" is ":print" for a range, and "@" runs a register.
func scanName(p *parser) string {
	if p.eof() {
		return ""
	}
	c := p.peek()
	if isAlpha(c) {
		start := p.i
		for !p.eof() && isAlpha(p.peek()) {
			p.i++
		}
		return p.s[start:p.i]
	}
	switch c {
	case '<', '>':
		start := p.i
		for p.peek() == c {
			p.i++
		}
		return p.s[start:p.i]
	case '!', '=', '&', '~', '#', '*', '@':
		p.i++
		return string(c)
	}
	return ""
}

// parseMods takes the command modifiers off the front.
//
// A modifier is a command whose whole job is to change the one after it, so
// the loop resolves each token through Lookup and stops at the first that is
// not one. That is what keeps ":s/silent/x/" a substitute: Lookup("s") answers
// substitute, and only ":sil" answers silent.
//
// A range in front of a modifier is E481, which is vim's answer and was
// measured: ":1,2vertical sp" says so rather than splitting.
func parseMods(p *parser, c *Cmd) error {
	for {
		save := p.i
		p.skipWhite()
		if !isAlpha(p.peek()) {
			p.i = save
			return nil
		}
		start := p.i
		for !p.eof() && isAlpha(p.peek()) {
			p.i++
		}
		name := p.s[start:p.i]
		bang := false
		if p.peek() == '!' {
			bang = true
			p.i++
		}
		cmd, ok := Lookup(name)
		if !ok || !isModifier(cmd.Name) {
			p.i = save
			return nil
		}
		// A modifier has to have something after it. ":vertical" on its own
		// is not a split, and putting the token back lets it reach dispatch
		// as an ordinary command rather than silently doing nothing.
		after := p.i
		p.skipWhite()
		if p.eof() {
			p.i = save
			return nil
		}
		p.i = after
		applyMod(cmd.Name, bang, &c.Mods)
	}
}

// isModifier reports whether a resolved command name is one of the modifiers.
// It is a separate question from applying one so that a modifier with nothing
// after it can be put back without having changed anything.
func isModifier(name string) bool {
	var m Mods
	return applyMod(name, false, &m)
}

// applyMod records one modifier and reports whether the name was one.
//
// ":tab" carries a count in vim, which is parsed by the caller as part of the
// range; it is stored as -1 here, meaning "after the current tab", because
// nothing in this vimrc ever writes ":3tab".
func applyMod(name string, bang bool, m *Mods) bool {
	switch name {
	case "silent":
		m.Silent = true
		m.SilentBang = m.SilentBang || bang
	case "vertical":
		m.Vertical = true
	case "tab":
		m.Tab = -1
	case "aboveleft", "leftabove":
		m.Aboveleft = true
	case "belowright", "rightbelow":
		m.Belowright = true
	case "topleft":
		m.Topleft = true
	case "botright":
		m.Botright = true
	case "keepalt":
		m.Keepalt = true
	case "keepjumps", "keepmarks":
		m.Keepjumps = true
	case "noautocmd":
		m.Noautocmd = true
	case "confirm":
		m.Confirm = true
	case "browse":
		m.Browse = true
	default:
		return false
	}
	return true
}

// parseRange reads the line range in front of a command.
//
// The loop is vim's: an address, then a separator, then another, for as long
// as separators keep coming. Only the last two survive, which is why ":1,2,3d"
// deletes lines 2 to 3 and not 1 to 3, and why this keeps two Addrs rather
// than a slice.
//
// The semicolon is not a comma with a different spelling. It moves the cursor
// to the first address before the second one is resolved, so "/a/;/b/" finds
// the first b after the first a and "/a/,/b/" finds the first b in the file.
// A semicolon that is not the last separator moves the cursor too, so the
// addresses in front of the surviving pair are kept in Range.Earlier rather
// than dropped: ":3;.,.+1d" deletes lines 3 and 4 and not 1 and 2.
func parseRange(p *parser) (Range, error) {
	var r Range
	// Every address in the order it was written, and the separator after each
	// of them. Only the last two decide the lines, and every one of the
	// separators can still move the cursor.
	var addrs []Addr
	var semis []bool
scan:
	for {
		p.skipWhite()
		if p.peek() == '%' {
			p.i++
			r.From = Addr{Kind: AddrLine, Line: 1}
			r.To = Addr{Kind: AddrLast}
			r.Given = 2
			r.Semicolon = false
			// "%" is a whole range on its own. A separator after it is
			// vim's business and nobody writes one, so the loop stops here.
			return r, nil
		}
		a, got, err := parseAddr(p)
		if err != nil {
			return r, err
		}
		if !got {
			// No address here. A separator still counts: ":,5d" is the
			// current line to line 5, which vim accepts.
			if p.peek() != ',' && p.peek() != ';' {
				break scan
			}
			a = Addr{Kind: AddrCurrent}
		}
		addrs = append(addrs, a)
		if r.Given < 2 {
			r.Given++
		}
		p.skipWhite()
		switch p.peek() {
		case ',':
			semis = append(semis, false)
		case ';':
			semis = append(semis, true)
		default:
			break scan
		}
		p.i++
	}
	if len(addrs) == 0 {
		return r, nil
	}
	r.To = addrs[len(addrs)-1]
	if len(addrs) == 1 {
		r.From = r.To
		return r, nil
	}
	r.From = addrs[len(addrs)-2]
	r.Semicolon = semis[len(addrs)-2]
	for i := 0; i < len(addrs)-2; i++ {
		r.Earlier = append(r.Earlier, Sep{Addr: addrs[i], Semicolon: semis[i]})
	}
	return r, nil
}

// parseAddr reads one address and its offsets.
//
// got is false when there was no address at all, which is not an error: ":d"
// has none and ":,5d" has none before the comma. An address with only an
// offset -- ":+3d" -- is the current line plus three, which is why the base
// defaults to AddrCurrent whenever an offset was found.
func parseAddr(p *parser) (Addr, bool, error) {
	var a Addr
	got := false

	switch c := p.peek(); {
	case c == '.':
		p.i++
		a.Kind = AddrCurrent
		got = true
	case c == '$':
		p.i++
		a.Kind = AddrLast
		got = true
	case isDigit(c):
		n := 0
		for isDigit(p.peek()) {
			n = n*10 + int(p.peek()-'0')
			p.i++
		}
		a.Kind = AddrLine
		a.Line = n
		got = true
	case c == '\'':
		p.i++
		if p.eof() {
			return a, false, ErrInvalidMark
		}
		a.Kind = AddrMark
		a.Mark = p.peek()
		p.i++
		got = true
	case c == '/' || c == '?':
		p.i++
		pat, closed := scanPattern(p, c)
		a.Pattern = pat
		if c == '/' {
			a.Kind = AddrSearchFwd
		} else {
			a.Kind = AddrSearchBack
		}
		_ = closed
		got = true
	case c == '\\':
		switch p.at(1) {
		case '/':
			a.Kind = AddrNextMatch
		case '?':
			a.Kind = AddrPrevMatch
		case '&':
			a.Kind = AddrLastSubst
		default:
			return a, false, ErrBackslash
		}
		p.i += 2
		got = true
	}

	// The offsets. Any number of them, each a sign and an optional count,
	// summed: ".+3+2" is five lines on and "$-1" is the line before the last.
	for {
		save := p.i
		p.skipWhite()
		c := p.peek()
		if c != '+' && c != '-' {
			p.i = save
			break
		}
		sign := 1
		if c == '-' {
			sign = -1
		}
		p.i++
		n := 0
		digits := false
		for isDigit(p.peek()) {
			n = n*10 + int(p.peek()-'0')
			p.i++
			digits = true
		}
		if !digits {
			n = 1
		}
		a.Offset += sign * n
		if !got {
			a.Kind = AddrCurrent
			got = true
		}
	}
	return a, got, nil
}

// scanPattern reads a search address's pattern up to its closing delimiter.
//
// A backslash protects the delimiter, and an unterminated pattern runs to the
// end of the line, which is what ":/foo" does: it searches for foo and there
// is no command after it.
func scanPattern(p *parser, delim byte) (string, bool) {
	var out strings.Builder
	for !p.eof() {
		c := p.peek()
		if c == '\\' && p.at(1) != 0 {
			out.WriteByte(c)
			out.WriteByte(p.at(1))
			p.i += 2
			continue
		}
		if c == delim {
			p.i++
			return out.String(), true
		}
		out.WriteByte(c)
		p.i++
	}
	return out.String(), false
}

// parseArgs fills in the register, the count, the destination address and the
// argument text, according to what the command's table entry says it takes.
//
// The order is vim's: register first, then count, then the rest. That is why
// ":d a 3" deletes three lines into register a and ":d 3" deletes three into
// the unnamed one, and why a register name is never mistaken for a file name.
func parseArgs(p *parser, cmd *Command, c *Cmd) {
	p.skipWhite()

	if cmd.Reg && !p.eof() && !(cmd.Count && isDigit(p.peek())) && validYankReg(p.peek()) {
		// One character, no separator required. ":y foo" yanks into register
		// f and then answers E488 for "oo", which is vim measured rather
		// than guessed: ":1,2 d extra" says "E488: Trailing characters:
		// xtra".
		c.Reg = p.peek()
		p.i++
		p.skipWhite()
	}
	if cmd.Count && isDigit(p.peek()) {
		n := 0
		for isDigit(p.peek()) {
			n = n*10 + int(p.peek()-'0')
			p.i++
		}
		c.Count = n
		p.skipWhite()
	}
	if cmd.Dest {
		if a, got, err := parseAddr(p); err == nil && got {
			c.Addr = a
			p.skipWhite()
		}
	}
	c.Args = strings.TrimRight(p.rest(), " \t")
	if cmd.Args == ArgNone {
		c.Args = strings.TrimSpace(c.Args)
	}
}

// validYankReg is vim's own test for a register name in an ex command's
// arguments: a letter, a digit, or one of the four punctuation registers a
// command may write. The read-only ones are refused here rather than at the
// register file, because vim refuses them here and the difference is whether
// ":y:" is a yank into ":" or a yank followed by E488.
func validYankReg(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '"', c == '-', c == '_', c == '*', c == '+':
		return true
	}
	return false
}
