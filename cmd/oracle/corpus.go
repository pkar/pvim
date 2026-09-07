package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// corpusFile is one buffer the fuzzer edits.
type corpusFile struct {
	name string
	data []byte
}

// builtinCorpus is the eight shapes. They are shapes and not
// contents: every one of them is a place vim's line model has an edge, and
// between them they cover the ones that have historically bitten.
//
// It is generated rather than committed so that the fuzzer works in a fresh
// checkout with no corpus directory, and so that the 40k-line log does not sit
// in git.
func builtinCorpus(vimrcPath string) []corpusFile {
	var out []corpusFile

	out = append(out, corpusFile{"go.txt", []byte(`package main

import (
	"fmt"
	"os"
)

// count returns how many times b appears in s.
func count(s, b string) int {
	n := 0
	for i := 0; i+len(b) <= len(s); i++ {
		if s[i:i+len(b)] == b {
			n++
		}
	}
	return n
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: count STRING SUBSTRING")
		os.Exit(2)
	}
	fmt.Println(count(os.Args[1], os.Args[2]))
}
`)})

	// A long log. Scrolling, G, counts and anything that walks the buffer
	// behave differently once the file is taller than the window, and 40k lines
	// is where a slow implementation stops being merely slow.
	var log strings.Builder
	for i := 0; i < 40000; i++ {
		log.WriteString("2026-09-02T10:")
		log.WriteString(strconv.Itoa(i%60/10) + strconv.Itoa(i%10))
		log.WriteString(":00 level=info msg=\"request served\" path=/v1/item/")
		log.WriteString(strconv.Itoa(i))
		log.WriteString(" ms=")
		log.WriteString(strconv.Itoa(i % 997))
		log.WriteString("\n")
	}
	out = append(out, corpusFile{"log.txt", []byte(log.String())})

	// The empty file: dd, p, J and every motion have a separate answer here,
	// and "--No lines in buffer--" is a message the candidate has to get right.
	out = append(out, corpusFile{"empty.txt", nil})

	// One line, no trailing newline: vim reports [noeol] and writing it back
	// must not invent one.
	out = append(out, corpusFile{"noeol.txt", []byte("the only line, and it does not end")})

	// CRLF. 'fileformat' is detected on read and put back on write, so an edit
	// that strips or doubles a CR shows up in the buffer diff.
	out = append(out, corpusFile{"crlf.txt", []byte("first\r\nsecond\r\nthird\r\n")})

	// Tabs and wide runes: every display-column question at once, and the one
	// place a byte column and a virtual column disagree.
	out = append(out, corpusFile{"wide.txt", []byte(
		"\tindented with a tab\n" +
			"日本語のテキストがここにある\n" +
			"mixed\tタブ\tand wide\n" +
			"    four spaces then\ta tab\n")})

	// A blank last line, which is not the same as a file ending in a newline
	// and is where dd, p and G disagree with a naive line list.
	out = append(out, corpusFile{"blanklast.txt", []byte("alpha\nbeta\ngamma\n\n")})

	// The config this editor exists to run, as a buffer. Long lines, comments,
	// backslash continuations and a dict literal.
	if b, err := os.ReadFile(vimrcPath); err == nil {
		out = append(out, corpusFile{"vimrc.txt", b})
	}
	return out
}

// loadCorpus prefers a corpus directory on disk and falls back to the built-in
// shapes when there is not one. The directory wins so that a shape found by
// hand can be added without touching this file.
func loadCorpus(dir, vimrcPath string) []corpusFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return builtinCorpus(vimrcPath)
	}
	var out []corpusFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, corpusFile{e.Name(), b})
	}
	if len(out) == 0 {
		return builtinCorpus(vimrcPath)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}
