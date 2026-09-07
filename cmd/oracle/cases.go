package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// testCase is one case on disk: three files sharing a base name.
//
//	NAME.in the buffer before
//	NAME.keys raw bytes, 0x1b for Escape, exactly what vim -s consumes
//	NAME.opts a:set line applied before the keys, empty for vanilla
//
// The keys file is raw and not an escaped notation on purpose. It is the file
// vim itself reads, so a case can be tried by hand with
// `vim --clean -i NONE -s NAME.keys copy` and nothing is lost in translation
// between what the harness sends and what a person reads.
type testCase struct {
	name string
	in   []byte
	keys []byte
	opts string
}

// loadCases reads every case in a directory, sorted by name so a run's output
// is the same order every time.
func loadCases(dir string) ([]testCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".keys"); ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)

	cases := make([]testCase, 0, len(names))
	for _, n := range names {
		c, err := loadCase(dir, n)
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("%s: no .keys files", dir)
	}
	return cases, nil
}

// loadCase reads one case by base name.
func loadCase(dir, name string) (testCase, error) {
	c := testCase{name: name}

	keys, err := os.ReadFile(filepath.Join(dir, name+".keys"))
	if err != nil {
		return c, err
	}
	c.keys = keys

	in, err := os.ReadFile(filepath.Join(dir, name+".in"))
	if err != nil {
		return c, fmt.Errorf("%s has no .in file: %w", name, err)
	}
	c.in = in

	opts, err := os.ReadFile(filepath.Join(dir, name+".opts"))
	switch {
	case err == nil:
		c.opts = strings.TrimSpace(string(opts))
	case os.IsNotExist(err):
		// An absent .opts is the same as an empty one. Both mean vanilla.
	default:
		return c, err
	}
	return c, nil
}

// writeCase writes a case out as its three files. This is how a minimised fuzz
// script becomes a case somebody can read.
func writeCase(dir string, c testCase) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range []struct {
		ext  string
		body []byte
	}{
		{".in", c.in},
		{".keys", c.keys},
		{".opts", []byte(c.opts)},
	} {
		if err := os.WriteFile(filepath.Join(dir, c.name+f.ext), f.body, 0o644); err != nil {
			return err
		}
	}
	return nil
}
