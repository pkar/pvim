// Package internal holds no code. It exists so that the architecture rules in
// doc.go have somewhere to live as a test, because go vet cannot express any of
// them and a comment saying "text imports nothing" is worth nothing at all.
//
// Every check here shells out to go list. That is deliberate: parsing imports
// out of the source with go/parser gets build tags wrong, misses the
// CGO_ENABLED-dependent file sets that are the entire point of the first check,
// and would be a second implementation of the thing the toolchain already
// knows. Slow and right beats fast and approximately right for a rule that only
// has to hold at commit time.
package internal

import (
	"encoding/json"
	"io/fs"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/tui"
)

// mod is this module's path. Everything under it is ours to have opinions about
// and everything else is the standard library or a pinned dependency.
const mod = "github.com/pkar/pvim"

// runtimeCgo is the one package in the graph allowed to carry cgo files. It is
// here because purego imports it from a file behind a //go:build cgo tag, to
// get C thread-local storage set up when someone does build with cgo; the
// CGO_ENABLED=0 build this project ships sees neither that file nor this
// package.
const runtimeCgo = "runtime/cgo"

// pkg is the slice of `go list -json` these tests read. The fields are named
// exactly as go list emits them.
type pkg struct {
	ImportPath string
	Dir        string
	CgoFiles   []string
	Imports    []string
	Deps       []string
}

// graph is every package in the build, keyed by import path: the packages under
// ./... and everything they transitively import.
type graph map[string]pkg

// list runs go list once and returns the build graph it printed.
//
// cgo is the CGO_ENABLED value to measure under, and it is a parameter rather
// than a constant because the answer depends on it: a package's file set
// changes with the setting, which is the whole subject of TestNoCgoInTheBinary
// and something every other check here wants held at 0.
func list(t *testing.T, cgo string, args ...string) graph {
	t.Helper()

	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = ".." // the tests live in internal/, go list wants the module root
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED="+cgo)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("go list (CGO_ENABLED=%s): %v\n%s", cgo, err, stderr)
	}

	g := graph{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decoding go list output: %v", err)
		}
		g[p.ImportPath] = p
	}
	if len(g) == 0 {
		t.Fatal("go list returned no packages")
	}
	return g
}

// trackSources makes every source directory in this module a cache input of the
// test binary, and returns the directories it walked.
//
// Without it these tests are the one thing an architecture test may not be:
// cached and wrong. go test serves a cached result when nothing the test read
// has changed, and what these tests read is the output of an exec'd go list,
// which the cache knows nothing about. So an `ok internal (cached)` survived a
// leaf package growing an import of screen, and make check went green on a tree
// that violated the layering. Walking the tree records each directory's listing
// -- the names, sizes and mtimes of its entries -- through the same hook the
// cache reads, so a file added, deleted or edited anywhere under the module
// invalidates the result and the rules get asked again.
func trackSources(t *testing.T) []string {
	t.Helper()

	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving the module root: %v", err)
	}
	var dirs []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		// .git churns on every command and none of it is Go; bin, dist and
		// testdata hold output and fixtures, and no package lives in them.
		if path != root {
			switch name := d.Name(); {
			case strings.HasPrefix(name, "."), name == "bin", name == "dist", name == "testdata":
				return filepath.SkipDir
			}
		}
		dirs = append(dirs, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return dirs
}

// load runs go list once and returns the whole build graph.
//
// CGO_ENABLED=0 is set because the file set a package presents depends on it,
// and a check that only holds under the default toolchain settings is not the
// check make static-check runs.
func load(t *testing.T) graph {
	t.Helper()
	trackSources(t)
	return list(t, "0", "-deps", "-json", "./...")
}

