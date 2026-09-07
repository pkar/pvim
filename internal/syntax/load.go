package syntax

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Finding vim's runtime, and reading a syntax file out of it.
//
// This package writes no syntax rules of its own. Every rule it runs comes out
// of the 773 files in vim's own syntax directory, which is the only reason a
// highlighter for Go, Python, JSON and everything else is a week of work rather
// than a decade: `syn keyword`, `syn match` and `syn region` are vim regexes,
// internal/regex already translates those, and what is left is an interpreter
// for the language those three commands make up.
//
// On a homebrew macOS box the runtime is not where the obvious guess puts it.
// /opt/homebrew/share/vim/vim92 holds "vimfiles" and nothing else; the real
// tree is inside MacVim.app, and /opt/homebrew/bin/vim is a symlink into it.
// internal/filetype found the same thing and says so in its own package doc.

// Runtime is where syntax files are read from: vim's 'runtimepath', in the
// order vim searches it.
type Runtime struct {
	Dirs []string
}

// runtimeCandidates is every place a vim runtime has been found on a machine
// this editor is meant to run on, most specific first. $VIMRUNTIME beats all of
// them when it is set, because that is what vim itself honours.
var runtimeCandidates = []string{
	"/opt/homebrew/Cellar/macvim/*/MacVim.app/Contents/Resources/vim/runtime",
	"/Applications/MacVim.app/Contents/Resources/vim/runtime",
	"/opt/homebrew/share/vim/vim*",
	"/usr/local/share/vim/vim*",
	"/usr/share/vim/vim*",
	"/usr/share/vim/vimfiles",
}

// DefaultRuntime finds vim's runtime directory.
//
// A runtime that has no syntax/syntax.vim in it is not a runtime, which is the
// check that skips /opt/homebrew/share/vim/vim92 on a homebrew box: it exists,
// it is on the obvious path, and all it holds is an empty vimfiles.
func DefaultRuntime() Runtime {
	var dirs []string
	add := func(d string) {
		if d == "" {
			return
		}
		if _, err := os.Stat(filepath.Join(d, "syntax", "syntax.vim")); err != nil {
			return
		}
		for _, have := range dirs {
			if have == d {
				return
			}
		}
		dirs = append(dirs, d)
	}
	add(os.Getenv("VIMRUNTIME"))
	for _, pat := range runtimeCandidates {
		matches, _ := filepath.Glob(pat)
		sort.Sort(sort.Reverse(sort.StringSlice(matches)))
		for _, m := range matches {
			add(m)
		}
	}
	return Runtime{Dirs: dirs}
}

