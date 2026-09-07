package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Finding and loading a colourscheme.
//
// ":colorscheme nofrils-dark" in vim is "runtime! colors/nofrils-dark.vim":
// every directory in 'runtimepath' is tried in order and the first file found
// is sourced. pvim has no 'runtimepath' option -- nothing else here needs one,
// and inventing a comma list nobody sets is a config surface with no
// config behind it -- so the search is a fixed list built the same way
// internal/ex/help.go builds its doc-directory list: the places this vimrc's
// scheme can actually be, user directories before the installed runtime, with
// globs for the parts that carry a version or a package name.
//
// The one that matters is the second entry. The scheme this vimrc asks for
// lives at
//
//	~/.vim/pack/pkar/start/nofrils/colors/nofrils-dark.vim
//
// which is a package and not a plain 'runtimepath' entry: vim adds pack/*/start/*
// to 'runtimepath' during startup, which is why "colorscheme nofrils-dark" on
// line 5 of the vimrc finds it. A search that only looked at ~/.vim/colors
// would answer E185 for the one scheme this editor has to load.
//
// Measured against /opt/homebrew/bin/vim 9.2.0321: with the real vimrc and no
// plugins loaded, ":hi Normal" prints
//
//	Normal xxx ctermfg=255 ctermbg=235 guifg=#eeeeee guibg=#262626
//
// and the message log is empty, so the search finding this file is the whole
// difference between that and E185.

// colorschemePaths is where a "colors/NAME.vim" is looked for, in order. Each
// entry is a glob relative to nothing: "~" is expanded, and a pattern with no
// match contributes nothing.
var colorschemePaths = []string{
	"~/.vim/colors/%s.vim",
	"~/.vim/pack/*/start/*/colors/%s.vim",
	"~/.vim/pack/*/opt/*/colors/%s.vim",
	"/opt/homebrew/share/vim/vim*/colors/%s.vim",
	"/opt/homebrew/Cellar/macvim/*/MacVim.app/Contents/Resources/vim/runtime/colors/%s.vim",
	"/usr/local/share/vim/vim*/colors/%s.vim",
	"/usr/share/vim/vim*/colors/%s.vim",
}

// findColorscheme returns the file a ":colorscheme name" would source.
//
// A name with a path separator or a ".." in it is refused rather than joined,
// because the name comes out of a config file and a scheme called "../../etc"
// reading a file outside the search list is not a feature.
func findColorscheme(name string) (string, bool) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return "", false
	}
	for _, pat := range colorschemePaths {
		matches, err := filepath.Glob(expandTilde(fmt.Sprintf(pat, name)))
		if err != nil {
			// The only error Glob returns is a malformed pattern, which is a
			// bug in the list above and not something a caller can act on.
			continue
		}
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && fi.Mode().IsRegular() {
				return m, true
			}
		}
	}
	return "", false
}

// expandTilde turns a leading "~/" into the home directory. Nothing else in a
// glob needs expanding and "~user" is not a form this list uses.
func expandTilde(p string) string {
	if len(p) < 2 || p[0] != '~' || p[1] != '/' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}
