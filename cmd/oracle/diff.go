package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// maxDiffLines caps what one failing case prints.
//
// Forty lines is enough for the offset, a three-line window from each side of
// each of the three artifacts, and a note saying what was cut. Uncapped output
// is how a fuzz run that found one bug in ten thousand scripts turns into a
// screen nobody reads.
const maxDiffLines = 40

// artifactDiff is one artifact that did not match.
type artifactDiff struct {
	name string
	ref  []byte
	cand []byte
}

// firstDifference returns the byte offset where two artifacts stop agreeing,
// and the 1-based line that offset falls on. A shorter side differs at its own
// end, which is the offset the caller wants: that is where the missing bytes
// were supposed to start.
func firstDifference(a, b []byte) (offset, line int) {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i, 1 + bytes.Count(a[:i], []byte("\n"))
}

// window renders the three lines around a line number, numbered, with control
// bytes made visible. Registers hold newlines and tabs and a diff that prints
// them raw is a diff you have to count spaces in.
func window(b []byte, line int) []string {
	lines := strings.Split(string(b), "\n")
	var out []string
	for n := line - 1; n <= line+1; n++ {
		if n < 1 || n > len(lines) {
			continue
		}
		out = append(out, fmt.Sprintf("%6d  %s", n, visible(lines[n-1])))
	}
	if len(out) == 0 {
		out = append(out, "         <empty>")
	}
	return out
}

// visible turns a line into something safe to print on a terminal. strconv's
// quoting is used for the escapes and then unwrapped, because the surrounding
// quotes make every line two characters wider for no reason.
func visible(s string) string {
	q := strconv.Quote(s)
	return q[1 : len(q)-1]
}

// report renders every artifact that differs, capped.
func (d artifactDiff) report() []string {
	off, line := firstDifference(d.ref, d.cand)
	out := []string{
		fmt.Sprintf("  %s differs at byte %d, line %d (vim %d bytes, pvim %d bytes)",
			d.name, off, line, len(d.ref), len(d.cand)),
		"    vim:",
	}
	for _, l := range window(d.ref, line) {
		out = append(out, "    "+l)
	}
	out = append(out, "    pvim:")
	for _, l := range window(d.cand, line) {
		out = append(out, "    "+l)
	}
	return out
}

// compare returns the artifacts that do not match, in the order they matter:
// the buffer first, because a wrong buffer explains everything under it, then
// the editor state, then the message line.
//
// The messages go through normaliseElapsed first, which is the one place this
// harness compares anything less than byte for byte, and the reason is written
// out there: the number in "1 second ago" is the clock and not the editor.
func compare(ref, cand artifacts) []artifactDiff {
	var out []artifactDiff
	for _, d := range []artifactDiff{
		{bufferName, ref.buffer, cand.buffer},
		{stateName, ref.state, cand.state},
		{messagesName, normaliseElapsed(ref.messages), normaliseElapsed(cand.messages)},
	} {
		if !bytes.Equal(d.ref, d.cand) {
			out = append(out, d)
		}
	}
	return out
}

// cap40 trims a report to maxDiffLines and says so.
func cap40(lines []string) []string {
	if len(lines) <= maxDiffLines {
		return lines
	}
	out := append([]string(nil), lines[:maxDiffLines-1]...)
	return append(out, fmt.Sprintf("  ... %d more lines", len(lines)-(maxDiffLines-1)))
}

// The three byte strings vim's u_add_time() puts on the end of an undo
// message, and what they are replaced by.
var (
	agoSuffix   = []byte(" ago")
	agoPlural   = []byte(" seconds")
	agoSingular = []byte(" second")
	agoAnyCount = []byte("N seconds ago")
)

// normaliseElapsed replaces the second count in vim's undo message with "N".
//
// "u" and CTRL-R print "6 changes; before #1 1 second ago", and the number is
// a reading of the wall clock between the change and the undo. That is the
// harness's own scheduling and not a decision either editor made: on a loaded
// box a case whose change spawns a subprocess -- ex_filter_bang_undo runs
// ":%!/usr/bin/sort" -- lands either side of a second boundary, and the same
// vim run twice disagrees with itself. Comparing it is a category error, so
// the count is compared for shape and not for value: both sides still have to
// print an elapsed-time clause, in the same place, spelled the same way around
// the number, and nothing else in msgs.txt is touched.
//
// It is also what makes pvim's answer here legitimate rather than lucky.
// internal/mode/undo.go prints "0 seconds ago" always, because the undo tree
// keeps no timestamps, and every case in testdata/keys passes only
// because vim's own answer is a fast zero too. That difference is D-007.
//
// Seconds only. vim spells minutes and hours differently again and no case in
// this harness runs for two minutes; the day one does, this function is where
// it says so.
func normaliseElapsed(b []byte) []byte {
	var out []byte
	copied, search := 0, 0
	for {
		j := bytes.Index(b[search:], agoSuffix)
		if j < 0 {
			break
		}
		end := search + j
		search = end + len(agoSuffix)

		var start int
		switch {
		case bytes.HasSuffix(b[:end], agoPlural):
			start = end - len(agoPlural)
		case bytes.HasSuffix(b[:end], agoSingular):
			start = end - len(agoSingular)
		default:
			continue // some other " ago", left alone
		}
		digits := start
		for digits > 0 && b[digits-1] >= '0' && b[digits-1] <= '9' {
			digits--
		}
		if digits == start {
			continue // " seconds ago" with no count in front of it
		}
		out = append(out, b[copied:digits]...)
		out = append(out, agoAnyCount...)
		copied = search
	}
	if copied == 0 {
		return b
	}
	return append(out, b[copied:]...)
}
