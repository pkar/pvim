package vimrc

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// The builtin functions.
//
// This is a closed set of ten and it is not the start of a standard library.
// A vimscript interpreter is out of scope, and the
// rule that keeps this file from becoming one is that a function gets a row
// here only when a vimrc pvim has to load calls it. The ten below are exactly
// the calls in the two files in testdata, grepped rather than remembered:
//
//	has fnameescape mkdir isdirectory expand resolve fnamemodify executable
//	filereadable exists
//
// Everything else is E117 with the name in the message, which is the same
// thing vim says about a function that is not there, because from the caller's
// side "pvim has no such function" and "vim has no such function" are one
// event.
//
// Six of them touch the world and go through Env for it, so that a test can
// answer without a filesystem and so that this package still opens no files of
// its own. fnamemodify and fnameescape are string functions and are here in
// full.

// ErrMkdir is E739, which is what vim says when mkdir() cannot make the
// directory. Vim returns 0 as well as saying it; this raises the error and
// stops the statement, because a vimrc that asked for a directory and did not
// get one has a broken 'undodir' two lines later and the quiet version of that
// is a swap file nobody can find.
var ErrMkdir = errors.New("E739: Cannot create directory")

// ErrFileMod is E-less in vim, which answers an unknown ":" modifier by
// leaving it in the string. pvim refuses instead: a modifier that silently
// does nothing is a path that is silently wrong, and the whole point of the
// line this package draws is that it is visible.
var ErrFileMod = errors.New("E15: Invalid expression: unsupported filename modifier")

// builtin is one row of the table: the name, and how many arguments vim lets
// it take. The maximum is vim's and not what the vimrc uses, so that a call
// with too many arguments says E118 rather than being quietly truncated.
type builtin struct {
	minArgs, maxArgs int
}

// builtins is the closed set. See the file comment for what closes it.
var builtins = map[string]builtin{
	"has":          {1, 1},
	"exists":       {1, 1},
	"filereadable": {1, 1},
	"expand":       {1, 1},
	"executable":   {1, 1},
	"isdirectory":  {1, 1},
	"resolve":      {1, 1},
	"fnameescape":  {1, 1},
	"fnamemodify":  {2, 2},
	"mkdir":        {1, 3},
}

// fnameescape escapes a file name for use in an ex command.
//
// The character set is vim's own, from :help fnameescape(): the characters
// that are special to a file-name argument, plus a leading "+" or ">" which
// would otherwise be read as part of the command. Measured against vim
// 9.2.0321: fnameescape('/a b/c#d%e.vim') is '/a\ b/c\#d\%e.vim'.
//
// This is what makes the live vimrc's six execute lines safe: the vim home is
// ~/work/agents/bash/vim and a "set undodir=" of a path with a
// space or a "%" in it would otherwise set an option the shell never sees.
func fnameescape(s string) string {
	const special = " \t\n*?[{`$\\%#'\"|!<"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(special, c) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	out := b.String()
	if strings.HasPrefix(out, "+") || strings.HasPrefix(out, ">") {
		out = "\\" + out
	}
	return out
}

// fnamemodify applies the ":" modifiers to a file name.
//
// Five of vim's modifiers are here and the rest are refused. The five are the
// ones that are a path operation and nothing else -- :p, :h, :t, :r, :e -- and
// they are the whole of what the two vimrcs use, which is ":p" inside an
// expand() and ":h" on the result. The ones left out are :., :~, :8, :S, :gs?
// and :s?, and the last two are substitutions in vim's regex dialect, which
// would drag internal/regex under this package to be answered honestly and
// would be answered dishonestly by anything cheaper.
//
// cwd is what ":p" makes a relative name absolute against. It comes from the
// caller rather than from os.Getwd so that this package still asks the world
// nothing directly.
//
// Measured fnamemodify('/a/b/c.vim', ...) is '/a/b' for :h,
// 'c.vim' for :t, '/a/b/c' for :r and 'vim' for :e.
func fnamemodify(name, mods, cwd string) (string, error) {
	for mods != "" {
		if !strings.HasPrefix(mods, ":") {
			return "", fmt.Errorf("%w: %s", ErrFileMod, mods)
		}
		switch mods[1:2] {
		case "p":
			if !filepath.IsAbs(name) {
				name = filepath.Join(cwd, name)
			}
			name = filepath.Clean(name)
		case "h":
			name = filepath.Dir(name)
		case "t":
			name = filepath.Base(name)
		case "r":
			name = strings.TrimSuffix(name, filepath.Ext(name))
		case "e":
			name = strings.TrimPrefix(filepath.Ext(name), ".")
		default:
			return "", fmt.Errorf("%w: %s", ErrFileMod, mods)
		}
		mods = mods[2:]
	}
	return name, nil
}

// splitFileMods cuts a name from the ":" modifiers glued to the end of it,
// which is the shape expand() takes: expand('<sfile>:p') is the name
// "<sfile>" and the modifier ":p".
//
// It cuts at the first ":" and no further, because none of the five modifiers
// this package keeps has a ":" inside it. That is exactly why :s? and :gs?
// are refused above rather than half-parsed here.
func splitFileMods(s string) (name, mods string) {
	if i := strings.Index(s, ":"); i >= 0 {
		return s[:i], s[i:]
	}
	return s, ""
}