// ours reports the import paths in g that belong to this module.
func (g graph) ours() []string {
	var paths []string
	for path := range g {
		if path == mod || strings.HasPrefix(path, mod+"/") {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

// need returns the package at path, or skips the test when it does not exist
// yet. The tree grows a package per phase and a rule about a package nobody has
// written is not a failure.
func (g graph) need(t *testing.T, path string) pkg {
	t.Helper()
	p, ok := g[path]
	if !ok {
		t.Skipf("%s does not exist yet", path)
	}
	return p
}

// cgoCarriers returns the packages in pkgPath's dependency graph that carry cgo
// files, measured with cgo ENABLED.
//
// Enabled is the only setting under which the question means anything. A file
// that imports "C" carries an implicit cgo build constraint, so at
// CGO_ENABLED=0 the go tool drops it from the package's file set and every
// package in every graph reports an empty CgoFiles list. Asking there answers
// itself, which is what make static-check's first check says in its own comment
// and why it measures the same way this does.
func cgoCarriers(t *testing.T, pkgPath string) []string {
	t.Helper()

	var carriers []string
	for path, p := range list(t, "1", "-deps", "-json", pkgPath) {
		if len(p.CgoFiles) > 0 {
			carriers = append(carriers, path)
		}
	}
	sort.Strings(carriers)
	return carriers
}

// TestNoCgoInTheBinary is the first of the four static checks, run here as well
// as in make static-check so that a cgo dependency fails a plain `go test` and
// not only the release build.
func TestNoCgoInTheBinary(t *testing.T) {
	trackSources(t)

	for _, path := range cgoCarriers(t, mod+"/cmd/pvim") {
		if path == runtimeCgo {
			continue
		}
		t.Errorf("%s has cgo files and is in cmd/pvim's dependency graph", path)
	}
}

// TestTheCgoCheckCanSeeCgo is the test for the test above.
//
// runtime/cgo carries exactly one cgo file, cgo.go, whenever the toolchain has
// cgo on, so a measurement that reports nothing in its graph is a measurement
// that cannot see cgo at all. Asked with CGO_ENABLED=0, which is how the check
// above used to ask, that is exactly what happens: the list comes back empty
// for every package alive, the loop above runs zero times and passes with a
// cgo-carrying dependency sitting in the binary.
func TestTheCgoCheckCanSeeCgo(t *testing.T) {
	if got := cgoCarriers(t, runtimeCgo); len(got) == 0 {
		t.Fatalf("no cgo files reported in %s's own graph: the measurement cannot see cgo, so TestNoCgoInTheBinary is quiet and not green", runtimeCgo)
	}
}

// TestSourcesCoverEveryPackage keeps the cache correct. Every package in the
// module has to sit under a directory trackSources walked, or an edit to it is
// invisible to the go test cache and the rule about it is decided from a result
// computed before the edit existed.
func TestSourcesCoverEveryPackage(t *testing.T) {
	walked := map[string]bool{}
	for _, dir := range trackSources(t) {
		walked[resolve(t, dir)] = true
	}

	g := load(t)
	for _, path := range g.ours() {
		if dir := g[path].Dir; !walked[resolve(t, dir)] {
			t.Errorf("%s lives in %s, which the source walk never read; an edit there would be served from cache", path, dir)
		}
	}
}

// resolve makes two paths comparable. go list prints the directory it found and
// the walk prints the one it opened, and on macOS those differ by /tmp being a
// symlink whenever the module is checked out under one.
func resolve(t *testing.T, path string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolving %s: %v", path, err)
	}
	return got
}

// TestRegexpIsFencedIn keeps the standard library's regexp behind
// internal/regex. Vim's dialect is translated into RE2 syntax in exactly one
// place; a second package reaching for regexp means a pattern is being compiled
// somewhere the translator never saw it, and vim's dialect quietly means
// something else.
//
// Direct imports only. A test file may import regexp anywhere, which is why
// only Imports is read and not TestImports.
func TestRegexpIsFencedIn(t *testing.T) {
	g := load(t)
	const allowed = mod + "/internal/regex"

	for _, path := range g.ours() {
		if path == allowed {
			continue
		}
		for _, imp := range g[path].Imports {
			if imp == "regexp" || imp == "regexp/syntax" {
				t.Errorf("%s imports %s; only %s and _test.go files may", path, imp, allowed)
			}
		}
	}
}

// TestLeavesAreLeaves holds internal/text and internal/regex at the bottom of
// the tree. Both are pure functions over bytes with a fuzzer and a table of
// vim's own test cases behind them, and both stay testable in isolation only
// for as long as neither can reach an option, a window or a mode.
func TestLeavesAreLeaves(t *testing.T) {
	// internal/options joined the list . It is the option table and
	// nothing else, and it has to stay that way: every layer above it reads
	// options, so an import in the other direction would put a cycle through
	// the middle of the editor. It is also what makes an option a typed field
	// rather than a map entry -- a leaf cannot ask anything a question, so a
	// field here is a field somebody above has to read.
	for _, leaf := range []string{mod + "/internal/text", mod + "/internal/regex", mod + "/internal/options"} {
		t.Run(leaf, func(t *testing.T) {
			g := load(t)
			p := g.need(t, leaf)
			for _, dep := range p.Deps {
				if strings.HasPrefix(dep, mod+"/") {
					t.Errorf("%s imports %s; it must import nothing else in this module", leaf, dep)
				}
			}
		})
	}
}

// TestScreenKnowsNoPixels keeps the grid model above both frontends. screen
// decides what character goes in what cell; raster decides what that looks like
// and gui and tui decide where it goes. If screen can see any of the three it
// will eventually ask one of them a question, and then the editor stops being
// runnable in a test with no display.
func TestScreenKnowsNoPixels(t *testing.T) {
	g := load(t)
	p := g.need(t, mod+"/internal/screen")

	for _, dep := range p.Deps {
		switch dep {
		case mod + "/internal/raster", mod + "/internal/gui", mod + "/internal/tui":
			t.Errorf("internal/screen imports %s; the grid model knows nothing below it", dep)
		}
	}
}

// frontend is the pair of packages allowed to touch the machine directly:
// internal/gui calls AppKit through purego and internal/clip the pasteboard
// behind it. Both are darwin-only and neither is in a test that runs without a
// window server.
var frontend = []string{mod + "/internal/gui", mod + "/internal/clip"}

// importersOutsideFrontend returns the packages of this module, other than the
// two frontend ones, that directly import imp or a package under it, as
// "importer imports import" strings ready to put in a failure message.
//
// Direct imports, because cmd/pvim necessarily reaches purego through gui and
// that is the arrangement working, not the rule breaking.
func (g graph) importersOutsideFrontend(imp string) []string {
	var found []string
	for _, path := range g.ours() {
		if underAny(path, frontend) {
			continue
		}
		for _, other := range g[path].Imports {
			if other == imp || strings.HasPrefix(other, imp+"/") {
				found = append(found, path+" imports "+other)
			}
		}
	}
	sort.Strings(found)
	return found
}

// underAny reports whether path is one of prefixes or a package under one.
func underAny(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// TestPuregoStaysInTheFrontend confines the one unsafe dependency in the tree.
//
// purego is how AppKit gets called without a C compiler and it is the single
// biggest risk in the design. Keeping it to internal/gui and internal/clip is
// what makes a failure there cost a window rather than the editor: every other
// package builds, tests and runs on a box with no window server.
func TestPuregoStaysInTheFrontend(t *testing.T) {
	g := load(t)

	for _, bad := range g.importersOutsideFrontend("github.com/ebitengine/purego") {
		t.Errorf("%s; purego belongs to internal/gui and internal/clip", bad)
	}
}

// TestUnsafeStaysInTheFrontend is the other half of the same rule, and the design
// states them in one sentence.
//
// The two frontend packages need unsafe to hand AppKit a pointer to a Go slice
// and to read one back. Nothing else does: a byte-to-string shortcut in
// internal/text buys nanoseconds and costs the property that every package
// below the window is memory-safe Go a person can read without checking
// lifetimes. Today the rule holds because nobody has reached for it, which is
// not the same as it holding.
func TestUnsafeStaysInTheFrontend(t *testing.T) {
	g := load(t)

	for _, bad := range g.importersOutsideFrontend("unsafe") {
		t.Errorf("%s; unsafe belongs to internal/gui and internal/clip", bad)
	}
}

// TestImportersOutsideFrontendFindsThem is the test for the two tests above. It
// hands the check a graph with a known violation in it rather than waiting for
// one to be committed, because a rule enforced by a loop over real packages
// reads exactly the same whether it works or whether it has quietly stopped
// matching anything.
func TestImportersOutsideFrontendFindsThem(t *testing.T) {
	g := graph{
		mod + "/internal/text": {
			ImportPath: mod + "/internal/text",
			Imports:    []string{"bytes", "unsafe"},
		},
		mod + "/internal/screen": {
			ImportPath: mod + "/internal/screen",
			Imports:    []string{"github.com/ebitengine/purego/objc"},
		},
		mod + "/internal/gui": {
			ImportPath: mod + "/internal/gui",
			Imports:    []string{"unsafe", "github.com/ebitengine/purego"},
		},
		mod + "/internal/clip": {
			ImportPath: mod + "/internal/clip",
			Imports:    []string{"unsafe"},
		},
	}

	for _, tc := range []struct {
		imp  string
		want []string
	}{
		{"unsafe", []string{mod + "/internal/text imports unsafe"}},
		{"github.com/ebitengine/purego", []string{mod + "/internal/screen imports github.com/ebitengine/purego/objc"}},
	} {
		got := g.importersOutsideFrontend(tc.imp)
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("importersOutsideFrontend(%q) = %v, want %v", tc.imp, got, tc.want)
		}
	}
}

// TestRasterDoesNotImportGui keeps the arrow pointing one way. gui imports
// raster to turn a screen into pixels it can blit; raster importing gui back
// would mean the rasteriser had learned what a window is, and the golden hash
// tests would need one to run.
func TestRasterDoesNotImportGui(t *testing.T) {
	g := load(t)
	p := g.need(t, mod+"/internal/raster")

	for _, dep := range p.Deps {
		if dep == mod+"/internal/gui" || strings.HasPrefix(dep, mod+"/internal/gui/") {
			t.Errorf("internal/raster imports %s; the arrow runs gui -> raster and only that way", dep)
		}
	}
}

// TestFrontendsAreStrangers keeps tui and gui as peers. They are two clients of
// the same Grid; the day one of them imports the other, the terminal build
// starts dragging AppKit behind it and the Linux port stops being a question of
// one directory.
func TestFrontendsAreStrangers(t *testing.T) {
	g := load(t)

	for _, pair := range [][2]string{
		{mod + "/internal/tui", mod + "/internal/gui"},
		{mod + "/internal/gui", mod + "/internal/tui"},
	} {
		from, to := pair[0], pair[1]
		p, ok := g[from]
		if !ok {
			continue // not written yet
		}
		for _, dep := range p.Deps {
			if dep == to || strings.HasPrefix(dep, to+"/") {
				t.Errorf("%s imports %s; the two frontends do not know each other exists", from, dep)
			}
		}
	}
}

// modeMachine is the one-way dependency graph of a later change, written as the
// direct imports inside this module each package is allowed to have.
//
// the whole mode machine in one package. It is six here so that
// the work can be split without two people editing one file all week, and a
// split is only worth anything while the arrows point one way: internal/motion
// answers where the cursor goes given a buffer, and the day it can ask
// internal/mode what mode the editor is in, it stops being testable with a
// buffer and a position and starts needing an editor.
//
// The two edges that are not obvious, and are here on purpose:
//
// - internal/operator imports internal/motion, for motion.Kind and
// motion.Force. An operator does not take a motion, it takes the span a
// motion resolved to, and vim's inclusive, exclusive and exclusive-becomes-
// linewise rules are the resolving. They live in operator.SpanForMotion
// because that is the only place that knows the operator, the motion and
// the force at once, and putting them in internal/mode instead would bury
// the three rules that decide what every d takes inside the largest package
// in the tree.
// - internal/textobj imports internal/register, for register.Type. A text
// object is charwise or linewise, an operator acts on charwise, linewise or
// blockwise text, and a register holds the same three. One enum, in the
// package furthest down, rather than three that need converting at every
// boundary.
var modeMachine = map[string][]string{
	"internal/register": {"internal/text"},
	"internal/motion":   {"internal/text", "internal/regex"},
	"internal/textobj":  {"internal/text", "internal/register"},
	"internal/search":   {"internal/text", "internal/regex"},
	"internal/operator": {"internal/text", "internal/register", "internal/motion"},
	"internal/mode": {
		"internal/text", "internal/regex", "internal/key",
		"internal/register", "internal/motion", "internal/textobj",
		"internal/search", "internal/operator",
	},
}

// TestModeMachineLayering holds every package of a later change to the imports listed
// above. An import that is allowed but unused is fine and an import that is
// not listed is a failure, which is the direction that matters: the list is
// permission, not a description.
func TestModeMachineLayering(t *testing.T) {
	g := load(t)

	for name, allowed := range modeMachine {
		t.Run(name, func(t *testing.T) {
			path := mod + "/" + name
			p := g.need(t, path)

			ok := map[string]bool{}
			for _, a := range allowed {
				ok[mod+"/"+a] = true
			}
			for _, imp := range p.Imports {
				if !strings.HasPrefix(imp, mod+"/") {
					continue // the standard library and the pinned dependencies
				}
				if !ok[imp] {
					t.Errorf("%s imports %s, which is not on its list; if the arrow is right, add it to modeMachine and say why", path, imp)
				}
			}
		})
	}
}

// TestModeMachineKnowsNoScreen keeps a later change above the display. Every package
// in the mode machine has to run in a test with no window, no terminal and no
// grid: that is what makes cmd/oracle able to drive the editor headless, and it
// is the property that would go first if a motion could ask how wide the window
// is instead of being told.
func TestModeMachineKnowsNoScreen(t *testing.T) {
	g := load(t)
	below := []string{
		mod + "/internal/screen", mod + "/internal/raster",
		mod + "/internal/gui", mod + "/internal/tui",
	}

	for name := range modeMachine {
		path := mod + "/" + name
		p, exists := g[path]
		if !exists {
			continue
		}
		for _, dep := range p.Deps {
			if underAny(dep, below) {
				t.Errorf("%s imports %s; the mode machine has to run with no display at all", path, dep)
			}
		}
	}
}

// phase3 is the one-way dependency graph of a later change, in the same shape as
// modeMachine above: the direct imports inside this module each package is
// allowed to have.
//
// The arrows and why each one points that way:
//
// - internal/options is a leaf, like internal/text and internal/regex, and
// TestLeavesAreLeaves says so a second time. The empty list here is the
// same rule written where somebody adding a package will read it.
// - internal/window imports internal/text and internal/options and not
// internal/screen, which is the edge worth defending: a window knows how
// many cells tall it is, not what a Grid is, so the scroll arithmetic is
// testable with two integers. internal/window has its own Rect for that
// reason and internal/screen converts.
// - internal/window does not import internal/motion either, which is why it
// has a Visible type of its own that internal/ex turns into a
// motion.Window. The arrow the other way is the one that must not exist:
// a motion that could ask a window a question stops being testable with a
// buffer and a position.
// - internal/substitute imports internal/regex because ":s" is where a
// user's pattern meets the translator, and it does the ":g" line marking
// but not the ":g" command running, because running one needs internal/ex
// and that would be a cycle.
// - internal/ex is where a later change meets a later change. It is the only package
// allowed to import internal/mode and internal/window at once, which is
// what makes it the place the two cursors are reconciled and the place a
// window.Visible becomes a motion.Window.
// - internal/vimrc imports internal/ex and internal/screen but not
// internal/mode: a vimrc line changes an option, a mapping, an autocmd, a
// user command, a highlight or a g: variable, and every one of those goes
// through the ex layer or the highlight table.
// - internal/keymap imports internal/key and nothing else, and that is the
// whole of it. A mapping is keys in and keys out; it knows no buffer, no
// window and no mode-as-behaviour, only the mode letter the frontend hands
// it. The arrow that must not exist is internal/mode importing it: a
// mapping is resolved in front of the mode machine, in cmd/pvim's session,
// which is what makes a right-hand side reach another mapping and what
// keeps internal/mode ignorant that mappings exist at all.
// - internal/tui imports the same three things internal/gui does, minus
// internal/raster, because a terminal draws cells and not pixels.
var phase3 = map[string][]string{
	"internal/options":    {},
	"internal/window":     {"internal/text", "internal/options"},
	"internal/quickfix":   {"internal/text"},
	"internal/substitute": {"internal/text", "internal/regex", "internal/options"},
	"internal/ex": {
		"internal/text", "internal/regex", "internal/key",
		"internal/options", "internal/window", "internal/quickfix",
		"internal/substitute", "internal/mode", "internal/motion",
		"internal/register", "internal/search",
	},
	"internal/keymap": {"internal/key"},
	"internal/vimrc": {
		"internal/options", "internal/ex", "internal/key", "internal/screen",
	},
	"internal/tui": {"internal/screen", "internal/key", "internal/options"},
}

// TestPhase3Layering holds every package of a later change to the imports listed
// above. As with modeMachine, the list is permission and not a description: an
// allowed import that is unused is fine and an import that is not listed is a
// failure.
func TestPhase3Layering(t *testing.T) {
	g := load(t)

	for name, allowed := range phase3 {
		t.Run(name, func(t *testing.T) {
			path := mod + "/" + name
			p := g.need(t, path)

			ok := map[string]bool{}
			for _, a := range allowed {
				ok[mod+"/"+a] = true
			}
			for _, imp := range p.Imports {
				if !strings.HasPrefix(imp, mod+"/") {
					continue // the standard library and the pinned dependencies
				}
				if !ok[imp] {
					t.Errorf("%s imports %s, which is not on its list; if the arrow is right, add it to phase3 and say why", path, imp)
				}
			}
		})
	}
}

// phase4 is the one-way dependency graph of a later change, in the same shape as
// modeMachine and phase3 above: the direct imports inside this module each
// package is allowed to have.
//
// Two packages and two arrows, and the interesting one is the arrow that is not
// here.
//
// - internal/clip imports internal/register and nothing else. It fills
// register.Clipboard, the interface internal/register has expected since
// a later change, so it needs register.Value and needs it in both directions.
// internal/register does NOT import it back and must not: the clipboard is
// injected with SetClipboard, which is what lets every test in the mode
// machine and every oracle run have no clipboard at all and treat "* and "+
// as ordinary registers.
// - internal/clip also imports internal/text, for one call:
// text.DisplayWidth, which measures a block arriving from the pasteboard in
// the display columns register.Value.Width is documented in. Counting those
// columns in this package instead would mean a third copy of the width
// table internal/text and internal/screen already hold one each, and a copy
// of a generated table is the kind of thing that is right the day it lands
// and wrong two Unicode revisions later. internal/text imports nothing else
// in the module, so the arrow costs one leaf package and no AppKit.
// - internal/clip does not import internal/gui, which is the arrow the next
// test is about.
// - internal/server imports nothing in this module. It is a unix socket and a
// line of JSON; the editor is on the other side of a Handler func. That is
// what makes it testable with two ends of a pipe and no editor, and it is
// why "pvim file" from a second shell is a feature that cannot break the
// first shell's buffer.
var phase4 = map[string][]string{
	"internal/clip":   {"internal/register", "internal/text"},
	"internal/server": {},
}

// TestPhase4Layering holds a later change's packages to the imports listed above. As
// with the two maps before it, the list is permission and not a description.
func TestPhase4Layering(t *testing.T) {
	g := load(t)

	for name, allowed := range phase4 {
		t.Run(name, func(t *testing.T) {
			path := mod + "/" + name
			p := g.need(t, path)

			ok := map[string]bool{}
			for _, a := range allowed {
				ok[mod+"/"+a] = true
			}
			for _, imp := range p.Imports {
				if !strings.HasPrefix(imp, mod+"/") {
					continue // the standard library and the pinned dependencies
				}
				if !ok[imp] {
					t.Errorf("%s imports %s, which is not on its list; if the arrow is right, add it to phase4 and say why", path, imp)
				}
			}
		})
	}
}

// phase5 is the one-way dependency graph of a later change, in the same shape as the
// maps above.
//
// Two packages and no arrows at all, which is the design and not an accident.
//
// - internal/lsp imports nothing in this module. A language server client is
// a child process, a pipe and JSON-RPC; what a completion item does to a
// buffer is cmd/pvim's. The arrow that must not appear is
// internal/lsp -> internal/text: the moment this package can be handed a
// *text.Buffer it grows a method that takes one, and then the wire format
// and the editor's data structure are one thing that has to change
// together. It has its own Position for the same reason internal/window
// has its own Rect, and it has to: the protocol counts UTF-16 code units
// and internal/text counts bytes, so a shared type would be a lie in one
// of the two packages. cmd/pvim/lsp.go is the seam that converts.
// - internal/lsp must also not grow a JSON dependency. encoding/json is what
// , and the reason is the static gate: every alternative is
// either behind a cgo file or one dependency away from one, and a later change is
// where that would land without anybody noticing until "make static-check"
// went red. TestNoCgoInTheGraph is what would catch it and this comment is
// what stops it being written in the first place.
// - internal/fmt imports nothing in this module either, including
// internal/lsp. The Go row of the format-on-save table runs through the
// language server, and it reaches it through a one-method Formatter
// interface declared in internal/fmt, so the table is testable with a
// function and a package with no server at all can still format Terraform.
var phase5 = map[string][]string{
	"internal/lsp": {},
	"internal/fmt": {},
}

// TestPhase5Layering holds a later change's packages to the imports listed above. As
// with the maps before it, the list is permission and not a description.
func TestPhase5Layering(t *testing.T) {
	g := load(t)

	for name, allowed := range phase5 {
		t.Run(name, func(t *testing.T) {
			path := mod + "/" + name
			p := g.need(t, path)

			ok := map[string]bool{}
			for _, a := range allowed {
				ok[mod+"/"+a] = true
			}
			for _, imp := range p.Imports {
				if !strings.HasPrefix(imp, mod+"/") {
					continue // the standard library and the pinned dependencies
				}
				if !ok[imp] {
					t.Errorf("%s imports %s, which is not on its list; if the arrow is right, add it to phase5 and say why", path, imp)
				}
			}
		})
	}
}

// TestLspUsesEncodingJSONAndNothingElse is the the gate written as
// a test: "the LSP client is where a JSON library with a cgo dependency would
// sneak in if anything but encoding/json were used".
//
// It looks at the whole transitive graph and not at the direct imports,
// because the way this goes wrong is a helper package that looks harmless and
// pulls a codec in behind it.
func TestLspUsesEncodingJSONAndNothingElse(t *testing.T) {
	g := load(t)
	p := g.need(t, mod+"/internal/lsp")
	for _, dep := range p.Deps {
		if strings.Contains(dep, "json") && dep != "encoding/json" && dep != "encoding/json/internal" {
			if strings.HasPrefix(dep, "encoding/json") {
				continue // the standard library's own internals
			}
			t.Errorf("internal/lsp reaches %s; only encoding/json is allowed", dep)
		}
	}
}

// phase7 is the one-way dependency graph of a later change, in the same shape as the
// three maps above.
//
// One package and no arrows at all, which is the whole design of it.
// internal/undofile is the three on-disk formats -- the undo tree, the swap
// file and the command line, search, mark and jump histories -- and it imports
// nothing in this module, not even internal/text. It has its own Pos for the
// same reason internal/window has its own Rect: a format package that could
// reach a *text.Buffer would grow a method that takes one, and then the file
// format and the editor's data structure would be one thing that has to change
// together. cmd/pvim/persist.go is the seam that converts, which is a file
// somebody reads when the two disagree.
//
// The arrow that must not appear is internal/text -> internal/undofile. The
// buffer has to be serialisable without knowing it is being serialised.
//
// internal/spell is the same shape and for the same reason: a word list and
// the rules over it, with no buffer and no window in sight. It takes a line as
// a []byte and answers byte ranges, so cmd/pvim/spell.go is the seam that
// turns those into a highlight.
//
// internal/tags has no arrows either, which took one decision to keep: it
// reads a ctags file and holds the stack, and the jump to a match -- a search
// for the tags file's pattern in a buffer -- is cmd/pvim's, because that is
// the layer that has the buffer and the window. Two consequences worth
// knowing: the package tests with no buffer at all, and internal/window does
// not have to know what a tag is, since cmd/pvim keeps the per-window stacks
// in a map of its own.
var phase7 = map[string][]string{
	"internal/undofile": {},
	"internal/spell":    {},
	"internal/tags":     {},
}

// TestPhase7Layering holds a later change's package to the imports listed above. As
// with the maps before it, the list is permission and not a description.
func TestPhase7Layering(t *testing.T) {
	g := load(t)

	for name, allowed := range phase7 {
		t.Run(name, func(t *testing.T) {
			path := mod + "/" + name
			p := g.need(t, path)

			ok := map[string]bool{}
			for _, a := range allowed {
				ok[mod+"/"+a] = true
			}
			for _, imp := range p.Imports {
				if !strings.HasPrefix(imp, mod+"/") {
					continue // the standard library and the pinned dependencies
				}
				if !ok[imp] {
					t.Errorf("%s imports %s, which is not on its list; if the arrow is right, add it to phase7 and say why", path, imp)
				}
			}
		})
	}
}

// TestClipAndGuiAreStrangers is the rule that decides which way the clipboard
// call goes, and it is worth stating in full because the obvious arrangement is
// the wrong one.
//
// NSPasteboard is an AppKit object and every call to it has to happen on the
// thread internal/gui owns. The obvious way to arrange that is for
// internal/clip to import internal/gui and call a function there. Do that and
// internal/clip's dependency graph contains internal/raster, internal/screen
// and purego -- so every package that wanted a clipboard would pull the whole
// window behind it, a GOOS=linux build of the editor core would drag AppKit's
// loader, and the day a Linux backend appears it stops being a matter of one
// directory. It would also put an import of internal/register inside
// internal/gui, which is the editor leaking into the window.
//
// So the arrow is inverted and there is no arrow at all: internal/clip talks to
// NSPasteboard itself through purego, exactly as internal/gui talks to AppKit,
// and it takes the thread it must run on as a plain func value. cmd/pvim passes
// gui.OnMain. The two packages never meet, and a box with no window passes
// nothing, installs no clipboard, and gets "* and "+ as ordinary registers.
//
// Deps and not Imports, in both directions: a transitive path from clip to gui
// through a third package would drag purego just as surely as a direct one.
func TestClipAndGuiAreStrangers(t *testing.T) {
	g := load(t)

	for _, pair := range [][2]string{
		{mod + "/internal/clip", mod + "/internal/gui"},
		{mod + "/internal/gui", mod + "/internal/clip"},
	} {
		from, to := pair[0], pair[1]
		p, ok := g[from]
		if !ok {
			continue // not written yet
		}
		for _, dep := range p.Deps {
			if dep == to || strings.HasPrefix(dep, to+"/") {
				t.Errorf("%s imports %s; the pasteboard call crosses on a func value from cmd/pvim, not on an import", from, dep)
			}
		}
	}
}

// TestClipDoesNotDragTheWindowIn is the same rule stated as what it buys, and
// it is the one that would actually fail first. internal/clip is the one
// frontend-adjacent package the editor core is allowed to import, so its whole
// dependency graph is the price of a clipboard: internal/register, and nothing
// that draws.
func TestClipDoesNotDragTheWindowIn(t *testing.T) {
	g := load(t)
	p := g.need(t, mod+"/internal/clip")

	below := []string{
		mod + "/internal/raster", mod + "/internal/gui",
		mod + "/internal/tui", mod + "/internal/screen",
	}
	for _, dep := range p.Deps {
		if underAny(dep, below) {
			t.Errorf("internal/clip imports %s; a package that wanted a clipboard would get that too", dep)
		}
	}
}

// TestServerKnowsNoEditor keeps the socket a socket. internal/server decodes a
// line of JSON and hands it to a Handler; the editor is on the other side of
// that func and nowhere inside this package, which is what lets both ends of
// the protocol be tested over a pipe with no buffer, no window and no vim.
func TestServerKnowsNoEditor(t *testing.T) {
	g := load(t)
	p := g.need(t, mod+"/internal/server")

	for _, dep := range p.Deps {
		if strings.HasPrefix(dep, mod+"/") {
			t.Errorf("internal/server imports %s; the editor reaches it through a Handler and not the other way", dep)
		}
	}
}

// TestEditorCoreKnowsNoFrontend keeps everything below the display above it.
//
// internal/options, internal/window, internal/quickfix, internal/substitute
// and internal/ex all have to run in a test with no window, no terminal and no
// grid, for the same reason the mode machine does: that is what lets
// cmd/oracle drive the whole editor headless and diff it against vim. A window
// that could ask how wide it is drawn would take that away.
//
// internal/vimrc is not in the list: it imports internal/screen on purpose,
// because a "hi" line is a highlight and the highlight table lives there.
func TestEditorCoreKnowsNoFrontend(t *testing.T) {
	g := load(t)
	below := []string{
		mod + "/internal/raster", mod + "/internal/gui", mod + "/internal/tui",
	}
	core := []string{
		mod + "/internal/options", mod + "/internal/window",
		mod + "/internal/quickfix", mod + "/internal/substitute",
		mod + "/internal/ex",
	}

	for _, path := range core {
		p, exists := g[path]
		if !exists {
			continue
		}
		for _, dep := range p.Deps {
			if underAny(dep, below) {
				t.Errorf("%s imports %s; the editor core has to run with no display at all", path, dep)
			}
			if dep == mod+"/internal/screen" && path != mod+"/internal/ex" {
				t.Errorf("%s imports internal/screen; only the layers that draw need the grid", path)
			}
		}
	}
}

// TestFrontendVocabulariesMatch is the check that keeps internal/tui and
// internal/gui peers rather than divergent.
//
// The two define the same event vocabulary and the same Client contract, name
// for name, and they define it twice because neither may import the other and
// a shared package would have to sit under both. Two copies of four structs is
// the cost; this test is what stops it becoming two different editors. It uses
// reflection rather than reading the source, so a field renamed on one side
// fails here and not in a frontend nobody has run this month.
//
// PasteEvent is the one deliberate asymmetry and is listed as such: a paste
// reaches the window through the pasteboard and the terminal through
// bracketed-paste bytes.
func TestFrontendVocabulariesMatch(t *testing.T) {
	for _, pair := range []struct {
		name     string
		gui, tui reflect.Type
	}{
		{"KeyEvent", reflect.TypeOf(gui.KeyEvent{}), reflect.TypeOf(tui.KeyEvent{})},
		{"MouseEvent", reflect.TypeOf(gui.MouseEvent{}), reflect.TypeOf(tui.MouseEvent{})},
		{"ResizeEvent", reflect.TypeOf(gui.ResizeEvent{}), reflect.TypeOf(tui.ResizeEvent{})},
		{"CloseEvent", reflect.TypeOf(gui.CloseEvent{}), reflect.TypeOf(tui.CloseEvent{})},
	} {
		t.Run(pair.name, func(t *testing.T) {
			if pair.gui.NumField() != pair.tui.NumField() {
				t.Fatalf("gui.%s has %d fields and tui.%s has %d", pair.name, pair.gui.NumField(), pair.name, pair.tui.NumField())
			}
			for i := 0; i < pair.gui.NumField(); i++ {
				a, b := pair.gui.Field(i), pair.tui.Field(i)
				if a.Name != b.Name {
					t.Errorf("field %d is %q in gui and %q in tui", i, a.Name, b.Name)
				}
				if a.Type.Kind() != b.Type.Kind() {
					t.Errorf("field %s is %s in gui and %s in tui", a.Name, a.Type, b.Type)
				}
			}
		})
	}

	// The Client contract, which is what the editor is written against.
	guiClient := reflect.TypeOf((*gui.Client)(nil)).Elem()
	tuiClient := reflect.TypeOf((*tui.Client)(nil)).Elem()
	if guiClient.NumMethod() != tuiClient.NumMethod() {
		t.Fatalf("gui.Client has %d methods and tui.Client has %d", guiClient.NumMethod(), tuiClient.NumMethod())
	}
	for i := 0; i < guiClient.NumMethod(); i++ {
		if a, b := guiClient.Method(i).Name, tuiClient.Method(i).Name; a != b {
			t.Errorf("method %d is %q on gui.Client and %q on tui.Client", i, a, b)
		}
	}
}
