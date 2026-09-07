// Package filetype answers one question: what kind of file is this.
//
// It exists because the vimrc is full of filetype-conditional autocommands and
// none of them can fire without an answer. Eight of its lines are FileType
// autocommands and three more set 'filetype' by hand, so an editor that never
// sets the option runs a third of the config as dead code: no 'expandtab' on
// yaml, no 'smartindent' and no python 'cinwords', no spell on markdown, and
// the go rule that wants noexpandtab ts=4 sw=4 firing on BufRead alone.
//
// What this package is NOT is the other half of "filetype plugin indent on".
// Vim answers with a filetype and then sources an ftplugin and an indent script
// per type, which is where python gets ts=4 sw=4 sts=4 and yaml gets sw=2.
// Both of those are out of scope here: 'autoindent' plus the vimrc's own
// 'smartindent cinwords' for python is the entire indent engine.
// So this package names the type and stops, and every option a type ends up
// with comes from the vimrc's own FileType autocommands. Measured against
// /opt/homebrew/bin/vim 9.2.0321, that difference is visible on
// exactly the types whose ftplugin sets an indent option and invisible on the
// rest; see filetype_test.go, which holds vim's answers as a table.
//
// Nothing here compiles a regexp. Vim's own detection is 1,671 lines of
// autocommand patterns and a few dozen vimscript functions that read the first
// lines of the file, and all of it is glob matching and prefix tests, so
// internal/regex stays the only door to the standard library's regexp and this
// package stays a leaf that imports nothing in this module.
//
// Everything in here was read out of vim's installed runtime rather than
// invented. The three sources, all of vim 9.2.0321:
//
// - runtime/autoload/dist/ft.vim, the ft_from_name and ft_from_ext
// dictionaries, ported whole into name.go and ext.go.
// - runtime/filetype.vim, the explicit autocommands, a cited selection of
// which is in patterns.go.
// - runtime/autoload/dist/script.vim, the content sniffing that runs when
// the name has told vim nothing, in content.go.
//
// The runtime is inside MacVim.app on this machine, not in
// /opt/homebrew/share/vim/vim92, which holds only "vimfiles".
package filetype

import (
	"path/filepath"
	"strings"
)

// Sniff is how many bytes of a file this package will look at. Vim's content
// checks read the first five lines and its FThtml reads forty; a few kilobytes
// covers both and means a caller opening a 40k-line log never hands over more
// than one read of it.
const Sniff = 8192

// Detect names the filetype of a file, or returns "" when nothing recognises
// it.
//
// path is the file's name as the editor knows it, which may be relative: the
// patterns that care about a directory are matched against whatever is here,
// the way vim matches its autocommand patterns against <afile>. head is the
// first bytes of the file, up to Sniff of them, and may be nil for a buffer
// with nothing in it yet -- a :edit of a file that does not exist is BufNewFile
// and vim runs the same detection over an empty buffer.
//
// The order is vim's, and it is the whole of what makes this correct rather
// than approximately correct. filetype.vim registers its autocommands in one
// augroup and vim runs them in registration order, with "setf" refusing to
// overwrite a filetype something earlier already set. Line 65 of that file is
// the DetectFromName() call, line 1329 is the DetectFromExt() call, and the
// 1,264 explicit patterns are between them. scripts.vim, the content sniffing,
// runs after all of it and only when nothing has answered.
//
// So: name, then patterns, then extension, then contents, then the two
// fallbacks vim puts deliberately last.
func Detect(path string, head []byte) string {
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}

	// filetype.vim line 65.
	if ft := byName[base]; ft != "" {
		return ft
	}
	// filetype.vim lines 66 to 1328, the cited selection in patterns.go.
	if ft := matchPatterns(path, base, head); ft != "" {
		return ft
	}
	// filetype.vim line 1329.
	if ft := byExt[extOf(base)]; ft != "" {
		return ft
	}
	// filetype.vim line 1344: "runtime! scripts.vim", guarded on nothing
	// having answered yet.
	if ft := fromContent(head); ft != "" {
		return ft
	}
	// filetype.vim line 1353, with its own comment saying why it is here:
	// "Plain text files, needs to be far down to not override others."
	if ft := matchLate(path, base, head); ft != "" {
		return ft
	}
	return ""
}

// extOf is vim's ':e' file-name modifier, plus the fallback DetectFromExt
// makes for a dotfile.
//
// Vim's version, from dist#ft#DetectFromExt:
//
//	var ext = fnamemodify(amatch, ':e')
//	const name = fnamemodify(amatch, ':t')
//	if ext == '' && name[0] == '.'
//	 ext = name[1 : ]
//	endif
//
// which is why ".bashrc" is looked up under the extension "bashrc" and not
// under nothing. Not filepath.Ext, which answers ".bashrc" for that name and
// would then have to be trimmed and special-cased into the same shape.
func extOf(base string) string {
	i := strings.LastIndex(base, ".")
	switch {
	case i < 0:
		return "" // no dot at all: "Makefile"
	case i == 0:
		return base[1:] // a leading dot and no other: ".bashrc"
	default:
		return base[i+1:] // "x.tar.gz" is "gz", as ':e' has it
	}
}

// firstLines splits the sniffed bytes into the first n lines, which is how
// every content check in vim is written: getline(1) through getline(5), and
// getline(n) up to forty for FThtml.
//
// A line ending in "\r" keeps it. That is vim's behaviour before 'fileformat'
// has been worked out, and it matters in the other direction: a CRLF shell
// script's first line is "#!/bin/sh\r", and trimming here would be this
// package deciding a question the buffer has not been asked yet. The checks
// that could care about it -- the shebang one -- match a prefix and a word, not
// a whole line, so they are right either way.
func firstLines(head []byte, n int) []string {
	if len(head) == 0 {
		return nil
	}
	out := strings.SplitN(string(head), "\n", n+1)
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// line returns the nth line of the sniffed bytes, one-based, empty when the
// file is shorter. It is getline(), and it answers "" past the end exactly as
// getline() does, which is what lets the content checks be written as flat
// comparisons with no bounds tests in them.
func line(head []byte, n int) string {
	lines := firstLines(head, n)
	if len(lines) < n {
		return ""
	}
	return lines[n-1]
}
