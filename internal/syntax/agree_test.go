package syntax

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgreement(t *testing.T) {
	if testing.Short() {
		t.Skip("-short skips the half that shells out to vim")
	}
	// The floor is what was measured, a point below the number
	// in the package doc so that noise in vim's own runtime does not turn this
	// red, and close enough that a real regression does. Raise it when the
	// number goes up; a drop is a bug and not a reason to lower it.
	for _, tc := range []struct {
		file, ft string
		floor    float64
	}{
		{"sample.go.txt", "go", 100},
		{"sample_real.go.txt", "go", 99.8},
		{"sample.json.txt", "json", 99.4},
		{"sample.py.txt", "python", 100},
		{"sample.md.txt", "markdown", 86},
		{"sample.tf.txt", "terraform", 99},
		{"sample.sh.txt", "sh", 54},
		{"sample.yaml.txt", "yaml", 36},
	} {
		t.Run(tc.file, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			src := strings.Split(strings.TrimRight(string(body), "\n"), "\n")

			dir := t.TempDir()
			path := filepath.Join(dir, strings.TrimSuffix(tc.file, ".txt"))
			if err := os.WriteFile(path, body, 0o644); err != nil {
				t.Fatal(err)
			}
			want := askVim(t, path)
			got := ours(t, tc.ft, src)
			if pct := compare(t, tc.file, want, got, src, 15); pct < tc.floor {
				t.Errorf("%s: %.1f%% of positions agree with vim, floor is %.1f%%", tc.file, pct, tc.floor)
			}
		})
	}
}
