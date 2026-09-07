package finder

import "testing"

// TestTheGate is the acceptance test for the finder, minus the
// editor: "hueveri" has to put hue_verify.go first.
//
// The distractors are the ones that make it a test rather than a tautology.
// Every one of them contains h, u, e, v, e, r, i in order, so the subsequence
// filter keeps all of them and only the score can tell them apart.
func TestTheGate(t *testing.T) {
	candidates := []string{
		"internal/hue/hue_verify.go",
		"internal/hue/hue_verifier_registry_internal.go",
		"docs/how-does-the-user-experience-verification-work-in-practice.md",
		"internal/human/user/every/river/index.go",
	}
	got := Rank(candidates, "hueveri")
	if len(got) == 0 {
		t.Fatal("hueveri matched nothing")
	}
	if best := candidates[got[0].Index]; best != "internal/hue/hue_verify.go" {
		t.Errorf("hueveri ranked %q first, want internal/hue/hue_verify.go", best)
		for _, m := range got {
			t.Logf("  %6d  %s", m.Score, candidates[m.Index])
		}
	}
}

func TestSubsequenceIsTheFilter(t *testing.T) {
	cases := []struct {
		candidate, pattern string
		ok                 bool
	}{
		{"internal/finder/score.go", "ifs", true},
		{"internal/finder/score.go", "sif", false}, // out of order
		{"internal/finder/score.go", "", true},     // an empty query keeps everything
		{"internal/finder/score.go", "zzz", false}, //
		{"README.md", "readme", true},              // folded, no uppercase in the query
		{"readme.md", "README", false},             // 'smartcase': an uppercase query is exact
		{"README.md", "README", true},              //
		{"a", "aa", false},                         // longer than the candidate
	}
	for _, c := range cases {
		if _, ok := Score(c.candidate, c.pattern); ok != c.ok {
			t.Errorf("Score(%q, %q) matched = %v, want %v", c.candidate, c.pattern, ok, c.ok)
		}
	}
}

// TestPathSeparatorBonus is one half of what the scorer is for: a query that
// starts a file name beats the same letters starting a directory.
func TestPathSeparatorBonus(t *testing.T) {
	head, _ := Score("internal/score/x.go", "score")
	tail, _ := Score("internal/x/score.go", "score")
	if tail <= head {
		t.Errorf("score in the file name scored %d, in a directory %d; the file name has to win", tail, head)
	}
}

// TestCamelBoundaryBonus is the other half.
func TestCamelBoundaryBonus(t *testing.T) {
	camel, _ := Score("HueVerify.go", "hv")
	flat, _ := Score("hxxxvxxx.go", "hv")
	if camel <= flat {
		t.Errorf("HueVerify scored %d for hv and hxxxvxxx scored %d; the camel boundary has to win", camel, flat)
	}
}

func TestRunsBeatScatter(t *testing.T) {
	run, _ := Score("xxfinderxx", "finder")
	scatter, _ := Score("fxixnxdxexr", "finder")
	if run <= scatter {
		t.Errorf("a run scored %d and a scatter %d", run, scatter)
	}
}

func TestRankIsBestFirstAndStable(t *testing.T) {
	// Two candidates that score identically: the input order decides, so a
	// walk's alphabetical order and an MRU list's recency both survive.
	in := []string{"a/x.go", "b/x.go"}
	got := Rank(in, "xgo")
	if len(got) != 2 {
		t.Fatalf("got %d matches, want 2", len(got))
	}
	if got[0].Score < got[1].Score {
		t.Error("Rank is not best first")
	}
	if got[0].Score == got[1].Score && got[0].Index != 0 {
		t.Error("Rank reordered a tie")
	}
}

func BenchmarkRankARepository(b *testing.B) {
	// About the size of this repository's own listing, which is what a
	// keystroke has to get through before the frame is drawn.
	names := make([]string, 0, 2000)
	for i := 0; i < 2000; i++ {
		names = append(names, "internal/package"+string(rune('a'+i%26))+"/some_file_name.go")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Rank(names, "iposfn")
	}
}
