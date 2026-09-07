package vimrc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/screen"
)

// Config is the loaded configuration: what the vimrc said, in the shapes the
// editor reads.
//
// It is a real Sink and not a test double. The alternative was a mock in the
// test file and a second implementation in cmd/pvim, which is two places for
// "does :setlocal write the buffer half only" to be answered differently.
// Everything above this package reads these fields.
type Config struct {
	// Opts is the option state every ":set" line landed in.
	Opts *options.Options
	// Table is the highlight table every ":hi" line landed in.
	Table *screen.Table

	// Maps is the mappings in the order they were defined, unmaps applied.
	Maps []Map
	// AutoCmds is the autocommands in the order they were defined.
	AutoCmds []AutoCmd
	// Commands is the user commands by name.
	Commands map[string]Command
	// Vars is every variable Read says pvim reads, by its qualified name.
	Vars map[string]Val
	// FTDetect, FTPlugin and FTIndent are where ":filetype" left its three
	// switches. All three start false, which is vim with no vimrc and no
	// defaults.vim: detection is a thing a config turns on, and this vimrc
	// turns it on with "filetype plugin indent on" on line 2.
	//
	// FTPlugin and FTIndent are recorded and read by nothing, because pvim
	// sources no ftplugin and no indent script -- see the FileType statement
	// for the wording on that. They are here so that the answer to
	// "did the line parse" is not the same as the answer to "did the line do
	// anything", which is the difference a gate has to be able to see.
	FTDetect, FTPlugin, FTIndent bool

	// SyntaxArg is the word from the last ":syntax", empty when the file has
	// none. Recorded rather than acted on; see Syntax.
	SyntaxArg string
	// FTRedetect counts the ":filetype detect" lines, which ask for the open
	// buffers to be scanned again rather than changing a switch. Nothing in
	// this vimrc uses one; a count rather than a bool because two of them are
	// two events and a caller replaying this Config has to make both.
	FTRedetect int

	// Paths is 'runtimepath', 'packpath' and 'viminfofile', which internal/
	// options has no rows for. See rtp.go for why they live here and what it
	// takes to move them where they belong.
	Paths RTP
	// Packages is the directories ":packloadall" put on 'runtimepath', in the
	// order it put them there. Empty when the file never ran one, which is the
	// preserved vimrc.
	Packages []string

	// Scheme is the name from the last ":colorscheme". It is not called
	// Colorscheme because the Sink method of that name has to be.
	Scheme string
	// Sources is every ":source", in order, for a caller that resolves paths
	// and loads them. This package never opens a file.
	Sources []Source
	// ExCmds is the commands handed through to internal/ex.
	ExCmds []Ex
	// Errors and Dropped accumulate across every Run against this Config, so
	// that a vimrc and the colourscheme it sourced report as one load.
	Errors  []Diag
	Dropped []Ignored

	// hi is the highlight definitions as parsed, in definition order, so that
	// the whole table can be resolved again when Normal changes. A colour
	// scheme that sets Normal last -- nofrils sets it first, but nothing
	// says a scheme must -- would otherwise leave every other group resolved
	// against the wrong background.
	hi    []Highlight
	hiIdx map[string]int
}

// NewConfig returns a Config with vim's option defaults and a highlight table
// holding only Normal.
func NewConfig() *Config {
	opts := options.Defaults()
	return &Config{
		Opts:     &opts,
		Table:    screen.NewTable(),
		Commands: map[string]Command{},
		Vars:     map[string]Val{},
		Paths:    NewRTP(),
		hiIdx:    map[string]int{},
	}
}

