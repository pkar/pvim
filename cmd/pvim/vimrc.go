package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkar/pvim/internal/ex"
	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/screen"
	"github.com/pkar/pvim/internal/vimrc"
)

// Reading ~/.vimrc.
//
// The dividing rule: pvim runs a statement if it changes an
// option, a mapping, an autocmd, a user command, a highlight, or a g: variable
// pvim itself reads. It logs and ignores a "let g:" it does not read, so the
// eleven g:go_* lines and the g:vim_ai_* lines are inert without noise. It
// errors on any command it does not know, so a plugin call sneaking into the
// vimrc is visible on the first launch.
//
// An error is never fatal. Vim prints each one and carries on, and so does
// this: the file is 243 lines and one bad line in it must not be the
// difference between an editor and no editor.

// vimrcPath is the file the editor reads at startup, which is $MYVIMRC when
// the environment names one and ~/.vimrc otherwise.
func vimrcPath() string {
	if p := os.Getenv("MYVIMRC"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".vimrc")
}

// loadVimrc reads a vimrc into the editor, putting every error on the message
// line and carrying on.
//
// guiRunning is what has('gui_running') answers, which is false in the
// terminal and true in the window. It decides two branches of this vimrc: the
// FastEscape augroup and the <C-c>/<C-x>/<C-v> clipboard mappings, both of
// which are meant for the terminal and both of which are guarded on it.
//
// A vimrc that is not there is not an error and says nothing. That is the
// --clean case arriving by another route, and a new machine's first launch.
func (e *editor) loadVimrc(path string, guiRunning bool) {
	if path == "" {
		return
	}
	// A colourscheme that names itself, or a ":so" of the file doing the
	// sourcing, is a loop with no bottom. Vim's own limit is 'maxfuncdepth'
	// at 100 and it says E169 when a script nests deeper than that; this is
	// the same shape with a number small enough that the stack is never the
	// thing that notices. Nothing in this vimrc nests past two.
	const maxDepth = 10
	if e.sourceDepth >= maxDepth {
		e.ed.Say(fmt.Sprintf("E169: Command too recursive: %s", path))
		return
	}
	e.sourceDepth++
	defer func() { e.sourceDepth-- }()

	src, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			e.ed.Say(fmt.Sprintf("E484: Can't open file %s", path))
		}
		return
	}
	// Remembered so that a file this one sources -- a colourscheme, or the
	// "so $MYVIMRC" in the vimrc's own BufWritePost autocmd -- asks
	// has('gui_running') the same question and gets the same answer.
	e.guiRunning = guiRunning
	sink := &vimrcSink{ed: e, rtp: vimrc.NewRTP()}
	env := &vimrcEnv{opt: e.opt, gui: guiRunning}
	res := vimrc.Run(src, path, sink, env)
	for _, d := range res.Errors {
		e.ed.Say(d.Error())
	}
	// The option state has moved, so the mode machine's flat copy of it and
	// the window's 'scrolloff' both have to move with it. Every ":set" in the
	// file lands before this and none of them takes effect in the editor until
	// it runs.
	e.applyOptions()
}

// applyOptions pushes the option state into the two places that hold a copy of
// it: the mode machine's flat struct, and the session's resolved 'scrolloff'.
func (e *editor) applyOptions() {
	w := e.tabs.Window()
	e.ed.SetOptions(ex.ModeOptions(e.opt, w))
	// The window half. The four-field copy this replaces did not carry 'spell'
	// either, so ":setlocal spell" inside the vimrc's "au BufEnter *.txt,*.md"
	// reached the option table and never the window.
	ex.ApplyWindowOptions(e.opt, w)
	e.sess.so = e.opt.ScrollOffValue()
	if w != nil {
		e.sess.so = w.ScrollOff(e.opt)
	}
	e.sess.sync()
}

// vimrcSink applies each parsed statement to the editor.
//
// The five statements that reach real state are SetOption, Map, AutoCmd,
// Command and Highlight, and each one goes to the layer that owns it:
// internal/options, the map table, the autocmd table, the ex layer's user
// commands, and the highlight table. The rest are logged.
type vimrcSink struct {
	ed *editor

	// rtp is 'runtimepath', 'packpath' and 'viminfofile', which internal/
	// options has no rows for. See internal/vimrc/rtp.go for why they live
	// beside the loader instead.
	//
	// It is per-load rather than per-editor, which is a real limitation and
	// not a choice: the editor struct is another package's to change this run,
	// so there is nowhere on it to keep the value. What that costs is a
	// ":set runtimepath?" typed at the cmdline after startup, which answers
	// E518 either way because the option table has no row. What it does
	// not cost is the thing the live vimrc needs, because lines 6, 7, 8 and 9
	// -- the two ":set"s, the ":packloadall" and the ":colorscheme" that reads
	// the result -- are all one file and so all one sink.
	rtp vimrc.RTP
}

