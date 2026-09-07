package main

import (
	"errors"
	"os"

	"github.com/pkar/pvim/internal/key"
)

// The -s keystroke script, played into a live frontend.
//
// vim's -s reads a file of raw bytes and types them as though a person had,
// then hands the editor back to whoever is at the keyboard. --oracle is the
// headless half of that: it runs the script over a file with no frontend at
// all and writes the artifacts cmd/oracle diffs. This is the other half, and
// the window is what makes it worth having: it is the only way to drive the
// window with no hands, which is what the gate item asks for and what
// internal/gui's manual check cannot do.
//
// The keys take exactly the path a person's keys take -- through the pump, onto
// the editor goroutine, one at a time -- so a script that finds a defect finds
// a real one. Nothing here is a test harness bypassing the frontend; there is
// no bypass.

// readScript reads a -s file and decodes it into keys.
//
// The decoding is scriptKeys', which is the oracle's, so a script that means
// one thing headless means the same thing in the window: an Escape at the end
// of the file is an Escape and not the start of a sequence still arriving.
func readScript(name string) ([]key.Key, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return scriptKeys(b), nil
}

// playScript types keys one at a time through feed, stopping at the first one
// that ends the editor.
//
// errMoreInput is not an end: it is the frontend saying the key was swallowed
// by a prompt that wants another, which is exactly what the next key in the
// script is for. Anything else non-nil is a quit or a failure and the rest of
// the script is dropped, the same way vim abandons a script when the editor it
// was typing into has gone.
func playScript(keys []key.Key, feed func(key.Key) error) error {
	for _, k := range keys {
		err := feed(k)
		if err == nil || errors.Is(err, errMoreInput) {
			continue
		}
		return err
	}
	return nil
}
