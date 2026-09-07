// The tests in this file are about the Makefile, which is as much a part of the
// static claim as any Go file: "static" here means four checks and an exit code
// rather than a word, and a check that cannot go red is not a check. Each one
// drives a real make with a stub tool in place of go or otool, because reading
// a recipe tells you what it meant to do and running it tells you what it does.
package pvim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runMake runs one make target from the module root and returns its combined
// output and exit code.
func runMake(t *testing.T, args ...string) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("no make on this box")
	}

	cmd := exec.Command("make", args...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("make %s: %v", strings.Join(args, " "), err)
	}
	return string(out), code
}

// stub writes an executable shell script and returns its path.
func stub(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// realGo is the go the stubs delegate to for everything they are not there to
// break.
func realGo(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go on PATH: %v", err)
	}
	return path
}

// TestOracleTargetReportsTheHarnessExitCode is the reason make oracle builds
// the harness instead of running it through go run.
//
// The oracle's exit codes are its whole interface: 4 is a difference nobody has
// registered, 5 is a side that produced no artifact at all, 1 is the harness
// itself falling over, and the register can only make a run green if 0 means
// what it says. go run reports every one of those as its own 1, so through a
// make target that used it, a crash and an unregistered diff and a missing
// artifact were the same event.
func TestOracleTargetReportsTheHarnessExitCode(t *testing.T) {
	// A go that skips the build, so the stub oracle put in ORACLEBIN survives.
	skipBuild := stub(t, "go", `if [ "$1" = "build" ]; then exit 0; fi
exec `+realGo(t)+` "$@"`)

	for _, want := range []int{0, 1, 4, 5} {
		oracle := stub(t, "oracle", "exit "+strconv.Itoa(want))
		out, code := runMake(t, "oracle", "GO="+skipBuild, "ORACLEBIN="+oracle)

		if want == 0 {
			if code != 0 {
				t.Errorf("oracle exit 0: make exited %d, want 0\n%s", code, out)
			}
			continue
		}
		// make reports 2 for any failed recipe whatever the child did, so the
		// code it saw has to be on stdout or it is gone.
		if code == 0 {
			t.Errorf("oracle exit %d: make exited 0, so a failing harness is a green run\n%s", want, out)
		}
		if msg := "oracle exited " + strconv.Itoa(want); !strings.Contains(out, msg) {
			t.Errorf("oracle exit %d: make did not say %q, so the code is lost\n%s", want, msg, out)
		}
	}
}

// TestOracleTargetsDoNotUseGoRun is the same rule read off the file, so the
// fuzz target is covered too without running 10,000 scripts through vim.
func TestOracleTargetsDoNotUseGoRun(t *testing.T) {
	for _, target := range []string{"oracle", "fuzz"} {
		recipe := recipeFor(t, target)
		if strings.Contains(recipe, "go run") {
			t.Errorf("the %s recipe uses go run, which reports every non-zero exit as 1:\n%s", target, recipe)
		}
		if !strings.Contains(recipe, "$(ORACLEBIN)") {
			t.Errorf("the %s recipe does not run $(ORACLEBIN):\n%s", target, recipe)
		}
	}
}