// SetOption is a ":set" line.
func (s *vimrcSink) SetOption(st vimrc.SetOption) error {
	return s.rtp.ApplySet(st.Args, func(rest string) error {
		_, err := s.ed.opt.ApplyLine(rest, st.Scope.Where())
		return err
	})
}

// Pack is ":packloadall", which the live vimrc runs on line 8.
//
// It puts the pack/*/start/* directories on 'runtimepath' and sources nothing,
// which is deliberately out of scope rather than a stub: the four
// packages on this machine hold nerdtree, vim-go, vim-terraform and nofrils,
// and pvim replaces the first three in Go and reads the fourth as a
// colourscheme on the next line. See internal/vimrc's Pack statement.
func (s *vimrcSink) Pack(st vimrc.Pack) error {
	s.rtp.LoadAll()
	return nil
}

// Let is a ":let" of a variable pvim reads; the ones it does not reach
// Ignored instead and never get here.
//
// TODO: the variables that are read are read by layers that do not exist yet
// -- the finder, the tree, the formatters and the gopls path -- so they are
// held rather than applied. mapleader is the exception and
// internal/vimrc applies it itself, because a mapping parsed before the leader
// is known is a mapping on the wrong key.
func (s *vimrcSink) Let(st vimrc.Let) error {
	s.ed.vars[st.Name] = st
	return nil
}

// Map is one of the map family.
//
// The statement goes two places. It is kept on the editor, in the order the
// file made them, because a ":map" listing and the vimrc gate both want the
// command as it was written; and it goes into the map table, where it starts
// firing on the next keystroke. See cmd/pvim/keymap.go for the table and
// internal/keymap for the resolution over it.
//
// The right-hand side is not looked at here, and that is the rule rather than
// an omission: ":NERDTreeToggle" is a command pvim does not have and will never
// have, and the mapping has to load without a word on the message line and fail
// with E492 when the keys are pressed. That is what vim does with a plugin that
// failed to install, and it is why the six mappings in this vimrc all define
// cleanly on a machine with no plugins on it at all.
func (s *vimrcSink) Map(st vimrc.Map) error {
	s.ed.maps = append(s.ed.maps, st)
	return s.ed.applyMap(st)
}

// AutoCmd is an ":autocmd".
//
// TODO: the autocmd table. The events the vimrc uses are
// BufNewFile, BufRead, BufEnter, BufWritePre, BufWritePost, FileType,
// InsertEnter and InsertLeave, and firing them needs hooks in the read, the
// write and the mode changes.
func (s *vimrcSink) AutoCmd(st vimrc.AutoCmd) error {
	s.ed.autocmds = append(s.ed.autocmds, st)
	return nil
}

// Command is a ":command!" definition, which the vimrc uses once for
// JsonPretty.
func (s *vimrcSink) Command(st vimrc.Command) error {
	if st.Delete {
		delete(s.ed.ctx.Cmds, st.Name)
		return nil
	}
	c := &ex.UserCommand{Name: st.Name, Repl: st.Repl}
	// The attribute list arrives as it was written, because which attributes
	// exist is the ex layer's table and not the parser's. The vimrc uses three
	// of them on its one command: -range, -nargs=0 and -bar.
	for _, a := range st.Attrs {
		name, value, _ := strings.Cut(strings.TrimPrefix(a, "-"), "=")
		switch name {
		case "range":
			c.Range = value
			if value == "" {
				c.Range = "."
			}
		case "nargs":
			c.NArgs = value
		case "bar":
			c.Bar = true
		case "bang":
			c.Bang = true
		case "complete":
			c.Complete = value
		}
	}
	return s.ed.ctx.Cmds.Define(c, st.Bang)
}

// FileType, the ":filetype" line, is in cmd/pvim/filetype.go with the detection
// it drives. This comment is here because the method is not: the placeholder
// that used to sit at this line asked whoever owns internal/filetype to replace
// the body rather than add a second FileType method to package main, and the
// replacement lives beside internal/filetype's caller instead.

