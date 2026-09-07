package raster

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/image/font/sfnt"
)

// DefaultFontPath is the file the vimrc's `set guifont=Monaco:h13` resolves to
// on macOS. It is a constant and not a lookup because the goldens are
// hashes of glyphs out of this exact file, and a test pins its sha256 so that
// the next macOS update changing it is a loud failure rather than a quiet
// difference in every pixel.
const DefaultFontPath = "/System/Library/Fonts/Monaco.ttf"

// DefaultPointSize is the `h13` in `Monaco:h13`.
const DefaultPointSize = 13.0

// fontDirs are searched, in order, for a family named in a guifont string. The
// user directory comes first so a font installed by hand wins over a system one
// with the same name, which is the order every macOS font API uses.
var fontDirs = []string{
	filepath.Join(os.Getenv("HOME"), "Library/Fonts"),
	"/Library/Fonts",
	"/System/Library/Fonts",
}

// GUIFont is a parsed `guifont` entry: a family and a size in points, plus the
// style bits vim's syntax allows on one.
//
// Vim's syntax is name:h<height>:w<width>:b:i:u:s:c<charset>, spaces escaped
// with a backslash, alternatives separated by commas. Width, underline,
// strikeout and charset are parsed and dropped: a monospace grid takes its
// advance from the font, and underline is a highlight attribute rather than a
// property of the face.
//
// Bold and Italic are kept, because `:set guifont?` has to print back what was
// set, and nothing applies them: NewFace builds one weight and the styles come
// from the screen.Attr bits on a cell's highlight, per cell. So
// `guifont=Monaco:h13:b` parses, round-trips and renders as plain Monaco. The
// day that matters is the day a face grows a default style, which is four
// lines in Font.Face and a golden regeneration.
type GUIFont struct {
	Family string
	Size   float64
	Bold   bool
	Italic bool
}

// DefaultGUIFont is what the vimrc asks for.
var DefaultGUIFont = GUIFont{Family: "Monaco", Size: DefaultPointSize}

// ParseGUIFont parses one vim 'guifont' value.
//
// A comma-separated list is vim's fallback list: the first entry that loads
// wins. Only the first is returned, because with no fallback chain there is
// nothing for the rest to fall back to, and a caller wanting them can split the
// string itself.
func ParseGUIFont(s string) (GUIFont, error) {
	first := splitUnescaped(s, ',')
	if len(first) == 0 || strings.TrimSpace(first[0]) == "" {
		return GUIFont{}, fmt.Errorf("raster: empty guifont")
	}
	parts := splitUnescaped(first[0], ':')

	f := GUIFont{Family: unescape(parts[0]), Size: DefaultPointSize}
	if f.Family == "" {
		return GUIFont{}, fmt.Errorf("raster: guifont %q has no font name", s)
	}
	for _, opt := range parts[1:] {
		if opt == "" {
			continue
		}
		switch opt[0] {
		case 'h':
			size, err := strconv.ParseFloat(opt[1:], 64)
			if err != nil || size <= 0 {
				return GUIFont{}, fmt.Errorf("raster: guifont %q has a bad height %q", s, opt)
			}
			f.Size = size
		case 'b':
			f.Bold = true
		case 'i':
			f.Italic = true
		case 'w', 'u', 's', 'c':
			// Accepted and dropped: see the type comment.
		default:
			return GUIFont{}, fmt.Errorf("raster: guifont %q has an unknown option %q", s, opt)
		}
	}
	return f, nil
}

// String renders g back into vim's syntax, which is what `:set guifont?` prints.
func (g GUIFont) String() string {
	b := strings.Builder{}
	b.WriteString(strings.ReplaceAll(g.Family, " ", `\ `))
	b.WriteString(":h")
	b.WriteString(strconv.FormatFloat(g.Size, 'f', -1, 64))
	if g.Bold {
		b.WriteString(":b")
	}
	if g.Italic {
		b.WriteString(":i")
	}
	return b.String()
}

// splitUnescaped splits on sep, ignoring separators preceded by a backslash,
// which is how vim escapes a space or a comma inside a font name.
func splitUnescaped(s string, sep byte) []string {
	var out []string
	start, esc := 0, false
	for i := 0; i < len(s); i++ {
		switch {
		case esc:
			esc = false
		case s[i] == '\\':
			esc = true
		case s[i] == sep:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// unescape drops the backslashes splitUnescaped had to respect.
func unescape(s string) string {
	b := strings.Builder{}
	esc := false
	for i := 0; i < len(s); i++ {
		if !esc && s[i] == '\\' {
			esc = true
			continue
		}
		esc = false
		b.WriteByte(s[i])
	}
	return b.String()
}

// FindFontFile returns the path of the file holding the named family.
//
// This is a filename match and not a font-database query, because the only
// database on macOS is CoreText and CoreText means a C compiler. Monaco,
// Menlo, Courier and every other family whose file is named after it resolve;
// one whose PostScript name differs from its filename does not, and says so.
func FindFontFile(family string) (string, error) {
	if family == "Monaco" {
		return DefaultFontPath, nil
	}
	base := strings.ReplaceAll(family, " ", "")
	for _, dir := range fontDirs {
		for _, ext := range []string{".ttf", ".ttc", ".otf"} {
			p := filepath.Join(dir, base+ext)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("raster: no file for font family %q in %s", family, strings.Join(fontDirs, ", "))
}

// Font is a parsed font file. One Font makes as many Faces as there are sizes
// and scales in play; parsing is the expensive half and happens once.
type Font struct {
	sf   *sfnt.Font
	path string
}

// LoadFont parses the font file at path.
func LoadFont(path string) (*Font, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("raster: reading font: %w", err)
	}
	sf, err := sfnt.Parse(b)
	if err != nil {
		return nil, fmt.Errorf("raster: parsing %s: %w", path, err)
	}
	return &Font{sf: sf, path: path}, nil
}

// Path is the file this font was read from, which is what a golden test wants
// to hash and what an error message wants to name.
func (f *Font) Path() string { return f.path }

// NewFace loads the font g names and returns a face for it at the given scale.
//
// scale is the display's backingScaleFactor: 1 on a plain display, 2 on a
// retina one. The size in points is multiplied by it, so a face at scale 2 is
// a real 26pt rasterisation and not a 13pt one blown up.
func NewFace(g GUIFont, scale int) (Face, error) {
	path, err := FindFontFile(g.Family)
	if err != nil {
		return nil, err
	}
	f, err := LoadFont(path)
	if err != nil {
		return nil, err
	}
	return f.Face(g.Size, scale)
}

// DefaultFace is Monaco at 13pt, the face the vimrc asks for, at the given
// scale.
func DefaultFace(scale int) (Face, error) {
	return NewFace(DefaultGUIFont, scale)
}
