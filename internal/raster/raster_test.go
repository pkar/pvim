package raster

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/screen"
)

// updateGoldens rewrites testdata/goldens/*.sha256 instead of checking them.
// Any rendering change regenerates the hashes in the same commit as the change;
// there is no other way to move one and no PNG in the repository to compare by
// eye, which is deliberate. Run it with:
//
//	go test ./internal/raster/ -update-goldens
var updateGoldens = flag.Bool("update-goldens", false, "rewrite the golden hashes")

// monacoSHA256 is the sha256 of the font every golden below was rendered from.
//
// Apple has already changed this file once (mtime ), and a changed
// Monaco means different glyph outlines, which means every golden in this
// package is wrong in a way that looks like a rasteriser bug. Pinning it makes
// that a one-line failure naming the font instead of an afternoon.
const monacoSHA256 = "e110b2fb6248c654878dee87e9805ac0b2afc8fab19099cf92bfe12ea085c028"

func TestMonacoIsTheFontWeThinkItIs(t *testing.T) {
	b, err := os.ReadFile(DefaultFontPath)
	if err != nil {
		t.Fatalf("reading %s: %v", DefaultFontPath, err)
	}
	got := hex.EncodeToString(sha256Sum(b))
	if got != monacoSHA256 {
		t.Fatalf(`%s has changed.

got  sha256 %s
want sha256 %s

Every golden in this package was rendered from the old file, so they are all
about to fail together. Look at the new font, decide the glyphs are acceptable,
then update monacoSHA256 and rerun with -update-goldens in the same commit.`,
			DefaultFontPath, got, monacoSHA256)
	}
}

func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

// TestMetricsAtBothScales pins the cell metrics: MacVim's
// Monaco:h13 is 8 device pixels of advance at scale 1 and 16 at scale 2, which
// is what makes an 80 column window 640 logical points wide.
func TestMetricsAtBothScales(t *testing.T) {
	for _, tc := range []struct {
		scale int
		cellW int
	}{
		{1, 8},
		{2, 16},
	} {
		f := testFace(t, tc.scale)
		m := f.Metrics()
		if m.CellW != tc.cellW {
			t.Errorf("scale %d: CellW = %d, want %d", tc.scale, m.CellW, tc.cellW)
		}
		if m.Scale != tc.scale {
			t.Errorf("scale %d: Metrics.Scale = %d", tc.scale, m.Scale)
		}
		if m.Ascent <= 0 || m.Ascent >= m.CellH {
			t.Errorf("scale %d: ascent %d is not inside a cell %d tall", tc.scale, m.Ascent, m.CellH)
		}
	}
}

// TestCellIsSquareAcrossScales holds the one relationship the GUI depends on:
// the logical width of a column does not change with the display it is on, so
// dragging a window between a retina and a non-retina screen does not reflow.
func TestCellIsSquareAcrossScales(t *testing.T) {
	one := testFace(t, 1).Metrics()
	two := testFace(t, 2).Metrics()
	if two.CellW != 2*one.CellW {
		t.Errorf("CellW %d at scale 1 and %d at scale 2; a column must not change logical width", one.CellW, two.CellW)
	}
}

func testFace(t *testing.T, scale int) Face {
	t.Helper()
	f, err := DefaultFace(scale)
	if err != nil {
		t.Fatalf("loading the default face at scale %d: %v", scale, err)
	}
	return f
}

// group is one named highlight in the fixture, kept as a table so a test can
// name a group instead of counting ids.
type group struct {
	name string
	hl   screen.Highlight
}

var fixtureGroups = []group{
	{"Comment", screen.Highlight{FG: screen.RGB{R: 0x99, G: 0x99, B: 0x99}, BG: screen.RGB{R: 0x26, G: 0x26, B: 0x26}, Attr: screen.AttrItalic}},
	{"Title", screen.Highlight{FG: screen.RGB{R: 0xff, G: 0xff, B: 0xff}, BG: screen.RGB{R: 0x26, G: 0x26, B: 0x26}, Attr: screen.AttrBold}},
	{"Search", screen.Highlight{FG: screen.RGB{R: 0x26, G: 0x26, B: 0x26}, BG: screen.RGB{R: 0xd7, G: 0xd7, B: 0x00}}},
	{"Visual", screen.Highlight{FG: screen.RGB{R: 0xee, G: 0xee, B: 0xee}, BG: screen.RGB{R: 0x44, G: 0x44, B: 0x44}, Attr: screen.AttrReverse}},
	{"Underlined", screen.Highlight{FG: screen.RGB{R: 0x87, G: 0xaf, B: 0xd7}, BG: screen.RGB{R: 0x26, G: 0x26, B: 0x26}, Attr: screen.AttrUnderline}},
	{"SpellBad", screen.Highlight{FG: screen.RGB{R: 0xee, G: 0xee, B: 0xee}, BG: screen.RGB{R: 0x26, G: 0x26, B: 0x26}, SP: screen.RGB{R: 0xd7, G: 0x00, B: 0x00}, Attr: screen.AttrUndercurl}},
	{"Struck", screen.Highlight{FG: screen.RGB{R: 0xee, G: 0xee, B: 0xee}, BG: screen.RGB{R: 0x26, G: 0x26, B: 0x26}, Attr: screen.AttrStrikethrough}},
}

