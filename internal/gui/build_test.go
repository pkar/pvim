package gui

import (
	"os/exec"
	"strings"
	"testing"
)

// TestLinuxBuilds is the guarantee that a failure in the AppKit backend costs a
// window and not the editor.
//
// The whole risk mitigation for the AppKit bet is that internal/tui is a peer
// of this package and every other package builds and tests with no window
// server. That only stays true if the tree cross-compiles, and the way it stops
// being true is somebody putting an objc call in a file with no build tag. This
// catches that in the same `go test` that catches everything else, rather than
// on a Linux box nobody has.
func TestLinuxBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiling takes a few seconds")
	}
	cmd := exec.Command("go", "build", "./internal/gui/...")
	cmd.Dir = "../.."
	cmd.Env = append(cmd.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("GOOS=linux build of internal/gui failed: %v\n%s", err, out)
	}
}

// cgoFilesInGraph lists every package in pkg's dependency graph that carries
// cgo files.
//
// It measures with cgo ENABLED, deliberately, and that is the whole trick. At
// CGO_ENABLED=0 the toolchain reports an empty CgoFiles list for every package
// in the tree whatever is in it, so asking the question there answers itself:
// the check comes back clean over a graph full of C. The Makefile's
// static-check says the same thing in sh, and this is the Go half of it.
func cgoFilesInGraph(t *testing.T, pkg string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", "{{if .CgoFiles}}{{.ImportPath}}{{end}}", pkg)
	cmd.Dir = "../.."
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	var found []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			found = append(found, line)
		}
	}
	return found
}

// TestNoCgoUnderneath keeps the one promise the title makes. purego is
// the only way AppKit is reached and it has no C in it; a dependency that grew
// a cgo file would silently reintroduce the C compiler this whole design exists
// to avoid, and `otool -L` at install time is far too late to find out.
func TestNoCgoUnderneath(t *testing.T) {
	if testing.Short() {
		t.Skip("go list -deps takes a moment")
	}
	var bad []string
	for _, pkg := range cgoFilesInGraph(t, "./internal/gui") {
		// runtime/cgo is the one allowed entry. purego carries a cgo.go behind
		// a //go:build cgo tag that the CGO_ENABLED=0 build never compiles, and
		// it pulls runtime/cgo into the graph only when cgo is on, which is the
		// condition this check has to measure under to measure anything.
		if pkg == "runtime/cgo" {
			continue
		}
		bad = append(bad, pkg)
	}
	if len(bad) > 0 {
		t.Errorf("packages with cgo files in internal/gui's graph:\n%s", strings.Join(bad, "\n"))
	}
}

// TestCgoCheckIsNotVacuous is the test for the test above.
//
// A check that cannot fail is worse than no check, because it is believed.
// runtime/cgo is the probe: it has cgo files by definition, so a query that
// reports a clean graph for it is a query that would report a clean graph for
// anything, which is what asking with CGO_ENABLED=0 does.
func TestCgoCheckIsNotVacuous(t *testing.T) {
	if testing.Short() {
		t.Skip("go list -deps takes a moment")
	}
	if got := cgoFilesInGraph(t, "runtime/cgo"); len(got) == 0 {
		t.Error("the cgo check reports no cgo files in runtime/cgo's own graph, so it would report none for any graph")
	}
}
