package raster

import (
	"os"
	"testing"

	"golang.org/x/image/font/sfnt"
)

// TestParseGUIFont covers the one value the vimrc sets and the syntax around
// it. Vim's own `:set guifont=` accepts more than this, and every extra form
// is one this editor's vimrc has never contained.
func TestParseGUIFont(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want GUIFont
	}{
		{"Monaco:h13", GUIFont{Family: "Monaco", Size: 13}},
		{"Monaco", GUIFont{Family: "Monaco", Size: DefaultPointSize}},
		{"Monaco:h13.5", GUIFont{Family: "Monaco", Size: 13.5}},
		{"Menlo:h12:b", GUIFont{Family: "Menlo", Size: 12, Bold: true}},
		{"Menlo:h12:b:i", GUIFont{Family: "Menlo", Size: 12, Bold: true, Italic: true}},
		{`Andale\ Mono:h14`, GUIFont{Family: "Andale Mono", Size: 14}},
		// Width, underline, strikeout and charset parse and are dropped.
		{"Monaco:h13:w8:u:s:cANSI", GUIFont{Family: "Monaco", Size: 13}},
		// A comma is vim's fallback list; with no fallback chain only the first
		// entry means anything.
		{"Monaco:h13,Menlo:h13", GUIFont{Family: "Monaco", Size: 13}},
		{`Comma\,Face:h10`, GUIFont{Family: "Comma,Face", Size: 10}},
	} {
		got, err := ParseGUIFont(tc.in)
		if err != nil {
			t.Errorf("ParseGUIFont(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseGUIFont(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestParseGUIFontRejects(t *testing.T) {
	for _, in := range []string{"", ":h13", "Monaco:h", "Monaco:hxx", "Monaco:h0", "Monaco:h-3", "Monaco:zebra"} {
		if got, err := ParseGUIFont(in); err == nil {
			t.Errorf("ParseGUIFont(%q) = %+v, want an error", in, got)
		}
	}
}

// TestGUIFontRoundTrip: `:set guifont?` has to print something `:set guifont=`
// takes back.
func TestGUIFontRoundTrip(t *testing.T) {
	for _, in := range []string{"Monaco:h13", "Menlo:h12:b:i", `Andale\ Mono:h14`} {
		first, err := ParseGUIFont(in)
		if err != nil {
			t.Fatalf("ParseGUIFont(%q): %v", in, err)
		}
		second, err := ParseGUIFont(first.String())
		if err != nil {
			t.Fatalf("ParseGUIFont(%q): %v", first.String(), err)
		}
		if first != second {
			t.Errorf("%q round-tripped through %q into %+v, want %+v", in, first.String(), second, first)
		}
	}
}

// TestFindFontFile checks the family lookup finds Monaco and says so plainly
// when a family has no file named after it. There is no font database without
// CoreText and CoreText means a C compiler, so this is as good as it gets.
func TestFindFontFile(t *testing.T) {
	path, err := FindFontFile("Monaco")
	if err != nil {
		t.Fatalf("FindFontFile(Monaco): %v", err)
	}
	if path != DefaultFontPath {
		t.Errorf("FindFontFile(Monaco) = %q, want %q", path, DefaultFontPath)
	}
	if _, err := FindFontFile("No Such Family At All"); err == nil {
		t.Error("FindFontFile found a font that does not exist")
	}
}

func TestFaceRejectsNonsenseSizes(t *testing.T) {
	f, err := LoadFont(DefaultFontPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Face(13, 0); err == nil {
		t.Error("Face accepted scale 0")
	}
	if _, err := f.Face(0, 1); err == nil {
		t.Error("Face accepted a zero point size")
	}
}

func TestLoadFontReportsABadFile(t *testing.T) {
	if _, err := LoadFont("/nonexistent/Nope.ttf"); err == nil {
		t.Error("LoadFont accepted a path that does not exist")
	}
	if _, err := LoadFont("/etc/hosts"); err == nil {
		t.Error("LoadFont parsed a file that is not a font")
	}
}

// TestMonacoHasNoBoldOrItalicFace is the measurement behind synthesising both.
//
// As macOS ships it, /System/Library/Fonts/Monaco.ttf holds one
// face, family "Monaco", subfamily "Regular", version 17.0d1e5, 1678 glyphs,
// post table ItalicAngle 0, and there is no other file named Monaco anywhere in
// the three font directories FindFontFile searches. So there is nothing to load
// for `gui=bold` or `gui=italic` and the choice is between the smear and the
// shear in glyph.go or text that is styled and does not look it.
//
// If Apple ever ships a Monaco Bold, this fails and the fix is a real face
// rather than a wider one.
func TestMonacoHasNoBoldOrItalicFace(t *testing.T) {
	b, err := os.ReadFile(DefaultFontPath)
	if err != nil {
		t.Fatalf("reading %s: %v", DefaultFontPath, err)
	}
	c, err := sfnt.ParseCollection(b)
	if err != nil {
		t.Fatalf("parsing %s as a collection: %v", DefaultFontPath, err)
	}
	if n := c.NumFonts(); n != 1 {
		t.Errorf("%s holds %d faces, not 1; one of them may be the bold", DefaultFontPath, n)
	}

	f, err := sfnt.Parse(b)
	if err != nil {
		t.Fatalf("parsing %s: %v", DefaultFontPath, err)
	}
	var buf sfnt.Buffer
	if got, err := f.Name(&buf, sfnt.NameIDSubfamily); err != nil || got != "Regular" {
		t.Errorf("subfamily is %q (%v), want \"Regular\"", got, err)
	}
	if a := f.PostTable().ItalicAngle; a != 0 {
		t.Errorf("italic angle is %v; the face is already oblique and shear() would double it", a)
	}

	for _, name := range []string{"Monaco Bold", "Monaco Italic", "Monaco-Bold", "MonacoItalic"} {
		if path, err := FindFontFile(name); err == nil {
			t.Errorf("there is a %s on disk at %s; synthesising it is no longer the only option", name, path)
		}
	}
}
