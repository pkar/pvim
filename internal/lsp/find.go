package lsp

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Where the server comes from, what it is rooted at, and how a path becomes a
// URI.

// BinDir is where the vimrc says the Go tools live.
//
//	let g:go_bin_path=expand('~/.vimgo')
//
// vim-go puts that directory on the front of $PATH before it starts anything,
// so the gopls this editor talks to has to be the same binary vim-go was
// talking to or the two disagree about which analysers run and which
// version of the module cache is warm. On this machine that is
// ~/.vimgo/gopls, golang.org/x/tools/gopls v0.21.0, installed,
// alongside dlv, godef, errcheck and five more; there is no gopls on $PATH at
// all, so a PATH-first lookup would find nothing and the language server
// would look unimplementable on the one machine it is for.
const BinDir = "~/.vimgo"

// ErrNoServer is what Find answers when there is no gopls anywhere. It is a
// sentinel so that cmd/pvim can say one sentence naming both places it looked
// rather than starting an editor that silently has no completion.
var ErrNoServer = errors.New("lsp: no language server found")

// Find locates a server binary: g:go_bin_path first, then $PATH.
//
// home is the user's home directory, taken as an argument rather than read
// from the environment so that a test can point it at a temporary directory
// with a fake gopls in it and get a deterministic answer. Empty means skip the
// bin directory and look only on $PATH.
func Find(home, name string) (string, error) {
	if home != "" {
		p := filepath.Join(home, strings.TrimPrefix(BinDir, "~/"), name)
		if executable(p) {
			return p, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", ErrNoServer
}

// executable reports whether p is a regular file this process may execute.
func executable(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}

// rootMarkers are the directory entries that mean "this is the top of a
// project", in the order they are looked for.
//
// ".git" is the marker and ctrlp's, and it is first for the same reason
// in both: it is the only one that is right for a repository holding several
// modules. "go.mod" is second and only reached when there is no .git above the
// file at all -- a module unpacked into a temporary directory, which is what
// most of this package's own tests are -- because a gopls rooted at a
// directory with no module in it answers every completion with nothing and
// says why in a window/showMessage nobody is reading.
var rootMarkers = []string{".git", "go.mod"}

// Root is the workspace root for a file: the nearest ancestor directory
// holding a root marker, or the file's own directory when there is none.
//
// It walks the file's own path and NOT the working directory, and that is the
// whole point of it. The vimrc sets 'autochdir', so getcwd() follows the
// buffer: with a cwd-rooted server, opening internal/gui/app.go and then
// internal/text/buffer.go starts two workspaces, each of which loads the whole
// module from scratch, and the second completion in a session takes eight
// seconds instead of eighty milliseconds. That is the hard part, and this
// function is the answer to it; internal/finder wants the same walk for the
// same reason, and the two should end up one function in an internal/project
// rather than two that agree.
//
// A .git that is a FILE and not a directory is still a marker: that is what a
// git worktree and a submodule leave behind, and a real checkout made
// with "git worktree add" would otherwise be rooted at the filesystem root.
func Root(path string) string {
	dir := path
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		dir = filepath.Dir(path)
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for _, marker := range rootMarkers {
		if found := walkUp(dir, marker); found != "" {
			return found
		}
	}
	return dir
}

// walkUp returns the nearest ancestor of dir, dir itself included, holding an
// entry called marker, or "".
func walkUp(dir, marker string) string {
	for {
		if _, err := os.Lstat(filepath.Join(dir, marker)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// URI turns an absolute path into a file: URI.
//
// Through net/url and not through string concatenation, because a path with a
// space, a "#" or a "%" in it has to be percent-encoded and gopls compares
// URIs as strings. "file://" plus a path is right for every directory in this
// repository and wrong for "~/Library/Application Support", which is the kind
// of thing that works until the day somebody edits a file under it.
func URI(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	u := url.URL{Scheme: "file", Path: path}
	return u.String()
}

// Path is URI backwards. A URI that is not a file: URI, or that does not
// parse, comes back as the empty string rather than as something that looks
// like a path, because a caller that opened it would create a file with a
// percent sign in its name.
func Path(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return u.Path
}

// LanguageID is the protocol's name for a filetype, which is nearly but not
// quite vim's 'filetype'.
//
// The three that matter here: vim calls a Go file "go" and so does the
// protocol; vim calls a Terraform file "terraform" and the protocol calls it
// "terraform" too; and vim's "gomod" is the protocol's "go.mod", which gopls
// insists on and which is the one rename in the table. Anything not named goes
// through unchanged, which is what the protocol says to do with an identifier
// it does not know.
func LanguageID(filetype string) string {
	switch filetype {
	case "gomod":
		return "go.mod"
	case "gosum":
		return "go.sum"
	case "gowork":
		return "go.work"
	default:
		return filetype
	}
}