// TestGoRunSwallowsTheExitCode measures the fact the two tests above are built
// on rather than asserting it in a comment. If a Go release ever makes go run
// propagate the status of the program it ran, this goes red and the oracle
// targets can go back to one line each.
func TestGoRunSwallowsTheExitCode(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	write("go.mod", "module exitcode\n\ngo 1.21\n")
	write("main.go", "package main\n\nimport \"os\"\n\nfunc main() { os.Exit(4) }\n")

	run := func(name string, args ...string) int {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		err := cmd.Run()
		if err == nil {
			return 0
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
		return 0
	}

	if got := run(realGo(t), "run", "."); got != 1 {
		t.Errorf("go run of a program that exits 4 exited %d, want the 1 go run collapses it to", got)
	}
	if code := run(realGo(t), "build", "-o", filepath.Join(dir, "exitcode"), "."); code != 0 {
		t.Fatalf("go build: exit %d", code)
	}
	if got := run(filepath.Join(dir, "exitcode")); got != 4 {
		t.Errorf("the built binary exited %d, want 4", got)
	}
}

// TestStaticCheckFailsWhenGoListFails covers the first of the four static
// checks. It used to capture a pipeline ending in `|| true`, which exits 0
// whatever go list did, so a module that would not resolve read as "no package
// in the graph carries cgo files" and the check printed green on an answer it
// never got.
func TestStaticCheckFailsWhenGoListFails(t *testing.T) {
	brokenList := stub(t, "go", `if [ "$1" = "list" ]; then echo "stub go: list is broken" >&2; exit 1; fi
exec `+realGo(t)+` "$@"`)

	out, code := runMake(t, "static-check", "GO="+brokenList)
	if code == 0 {
		t.Errorf("static-check exited 0 with a go list that cannot run:\n%s", out)
	}
	if strings.Contains(out, "static-check: green") {
		t.Errorf("static-check called itself green with a go list that cannot run:\n%s", out)
	}
}

// TestStaticCheckFailsWithoutOtool covers the second, which the Makefile calls
// the check with teeth. build runs at CGO_ENABLED=0, so a box with Go and no
// Xcode command line tools compiles pvim and has no otool, and the check used
// to print green there without ever running.
func TestStaticCheckFailsWithoutOtool(t *testing.T) {
	out, code := runMake(t, "static-check", "OTOOL=/nonexistent/otool")
	if code == 0 {
		t.Errorf("static-check exited 0 with no otool on the box:\n%s", out)
	}
	if strings.Contains(out, "static-check: green") {
		t.Errorf("static-check called itself green without running otool:\n%s", out)
	}
}

// recipeFor returns the recipe lines of one Makefile target, joined. Make
// recipes are the tab-indented lines after `target:` up to the first line that
// is neither indented nor blank.
func recipeFor(t *testing.T, target string) string {
	t.Helper()
	src, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}

	var recipe []string
	in := false
	for _, line := range strings.Split(string(src), "\n") {
		switch {
		case strings.HasPrefix(line, target+":"):
			in = true
		case !in:
		case strings.HasPrefix(line, "\t"):
			recipe = append(recipe, line)
		case strings.TrimSpace(line) == "":
		default:
			in = false
		}
	}
	if len(recipe) == 0 {
		t.Fatalf("no recipe for target %q in the Makefile", target)
	}
	return strings.Join(recipe, "\n")
}

// TestTestTargetOutlastsTheSlowPackages is about the number of seconds `make
// check` has left before it fails for a reason that is not the code.
//
// `go test` with no -timeout gives every package binary the 10m default, and
// two packages here shell out to /opt/homebrew/bin/vim thousands of times:
// cmd/oracle took 557s on an idle machine, internal/motion 307s
// beside it. 43 seconds of margin on the fastest box this will ever run on is
// not margin. Past 600s go test kills the binary with "panic: test timed out
// after 10m0s" and a goroutine dump, and the person reading that sees a broken
// harness rather than a slow machine.
//
// The target is run with a stub go rather than read, because a recipe that
// mentions -timeout and a recipe that passes it to go are different things.
func TestTestTargetOutlastsTheSlowPackages(t *testing.T) {
	echoArgs := stub(t, "go", `echo "ARGV: $@"`)

	out, code := runMake(t, "test", "GO="+echoArgs)
	if code != 0 {
		t.Fatalf("make test with a stub go exited %d:\n%s", code, out)
	}

	d, ok := timeoutFlag(out)
	if !ok {
		t.Fatalf("make test runs go with no -timeout, so every package gets the 10m default and cmd/oracle finishes 43s inside it:\n%s", out)
	}
	if d <= 10*time.Minute {
		t.Errorf("make test passes -timeout %s, which is not more than go test's own 10m default; the flag is there to buy headroom over a measured 557s package", d)
	}
}

