//go:build darwin || linux

package tui

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// signalChildEnv marks the re-executed test binary as the child half of
// TestAFatalSignalWaitsForTheRestore.
//
// A subprocess is the only honest way to test this. The thing being asserted
// is that the process does not die in the middle of putting the terminal back,
// and a test that asserts it in its own process asserts it by dying.
const signalChildEnv = "PVIM_TUI_SIGNAL_CHILD"

// The two lines the child prints around its restore. The parent sends the
// SIGTERM when it reads the first one and asks whether it ever read the
// second.
const (
	restoreStartLine = "restore-start"
	restoreDoneLine  = "restore-done"
)

// restoreWindow is how long the child's restore takes. Long enough that the
// parent's signal lands inside it every time, which is the point of the test,
// and short enough that three runs are a second.
const restoreWindow = 250 * time.Millisecond

// slowRestoreDriver is a driver whose restore announces itself, takes its
// time, and announces that it finished. It stands in for termios, which under
// "go test" there is no tty to call.
type slowRestoreDriver struct{ winch chan struct{} }

func (d *slowRestoreDriver) Raw() (func() error, error) {
	return func() error {
		// Straight to the descriptor: a buffered writer would put the answer
		// in a buffer the child never gets to flush, which is the failure
		// being measured rather than a report of it.
		os.Stdout.WriteString(restoreStartLine + "\n")
		time.Sleep(restoreWindow)
		os.Stdout.WriteString(restoreDoneLine + "\n")
		return nil
	}, nil
}

func (d *slowRestoreDriver) Size() (rows, cols int, err error) { return 24, 80, nil }

func (d *slowRestoreDriver) Resized() (<-chan struct{}, func()) { return d.winch, func() {} }

// TestFatalSignalChild is the child. It runs one Loop that ends on its first
// event, which puts the deferred Stop and its slow restore exactly where the
// parent aims the SIGTERM, and then waits long enough for a re-raised signal
// to arrive so that a child which exits quietly is a child whose handler never
// fired.
func TestFatalSignalChild(t *testing.T) {
	if os.Getenv(signalChildEnv) == "" {
		t.Skip("the child half of TestAFatalSignalWaitsForTheRestore")
	}

	r, w := io.Pipe()
	defer w.Close()
	term := &Terminal{
		In:  r,
		Out: io.Discard,
		Drv: &slowRestoreDriver{winch: make(chan struct{})},
		opt: vimrcOptions(),
	}
	term.Loop(func(c Client, ev Event) error { return errDone })
	time.Sleep(2 * time.Second)
}

// TestAFatalSignalWaitsForTheRestore is the finding, end to end: a SIGTERM
// that lands while the editor is already on its way out must not kill the
// process before the terminal has been put back.
//
// Both halves of the fix are needed to pass it. onFatalSignal has to still be
// subscribed while the deferred Stop runs, or the signal kills the child with
// its default disposition; and Stop's second caller has to wait for the first,
// or the handler concludes the terminal is safe and re-raises into a restore
// that is half done. Either one alone leaves the child dead with
// "restore-done" never printed.
func TestAFatalSignalWaitsForTheRestore(t *testing.T) {
	if os.Getenv(signalChildEnv) != "" {
		t.Skip("the parent half does not run inside the child")
	}
	if testing.Short() {
		t.Skip("re-executes the test binary three times")
	}

	for run := 0; run < 3; run++ {
		cmd := exec.Command(os.Args[0], "-test.run=TestFatalSignalChild", "-test.timeout=60s")
		cmd.Env = append(os.Environ(), signalChildEnv+"=1")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("run %d: StdoutPipe: %v", run, err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatalf("run %d: starting the child: %v", run, err)
		}

		var lines []string
		signalled := false
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			lines = append(lines, line)
			if line == restoreStartLine && !signalled {
				signalled = true
				if err := cmd.Process.Signal(unix.SIGTERM); err != nil {
					t.Errorf("run %d: sending SIGTERM: %v", run, err)
				}
			}
		}
		cmd.Wait()

		if !signalled {
			t.Fatalf("run %d: the child never reached its restore; it printed %q", run, lines)
		}
		if !contains(lines, restoreDoneLine) {
			t.Errorf("run %d: SIGTERM killed the child in the middle of the restore, leaving the terminal in raw mode; it printed %q", run, lines)
		}
	}
}

func contains(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
