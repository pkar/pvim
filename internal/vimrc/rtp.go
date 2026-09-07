package vimrc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The 'runtimepath' family.
//
// # Why these three options are here and not in internal/options
//
// They are options, they belong in internal/options, and they are not there:
// that package has no row for 'runtimepath', 'packpath' or 'viminfofile', and
// it is owned by somebody else this run. Until it has three rows, a ":set
// runtimepath^=..." reaching options.ApplyLine is E518 and the live vimrc
// fails on its own line 6. So the loaded configuration holds them, both sinks
// route the three names here before the option table sees them, and the day
// internal/options grows the rows this file becomes a delete and three
// forwarding lines. Nothing above this reads them through Options, so nothing
// above this has to change back.
//
// # What pvim's 'runtimepath' is for
//
// One thing: finding "colors/NAME.vim" for ":colorscheme". pvim sources no
// plugin, no ftplugin, no indent script, no syntax file and no autoload, all
// of which is deliberately out of scope rather than an omission, so the
// other nine things vim's 'runtimepath' does have nothing here to do.
//
// # Where it differs from vim
//
// The default. Vim's is
//
//	~/.vim,$VIM/vimfiles,$VIMRUNTIME,$VIM/vimfiles/after,~/.vim/after
//
// and three of those five name the installed vim runtime, which pvim does not
// have and will not ship: there is no $VIMRUNTIME to point at, and inventing
// one that holds nothing would put three directories on the path so that
// ":set runtimepath?" could print a longer answer. So pvim's default is the
// two entries that are the user's, and after the live vimrc has run, vim's
// answer and pvim's agree on the first six entries -- the vim home and the
// four packages and ~/.vim -- and differ by the three MacVim directories on
// the end. Measured with "vim -u ~/.vimrc".

// pathOptions is the three option names this file owns, each with the
// abbreviation vim accepts for it. A ":set" item naming any of them is routed
// here and the rest go to internal/options.
var pathOptions = map[string]string{
	"runtimepath": "runtimepath", "rtp": "runtimepath",
	"packpath": "packpath", "pp": "packpath",
	"viminfofile": "viminfofile", "vif": "viminfofile",
}

// RTP is the value of the three options, and the ":packloadall" that changes
// the first of them.
type RTP struct {
	// RuntimePath is 'runtimepath'.
	RuntimePath string
	// PackPath is 'packpath'.
	PackPath string
	// ViminfoFile is 'viminfofile'. Nothing reads it yet: the history and
	// marks file is later work and pvim's own format, not vim's. It is here
	// because the live vimrc sets it and a ":set" of an option pvim silently
	// discards is worse than either running it or refusing it.
	ViminfoFile string
}

// NewRTP returns the three options at pvim's defaults. See the file comment for
// how the 'runtimepath' default differs from vim's and why.
func NewRTP() RTP {
	home, err := os.UserHomeDir()
	if err != nil {
		return RTP{}
	}
	vimdir := filepath.Join(home, ".vim")
	return RTP{
		RuntimePath: vimdir + "," + filepath.Join(vimdir, "after"),
		PackPath:    vimdir,
	}
}

// SetItem applies one ":set" item and reports whether it named one of the
// three.
//
// The four assignment forms vim has for a comma-list option are here, because
// the live vimrc uses two of them on consecutive lines: "^=" prepends on line
// 6 and "=" replaces on line 7. A bare name and a "?" are a query, which at
// startup has nowhere to print to and so changes nothing.
func (r *RTP) SetItem(item string) (bool, error) {
	name, rest, _ := cutOptionName(item)
	full, ok := pathOptions[name]
	if !ok {
		return false, nil
	}

	field := &r.RuntimePath
	switch full {
	case "packpath":
		field = &r.PackPath
	case "viminfofile":
		field = &r.ViminfoFile
	}

	op, value, assigned := cutOptionOp(rest)
	if !assigned {
		// A bare name, a "?", a "!" or a "&". None of them is an assignment
		// and none of the three is a boolean, so there is nothing to toggle
		// and nothing to print at startup.
		return true, nil
	}
	value = unescapeSetValue(value)

	switch op {
	case "=", ":":
		*field = value
	case "+=":
		*field = appendPathEntry(*field, value)
	case "^=":
		*field = prependPathEntry(*field, value)
	case "-=":
		*field = removePathEntry(*field, value)
	default:
		return true, fmt.Errorf("E488: Trailing characters: %s", item)
	}
	return true, nil
}

// cutOptionName takes the option name off the front of a ":set" item.
func cutOptionName(item string) (name, rest string, ok bool) {
	i := 0
	for i < len(item) && item[i] >= 'a' && item[i] <= 'z' {
		i++
	}
	return item[:i], item[i:], i > 0
}

// cutOptionOp reads the assignment operator and the value after an option
// name, and reports whether there was one.
func cutOptionOp(rest string) (op, value string, ok bool) {
	for _, cand := range []string{"+=", "^=", "-=", "=", ":"} {
		if strings.HasPrefix(rest, cand) {
			return cand, rest[len(cand):], true
		}
	}
	return "", "", false
}

