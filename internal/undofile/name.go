package undofile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The two suffixes. See the package comment for why the swap file shares vim's
// name and the undo file does not.
const (
	swapSuffix = ".swp"
	undoSuffix = ".pvundo"
)

// ErrNoDir is a 'directory' or 'undodir' with nothing usable in it. Vim answers
// E303 for the swap case and carries on without one; the caller decides.
var ErrNoDir = errors.New("no usable directory in the list")

// Dirs splits an option value in the shape of 'directory' or 'undodir' into its
// entries, with "~" expanded against home.
//
// Vim's list separator is a comma and a backslash escapes one, which is how a
// directory with a comma in its name is written. Empty entries are dropped: the
// vimrc builds these with :execute and a stray comma from a fnameescape is a
// typo, not a request to write the swap file into the current directory.
func Dirs(list, home string) []string {
	var out []string
	var cur strings.Builder
	for i := 0; i < len(list); i++ {
		switch c := list[i]; c {
		case '\\':
			if i+1 < len(list) {
				i++
				cur.WriteByte(list[i])
			}
		case ',':
			out = appendDir(out, cur.String(), home)
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return appendDir(out, cur.String(), home)
}

func appendDir(out []string, entry, home string) []string {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return out
	}
	if home != "" {
		switch {
		case entry == "~":
			entry = home
		case strings.HasPrefix(entry, "~/"):
			entry = filepath.Join(home, entry[2:]) + trailingSlashes(entry)
		}
	}
	return append(out, entry)
}

// trailingSlashes returns the run of separators at the end of a path, which
// filepath.Join eats and which is the whole of what "//" means here.
func trailingSlashes(p string) string {
	i := len(p)
	for i > 0 && os.IsPathSeparator(p[i-1]) {
		i--
	}
	return p[i:]
}

// SwapName is where the swap file for file goes, given one entry of
// 'directory'.
//
// Vim's three shapes, measured against vim 9.2.0321 rather than
// read out of the documentation, each with a file /tmp/.../work/f.txt:
//
//	directory=. -> <the file's own directory>/.f.txt.swp
//	directory=/some/dir -> /some/dir/f.txt.swp
//	directory=/some/dir// -> /some/dir/%private%tmp%...%work%f.txt.swp
//
// The third is the one the vimrc asks for and the one that matters. A trailing
// "//" means "encode the whole path in the name", so two files called main.go
// in two repositories get two swap files instead of one that each steals from
// the other. Vim builds that name in make_percent_swname(): resolve the path,
// replace every separator with '%', drop one trailing slash from the directory
// and join. This does the same, and TestSwapNameMatchesVim pins it against the
// name vim wrote.
//
// The path is resolved through symlinks first, as vim's fix_fname does, which
// is why /tmp/x on macOS produces %private%tmp%x: two names for one file must
// not become two swap files, or a crash in one leaves a warning the other never
// sees.
func SwapName(dir, file string) (string, error) {
	return nameIn(dir, file, swapSuffix, true)
}

// UndoName is where the undo history for file goes, given one entry of
// 'undodir'.
//
// The rule is vim's for 'undodir' and not for 'directory', and they differ: a
// 'undodir' entry other than "." always encodes the full path, whether or not
// it ends in "//", because ":help 'undodir'" says the undo file name is the
// file name with all path separators replaced by '%' and offers no second
// shape. The vimrc's trailing "//" is therefore decoration there, and honoured
// either way.
//
// The suffix is ".pvundo" and vim's is nothing at all. See the package comment.
func UndoName(dir, file string) (string, error) {
	return nameIn(dir, file, undoSuffix, false)
}

// nameIn is the body of both. percentOnlyWithSlashes is what separates
// 'directory' from 'undodir': the swap file encodes the path only when the
// entry ends in "//" and the undo file always does.
func nameIn(dir, file, suffix string, percentOnlyWithSlashes bool) (string, error) {
	if dir == "" {
		return "", ErrNoDir
	}
	full, err := resolve(file)
	if err != nil {
		return "", err
	}
	if dir == "." {
		// Beside the file, hidden, which is what vim does and what
		// 'directory' defaults to in front of the three /tmp entries.
		return filepath.Join(filepath.Dir(full), "."+filepath.Base(full)+suffix), nil
	}
	slashes := trailingSlashes(dir)
	base := strings.TrimRight(dir, string(os.PathSeparator))
	if base == "" {
		base = string(os.PathSeparator)
	}
	if percentOnlyWithSlashes && len(slashes) < 2 {
		return filepath.Join(base, filepath.Base(full)+suffix), nil
	}
	return filepath.Join(base, percentPath(full)+suffix), nil
}

// percentPath is vim's make_percent_swname: every path separator becomes '%'.
//
// A '%' already in the file's name is left alone, exactly as vim leaves it. The
// encoding is therefore not reversible -- /a%b/c and /a/b/c collide -- and vim
// has lived with that since 1994. Reversibility is not what the name is for:
// the swap file's header carries the real path, and the name only has to be
// unique enough in practice and recognisable in an ls.
func percentPath(full string) string {
	var b strings.Builder
	b.Grow(len(full))
	for i := 0; i < len(full); i++ {
		if os.IsPathSeparator(full[i]) {
			b.WriteByte('%')
			continue
		}
		b.WriteByte(full[i])
	}
	return b.String()
}

// resolve is vim's fix_fname: absolute, cleaned, and through any symlink.
//
// A file that does not exist yet cannot be resolved, and that is not an error:
// ":e newfile" gets a swap file too. The directories above it are resolved
// instead, so /tmp/new.txt and /private/tmp/new.txt still agree.
func resolve(file string) (string, error) {
	if file == "" {
		return "", errors.New("no file name")
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	dir, base := filepath.Split(abs)
	if resolved, err := filepath.EvalSymlinks(filepath.Clean(dir)); err == nil {
		return filepath.Join(resolved, base), nil
	}
	return abs, nil
}