// Highlight is a "hi" or "hi link" line, from the vimrc or from a colorscheme
// it sourced.
func (s *vimrcSink) Highlight(st vimrc.Highlight) error {
	t := s.ed.hl
	if st.Clear {
		// ":hi clear" with no group is a fresh table, which is what every
		// colourscheme starts with: nofrils-dark's line 7 is exactly this.
		// With a group it is "put that one group back to its default", and
		// pvim has no built-in defaults to put it back to, so the group is
		// left holding Normal's colours by setting it to Normal's.
		if st.Group == "" {
			s.ed.hl = screen.NewTable()
			s.ed.hlSet = nil
			return nil
		}
		t.Set(st.Group, t.Look(screen.Normal))
		return nil
	}
	if st.Default && s.ed.hlSet[st.Group] {
		// ":hi default" does not overrule a group that already has a
		// definition. The set is the editor's own and not the table's, because
		// screen.Table interns a name the moment it is asked about one and so
		// cannot answer "have you seen this" without inventing an answer.
		// Nothing in the four nofrils files writes a "hi default"; vim's own
		// defaults are full of them and the first colourscheme that is not
		// nofrils will bring some.
		return nil
	}
	if s.ed.hlSet == nil {
		s.ed.hlSet = map[string]bool{}
	}
	s.ed.hlSet[st.Group] = true
	if st.Link != "" {
		t.Set(st.Group, t.Look(t.ID(st.Link)))
		return nil
	}
	// Normal is passed in because "guifg=fg" and "guibg=bg" mean "whatever
	// Normal has", which is a lookup this package cannot do for itself.
	h, err := vimrc.ToHighlight(st, t.Look(screen.Normal))
	if err != nil {
		return err
	}
	t.Set(st.Group, h)
	return nil
}

// Ignored is the logged-and-dropped half of the dividing rule: a "let" of a
// variable nothing reads, a function definition, a call of one, and the
// commands accepted and made inert. It says nothing on the message line,
// because saying something once per plugin variable would be forty lines of
// noise on every startup.
func (s *vimrcSink) Ignored(st vimrc.Ignored) error {
	s.ed.ignored = append(s.ed.ignored, st)
	return nil
}

// Colorscheme is ":colorscheme nofrils-dark", which is a search down
// 'runtimepath' for colors/NAME.vim and then this same loader over it.
//
// TODO: the runtimepath search. The file this vimrc wants
// is under ~/.vim/pack/pkar/start/nofrils/colors/, which is a pack directory
// and not a plain runtimepath entry, so the walk has to know about packages.
func (s *vimrcSink) Colorscheme(st vimrc.Colorscheme) error {
	path, ok := s.findScheme(st.Name)
	if !ok {
		return fmt.Errorf("E185: Cannot find color scheme '%s'", st.Name)
	}
	s.ed.colorscheme = st.Name
	s.ed.loadVimrc(path, s.ed.guiRunning)
	return nil
}

// findScheme resolves a colourscheme name to a file.
//
// 'runtimepath' first, which is what vim does and what the live vimrc's lines 6
// to 8 are for: it puts its own vim home on the path and then loads its
// packages, and the scheme it wants is inside one of them. Then the fixed
// search list in colors.go, which is what the preserved vimrc needs and what a
// vimrc that never touched 'runtimepath' has always used.
//
// A name with a path separator in it is refused by findColorscheme and has to
// be refused here too, for the same reason: the name comes out of a config
// file and a scheme called "../../etc/passwd" reading a file outside the
// search list is not a feature.
func (s *vimrcSink) findScheme(name string) (string, bool) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return "", false
	}
	if path, ok := s.rtp.Find(filepath.Join("colors", name+".vim")); ok {
		return path, true
	}
	return findColorscheme(name)
}

// Source is ":so FILE", which the vimrc's BufWritePost autocmd uses on itself.
func (s *vimrcSink) Source(st vimrc.Source) error {
	s.ed.loadVimrc(st.Path, s.ed.guiRunning)
	return nil
}

// Ex is a plain ex command in the vimrc, which goes through the same path a
// typed one does.
func (s *vimrcSink) Ex(st vimrc.Ex) error { return s.ed.ctx.RunLine(st.Line) }