// find returns the first path in the runtime matching a relative name like
// "syntax/go.vim", and whether there was one.
func (rt Runtime) find(rel string) (string, bool) {
	for _, d := range rt.Dirs {
		p := filepath.Join(d, filepath.FromSlash(rel))
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// findAll returns every path in the runtime matching a relative pattern, which
// is what `runtime!` sources.
func (rt Runtime) findAll(rel string) []string {
	var out []string
	for _, d := range rt.Dirs {
		matches, _ := filepath.Glob(filepath.Join(d, filepath.FromSlash(rel)))
		sort.Strings(matches)
		out = append(out, matches...)
	}
	return out
}

// Load reads the syntax file for a filetype and returns the rules in it.
//
// The error is a MissingError when the runtime has no file for the type, which
// is not a failure: most of the 773 files are for languages nobody here edits,
// and a filetype with no syntax file is a buffer that draws in Normal, exactly
// as vim draws it.
func Load(rt Runtime, filetype string) (*Syntax, error) {
	if filetype == "" {
		return nil, MissingError{Refusal: Refusal{File: "syntax/", Detail: "no filetype"}}
	}
	rel := "syntax/" + filetype + ".vim"
	path, ok := rt.find(rel)
	if !ok {
		return nil, MissingError{Refusal: Refusal{File: rel, Detail: filetype}}
	}

	s := newSyntax(filetype)
	ev := &evaluator{s: s, vars: map[string]value{}, funcs: map[string]*function{}}
	r := &runner{s: s, ev: ev, rt: rt, file: rel}
	s.pending = r.include
	if err := r.sourceFile(path, rel); err != nil {
		return nil, err
	}
	s.finish()
	return s, nil
}

// sourceFile reads a file and runs it.
func (r *runner) sourceFile(path, rel string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return MissingError{Refusal: Refusal{File: rel, Detail: err.Error()}}
	}
	saveFile, saveStop, savePath := r.file, r.stop, r.path
	r.file, r.stop, r.path = rel, false, path
	r.block(join(strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")))
	r.file, r.stop, r.path = saveFile, saveStop, savePath
	return nil
}

// expandSfile resolves vim's <sfile> against the file being sourced.
//
// typescript.vim is `source <sfile>:h/shared/typescriptcommon.vim` and is
// nothing but that line plus a dozen rules, so a reader that cannot expand
// <sfile> loads a typescript buffer with no rules at all. The modifiers that
// appear in the runtime are:h, :p and:p:h, and they are the only three here.
func expandSfile(arg, path string) string {
	if !strings.HasPrefix(arg, "<sfile>") {
		return arg
	}
	rest := arg[len("<sfile>"):]
	out := path
	for {
		switch {
		case strings.HasPrefix(rest, ":p"):
			if abs, err := filepath.Abs(out); err == nil {
				out = abs
			}
			rest = rest[2:]
		case strings.HasPrefix(rest, ":h"):
			out = filepath.Dir(out)
			rest = rest[2:]
		case strings.HasPrefix(rest, ":t"):
			out = filepath.Base(out)
			rest = rest[2:]
		default:
			return out + rest
		}
	}
}

// source runs `:source path`.
func (r *runner) source(arg string, line int) {
	arg = strings.Trim(strings.TrimSpace(arg), `"`)
	if arg == "" {
		return
	}
	arg = expandSfile(arg, r.path)
	if strings.HasPrefix(arg, "$VIMRUNTIME/") {
		arg = strings.TrimPrefix(arg, "$VIMRUNTIME/")
	}
	if path, ok := r.rt.find(arg); ok {
		r.sourceFile(path, arg)
		return
	}
	if _, err := os.Stat(arg); err == nil {
		r.sourceFile(arg, arg)
		return
	}
	at := r.at(line)
	at.Detail = "source " + arg
	r.s.refuse(MissingError{Refusal: at})
}

// runtime runs `:runtime[!] {file}`, which is how markdown pulls in html and
// how every syntax file that builds on another one says so.
//
// The bang sources every match on the runtime path and the plain form sources
// the first, which is vim, and on a machine with one runtime directory the two
// are the same thing.
func (r *runner) runtime(arg string, all bool, line int) {
	arg = strings.TrimSpace(arg)
	for _, kw := range []string{"START ", "OPT ", "where=ALL ", "where=START ", "where=OPT "} {
		arg = strings.TrimPrefix(arg, kw)
	}
	if arg == "" {
		return
	}
	if r.s.includeDepth > 8 {
		at := r.at(line)
		at.Detail = "runtime " + arg
		r.s.refuse(ScriptError{Refusal: at})
		return
	}
	r.s.includeDepth++
	defer func() { r.s.includeDepth-- }()

	for _, one := range strings.Fields(arg) {
		paths := r.rt.findAll(one)
		if len(paths) == 0 {
			continue
		}
		if !all {
			paths = paths[:1]
		}
		for _, p := range paths {
			r.sourceFile(p, one)
		}
	}
}

// synInclude runs `syn include [@cluster] {file}`.
//
// Everything the included file defines becomes contained and joins the
// cluster, which is how markdown puts a fenced code block's language inside a
// region and how html puts javascript inside a <script>.
func (s *Syntax) synInclude(arg string, at Refusal) {
	arg = strings.TrimSpace(arg)
	group := ""
	if strings.HasPrefix(arg, "@") {
		group, arg = word(arg)
		group = group[1:]
		arg = strings.TrimSpace(arg)
	}
	if arg == "" {
		return
	}
	at.Detail = "syn include " + arg
	if s.pending == nil {
		s.refuse(CommandError{Refusal: at})
		return
	}
	s.pending(group, arg, at)
}

// include is what `syn include` does once the name has been read: source the
// file with every item it defines forced contained and joined to the cluster.
//
// b:current_syntax is cleared around it because every syntax file in the
// runtime opens with `if exists("b:current_syntax") | finish | endif`, and an
// include that left it set would source nothing at all.
func (r *runner) include(cluster, file string, at Refusal) {
	if r.s.includeDepth > 8 {
		r.s.refuse(ScriptError{Refusal: at})
		return
	}
	file = expandSfile(file, r.path)
	path, ok := r.rt.find(file)
	if !ok {
		if p, err := filepath.Abs(file); err == nil {
			if _, err := os.Stat(p); err == nil {
				path, ok = p, true
			}
		}
	}
	if !ok {
		r.s.refuse(MissingError{Refusal: at})
		return
	}

	saveCluster, saveContained := r.s.intoCluster, r.s.forceContained
	saveSyntax, hadSyntax := r.ev.vars["b:current_syntax"]
	delete(r.ev.vars, "b:current_syntax")
	if cluster != "" {
		r.s.intoCluster = cluster
		// The cluster has to exist even when the file defines nothing, or a
		// contains=@markdownHighlight_go resolves to every item instead of
		// none.
		r.s.cl(cluster)
	}
	r.s.forceContained = true
	r.s.includeDepth++

	r.sourceFile(path, file)

	r.s.includeDepth--
	r.s.intoCluster, r.s.forceContained = saveCluster, saveContained
	delete(r.ev.vars, "b:current_syntax")
	if hadSyntax {
		r.ev.vars["b:current_syntax"] = saveSyntax
	}
}
