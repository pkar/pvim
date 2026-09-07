package x11

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkar/pvim/internal/raster"
)

// The font, found by name on a filesystem rather than asked of a font server.
//
// internal/raster's FindFontFile searches the three macOS font directories and
// short-circuits Monaco to /System/Library/Fonts/Monaco.ttf, which is correct
// there and useless here. This is the Linux half of the same question, and it
// lives in this package rather than in internal/raster because internal/raster
// is where the golden hashes are: its font lookup is pinned to one file whose
// sha256 a test asserts, and a second platform's search path in the same
// function is a second thing that can move those goldens.
//
// It is a filename match and not a font-database query, for the same reason
// internal/raster gives: the database is fontconfig and fontconfig is C. So a
// family whose file is named after it resolves, and one whose file is named
// something else does not and says so. On a Debian box that covers everything
// under /usr/share/fonts/truetype, which is where the names people actually put
// in 'guifont' live.
//
// # Why the fallback exists at all
//
// The vimrc says `set guifont=Monaco:h13`. Monaco is a macOS system font, it is
// not on a Linux box, and the vimrc is not going to change, because it is the
// same file MacVim reads and the whole design of this editor is that it stays
// that way. So a Linux window that refused to open because it could not find
// Monaco would be an editor that cannot start on its own configuration. It
// falls back instead, to DejaVu Sans Mono, which is the monospace face on every
// Debian, Ubuntu, Fedora and Arch install that has any font at all, and the
// caller is told what it got so `:set guifont?` can print the truth rather than
// the request.

// FallbackFamily is what a 'guifont' this box does not have resolves to.
//
// DejaVu Sans Mono and not Liberation Mono or Noto Sans Mono: it is in
// fonts-dejavu-core, which every one of the four distributions above installs
// as a dependency of something in a default desktop, and its metrics are close
// enough to Monaco's that a window sized for one is not absurd in the other.
const FallbackFamily = "DejaVu Sans Mono"

// fallbackChain is tried in order when the requested family is not on the box.
// It is a chain rather than one name because a container or a minimal install
// may have exactly one monospace font and it may not be the first choice.
var fallbackChain = []string{
	FallbackFamily,
	"Liberation Mono",
	"Noto Sans Mono",
	"DejaVu Sans Mono Book",
	"FreeMono",
	"Nimbus Mono PS",
	"Ubuntu Mono",
}

// fontRoots are searched, in order, for a family named in a guifont string.
//
// The user's directories come first so a font dropped in ~/.fonts wins over a
// packaged one with the same name, which is the order fontconfig itself uses
// and the order anybody who put a file there expects. ~/.fonts is the old name
// and ~/.local/share/fonts the XDG one; both are live on a box and
// fontconfig reads both, so both are here.
func fontRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	var roots []string
	if home != "" {
		roots = append(roots,
			filepath.Join(home, ".fonts"),
			filepath.Join(home, ".local", "share", "fonts"),
		)
	}
	return append(roots,
		"/usr/local/share/fonts",
		"/usr/share/fonts",
	)
}

// fontExts are the file types x/image/font/sfnt can parse. A .pfb or a .pcf in
// the same directory is skipped rather than parsed and rejected, which keeps
// the error message about the font that was asked for.
var fontExts = map[string]bool{".ttf": true, ".ttc": true, ".otf": true, ".otc": true}

// FindFontFile returns the path of the file holding the named family, searching
// the Linux font directories.
func FindFontFile(family string) (string, error) {
	return findFontIn(fontRoots(), family)
}

