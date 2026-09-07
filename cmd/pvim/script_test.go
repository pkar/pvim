package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pkar/pvim/internal/key"
)

// TestReadScriptDecodesLikeTheOracle. A -s file is raw bytes and an Escape at
// the end of it is an Escape, which is the one thing a decoder that waits for
// more input gets wrong and the reason every case in testdata/keys ends with
// one.
func TestReadScriptDecodesLikeTheOracle(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "keys")
	if err := os.WriteFile(name, []byte("iab\x1b"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readScript(name)
	if err != nil {
		t.Fatal(err)
	}
	want := []key.Key{key.Rune('i'), key.Rune('a'), key.Rune('b'), {Special: key.KeyEsc}}
	if len(got) != len(want) {
		t.Fatalf("decoded %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("key %d is %v, want %v", i, got[i], want[i])
		}
	}
}

func TestReadScriptReportsAMissingFile(t *testing.T) {
	if _, err := readScript(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("a script that is not there read without an error")
	}
}

// TestPlayScriptStopsAtAQuitAndNotAtAPrompt. errMoreInput is the frontend
// saying a prompt swallowed the key and wants another, which is what the rest
// of the script is for; anything else non-nil ends the script where it stands.
func TestPlayScriptStopsAtAQuitAndNotAtAPrompt(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name   string
		answer map[rune]error
		typed  string
		want   error
	}{
		{"every key taken", nil, "abc", nil},
		{"a prompt in the middle", map[rune]error{'b': errMoreInput}, "abc", nil},
		{"a quit halfway", map[rune]error{'b': errQuit}, "ab", errQuit},
		{"a failure halfway", map[rune]error{'b': boom}, "ab", boom},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var typed string
			err := playScript([]key.Key{key.Rune('a'), key.Rune('b'), key.Rune('c')}, func(k key.Key) error {
				typed += string(k.Rune)
				return c.answer[k.Rune]
			})
			if !errors.Is(err, c.want) {
				t.Errorf("playScript returned %v, want %v", err, c.want)
			}
			if typed != c.typed {
				t.Errorf("typed %q, want %q", typed, c.typed)
			}
		})
	}
}

// TestWindowSmoke is later work's smoke gate: a real AppKit window with a real
// buffer behind it, keys through the real editor goroutine, and an exit status.
//
// It is behind an environment variable for the same reason
// internal/clip's pasteboard round trip is: it takes over the display of
// whoever is running the suite. A window that opens and closes in the middle of
// `make check` is not a failure but it is a surprise, and over ssh, where
// AppKit's sharedApplication comes back nil, it is a failure that says nothing
// about this editor. So `make check` does not open windows and this line does:
//
//	PVIM_WINDOW_SMOKE=1 go test -run TestWindowSmoke ./cmd/pvim/
//
// What it proves that no other test in the tree can: that internal/gui, the
// pump, the editor and internal/screen are wired to each other and not merely
// to fakeClient. The assertion is the buffer on disk, and it is the buffer
// --oracle writes from the identical script with no window at all, so a window
// that types into the wrong buffer, drops a key or answers a prompt with the
// wrong one fails here by content rather than by exit status.
func TestWindowSmoke(t *testing.T) {
	if os.Getenv("PVIM_WINDOW_SMOKE") != "1" {
		t.Skip("opens a window on somebody's display; set PVIM_WINDOW_SMOKE=1")
	}
	dir := shortDir(t)
	bin := filepath.Join(dir, "pvim")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("building pvim: %v\n%s", err, out)
	}

	// A script with an insert, a motion, an operator and a prompt in it. The
	// prompt is the interesting one: ":6,3d" asks "Backwards range given, OK to
	// swap (y/n)?" and the "y" after it is read by the pump's getch from inside
	// the handler that is holding the run loop, which is the coroutine
	// arrangement window.go exists for.
	const script = "ggOtop\x1b:6,3d\ry:$\rAend\x1b:wq\r"
	keys := filepath.Join(dir, "keys")
	if err := os.WriteFile(keys, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	const before = "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\n"
	run := func(name string, args ...string) string {
		t.Helper()
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte(before), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bin, append(args, file)...)
		var stdOut, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdOut, &errOut
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: %v\nstderr: %s", name, err, errOut.Bytes())
		}
		if errOut.Len() != 0 {
			t.Errorf("%s wrote to stderr: %s", name, errOut.Bytes())
		}
		if stdOut.Len() != 0 {
			t.Errorf("%s wrote to stdout: %s", name, stdOut.Bytes())
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// --new so the socket in the runner's own ~/.cache/vim is not consulted:
	// this must be its own editor and its own window, not a tab in whatever
	// was already up.
	window := run("window.txt", "--clean", "--new", "-g", "-s", keys)
	headless := run("headless.txt", "--clean", "--oracle", "-s", keys)
	if window != headless {
		t.Errorf("the window wrote\n%q\nand --oracle wrote\n%q", window, headless)
	}
	const want = "top\nline 1\nline 6end\n"
	if window != want {
		t.Errorf("the window wrote\n%q\nwant\n%q", window, want)
	}
}
