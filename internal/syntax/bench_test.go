package syntax

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The numbers in the package doc come from here, on a 40,000-line Go file.
//
// Five measurements because they answer five different questions, and only two
// of them are ever on the path of a frame:
//
//	WholeFile every line from the top, which is the honest cost of the
//	 work the cache is there to avoid
//	FirstScreen opening a file and drawing the first sixty lines
//	Redraw every frame after that, off the span cache
//	Jump:40000 into a file nobody has walked, which is where
//	 `syn sync minlines` earns its keep
//	Edit a keystroke: everything from the changed line down is thrown
//	 away and the sixty lines on screen are recomputed

// bigFile builds a Go file of about n lines out of a real one.
func bigFile(tb testing.TB, n int) []string {
	tb.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "sample_real.go.txt"))
	if err != nil {
		tb.Fatal(err)
	}
	one := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	out := make([]string, 0, n)
	for len(out) < n {
		out = append(out, one...)
	}
	return out[:n]
}

// benchSetup loads the go syntax and builds the file.
func benchSetup(b *testing.B) (*Syntax, lines) {
	b.Helper()
	s, err := Load(DefaultRuntime(), "go")
	if err != nil {
		b.Skipf("no go syntax file: %v", err)
	}
	return s, lines(bigFile(b, 40000))
}

const screen = 60

func BenchmarkWholeFile(b *testing.B) {
	s, src := benchSetup(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h := NewHighlighter(s, src)
		for l := 1; l <= len(src); l++ {
			h.SpansOn(l)
		}
	}
	b.ReportMetric(float64(len(src)), "lines/op")
}

func BenchmarkFirstScreen(b *testing.B) {
	s, src := benchSetup(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h := NewHighlighter(s, src)
		for l := 1; l <= screen; l++ {
			h.SpansOn(l)
		}
	}
}

func BenchmarkRedraw(b *testing.B) {
	s, src := benchSetup(b)
	h := NewHighlighter(s, src)
	for l := 1; l <= screen; l++ {
		h.SpansOn(l)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for l := 1; l <= screen; l++ {
			h.SpansOn(l)
		}
	}
}

func BenchmarkJump(b *testing.B) {
	s, src := benchSetup(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h := NewHighlighter(s, src)
		for l := 20000; l < 20000+screen; l++ {
			h.SpansOn(l)
		}
	}
}

func BenchmarkEdit(b *testing.B) {
	s, src := benchSetup(b)
	h := NewHighlighter(s, src)
	for l := 20000; l < 20000+screen; l++ {
		h.SpansOn(l)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Changed(20000)
		for l := 20000; l < 20000+screen; l++ {
			h.SpansOn(l)
		}
	}
}
