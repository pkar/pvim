package x11

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The font search, checked against a directory tree built to look like the ones
// on a real box. There is no Linux machine here and no /usr/share/fonts, so the
// only honest way to test the search is to build the shapes those directories
// have and search them.

// plant makes a tree of empty files and returns the root. The files are empty
// because findFontIn never opens one: it matches names, and parsing is
// ResolveFont's job.
func plant(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFindFontInDebianLayout(t *testing.T) {
	// The tree a Debian box actually has under /usr/share/fonts.
	root := plant(t,
		"truetype/dejavu/DejaVuSans.ttf",
		"truetype/dejavu/DejaVuSansMono.ttf",
		"truetype/dejavu/DejaVuSansMono-Bold.ttf",
		"truetype/liberation/LiberationMono-Regular.ttf",
		"truetype/liberation/LiberationMono-Bold.ttf",
		"X11/misc/fixed.pcf.gz",
	)
	for _, tc := range []struct {
		family string
		want   string
	}{
		{"DejaVu Sans Mono", "truetype/dejavu/DejaVuSansMono.ttf"},
		{"DejaVuSansMono", "truetype/dejavu/DejaVuSansMono.ttf"},
		{"dejavu sans mono", "truetype/dejavu/DejaVuSansMono.ttf"},
		{"DejaVu Sans", "truetype/dejavu/DejaVuSans.ttf"},
		{"Liberation Mono", "truetype/liberation/LiberationMono-Regular.ttf"},
	} {
		got, err := findFontIn([]string{root}, tc.family)
		if err != nil {
			t.Errorf("%s: %v", tc.family, err)
			continue
		}
		if want := filepath.Join(root, tc.want); got != want {
			t.Errorf("%s resolved to %s, want %s", tc.family, got, want)
		}
	}
}

// TestFindFontDoesNotTakeABoldFile is the rank that matters. A 'guifont' with
// no style in it must not resolve to the bold file just because the bold file
// sorts first in the directory.
func TestFindFontDoesNotTakeABoldFile(t *testing.T) {
	root := plant(t, "a/DejaVuSansMono-Bold.ttf", "a/DejaVuSansMono-Oblique.ttf")
	if got, err := findFontIn([]string{root}, "DejaVu Sans Mono"); err == nil {
		t.Errorf("resolved to %s, want a refusal: only styled files are present", got)
	}
}

// TestFindFontPrefersTheUserDirectory pins the order fontconfig uses and the
// order anybody who dropped a file in ~/.fonts expects.
func TestFindFontPrefersTheUserDirectory(t *testing.T) {
	user := plant(t, "DejaVuSansMono.ttf")
	system := plant(t, "truetype/dejavu/DejaVuSansMono.ttf")
	got, err := findFontIn([]string{user, system}, "DejaVu Sans Mono")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(user, "DejaVuSansMono.ttf"); got != want {
		t.Errorf("resolved to %s, want the user's copy at %s", got, want)
	}
}

// TestFindFontPrefersTheExactName keeps an exact filename ahead of a -Regular
// one within the same root, whatever order the walk happens to see them in.
func TestFindFontPrefersTheExactName(t *testing.T) {
	root := plant(t, "z/DejaVuSansMono.ttf", "a/DejaVuSansMono-Regular.ttf")
	got, err := findFontIn([]string{root}, "DejaVu Sans Mono")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "z", "DejaVuSansMono.ttf"); got != want {
		t.Errorf("resolved to %s, want %s", got, want)
	}
}

func TestFindFontSkipsFormatsWeCannotParse(t *testing.T) {
	root := plant(t, "misc/Monaco.pcf", "misc/Monaco.pfb", "misc/Monaco.bdf")
	if got, err := findFontIn([]string{root}, "Monaco"); err == nil {
		t.Errorf("resolved to %s, want a refusal: none of those are sfnt files", got)
	}
}

// TestFindFontSurvivesAMissingRoot is the case every Linux box hits: ~/.fonts
// does not exist on most of them, and a walk over a directory that is not there
// must be a skip and not a failure.
func TestFindFontSurvivesAMissingRoot(t *testing.T) {
	real := plant(t, "DejaVuSansMono.ttf")
	roots := []string{filepath.Join(real, "does-not-exist"), real}
	if _, err := findFontIn(roots, "DejaVu Sans Mono"); err != nil {
		t.Errorf("a missing root broke the search: %v", err)
	}
}

// TestMonacoIsNotOnALinuxBox is the whole reason for the fallback, written as a
// test so that the error message names both the family and where it looked.
func TestMonacoIsNotOnALinuxBox(t *testing.T) {
	root := plant(t, "truetype/dejavu/DejaVuSansMono.ttf")
	_, err := findFontIn([]string{root}, "Monaco")
	if err == nil {
		t.Fatal("Monaco resolved on a tree that does not have it")
	}
	if !strings.Contains(err.Error(), "Monaco") || !strings.Contains(err.Error(), root) {
		t.Errorf("the error does not say what was wanted or where it looked: %v", err)
	}
}

func TestNormalizeFamily(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"DejaVu Sans Mono", "dejavusansmono"},
		{"DejaVuSansMono", "dejavusansmono"},
		{"dejavu-sans-mono", "dejavusansmono"},
		{"Deja_Vu.Sans Mono", "dejavusansmono"},
		{"", ""},
	} {
		if got := normalizeFamily(tc.in); got != tc.want {
			t.Errorf("normalizeFamily(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFontRootsAreTheOnesFontconfigReads(t *testing.T) {
	roots := fontRoots()
	want := []string{".fonts", ".local/share/fonts", "/usr/local/share/fonts", "/usr/share/fonts"}
	if len(roots) != len(want) {
		t.Fatalf("got %d roots %v, want %d", len(roots), roots, len(want))
	}
	for i, w := range want {
		if !strings.HasSuffix(filepath.ToSlash(roots[i]), w) {
			t.Errorf("root %d is %s, want one ending in %s", i, roots[i], w)
		}
	}
}

// TestFallbackChainStartsWithDejaVu pins what by name: Monaco
// is a macOS font, the vimrc names it, and DejaVu Sans Mono is what a Linux box
// gets instead.
func TestFallbackChainStartsWithDejaVu(t *testing.T) {
	if fallbackChain[0] != FallbackFamily || FallbackFamily != "DejaVu Sans Mono" {
		t.Errorf("the fallback chain starts with %q and FallbackFamily is %q", fallbackChain[0], FallbackFamily)
	}
	for _, f := range fallbackChain {
		if normalizeFamily(f) == "" {
			t.Errorf("the fallback chain holds an empty family: %v", fallbackChain)
		}
	}
}
