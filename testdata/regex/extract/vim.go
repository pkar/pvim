package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// pair is one pattern and one input to run it against.
type pair struct{ pat, in string }

// result is what vim said about a pair: the byte offset the match starts at,
// -1 for no match, the text that matched, and the exception if vim refused the
// pattern outright.
type result struct {
	start int
	match string
	err   string
}

// runVim answers every pair in one vim process.
//
// One process for the lot, not one per pair: vim takes about 40ms to start and
// the fuzz alone asks a few thousand questions, so a process per question is
// the difference between a minute and two hours. The questions go in as a
// vimscript list literal and the answers come back through writefile(), which
// is the only channel out of a -es vim that does not go through the terminal
// and get line-wrapped.
func runVim(vimPath string, pairs []pair) ([]result, error) {
	dir, err := os.MkdirTemp("", "pvim-regex-oracle")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	script := filepath.Join(dir, "cases.vim")
	outFile := filepath.Join(dir, "out.tsv")

	var b strings.Builder
	b.WriteString(oracleScript)
	b.WriteString("let s:out = " + vimQuote(outFile) + "\n")
	b.WriteString("let s:cases = [\n")
	for _, p := range pairs {
		b.WriteString("  \\ [" + vimQuote(p.pat) + ", " + vimQuote(p.in) + "],\n")
	}
	b.WriteString("  \\ ]\n")
	b.WriteString("call s:Run(s:cases, s:out)\n")
	if err := os.WriteFile(script, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}

	cmd := exec.Command(vimPath, "--clean", "-es", "--not-a-term", "-S", script, "-c", "qa!")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w", vimPath, err)
	}

	f, err := os.Open(outFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []result
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) != 3 {
			return nil, fmt.Errorf("vim wrote a row with %d fields: %q", len(f), sc.Text())
		}
		start, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("vim wrote a bad offset %q", f[0])
		}
		out = append(out, result{start: start, match: unescapeTSV(f[1]), err: unescapeTSV(f[2])})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) != len(pairs) {
		return nil, fmt.Errorf("asked vim %d questions and got %d answers", len(pairs), len(out))
	}
	return out, nil
}

// oracleScript is the vimscript half of the oracle.
//
// The:substitute at the top is what gives `~` a value: vim expands `~` to the
// last substitute string, so a table with a `~` row in it has to have run one.
// "bc" is two ordinary characters on purpose, so that a `~\+` row shows whether
// the whole string repeats or only its last character.
const oracleScript = `
set noignorecase nosmartcase magic
new
call setline(1, 'xxx')
silent! %s/x/bc/
func! s:Enc(s)
  let r = substitute(a:s, '\\', '\\\\', 'g')
  let r = substitute(r, "\t", '\\t', 'g')
  let r = substitute(r, "\n", '\\n', 'g')
  let r = substitute(r, "\r", '\\r', 'g')
  let r = substitute(r, "\e", '\\e', 'g')
  return r
endfunc
func! s:Run(cases, out)
  let rows = []
  for [pat, txt] in a:cases
    let start = -1
    let mtext = ''
    let ex = ''
    try
      let start = match(txt, pat)
      let mtext = matchstr(txt, pat)
    catch
      let start = -1
      let mtext = ''
      let ex = v:exception
    endtry
    call add(rows, join([start, s:Enc(mtext), s:Enc(ex)], "\t"))
  endfor
  call writefile(rows, a:out)
endfunc
`

// vimQuote writes s as a vimscript double-quoted string.
//
// Everything outside printable ASCII goes out as a \u escape rather than as
// itself, so the generated script is pure ASCII and cannot be broken by
// whatever the locale of the shell running it happens to be.
func vimQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == 0x1b:
			b.WriteString(`\e`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7f:
			b.WriteRune(r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// escapeTSV makes a field safe to put in a tab-separated column. It is the Go
// half of s:Enc above and the two have to agree exactly.
func escapeTSV(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		"\t", `\t`,
		"\n", `\n`,
		"\r", `\r`,
		"\x1b", `\e`,
	)
	return r.Replace(s)
}

// unescapeTSV reverses escapeTSV.
func unescapeTSV(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'e':
			b.WriteByte(0x1b)
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