// Leader is 'mapleader' as the file left it, which is "," in this vimrc. Vim's
// value when nothing set one is a backslash.
func (c *Config) Leader() string {
	if v, ok := c.Vars["g:mapleader"]; ok {
		return v.String()
	}
	return `\`
}

// SetOption applies a ":set" line.
//
// The three 'runtimepath'-family names go to Paths and everything else to the
// option table. See rtp.go for why that split exists and how it goes away.
func (c *Config) SetOption(s SetOption) error {
	return c.Paths.ApplySet(s.Args, func(rest string) error {
		_, err := c.Opts.ApplyLine(rest, s.Scope.Where())
		return err
	})
}

// Pack is ":packloadall": the pack directories go onto 'runtimepath' and
// nothing is sourced. See the Pack statement.
func (c *Config) Pack(p Pack) error {
	c.Packages = append(c.Packages, c.Paths.LoadAll()...)
	return nil
}

// Let records a variable pvim reads.
func (c *Config) Let(l Let) error {
	if l.Unlet {
		delete(c.Vars, l.Name)
		return nil
	}
	c.Vars[l.Name] = l.Val
	return nil
}

// Map records a mapping.
//
// An unmap removes the mappings with the same left-hand side in any of the
// modes it names, and a mapclear removes every mapping in them. Modes are
// letters and a bare ":map" defines three at once, so the overlap is by
// letter and not by command.
func (c *Config) Map(m Map) error {
	if m.Unmap {
		keep := c.Maps[:0]
		for _, old := range c.Maps {
			if sharesMode(old.Modes, m.Modes) && (m.Clear || old.LHSText == m.LHSText) {
				continue
			}
			keep = append(keep, old)
		}
		c.Maps = keep
		return nil
	}
	for i, old := range c.Maps {
		if old.Modes == m.Modes && old.LHSText == m.LHSText && old.Buffer == m.Buffer {
			c.Maps[i] = m
			return nil
		}
	}
	c.Maps = append(c.Maps, m)
	return nil
}

func sharesMode(a, b string) bool {
	return strings.ContainsAny(a, b)
}

// AutoCmd records an autocommand, or removes the ones a ":au!" names.
func (c *Config) AutoCmd(a AutoCmd) error {
	if a.Clear {
		// ":au!" with no arguments empties the current group, which is what
		// the three augroups in the vimrc open with. With an event, and then
		// with a pattern, it narrows.
		keep := c.AutoCmds[:0]
		for _, old := range c.AutoCmds {
			matches := old.Group == a.Group &&
				(len(a.Events) == 0 || sharesEvent(old.Events, a.Events)) &&
				(len(a.Patterns) == 0 || samePatterns(old.Patterns, a.Patterns))
			if matches {
				continue
			}
			keep = append(keep, old)
		}
		c.AutoCmds = keep
		if a.Cmd == "" {
			return nil
		}
	}
	if a.Cmd == "" {
		return nil // a listing, or a clear that has already happened
	}
	c.AutoCmds = append(c.AutoCmds, a)
	return nil
}

// samePatterns reports whether two pattern lists share one. Vim compares the
// pattern text and not what it matches, so ":au! BufRead *.go" leaves
// ":au BufRead *.g?" alone.
func samePatterns(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

func sharesEvent(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// Command records a user command.
func (c *Config) Command(cmd Command) error {
	if cmd.Delete {
		delete(c.Commands, cmd.Name)
		return nil
	}
	c.Commands[cmd.Name] = cmd
	return nil
}

// Highlight records a highlight definition and resolves the table again.
func (c *Config) Highlight(h Highlight) error {
	switch {
	case h.Clear && h.Group == "":
		c.hi = nil
		c.hiIdx = map[string]int{}
		c.Table = screen.NewTable()
		return nil
	case h.Clear:
		// ":hi clear {group}" resets the group to its default in vim, and
		// pvim has no built-in defaults to reset to, so it forgets the
		// definition and the group renders as Normal.
		if i, ok := c.hiIdx[h.Group]; ok {
			c.hi = append(c.hi[:i], c.hi[i+1:]...)
			c.reindex()
		}
		return c.rebuild()
	}
	if i, ok := c.hiIdx[h.Group]; ok {
		// ":hi default" does not overrule an existing definition, whatever
		// that definition was. Nothing in the four nofrils files writes one;
		// vim's own defaults are full of them and the first colourscheme that
		// is not nofrils will bring some.
		if h.Default {
			return nil
		}
		c.hi[i] = h
	} else {
		c.hiIdx[h.Group] = len(c.hi)
		c.hi = append(c.hi, h)
	}
	return c.rebuild()
}

func (c *Config) reindex() {
	c.hiIdx = make(map[string]int, len(c.hi))
	for i, h := range c.hi {
		c.hiIdx[h.Group] = i
	}
}

// rebuild resolves every definition into the table, in definition order.
//
// Normal is resolved first whatever its place in the file, because every other
// group inherits from it: "guibg=NONE" means Normal's background and
// "guifg=fg" means Normal's foreground, and a scheme resolved in file order
// with Normal at the end would give every group the default colours and then
// change the default underneath them.
func (c *Config) rebuild() error {
	normal := screen.DefaultNormal
	for _, h := range c.hi {
		if h.Group == screen.NormalName && h.Link == "" {
			got, err := ToHighlight(h, normal)
			if err != nil {
				return err
			}
			normal = got
		}
	}
	c.Table = screen.NewTable()
	c.Table.Set(screen.NormalName, normal)

	var firstErr error
	for _, h := range c.hi {
		if h.Link != "" {
			c.Table.Set(h.Group, c.Table.Look(c.Table.ID(h.Link)))
			continue
		}
		got, err := ToHighlight(h, normal)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		c.Table.Set(h.Group, got)
	}
	return firstErr
}

// FileType applies a ":filetype" line to the three switches.
//
// A bare ":filetype" is a report and changes nothing, which is why it is the
// one form that returns early: a Config is not a message line and has nowhere
// to print to.
func (c *Config) FileType(f FileType) error {
	if f.Report {
		return nil
	}
	if f.Detect {
		c.FTDetect = f.On
	}
	if f.Plugin {
		c.FTPlugin = f.On
	}
	if f.Indent {
		c.FTIndent = f.On
	}
	if f.Redetect {
		c.FTRedetect++
	}
	return nil
}

// Colorscheme records the name. The file itself is the caller's to find and to
// hand back to Run: resolving a name against 'runtimepath' is not something a
// parser should know how to do.
func (c *Config) Colorscheme(cs Colorscheme) error {
	c.Scheme = cs.Name
	return nil
}

// Source records a ":source".
func (c *Config) Source(s Source) error {
	c.Sources = append(c.Sources, s)
	return nil
}

// Ex records a command for internal/ex to run.
func (c *Config) Ex(e Ex) error {
	c.ExCmds = append(c.ExCmds, e)
	return nil
}

// Ignored records a logged-and-dropped statement.
func (c *Config) Ignored(i Ignored) error {
	c.Dropped = append(c.Dropped, i)
	return nil
}

// Unknown records a failure. It returns nil because the loader has already
// recorded the diagnostic and a second error here would double-count it.
func (c *Config) Unknown(u Unknown) error {
	c.Errors = append(c.Errors, Diag{Pos: u.Pos, Text: u.Text, Err: u.Err})
	return nil
}

// Match returns the autocommands registered for an event that match a file
// name, in the order they were defined, which is the order vim runs them in.
//
// Vim's pattern rule is the one that matters here: a pattern with no "/" in it
// is matched against the file's tail and one with a "/" against the whole
// path, which is why ".vimrc" in the vimrc's own BufWritePost autocommand
// matches ~/.vimrc.
func (c *Config) Match(event, name string) []AutoCmd {
	return MatchAutoCmds(c.AutoCmds, event, name)
}

// MatchAutoCmds is Config.Match over any list of autocommands, for a caller
// that holds its own rather than a Config: cmd/pvim keeps the vimrc's
// autocommands on the editor in definition order, and firing an event there
// has to mean exactly what firing it here means or the two disagree about
// which files a BufRead covers.
//
// name is what the pattern is matched against, and it is NOT always a file
// name. Vim matches a FileType pattern against the filetype, a Syntax pattern
// against the syntax name and a User pattern against the user event's name,
// which is why this takes a target rather than reading one off a buffer. Get
// that wrong and "au FileType python" fires on every file in a directory
// called python and on no python file at all.
func MatchAutoCmds(cmds []AutoCmd, event, name string) []AutoCmd {
	canonical, ok := canonicalEvent(event)
	if !ok {
		return nil
	}
	var out []AutoCmd
	for _, a := range cmds {
		if !sharesEvent(a.Events, []string{canonical}) {
			continue
		}
		for _, pat := range a.Patterns {
			target := name
			if !strings.Contains(pat, "/") {
				target = filepath.Base(name)
			}
			if globMatch(pat, target) {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// globMatch is vim's autocommand pattern match: "*" is any run of characters
// including a path separator, "?" is one character, and everything else is
// itself.
//
// Not filepath.Match, whose "*" stops at a separator: "au BufWritePre *" has
// to match "~/x.go" and filepath.Match says it does not. Character
// classes are not here because no pattern in the vimrc has one and a
// half-written class would match silently wrong.
func globMatch(pat, name string) bool {
	// px and nx are the current positions; star and mark remember the last
	// "*" so a mismatch can back up to it, which is the standard two-pointer
	// glob and is linear.
	px, nx, star, mark := 0, 0, -1, 0
	for nx < len(name) {
		switch {
		case px < len(pat) && (pat[px] == '?' || pat[px] == name[nx]):
			px++
			nx++
		case px < len(pat) && pat[px] == '*':
			star, mark = px, nx
			px++
		case star >= 0:
			px = star + 1
			mark++
			nx = mark
		default:
			return false
		}
	}
	for px < len(pat) && pat[px] == '*' {
		px++
	}
	return px == len(pat)
}

// DefaultEnv is the world a real load asks its questions of.
//
// The has() answers are pinned: true for gui_running, mouse,
// clipboard, persistent_undo, autocmd and conceal, false for everything else,
// which is enough for every branch in this vimrc. GUI is a field and not a
// constant because the answer decides which half of the file runs: the
// terminal frontend has to say false, and then the FastEscape autocommands
// and the <C-c>/<C-v> clipboard mappings take effect exactly as they do in
// terminal vim.
//
// The list is six and not eight because this is the env the gate grades the
// vimrc through, and a gate that answers has() differently from the editor
// grades a file nobody runs. Measured against
// /opt/homebrew/bin/vim 9.2.0321: has('unnamed') is 0 there and was true
// here, which is a guess where vim had an answer. has('multi_byte') is 1
// there and is false here, deliberately, because it is false in the editor's
// own env and pvim is utf-8 and latin1 only: a branch that turns on
// multi-byte handling has nothing to turn on. That one is a real difference
// and belongs in the register the day a vimrc asks for it.
type DefaultEnv struct {
	// GUI is has('gui_running').
	GUI bool
	// Opts is what "&option" reads.
	Opts *options.Options
	// Vars is the script-local environment: $MYVIMRC and $MYGVIMRC, which vim
	// sets itself and the process environment does not have.
	Vars map[string]string
}

// guiFeatures is every has() this vimrc asks about that depends on the
// frontend. Everything else in trueFeatures is true in both.
var trueFeatures = map[string]bool{
	"mouse": true, "clipboard": true, "persistent_undo": true,
	"autocmd": true, "conceal": true,
}

// Has answers has().
func (e *DefaultEnv) Has(feature string) bool {
	if feature == "gui_running" {
		return e.GUI
	}
	return trueFeatures[feature]
}

// Exists answers exists() for the two forms the loader does not answer itself.
// pvim defines no vimscript functions, so "*name" is always false; a command
// is the ex layer's business and is not visible from here.
func (e *DefaultEnv) Exists(name string) bool {
	if opt, ok := strings.CutPrefix(name, "&"); ok {
		_, err := e.Option(opt)
		return err == nil
	}
	return false
}

// Option answers "&name".
func (e *DefaultEnv) Option(name string) (options.Value, error) {
	if e.Opts == nil {
		defaults := options.Defaults()
		e.Opts = &defaults
	}
	return e.Opts.Get(name)
}

// Expand answers expand() and $VAR, for the three forms the vimrc uses: a
// leading "~", a "$NAME" on its own and a "$NAME" at the front of a path.
func (e *DefaultEnv) Expand(s string) string {
	if strings.HasPrefix(s, "$") {
		name := s[1:]
		rest := ""
		if i := strings.IndexAny(name, "/\\"); i >= 0 {
			name, rest = name[:i], name[i:]
		}
		value, ok := e.Vars[name]
		if !ok {
			value = os.Getenv(name)
		}
		return value + rest
	}
	if s == "~" || strings.HasPrefix(s, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return s
		}
		return filepath.Join(home, strings.TrimPrefix(s[1:], "/"))
	}
	return s
}

// FileReadable answers filereadable().
func (e *DefaultEnv) FileReadable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// Executable answers executable().
//
// A name with no path separator is looked up on $PATH, which is what vim does
// and what makes executable('jq') mean the same thing here as in a shell. A
// name with one is checked where it is, and it has to be a regular file with
// an execute bit: the live vimrc asks about /opt/homebrew/bin/bash and takes
// the else branch to /bin/bash when the answer is no, so a wrong yes here is a
// 'shell' pointing at a file that is not there.
func (e *DefaultEnv) Executable(name string) bool {
	if name == "" {
		return false
	}
	if !strings.ContainsRune(name, os.PathSeparator) {
		_, err := exec.LookPath(name)
		return err == nil
	}
	info, err := os.Stat(name)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// IsDirectory answers isdirectory().
func (e *DefaultEnv) IsDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// MkDir answers mkdir().
//
// Only the "p" flag is honoured, because it is the only one the live vimrc
// passes and the other three -- "D" and "R" for delete-on-exit, and the
// undocumented ones -- are about a lifetime this loader does not have. A flag
// string with anything else in it is an error rather than an ignored
// character: mkdir(x, 'D') in vim removes the directory when vim exits, and
// doing the create and quietly skipping the remove is the kind of difference
// that is found by running out of disk.
func (e *DefaultEnv) MkDir(path, flags string, mode int) error {
	for _, f := range flags {
		if f != 'p' {
			return fmt.Errorf("mkdir flag %q is not one pvim has", string(f))
		}
	}
	perm := os.FileMode(mode) & os.ModePerm
	if strings.ContainsRune(flags, 'p') {
		return os.MkdirAll(path, perm)
	}
	return os.Mkdir(path, perm)
}

// Resolve answers resolve(): follow every symbolic link in the path.
//
// A path that is not there comes back as it went in, which is vim's answer
// too, so that resolve() on a file about to be created is not an error.
func (e *DefaultEnv) Resolve(path string) string {
	got, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return got
}

// Cwd is the working directory, which fnamemodify's ":p" resolves a relative
// name against. An error means there is no directory to be relative to, and an
// empty string leaves the name alone rather than rooting it at "/".
func (e *DefaultEnv) Cwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// Syntax is ":syntax". Config records the word and runs nothing: it is the
// read-only view of a vimrc that tests and tools use, and turning a highlighter
// on is an editor's job.
func (c *Config) Syntax(st Syntax) error {
	c.SyntaxArg = st.Arg
	return nil
}