// timeoutFlag returns the duration a -timeout in out carries, in either
// spelling go test accepts.
func timeoutFlag(out string) (time.Duration, bool) {
	fields := strings.Fields(out)
	for i, f := range fields {
		var val string
		switch {
		case f == "-timeout" && i+1 < len(fields):
			val = fields[i+1]
		case strings.HasPrefix(f, "-timeout="):
			val = strings.TrimPrefix(f, "-timeout=")
		default:
			continue
		}
		d, err := time.ParseDuration(val)
		if err != nil {
			return 0, false
		}
		return d, true
	}
	return 0, false
}

// TestMakefileDescribesTheGateItHas holds the Makefile's own prose to the
// recipe underneath it, the way status_test.go holds the notes to the code.
//
// Check 4 used to count the entries in bin/ and demand exactly one. It was
// replaced by a grep for a .dylib, .so, .a or .framework sidecar, doc.go was
// updated with it and three comments in this file were not: two of them gave
// the deleted headcount as the live reason the oracle and the linux cross build
// are built into dist/, and one said `make oracle` builds into bin/ when
// ORACLEBIN has been dist/oracle since the first commit. A reader following any
// of the three believes the gate enforces a rule it does not, and the next
// person to move those two binaries back into bin/ gets no signal at all.
//
// .gitignore is held to the same rule and for the same reason: it is where a
// reader looks to find out what bin/ and dist/ are for, and it carried the
// headcount too.
func TestMakefileDescribesTheGateItHas(t *testing.T) {
	src, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	makefile := string(src)

	// What check 4 actually does, so the prose below is being held to
	// something and not to another comment.
	recipe := recipeFor(t, "static-check")
	if !strings.Contains(recipe, "dylib|so|a|framework") {
		t.Fatalf("check 4 no longer greps bin/ for a library sidecar; this test is about the prose describing that check and needs moving with it:\n%s", recipe)
	}
	if strings.Contains(recipe, "wc -l") {
		t.Fatalf("check 4 counts the entries in bin/ again; if that is deliberate the comments this test guards have to come back too:\n%s", recipe)
	}

	// The tool binaries the two dist/ notes are about.
	for _, v := range []string{"ORACLEBIN", "LINUXBIN"} {
		if got := makeVar(t, makefile, v); !strings.HasPrefix(got, "dist/") {
			t.Errorf("%s is %q; the comment above it explains why it is not in bin/, so it had better not be", v, got)
		}
	}

	gitignore, err := os.ReadFile(".gitignore")
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}

	for _, file := range []struct{ name, src string }{
		{"the Makefile", makefile},
		{".gitignore", string(gitignore)},
	} {
		for _, claim := range []struct{ phrase, why string }{
			{"the files in bin", "check 4 does not count anything; it greps for a .dylib, .so, .a or .framework"},
			{"exactly one file", "check 4 lets a second binary sit in bin/, so this is not the reason anything is built into dist/"},
			{"gate counts", "the static gate counts nothing; check 4 greps bin/ for a library sidecar"},
			{"bin/oracle", "ORACLEBIN is dist/oracle and always has been"},
		} {
			if strings.Contains(file.src, claim.phrase) {
				t.Errorf("%s says %q: %s", file.name, claim.phrase, claim.why)
			}
		}
	}
}

// makeVar returns the right hand side of a `NAME ?= value` or `NAME := value`
// assignment in the Makefile.
func makeVar(t *testing.T, makefile, name string) string {
	t.Helper()

	for _, line := range strings.Split(makefile, "\n") {
		rest, ok := strings.CutPrefix(line, name)
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		for _, op := range []string{"?=", ":=", "="} {
			if v, ok := strings.CutPrefix(rest, op); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	t.Fatalf("no assignment to %s in the Makefile", name)
	return ""
}
