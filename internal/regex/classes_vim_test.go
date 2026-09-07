package regex

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// sweptAtoms are the atoms whose bodies are pasted-in code point lists rather
// than rules. Every one of them is a table in vim too, and a table is the one
// kind of answer that cannot be checked by reading the code.
var sweptAtoms = []string{`\k`, `\K`, `\p`, `\P`, `[[:lower:]]`, `[[:upper:]]`}

// sweptWindows are the code point ranges the sweep covers: the whole BMP, and
// then the four blocks above it where a truncated table would show up, which is
// the Deseret and Adlam letters that have case, the mathematical digits, and
// the emoji. Sweeping all 1.1 million code points takes half a minute and finds
// nothing the plane boundaries do not.
var sweptWindows = [][2]rune{
	{0x20, 0xffff},
	{0x10400, 0x104ff},
	{0x1d7c0, 0x1d7ff},
	{0x1e900, 0x1e95f},
	{0x1f300, 0x1f6ff},
}

// TestClassesAgainstVim asks vim again for every code point in the swept
// windows and fails when a table in classtab.go has drifted from the binary.
//
// This is the test that makes the tables maintainable. They were read off vim
// 9.2 and there is no way to tell by looking at one whether it is still right
// after a brew upgrade moves vim, so the check is the sweep itself, run every
// time, rather than a note asking somebody to regenerate.
func TestClassesAgainstVim(t *testing.T) {
	if testing.Short() {
		t.Skip("the class sweep needs vim")
	}
	if _, err := os.Stat(vimBinary); err != nil {
		t.Skipf("no oracle at %s", vimBinary)
	}

	want, err := sweepVim(t, sweptAtoms, sweptWindows)
	if err != nil {
		t.Fatalf("sweeping vim: %v", err)
	}
	for i, atom := range sweptAtoms {
		re := mustCompile(t, atom)
		got := sweepRanges(func(r rune) bool { return re.MatchString(string(r)) }, sweptWindows)
		if diff := rangeDiff(got, want[i]); diff != "" {
			t.Errorf("%s disagrees with vim:\n%s\nsource %s", atom, diff, re.Source())
		}
	}
}

// codeRange is one run of code points a class matches.
type codeRange struct{ lo, hi rune }

func (c codeRange) String() string {
	if c.lo == c.hi {
		return fmt.Sprintf("U+%04X", c.lo)
	}
	return fmt.Sprintf("U+%04X-U+%04X", c.lo, c.hi)
}

// sweepRanges walks the windows and collects the runs where match says yes.
//
// The surrogates are skipped rather than asked: no well-formed UTF-8 holds one,
// Go turns a surrogate rune into U+FFFD on the way into a string, and vim's
// answer for a code point neither side can represent is not evidence of
// anything.
func sweepRanges(match func(rune) bool, windows [][2]rune) []codeRange {
	var out []codeRange
	for _, w := range windows {
		start := rune(-1)
		for cp := w[0]; cp <= w[1]; cp++ {
			yes := match(cp) && !(cp >= 0xd800 && cp <= 0xdfff)
			switch {
			case yes && start < 0:
				start = cp
			case !yes && start >= 0:
				out = append(out, codeRange{start, cp - 1})
				start = -1
			}
		}
		if start >= 0 {
			out = append(out, codeRange{start, w[1]})
		}
	}
	return out
}

// rangeDiff reports the first few runs the two sides do not agree on, or "".
func rangeDiff(got, want []codeRange) string {
	if len(got) == len(want) {
		same := true
		for i := range got {
			if got[i] != want[i] {
				same = false
				break
			}
		}
		if same {
			return ""
		}
	}
	var b strings.Builder
	shown := 0
	for i := 0; i < len(got) || i < len(want); i++ {
		var g, w string
		if i < len(got) {
			g = got[i].String()
		}
		if i < len(want) {
			w = want[i].String()
		}
		if g == w {
			continue
		}
		shown++
		if shown > 8 {
			fmt.Fprintf(&b, "  ... %d runs here, %d in vim\n", len(got), len(want))
			break
		}
		fmt.Fprintf(&b, "  run %d: pvim %s, vim %s\n", i, or(g, "none"), or(w, "none"))
	}
	return b.String()
}

func or(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

// sweepVim runs the whole sweep in one vim process and gives back the runs each
// atom matched, one slice per atom in the order they were asked.
func sweepVim(t *testing.T, atoms []string, windows [][2]rune) ([][]codeRange, error) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "sweep.vim")
	outFile := filepath.Join(dir, "out.txt")

	var b strings.Builder
	b.WriteString("let s:out = " + vimQuoteTest(outFile) + "\n")
	b.WriteString("let s:pats = [\n")
	for _, a := range atoms {
		// The atom has to match the whole single-character string, or \P would
		// answer for the empty string and every code point would be a match.
		b.WriteString("  \\ " + vimQuoteTest(`^`+a+`$`) + ",\n")
	}
	b.WriteString("  \\ ]\n")
	b.WriteString("let s:windows = [\n")
	for _, w := range windows {
		fmt.Fprintf(&b, "  \\ [%d, %d],\n", w[0], w[1])
	}
	b.WriteString("  \\ ]\n")
	b.WriteString(sweepScript)

	if err := os.WriteFile(script, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	cmd := exec.Command(vimBinary, "--clean", "-es", "--not-a-term", "-S", script, "-c", "qa!")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	text, err := os.ReadFile(outFile)
	if err != nil {
		return nil, err
	}
	out := make([][]codeRange, len(atoms))
	for _, line := range strings.Split(strings.TrimSpace(string(text)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			return nil, fmt.Errorf("vim wrote %q", line)
		}
		i, err1 := strconv.Atoi(f[0])
		lo, err2 := strconv.ParseInt(f[1], 16, 32)
		hi, err3 := strconv.ParseInt(f[2], 16, 32)
		if err1 != nil || err2 != nil || err3 != nil || i < 0 || i >= len(atoms) {
			return nil, fmt.Errorf("vim wrote %q", line)
		}
		out[i] = append(out[i], codeRange{rune(lo), rune(hi)})
	}
	return out, nil
}

// sweepScript is the loop, in vimscript. It writes one line per run: the index
// of the atom and the first and last code point in hexadecimal.
const sweepScript = `
set noignorecase nosmartcase magic
let s:rows = []
for s:i in range(len(s:pats))
  for s:w in s:windows
    let s:start = -1
    let s:cp = s:w[0]
    while s:cp <= s:w[1]
      if s:cp >= 0xd800 && s:cp <= 0xdfff
        let s:m = 0
      else
        let s:m = (nr2char(s:cp) =~# s:pats[s:i])
      endif
      if s:m
        if s:start < 0
          let s:start = s:cp
        endif
      elseif s:start >= 0
        call add(s:rows, printf('%d %x %x', s:i, s:start, s:cp - 1))
        let s:start = -1
      endif
      let s:cp += 1
    endwhile
    if s:start >= 0
      call add(s:rows, printf('%d %x %x', s:i, s:start, s:w[1]))
    endif
  endfor
endfor
call writefile(s:rows, s:out)
`