// findFontIn is FindFontFile over an explicit set of roots, which is what makes
// it testable: a test builds a directory of empty files with the names a real
// box has and asks which one wins, with no font on the machine involved.
//
// The roots are searched in order and the whole of each is walked, because
// /usr/share/fonts is a tree -- truetype/dejavu/DejaVuSansMono.ttf on Debian,
// dejavu-sans-mono-fonts/DejaVuSansMono.ttf on Fedora -- and nothing says where
// in it a family will be. Within one root the best match wins rather than the
// first, so DejaVuSansMono.ttf beats DejaVuSansMono-Bold.ttf however the
// directory happens to be ordered.
func findFontIn(roots []string, family string) (string, error) {
	want := normalizeFamily(family)
	if want == "" {
		return "", fmt.Errorf("x11: no font family to look for")
	}
	for _, root := range roots {
		best, rank := "", 0
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// An unreadable directory is skipped, not fatal. /usr/share/
				// fonts on a box with a broken package is exactly this, and an
				// editor that refuses to start over it would be absurd.
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if !fontExts[ext] {
				return nil
			}
			r := matchRank(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), want)
			if r == 0 {
				return nil
			}
			if best == "" || r < rank || (r == rank && path < best) {
				best, rank = path, r
			}
			return nil
		})
		if best != "" {
			return best, nil
		}
	}
	return "", fmt.Errorf("x11: no file for font family %q in %s", family, strings.Join(roots, ", "))
}

// matchRank scores a font file's base name against a normalised family name.
// Zero is no match; lower is better.
//
// Three ranks and no fuzzy matching. An exact name is what a file installed by
// hand looks like; the "-Regular" and "Book" suffixes are what a packaged
// family looks like, and DejaVu in particular ships its plain weight as
// DejaVuSansMono.ttf on Debian and as DejaVuSansMono-Book on nothing at all,
// which is why Book is in fallbackChain as a family instead. Anything else --
// Bold, Oblique, a numbered weight -- does not match, because a 'guifont' with
// no style in it asking for the bold file is worse than not finding it.
func matchRank(base, want string) int {
	got := normalizeFamily(base)
	switch {
	case got == want:
		return 1
	case got == want+"regular":
		return 2
	case got == want+"book":
		return 3
	}
	return 0
}

// normalizeFamily folds a family name or a file name to the form the two are
// compared in: lowercase with the separators dropped, so "DejaVu Sans Mono",
// "DejaVuSansMono" and "dejavu-sans-mono" are one name.
func normalizeFamily(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '-', '_', '.':
			continue
		}
		b.WriteRune(unicodeLower(r))
	}
	return b.String()
}

// unicodeLower is ASCII lowercasing, which is all a font file name needs and
// all a family name in a vimrc has ever been. A full unicode.ToLower would fold
// a Turkish dotted I differently depending on nothing this cares about.
func unicodeLower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

// ResolveFont finds a face for g, falling back when this box does not have the
// family g names.
//
// It returns the face, the GUIFont actually in force and an error. The second
// return is the point of the function: the vimrc asks for Monaco, this box has
// DejaVu Sans Mono, and `:set guifont?` has to print what is on the screen
// rather than what was requested, exactly as vim does when a font load fails
// over to another entry in a comma-separated 'guifont'.
//
// The size is never changed by the fallback. A 13-point request stays 13 points
// in whatever face answers it, because the height is the thing the person
// actually chose and the family is the thing they could not have known.
func ResolveFont(g raster.GUIFont, scale int) (raster.Face, raster.GUIFont, error) {
	tried := []string{g.Family}
	if path, err := FindFontFile(g.Family); err == nil {
		if face, err := faceFrom(path, g, scale); err == nil {
			return face, g, nil
		}
	}
	for _, family := range fallbackChain {
		if family == g.Family {
			continue
		}
		tried = append(tried, family)
		path, err := FindFontFile(family)
		if err != nil {
			continue
		}
		sub := g
		sub.Family = family
		if face, err := faceFrom(path, sub, scale); err == nil {
			return face, sub, nil
		}
	}
	return nil, raster.GUIFont{}, fmt.Errorf("x11: no usable font on this box, tried %s in %s",
		strings.Join(tried, ", "), strings.Join(fontRoots(), ", "))
}

// faceFrom parses a font file and builds a face at g's size and the given
// scale.
func faceFrom(path string, g raster.GUIFont, scale int) (raster.Face, error) {
	f, err := raster.LoadFont(path)
	if err != nil {
		return nil, err
	}
	return f.Face(g.Size, scale)
}