// Unknown is the E492 path: a command this editor does not have. It is an
// error with the file and the line number in front of it, and startup carries
// on, which is what vim does.
func (s *vimrcSink) Unknown(st vimrc.Unknown) error {
	if st.Err != nil {
		return st.Err
	}
	return fmt.Errorf("%w: %s", ex.ErrNotAnEditorCommand, st.Text)
}

// vimrcEnv answers the questions the vimrc's ":if" lines ask.
type vimrcEnv struct {
	opt *options.Options
	gui bool
}

// Has answers has(). The list is the and it is closed: true for the six
// features every branch in this vimrc turns on, false for everything else.
// gui_running is the one that is not a constant, because it is what tells the
// terminal frontend from the window.
func (v *vimrcEnv) Has(feature string) bool {
	switch feature {
	case "gui_running":
		return v.gui
	case "mouse", "clipboard", "persistent_undo", "autocmd", "conceal":
		return true
	}
	return false
}

// Exists answers exists(): an option name, and nothing else. A function or a
// variable is false, which is right for this editor -- there are no functions
// -- and is the answer that makes an "if exists('*Foo')" guard skip the block
// that calls Foo.
func (v *vimrcEnv) Exists(name string) bool {
	if opt, ok := strings.CutPrefix(name, "&"); ok {
		_, err := v.opt.Get(opt)
		return err == nil
	}
	return false
}

// Option answers "&name", which this vimrc reads twice: "if !&scrolloff" and
// "if !&sidescrolloff".
func (v *vimrcEnv) Option(name string) (options.Value, error) { return v.opt.Get(name) }

// Expand answers expand() and environment variables. The three this vimrc uses
// are "~/.vimgo", "$MYVIMRC" and "$MYGVIMRC".
func (v *vimrcEnv) Expand(s string) string {
	s = os.ExpandEnv(s)
	if s == "~" || strings.HasPrefix(s, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(s, "~"))
		}
	}
	return s
}

// FileReadable answers filereadable(), which the BufWritePost autocmd uses on
// $MYGVIMRC. A directory is not readable, which is vim's answer too.
func (v *vimrcEnv) FileReadable(path string) bool {
	info, err := os.Stat(v.Expand(path))
	return err == nil && !info.IsDir()
}

// The five that answer the live vimrc's filesystem questions. They are
// internal/vimrc's DefaultEnv answers, and they are delegated rather than
// written twice: this env exists to give the editor's own answers to has(),
// exists() and "&option", and the filesystem does not have an editor's opinion
// about it. Two answers to isdirectory() would be two answers to "did the
// state directory get made".
func (v *vimrcEnv) Executable(name string) bool  { return v.fs().Executable(name) }
func (v *vimrcEnv) IsDirectory(path string) bool { return v.fs().IsDirectory(path) }
func (v *vimrcEnv) MkDir(path, flags string, mode int) error {
	return v.fs().MkDir(path, flags, mode)
}
func (v *vimrcEnv) Resolve(path string) string { return v.fs().Resolve(path) }
func (v *vimrcEnv) Cwd() string                { return v.fs().Cwd() }

// fs is the shared answers. Nothing of this env goes into it but the two
// fields it might be asked about, because every one of the five is a question
// about the filesystem and the evaluator has already expanded the path before
// it asks.
func (v *vimrcEnv) fs() *vimrc.DefaultEnv {
	return &vimrc.DefaultEnv{GUI: v.gui, Opts: v.opt}
}

// runMap runs a map-family command typed at the cmdline.
//
// internal/ex parses the command name and hands the line back rather than
// building the mapping itself, because internal/vimrc imports internal/ex and
// the dependency cannot go the other way. The whole chain past this point
// already existed for the vimrc: Loader.Line parses the family, vimrcSink.Map
// calls applyMap, and that fills internal/keymap. This is the missing line.
func (e *editor) runMap(line string) error {
	res := vimrc.Run([]byte(line), "map", &vimrcSink{ed: e}, &vimrcEnv{opt: e.opt, gui: e.guiRunning})
	if len(res.Errors) > 0 {
		return res.Errors[0]
	}
	return nil
}

// Syntax is ":syntax on|off|enable|...".
//
// The word reaches internal/syntax, which is the engine that reads vim's own
// syntax files. It used to be dropped, because highlighting was out of scope,
// until the config actually being run turned out to have "syntax enable"
// uncommented.
func (s *vimrcSink) Syntax(st vimrc.Syntax) error {
	if s.ed.syn == nil {
		return nil
	}
	return s.ed.syn.command(st.Arg)
}
