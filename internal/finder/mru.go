package finder

import "path/filepath"

// MRU is ctrlp's most-recently-used file list.
//
// The rules are autoload/ctrlp/mrufiles.vim's:
//
// - absolute paths, newest first (s:addtomrufs does fnamemodify(fn, ':p')
// and inserts at 0 after removing any earlier copy);
// - a cap of g:ctrlp_mruf_max, 250 there and here;
// - a buffer with a non-empty 'buftype' is never recorded, which is what
// keeps the quickfix window, the ":!" output and this editor's own match
// window out of it;
// - case sensitive by default (g:ctrlp_mruf_case_sensitive is 1).
//
// What is NOT reproduced is where the list comes from and where it goes.
// ctrlp records on BufWinEnter, BufWinLeave and BufWritePost and writes the
// list to a cache file at VimLeavePre so it survives a restart. pvim has no
// autocommand for the first two events and its own history file for the third,
// so the caller pushes instead -- see cmd/pvim/finder.go, which notices the
// current buffer changing -- and the list dies with the process. A list that
// outlives the session belongs with the rest of persistence and is filed as
// such rather than half-built here.
type MRU struct {
	// Max is the cap. Zero means DefaultMRUMax.
	Max   int
	paths []string
}

// DefaultMRUMax is g:ctrlp_mruf_max.
const DefaultMRUMax = 250

// Push records a file as the most recent. An empty name, or one that is
// already at the front, does nothing.
func (m *MRU) Push(name string) {
	if name == "" {
		return
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		abs = name
	}
	if len(m.paths) > 0 && m.paths[0] == abs {
		return
	}
	out := m.paths[:0]
	for _, p := range m.paths {
		if p != abs {
			out = append(out, p)
		}
	}
	m.paths = append([]string{abs}, out...)
	max := m.Max
	if max <= 0 {
		max = DefaultMRUMax
	}
	if len(m.paths) > max {
		m.paths = m.paths[:max]
	}
}

// List is the paths, newest first.
func (m *MRU) List() []string {
	out := make([]string, len(m.paths))
	copy(out, m.paths)
	return out
}
