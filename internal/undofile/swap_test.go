package undofile

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func lines(ss ...string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

func text(ls [][]byte) string {
	var b strings.Builder
	for i, l := range ls {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.Write(l)
	}
	return b.String()
}

// newSwap opens a swap file over a real file in a temp directory and returns
// both. The file exists because half of what a swap file records is about it.
func newSwap(t *testing.T, content string) (*Swap, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	meta := SwapMeta{File: file, Size: st.Size(), Mtime: st.ModTime(), SHA: Digest([]byte(content))}
	path, err := SwapName(dir+"//", file)
	if err != nil {
		t.Fatal(err)
	}
	s, err := CreateSwap(path, meta, splitLines(content), false, Pos{Line: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Remove() })
	return s, file
}

func splitLines(s string) [][]byte {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return bytes.Split([]byte(s), []byte("\n"))
}

func TestSwapRoundTrip(t *testing.T) {
	s, file := newSwap(t, "alpha\nbravo\ncharlie\n")
	if err := s.Sync(lines("alpha", "BRAVO", "charlie"), false, Pos{Line: 2, Col: 3}); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(lines("alpha", "BRAVO", "charlie", "delta"), false, Pos{Line: 4}); err != nil {
		t.Fatal(err)
	}

	info, rec, err := ReadSwap(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := text(rec.Lines); got != "alpha\nBRAVO\ncharlie\ndelta" {
		t.Errorf("recovered %q", got)
	}
	if rec.Cursor != (Pos{Line: 4}) {
		t.Errorf("cursor %+v", rec.Cursor)
	}
	if rec.Truncated {
		t.Error("a complete swap file was called truncated")
	}
	if info.File != file {
		t.Errorf("file %q, want %q", info.File, file)
	}
	if !info.Modified {
		t.Error("modified is false with two unsaved changes in the log")
	}
	if info.PID != os.Getpid() {
		t.Errorf("pid %d, want %d", info.PID, os.Getpid())
	}
	if !info.Running {
		t.Error("the process that wrote it is this one and it is running")
	}
}

// A sync that changes nothing writes nothing. This is what makes the swap file
// callable from the key loop: every motion, every failed search and every
// ":set" goes through it.
func TestSwapWritesNothingForNoChange(t *testing.T) {
	s, _ := newSwap(t, "alpha\nbravo\n")
	before, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if err := s.Sync(lines("alpha", "bravo"), false, Pos{Line: 1, Col: i}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Errorf("50 syncs of an unchanged buffer grew the swap file from %d to %d bytes",
			before.Size(), after.Size())
	}
}

// One line changed in a long file writes one line, not the file. The delta is
// the reason this is affordable on the 40k-line log in the corpus.
func TestSwapWritesOnlyTheChange(t *testing.T) {
	var ls [][]byte
	for i := 0; i < 2000; i++ {
		ls = append(ls, []byte("line "+strconv.Itoa(i)+" of a long file"))
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(file, Join(ls, false), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := SwapName(dir+"//", file)
	if err != nil {
		t.Fatal(err)
	}
	s, err := CreateSwap(path, SwapMeta{File: file}, ls, false, Pos{Line: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Remove()

	full, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := append([][]byte(nil), ls...)
	changed[1000] = []byte("x")
	if err := s.Sync(changed, false, Pos{Line: 1001}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if grew := after.Size() - full.Size(); grew > 64 {
		t.Errorf("one changed line grew the swap file by %d bytes", grew)
	}
	_, rec, err := ReadSwap(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(rec.Lines[1000]) != "x" || len(rec.Lines) != 2000 {
		t.Errorf("replay gave %d lines with %q at 1000", len(rec.Lines), rec.Lines[1000])
	}
}

// A log that has grown past a few copies of the buffer starts again with a
// snapshot, so an afternoon of ":%s" does not leave a swap file bigger than the
// undo history.
func TestSwapSnapshotsWhenTheLogGrows(t *testing.T) {
	s, _ := newSwap(t, "alpha\nbravo\ncharlie\n")
	for i := 0; i < 200; i++ {
		ls := lines("alpha", "bravo", "charlie", strconv.Itoa(i))
		if err := s.Sync(ls, false, Pos{Line: 4}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	// Four short lines is about 30 bytes; a log that never snapshotted would be
	// 200 records of it plus headers. The bound is loose on purpose: what is
	// being tested is that there is a bound.
	if st.Size() > 4096 {
		t.Errorf("swap file is %d bytes after 200 one-line changes", st.Size())
	}
	_, rec, err := ReadSwap(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := text(rec.Lines); got != "alpha\nbravo\ncharlie\n199" {
		t.Errorf("recovered %q", got)
	}
}

// ":w" takes "modified: YES" off the E325 message, without the header being
// rewritten.
func TestSwapSavedClearsModified(t *testing.T) {
	s, file := newSwap(t, "alpha\n")
	if err := s.Sync(lines("alpha", "bravo"), false, Pos{Line: 2}); err != nil {
		t.Fatal(err)
	}
	if !s.Modified() {
		t.Fatal("not modified after a change")
	}
	content := Join(lines("alpha", "bravo"), false)
	if err := os.WriteFile(file, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Saved(SwapMeta{File: file, Size: int64(len(content)), SHA: Digest(content)}); err != nil {
		t.Fatal(err)
	}
	if s.Modified() {
		t.Error("still modified after the write")
	}
	info, _, err := ReadSwap(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Modified {
		t.Error("the swap file on the disk still says modified after a :w")
	}
}

// The final newline. A file without one that comes back with one has grown a
// byte nobody typed, and recovery is exactly when nobody would notice.
func TestSwapKeepsTheMissingNewline(t *testing.T) {
	s, _ := newSwap(t, "alpha\nbravo")
	if err := s.Sync(lines("alpha", "bravo", "charlie"), true, Pos{Line: 3}); err != nil {
		t.Fatal(err)
	}
	_, rec, err := ReadSwap(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !rec.NoEOL {
		t.Error("the missing final newline was not recovered")
	}
	if got := string(Join(rec.Lines, rec.NoEOL)); got != "alpha\nbravo\ncharlie" {
		t.Errorf("recovered %q", got)
	}
}

// Two editors on one file: the second gets its own swap file rather than
// writing into the first one's, which is what vim's ".swo" is for.
func TestSwapWillNotReuseALiveOne(t *testing.T) {
	s, file := newSwap(t, "alpha\n")
	second, err := CreateSwap(s.Path(), SwapMeta{File: file}, lines("alpha"), false, Pos{Line: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Remove()
	if second.Path() == s.Path() {
		t.Fatal("the second editor opened the first one's swap file")
	}
	if !strings.HasSuffix(second.Path(), ".swo") {
		t.Errorf("second swap file is %q, want a .swo", second.Path())
	}
}

// The one that matters: a kill -9 in the middle of a write leaves a torn record
// at the end, and everything before it is still readable. Not simulated -- the
// bytes are truncated at every offset in turn, which is every way a write can
// be cut short.
func TestSwapSurvivesATornTail(t *testing.T) {
	s, _ := newSwap(t, "alpha\nbravo\n")
	if err := s.Sync(lines("alpha", "bravo", "charlie"), false, Pos{Line: 3}); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(lines("alpha", "bravo", "charlie", "delta"), false, Pos{Line: 4}); err != nil {
		t.Fatal(err)
	}
	whole, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	torn := filepath.Join(t.TempDir(), "torn.swp")
	sawTorn := false
	for n := 1; n <= len(whole); n++ {
		if err := os.WriteFile(torn, whole[:n], 0o600); err != nil {
			t.Fatal(err)
		}
		_, rec, err := ReadSwap(torn)
		if err != nil {
			// Everything shorter than the header plus one whole record is
			// unreadable, and that is honest: nothing was ever synced.
			continue
		}
		got := text(rec.Lines)
		switch got {
		case "alpha\nbravo", "alpha\nbravo\ncharlie", "alpha\nbravo\ncharlie\ndelta":
		default:
			t.Fatalf("truncated at %d bytes recovered %q, which was never in the buffer", n, got)
		}
		// Truncated is false when the cut happened to land exactly on a
		// record boundary, which is a complete file one sync short and not a
		// torn one. Everywhere else it has to be true.
		sawTorn = sawTorn || rec.Truncated
	}
	if !sawTorn {
		t.Error("no truncation was ever reported")
	}
}

// A vim swap file is not readable here and must not be silently ignored: it
// means somebody may be editing this file, which is the whole point of E325.
func TestInspectAForeignSwapFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "%tmp%f.txt.swp")
	// The first bytes of a real vim swap file: b0_id is "b0VIM 9.2".
	if err := os.WriteFile(path, append([]byte("b0VIM 9.2\x00"), make([]byte, 4086)...), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect refused a vim swap file: %v", err)
	}
	if !info.Foreign {
		t.Error("a vim swap file was not marked foreign")
	}
	if info.Unreadable == "" {
		t.Error("no clause to put in the message in place of the header")
	}
	msg := info.Attention(filepath.Join(dir, "f.txt"))
	if !strings.Contains(msg, "E325: ATTENTION") || !strings.Contains(msg, info.Unreadable) {
		t.Errorf("message does not say what is wrong:\n%s", msg)
	}
}

func TestReadSwapRefusesRubbish(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.swp")
	if err := os.WriteFile(path, []byte("pvimswap"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadSwap(path); err == nil {
		t.Error("a header with no version was accepted")
	}
	if err := os.WriteFile(path, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadSwap(path); !errors.Is(err, ErrMagic) {
		t.Errorf("a foreign file gave %v, want ErrMagic", err)
	}
}

// TestSwapSurvivesARealKill kills a real process in the middle of a real edit
// and recovers the buffer from what it left on the disk.
//
// A child process of this test binary opens a swap file, syncs a buffer into it
// one line at a time, says it is ready and then waits. The parent SIGKILLs it --
// no signal handler, no defer, no flush, exactly what a kernel OOM kill or a
// power button does -- and then reads the swap file back. Nothing about it is
// simulated: the point of a swap file is what happens to the bytes on the disk
// when the process holding them stops existing, and a fake kill would test the
// test.
//
// The child exits by itself after thirty seconds so that a parent that dies
// first cannot leave one behind, and the parent waits for it either way.
func TestSwapSurvivesARealKill(t *testing.T) {
	if os.Getenv(killChildEnv) != "" {
		t.Skip("this process is the child")
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestSwapKillChild", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), killChildEnv+"="+dir)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The child is killed and waited for whatever happens below, including a
	// t.Fatal: an orphan holding a swap file open is exactly the mess this
	// package exists to clean up after.
	killed := false
	defer func() {
		if !killed {
			cmd.Process.Kill()
		}
		cmd.Wait()
	}()

	ready := filepath.Join(dir, "ready")
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the child never became ready")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	killed = true
	state, err := cmd.Process.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if state.ExitCode() != -1 {
		t.Errorf("the child exited with %d rather than being killed", state.ExitCode())
	}

	swap := filepath.Join(dir, "f.txt.swp")
	info, rec, err := ReadSwap(swap)
	if err != nil {
		t.Fatalf("reading the swap file the killed process left: %v", err)
	}
	// The child wrote lines "0" through "99" one at a time and never wrote the
	// file itself, so what comes back has to be a prefix of that and has to end
	// where the last completed sync ended.
	if len(rec.Lines) < 2 {
		t.Fatalf("recovered %d lines; the child had synced at least ten", len(rec.Lines))
	}
	for i, l := range rec.Lines {
		if want := strconv.Itoa(i); string(l) != want {
			t.Fatalf("recovered line %d is %q, want %q", i+1, l, want)
		}
	}
	if !info.Modified {
		t.Error("the recovered buffer was never written to the file, so it is modified")
	}
	if info.PID != cmd.Process.Pid {
		t.Errorf("swap file names process %d, the killed one was %d", info.PID, cmd.Process.Pid)
	}
	if info.Running {
		t.Error("the killed process is reported as still running")
	}
	// And the E325 the next launch would print names it as a crash rather than
	// as a second editor, which is the difference the prompt turns on.
	msg := info.Attention(filepath.Join(dir, "f.txt"))
	if strings.Contains(msg, "(STILL RUNNING)") {
		t.Errorf("the message says the killed process is still running:\n%s", msg)
	}
	if !strings.Contains(msg, "(D)elete it") {
		t.Errorf("a dead process's swap file must offer (D)elete it:\n%s", msg)
	}
	if err := os.Remove(swap); err != nil {
		t.Error(err)
	}
}

// killChildEnv is how the child of TestSwapSurvivesARealKill is told it is one,
// and where to write.
const killChildEnv = "PVIM_SWAP_KILL_DIR"

// TestSwapKillChild is the child process. It is a test so that the test binary
// can be its own helper, and it does nothing at all unless the environment says
// it is the child.
func TestSwapKillChild(t *testing.T) {
	dir := os.Getenv(killChildEnv)
	if dir == "" {
		t.Skip("run by TestSwapSurvivesARealKill, with " + killChildEnv + " set")
	}
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := CreateSwap(filepath.Join(dir, "f.txt.swp"), SwapMeta{File: file}, nil, false, Pos{Line: 1})
	if err != nil {
		t.Fatal(err)
	}
	var ls [][]byte
	for i := 0; i < 100; i++ {
		ls = append(ls, []byte(strconv.Itoa(i)))
		if err := s.Sync(ls, false, Pos{Line: i + 1}); err != nil {
			t.Fatal(err)
		}
		if i == 9 {
			if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Waiting to be killed. The sleep is the upper bound on how long this
	// process can outlive a parent that died before it could kill anything.
	time.Sleep(30 * time.Second)
}