// fixture is the grid every golden is rendered from: one line per interesting
// case, so a change to any of them moves exactly one hash and the failure names
// what broke.
func fixture() Frame {
	tab := screen.NewTable()
	id := map[string]screen.HLID{}
	for _, g := range fixtureGroups {
		id[g.name] = tab.Set(g.name, g.hl)
	}

	g := screen.NewGrid(10, 40)
	g.SetString(0, 0, "func main() { fmt.Println(\"hi\") }", screen.Normal)
	g.SetString(1, 0, "// a comment in italics", id["Comment"])
	g.SetString(2, 0, "A HEADING IN BOLD", id["Title"])
	g.SetString(3, 0, "matched text", id["Search"])
	g.SetString(4, 0, "reversed text", id["Visual"])
	g.SetString(5, 0, "https://example.invalid", id["Underlined"])
	g.SetString(6, 0, "mispeled wrds", id["SpellBad"])
	g.SetString(7, 0, "deleted line", id["Struck"])
	// Wide runes and a rune Monaco has no glyph for at all, which must come out
	// as the .notdef box and take two cells.
	g.SetString(8, 0, "wide 世界 emoji \U0001F600 end", screen.Normal)
	g.SetString(9, 0, "!\"#$%&'()*+,-./0123456789:;<=>?@", screen.Normal)

	return Frame{Grid: g, HL: tab, CursorRow: 3, CursorCol: 4, Cursor: CursorNone}
}

// TestGoldens is the determinism gate. Same frame in, byte-identical pixels
// out: if a hash moves and the font did not, the rasteriser changed and the
// commit that changed it says so.
func TestGoldens(t *testing.T) {
	cases := []struct {
		name   string
		cursor CursorShape
	}{
		{"grid", CursorNone},
		{"cursor-block", CursorBlock},
		{"cursor-bar", CursorBar},
		{"cursor-underline", CursorUnderline},
		{"cursor-hollow", CursorHollow},
	}
	for _, tc := range cases {
		for _, scale := range []int{1, 2} {
			name := goldenName(tc.name, scale)
			t.Run(name, func(t *testing.T) {
				f := fixture()
				f.Cursor = tc.cursor
				opt := PaintOptions{Face: testFace(t, scale)}
				dst := NewImage(f, opt)
				if err := Paint(dst, f, opt); err != nil {
					t.Fatalf("Paint: %v", err)
				}
				checkGolden(t, name, dst)
			})
		}
	}
}

// TestUndercurlGolden renders the spell-error case on its own and at both
// scales, because the wave is the one decoration whose phase depends on the
// absolute x coordinate and a change there does not move any other hash much.
func TestUndercurlGolden(t *testing.T) {
	for _, scale := range []int{1, 2} {
		name := goldenName("undercurl", scale)
		t.Run(name, func(t *testing.T) {
			tab := screen.NewTable()
			id := tab.Set("SpellBad", screen.Highlight{
				FG:   screen.RGB{R: 0xee, G: 0xee, B: 0xee},
				BG:   screen.RGB{R: 0x26, G: 0x26, B: 0x26},
				SP:   screen.RGB{R: 0xd7, G: 0x00, B: 0x00},
				Attr: screen.AttrUndercurl,
			})
			g := screen.NewGrid(2, 24)
			g.SetString(0, 0, "wrods with a squiggle", id)
			g.SetString(1, 0, "plain", screen.Normal)

			f := Frame{Grid: g, HL: tab, Cursor: CursorNone}
			opt := PaintOptions{Face: testFace(t, scale)}
			dst := NewImage(f, opt)
			if err := Paint(dst, f, opt); err != nil {
				t.Fatalf("Paint: %v", err)
			}
			checkGolden(t, name, dst)
		})
	}
}

func goldenName(base string, scale int) string {
	if scale == 1 {
		return base + "@1x"
	}
	return base + "@2x"
}

// checkGolden hashes the RGBA bytes and compares against testdata.
//
// On a mismatch it writes the PNG to the temp directory and names the path, so
// that a human can look at what actually rendered. The PNG never goes in the
// repository: a 300 KB binary per case, rewritten on every antialiasing tweak,
// is a worse record of intent than a 64-character hash.
func checkGolden(t *testing.T, name string, img *image.RGBA) {
	t.Helper()
	got := hex.EncodeToString(sha256Sum(img.Pix))
	path := filepath.Join("testdata", "goldens", name+".sha256")

	if *updateGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the golden: %v\nrun: go test ./internal/raster/ -update-goldens", err)
	}
	if strings.TrimSpace(string(want)) == got {
		return
	}
	t.Errorf("%s: pixels changed\n got %s\nwant %s\nrendered: %s",
		name, got, strings.TrimSpace(string(want)), writePNG(t, name, img))
}

// writePNG dumps a frame somewhere a person can open it and returns the path,
// or the reason there is no path.
func writePNG(t *testing.T, name string, img *image.RGBA) string {
	t.Helper()
	path := filepath.Join(os.TempDir(), "pvim-raster-"+name+".png")
	f, err := os.Create(path)
	if err != nil {
		return "could not write a PNG: " + err.Error()
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return "could not encode a PNG: " + err.Error()
	}
	return path
}
