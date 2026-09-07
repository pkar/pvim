package screen

import (
	"strings"
	"testing"
)

// Syntax spans on the grid.
//
// internal/screen knows nothing about syntax files, vim regexes or region
// nesting: it is handed byte ranges with a highlight id on them, per window per
// line, and its whole job is to put them on the right cells. These tests are
// about that, which is why every span here is written by hand rather than
// produced by internal/syntax.

func TestSyntaxSpansPaintTheirBytes(t *testing.T) {
	r := newRig(4, 20, "abc def ghi")
	hl := r.s.HL.ID("Comment")
	r.f.Syntax = map[int]SpanSource{
		r.w.ID: SpanList{1: {{Start: 4, End: 7, HL: hl}}},
	}
	got := r.renderHL()
	if !strings.Contains(got, "a=Comment") {
		t.Fatalf("no Comment in the dump:\n%s", got)
	}
	if !strings.Contains(got, "....aaa.............") {
		t.Errorf("the span landed on the wrong cells:\n%s", got)
	}
}

// A span over a tab covers every cell the tab expands to, which is the same
// rule 'hlsearch' follows and for the same reason: the span is over bytes and
// the tab is one byte.
func TestSyntaxSpanOverATab(t *testing.T) {
	r := newRig(4, 20, "ab\tcd")
	r.opt.B.TabStop = 4
	hl := r.s.HL.ID("Comment")
	r.f.Syntax = map[int]SpanSource{
		r.w.ID: SpanList{1: {{Start: 0, End: 4, HL: hl}}},
	}
	if got := r.renderHL(); !strings.Contains(got, "aaaaa...............") {
		t.Errorf("a span across a tab did not cover its cells:\n%s", got)
	}
}

// 'hlsearch' draws over syntax and not under it: a search match on a comment
// is a search match. Vim's rule, and the order the two are applied in.
func TestSearchDrawsOverSyntax(t *testing.T) {
	r := newRig(4, 20, "abc def")
	com := r.s.HL.ID("Comment")
	r.f.Syntax = map[int]SpanSource{
		r.w.ID: SpanList{1: {{Start: 0, End: 7, HL: com}}},
	}
	r.f.Search = MatchList{{Line: 1, Start: 4, End: 7}}
	got := r.renderHL()
	if !strings.Contains(got, "aaaabbb.............") {
		t.Errorf("search did not win over syntax:\n%s", got)
	}
	if !strings.Contains(got, "a=Comment") || !strings.Contains(got, "b=Search") {
		t.Errorf("legend: %s", got)
	}
}

// A window with no entry in the map draws plain, which is what a buffer whose
// filetype has no syntax file gets, and what every window gets with ':syntax
// off'.
func TestNoSyntaxSourceDrawsPlain(t *testing.T) {
	r := newRig(4, 20, "abc def")
	r.f.Syntax = map[int]SpanSource{r.w.ID + 99: SpanList{1: {{Start: 0, End: 3, HL: 7}}}}
	if got := r.renderHL(); strings.Contains(got, "highlights:") {
		t.Errorf("a span for another window was drawn:\n%s", got)
	}
}