// unescapeSetValue drops the backslashes vim's ":set" uses to protect a space,
// a comma or a backslash inside a value. It is what makes fnameescape's output
// arrive as the path it started as.
func unescapeSetValue(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// pathEntries splits a comma list, dropping the empty entries a "^=" onto an
// empty option would otherwise leave.
func pathEntries(s string) []string {
	var out []string
	for _, e := range strings.Split(s, ",") {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

func appendPathEntry(list, value string) string {
	if list == "" {
		return value
	}
	return list + "," + value
}

func prependPathEntry(list, value string) string {
	if list == "" {
		return value
	}
	return value + "," + list
}

func removePathEntry(list, value string) string {
	var keep []string
	for _, e := range pathEntries(list) {
		if e != value {
			keep = append(keep, e)
		}
	}
	return strings.Join(keep, ",")
}

// LoadAll is ":packloadall": put every "pack/*/start/*" directory under
// 'packpath' onto 'runtimepath'. It returns the directories it added, in the
// order it added them, so a caller can say what happened.
//
// It sources nothing. See the Pack statement for why that is deliberate and
// not a stub.
//
// The insertion point is vim's and it was measured rather than guessed. Vim's
// add_pack_dir_to_rtp puts each package directory immediately after the
// 'runtimepath' entry it lives under, so four packages under one packpath
// entry all land at the same index and come out in the reverse of the order
// they were found. Measured with "vim -u
// ~/.vimrc" and ":echo &runtimepath": the vim
// home, then vim-terraform, vim-go, nofrils, nerdtree -- reverse alphabetical
// -- then ~/.vim. Appending them instead would have put them after ~/.vim,
// where a colours file in ~/.vim would win over a package's, which is the
// opposite of what vim does.
//
// The "after" half of vim's version is left out: vim also appends
// "pack/*/start/*/after" for the ones that exist, and none of the four
// packages on this machine has one. It goes in the day a package does, and
// until then it is a directory list nobody can produce.
func (r *RTP) LoadAll() []string {
	var added []string
	for _, root := range pathEntries(r.PackPath) {
		dirs, err := filepath.Glob(filepath.Join(root, "pack", "*", "start", "*"))
		if err != nil {
			// The only error Glob returns is a malformed pattern, and the
			// pattern is a constant with a path joined into it.
			continue
		}
		for _, dir := range dirs {
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				continue
			}
			if r.has(dir) {
				continue
			}
			r.insertAfter(root, dir)
			added = append(added, dir)
		}
	}
	return added
}

// has reports whether a directory is already on 'runtimepath'.
func (r *RTP) has(dir string) bool {
	for _, e := range pathEntries(r.RuntimePath) {
		if e == dir {
			return true
		}
	}
	return false
}

// insertAfter puts dir on 'runtimepath' immediately after the entry it lives
// under, or at the front when that entry is not on the path at all.
func (r *RTP) insertAfter(root, dir string) {
	entries := pathEntries(r.RuntimePath)
	at := 0
	for i, e := range entries {
		if e == root {
			at = i + 1
			break
		}
	}
	out := make([]string, 0, len(entries)+1)
	out = append(out, entries[:at]...)
	out = append(out, dir)
	out = append(out, entries[at:]...)
	r.RuntimePath = strings.Join(out, ",")
}

// Find looks a relative path up down 'runtimepath' and returns the first file
// that is there, which is what vim's ":runtime" does and what ":colorscheme
// NAME" is spelt as: "runtime! colors/NAME.vim".
func (r *RTP) Find(rel string) (string, bool) {
	for _, dir := range pathEntries(r.RuntimePath) {
		path := filepath.Join(dir, rel)
		if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
			return path, true
		}
	}
	return "", false
}

// splitSetArgs splits a ":set" argument list the way vim does: on white space
// that is not preceded by a backslash, so that a value with an escaped space
// in it stays one item.
//
// It is here rather than in internal/options because the caller has to route
// each item to one of two places before either of them parses it, and asking
// the option table to split a line it is then not going to be given is worse
// than twelve lines.
func splitSetArgs(args string) []string {
	var out []string
	var b strings.Builder
	for i := 0; i < len(args); i++ {
		switch c := args[i]; {
		case c == '\\' && i+1 < len(args):
			b.WriteByte(c)
			i++
			b.WriteByte(args[i])
		case c == ' ' || c == '\t':
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteByte(c)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// splitPathOptions sorts a ":set" argument list into the items this file owns
// and the items internal/options owns, keeping the second half in its original
// spelling so the option table sees exactly what was typed.
func splitPathOptions(args string) (paths []string, rest string) {
	var keep []string
	for _, item := range splitSetArgs(args) {
		name, _, _ := cutOptionName(item)
		if _, ok := pathOptions[name]; ok {
			paths = append(paths, item)
			continue
		}
		keep = append(keep, item)
	}
	return paths, strings.Join(keep, " ")
}

// ApplySet routes a whole ":set" argument list: the 'runtimepath' family here,
// everything else back to the caller through apply.
//
// Both sinks call it, which is the point: a rule about which layer answers for
// 'runtimepath' that lived in two places would be answered two ways the first
// time one of them changed.
func (r *RTP) ApplySet(args string, apply func(string) error) error {
	paths, rest := splitPathOptions(args)
	var firstErr error
	for _, item := range paths {
		if _, err := r.SetItem(item); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if rest != "" {
		if err := apply(rest); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
