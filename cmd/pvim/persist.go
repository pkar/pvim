package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/undofile"
)

// Persistence: the undo history, the swap file and the command-line, search,
// mark and jump histories, joined to the editor.
//
// internal/undofile owns the three formats and knows nothing about a buffer;
// this file is the seam between them and cmd/pvim. Every function here takes
// the state it needs as an argument rather than reaching into the editor, so
// that all of it is testable with a *text.Buffer and a temporary directory and
// none of it needs a window.
//
// # Where the files go
//
// 'undodir' and 'directory', as the vimrc sets them, with ~/.cache/vim as the
// fallback for a run with no vimrc behind it. Vim will not create either
// directory and pvim does: cmd/pvim/main.go's cacheDir makes ~/.cache/vim on
// every launch that is not --oracle, and mkdirs below makes whatever the
// options name. That is not a detail. Checking this machine found
// `set undofile` with `undodir=~/.cache/vim` in this vimrc and no such
// directory on the disk, which means MacVim has silently persisted no undo
// history on this machine for as long as the option has been set -- vim writes
// nothing and says nothing when undodir does not exist. pvim is the first
// editor here for which 'undofile' does anything at all.
//
// # Who calls this
//
// persistwire.go, which is the six places in the editor's life cycle that need
// it, and recover.go, which is the E325 prompt at startup. Both frontends reach
// them through newFrontendEditor; --oracle reaches none of them, which is what
// keeps a graded run reproducible.

// persist is the whole of a session's on-disk state.
type persist struct {
	// undoDirs and swapDirs are 'undodir' and 'directory', split into entries.
	// Vim tries each in turn and takes the first that works, which is what
	// makes the default ".,~/tmp,/var/tmp,/tmp" mean anything.
	undoDirs []string
	swapDirs []string

	// histPath is the one history file, in the first undo directory or
	// ~/.cache/vim.
	histPath string

	// swap is the open swap file for the current buffer, or nil when the
	// buffer has no name, 'swapfile' is off, or no directory could be written.
	swap *undofile.Swap

	// on is 'undofile' and 'swapfile' as the options had them when this was
	// built. Kept rather than read through a pointer so that a persist is a
	// value a test can build without an options table.
	undoOn, swapOn bool
}

// cacheFallback is where the files go when the options name nothing usable,
// which is also where them and where the instance socket already
// lives.
const cacheFallback = ".cache/vim"

// newPersist reads the option values into a persist and creates the
// directories they name.
//
// A directory that cannot be made is not fatal and not silent: it is dropped
// from the list, and if nothing is left the feature is off for the session. Vim
// answers E303 for the swap case and carries on, and an editor that refuses to
// open a file because it could not make a cache directory would be worse than
// one that loses its history.
func newPersist(home string, o *options.Options) *persist {
	p := &persist{undoOn: o.B.UndoFile, swapOn: o.B.SwapFile}
	fallback := filepath.Join(home, cacheFallback)
	p.undoDirs = usable(undofile.Dirs(o.G.UndoDir, home), fallback)
	p.swapDirs = usable(undofile.Dirs(o.G.Directory, home), fallback)
	dir := fallback
	if len(p.undoDirs) > 0 && p.undoDirs[0] != "." {
		dir = p.undoDirs[0]
	}
	p.histPath = filepath.Join(trimSlashes(dir), "history")
	return p
}

// usable keeps the entries that exist or can be made, with the fallback on the
// end so that a vimrc naming a directory on a disk that is not mounted still
// gets persistence.
func usable(dirs []string, fallback string) []string {
	var out []string
	for _, d := range dirs {
		if d == "." {
			// The file's own directory, which is always there.
			out = append(out, d)
			continue
		}
		if err := os.MkdirAll(trimSlashes(d), 0o700); err != nil {
			continue
		}
		out = append(out, d)
	}
	if fallback != "" {
		if err := os.MkdirAll(fallback, 0o700); err == nil {
			out = append(out, fallback+string(os.PathSeparator)+string(os.PathSeparator))
		}
	}
	return out
}

// trimSlashes takes the "//" off a 'directory' entry, which is a marker in the
// option and not part of the path.
func trimSlashes(d string) string {
	for len(d) > 1 && os.IsPathSeparator(d[len(d)-1]) {
		d = d[:len(d)-1]
	}
	return d
}

// undoPath is where file's history goes, or "" when there is nowhere to put it.
func (p *persist) undoPath(file string) string {
	for _, d := range p.undoDirs {
		if name, err := undofile.UndoName(d, file); err == nil {
			return name
		}
	}
	return ""
}

// swapPath is where file's swap file goes, or "" when there is nowhere.
func (p *persist) swapPath(file string) string {
	for _, d := range p.swapDirs {
		if name, err := undofile.SwapName(d, file); err == nil {
			return name
		}
	}
	return ""
}

// bufferLines is a buffer's lines as the swap file wants them.
//
// A copy per line, because internal/text hands out the line it holds and the
// swap file keeps what it was given until the next sync. Copying 40k lines on
// every sync would be visible; copying them only when something changed is not,
// which is why undofile.Swap.Sync compares before it copies.
func bufferLines(b *text.Buffer) [][]byte {
	if b == nil {
		return nil
	}
	out := make([][]byte, b.LineCount())
	for i := range out {
		out[i] = b.Line(i + 1)
	}
	return out
}

// fileMeta is what the swap file records about the file on the disk. A file
// that is not there yet -- ":e newfile" -- gets a zero size and no digest,
// which is what it is.
func fileMeta(file string) undofile.SwapMeta {
	m := undofile.SwapMeta{File: file}
	data, err := os.ReadFile(file)
	if err != nil {
		return m
	}
	m.Size = int64(len(data))
	m.SHA = undofile.Digest(data)
	if st, err := os.Stat(file); err == nil {
		m.Mtime = st.ModTime()
	}
	return m
}

// openSwap starts a swap file for a buffer.
//
// Nothing at all for a buffer with no name, a scratch buffer or 'noswapfile',
// which is vim: the swap file is named after the file and a buffer without one
// has nothing to name it after.
func (p *persist) openSwap(file string, b *text.Buffer, cursor text.Pos) error {
	p.closeSwap()
	if !p.swapOn || file == "" {
		return nil
	}
	path := p.swapPath(file)
	if path == "" {
		return fmt.Errorf("E303: Unable to open swap file for %q, recovery impossible", file)
	}
	s, err := undofile.CreateSwap(path, fileMeta(file), bufferLines(b), b.NoEOL(), pos(cursor))
	if err != nil {
		return err
	}
	p.swap = s
	return nil
}

// syncSwap writes whatever has changed since the last call, and writes nothing
// when nothing has.
//
// Cheap enough to call after every keystroke, which is where it belongs: vim
// syncs every 'updatecount' characters and after 'updatetime' of idleness and
// therefore has a window in which a crash costs work, and the only reason for
// that window is that vim's swap file is a block file with a cost per write.
// This one has no such cost when there is nothing to write.
func (p *persist) syncSwap(b *text.Buffer, cursor text.Pos) error {
	if p.swap == nil || b == nil {
		return nil
	}
	return p.swap.Sync(bufferLines(b), b.NoEOL(), pos(cursor))
}

// savedSwap records a ":w", which is what takes "modified: YES" off the E325
// message the next launch would print.
func (p *persist) savedSwap(file string) error {
	if p.swap == nil {
		return nil
	}
	return p.swap.Saved(fileMeta(file))
}

// closeSwap deletes the swap file, which is what a clean exit does. A swap file
// left behind by an editor that quit properly is an E325 on the next open about
// a crash that never happened.
func (p *persist) closeSwap() error {
	if p.swap == nil {
		return nil
	}
	err := p.swap.Remove()
	p.swap = nil
	return err
}

// swapCheck is what a launch finds: nil when the file has no swap file, and the
// header of one when it does.
func (p *persist) swapCheck(file string) *undofile.SwapInfo {
	if !p.swapOn || file == "" {
		return nil
	}
	for _, d := range p.swapDirs {
		path, err := undofile.SwapName(d, file)
		if err != nil {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		info, err := undofile.Inspect(path)
		if err != nil {
			continue
		}
		return &info
	}
	return nil
}

// swapAction is what the answer to the E325 prompt means for the launch.
type swapAction struct {
	// Data is the buffer contents to use, and nil means "read the file the
	// ordinary way". It is set only by (R)ecover.
	Data []byte
	// NoEOL is the recovered file's missing final newline, which travels with
	// Data or is meaningless without it.
	NoEOL bool
	// Cursor is where the crashed session's cursor was.
	Cursor text.Pos
	// ReadOnly is [O]pen Read-Only: the buffer is opened and 'readonly' is on.
	ReadOnly bool
	// Swap says a swap file should be started for this session. False for
	// read-only and for the two ways out, and true for the rest -- including
	// (E)dit anyway, where undofile.CreateSwap picks the next free name.
	Swap bool
	// Message is what to put on the message line afterwards.
	Message string
	// Quit is (Q)uit: do not open this file. Abort is (A)bort: stop pvim.
	Quit  bool
	Abort bool
}

// chooseSwap applies one answer to the E325 prompt.
//
// The six outcomes are vim's, and the two that do work are (R)ecover, which
// reads the swap file into the buffer and prints what vim prints, and (D)elete
// it, which removes the swap file and opens the file normally.
func (p *persist) chooseSwap(c undofile.SwapChoice, info undofile.SwapInfo, file string) (swapAction, error) {
	switch c {
	case undofile.SwapReadOnly:
		return swapAction{ReadOnly: true}, nil
	case undofile.SwapEdit:
		return swapAction{Swap: true}, nil
	case undofile.SwapRecover:
		_, rec, err := undofile.ReadSwap(info.Path)
		if err != nil {
			return swapAction{}, fmt.Errorf("E306: Cannot open %s: %w", info.Path, err)
		}
		data := undofile.Join(rec.Lines, rec.NoEOL)
		same := false
		if onDisk, err := os.ReadFile(file); err == nil {
			same = string(onDisk) == string(data)
		}
		msg := undofile.RecoveredMessage(info.Path, file, same)
		if rec.Truncated {
			// The last record was cut short, which is what a kill in the
			// middle of a write leaves. Saying so is the honest thing: the
			// person has just been told the recovery worked and one sync's
			// worth of typing is not in it.
			msg += "The last change was not written in full; it is not in this buffer.\n"
		}
		return swapAction{
			Data:    data,
			NoEOL:   rec.NoEOL,
			Cursor:  text.Pos{Line: rec.Cursor.Line, Col: rec.Cursor.Col},
			Swap:    true,
			Message: msg,
		}, nil
	case undofile.SwapDelete:
		if err := os.Remove(info.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return swapAction{}, err
		}
		return swapAction{Swap: true, Message: "Swap file deleted"}, nil
	case undofile.SwapQuit:
		return swapAction{Quit: true}, nil
	default:
		return swapAction{Abort: true}, nil
	}
}

// saveUndo writes the buffer's undo tree beside the file, and deletes the undo
// file rather than leaving a stale one when there is no history to keep.
//
// data is the file's contents as they were written, which is what the header's
// digest is taken over: a tree stored against a digest of the buffer before the
// write would be refused by its own reader on the next launch.
func (p *persist) saveUndo(file string, data []byte, tr undofile.Tree) error {
	if !p.undoOn || file == "" {
		return nil
	}
	path := p.undoPath(file)
	if path == "" {
		return nil
	}
	if len(tr.Nodes) == 0 {
		// Nothing to remember. Removing rather than writing an empty tree, so
		// that a file opened, looked at and closed does not accumulate a
		// history file per launch.
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return undofile.WriteUndo(path, undofile.Undo{
		File:    file,
		Size:    int64(len(data)),
		SHA:     undofile.Digest(data),
		Written: time.Now(),
		Tree:    tr,
	})
}

// loadUndo reads the history for a file whose contents are data.
//
// Three answers and only one of them is an error. A history that is not there
// is the ordinary case; a history that no longer describes the file is
// deliberately refused by internal/undofile and is deleted here, because it
// will never match again and leaving it costs a stat and a read on every launch
// for the life of the file. Anything else is a real failure and worth a message.
func (p *persist) loadUndo(file string, data []byte) (undofile.Tree, bool, error) {
	if !p.undoOn || file == "" {
		return undofile.Tree{}, false, nil
	}
	path := p.undoPath(file)
	if path == "" {
		return undofile.Tree{}, false, nil
	}
	u, err := undofile.LoadUndo(path, data)
	switch {
	case err == nil:
		return u.Tree, true, nil
	case errors.Is(err, os.ErrNotExist):
		return undofile.Tree{}, false, nil
	case errors.Is(err, undofile.ErrStale), errors.Is(err, undofile.ErrCorrupt),
		errors.Is(err, undofile.ErrMagic), errors.Is(err, undofile.ErrVersion):
		os.Remove(path)
		return undofile.Tree{}, false, nil
	default:
		return undofile.Tree{}, false, err
	}
}

// loadHistory reads the history file. A launch with no history file is the
// first launch and is not a failure.
func (p *persist) loadHistory() (undofile.History, error) {
	h, err := undofile.ReadHistory(p.histPath)
	switch {
	case err == nil:
		return h, nil
	case errors.Is(err, os.ErrNotExist):
		return undofile.History{}, nil
	case errors.Is(err, undofile.ErrCorrupt), errors.Is(err, undofile.ErrMagic),
		errors.Is(err, undofile.ErrVersion):
		os.Remove(p.histPath)
		return undofile.History{}, nil
	default:
		return undofile.History{}, err
	}
}

// saveHistory writes the history file, trimmed to 'history'.
func (p *persist) saveHistory(h undofile.History, limit int) error {
	h.Trim(limit)
	return undofile.WriteHistory(p.histPath, h)
}

// marksOf collects the marks that outlive a session: A-Z, which vim calls file
// marks because each one names a file as well as a position.
//
// The lowercase marks are deliberately not here. They belong to a buffer, vim
// drops them at exit, and a 'a that survived a restart pointing into a file
// that has been rewritten since would be worse than no mark at all. Nor are
// '0 to '9: those are not buffer marks at all in vim, they are the file mark
// list itself -- '0 is where the cursor was when the editor last exited, '1 the
// time before that -- which is History.Files and recordExit below.
func marksOf(file string, b *text.Buffer) []undofile.Mark {
	if b == nil || file == "" {
		return nil
	}
	var out []undofile.Mark
	for _, name := range markNames() {
		if at, ok := b.Mark(name); ok {
			out = append(out, undofile.Mark{
				Name:  name,
				Place: undofile.Place{File: file, Pos: pos(at)},
			})
		}
	}
	return out
}

// mergeMarks folds this session's file marks into the ones already in the
// history file.
//
// A-Z are one set across all files in vim, not one set per file: setting 'A in
// another buffer moves the only A there is. So a name this session has set wins
// wherever it used to point, a name it has not set keeps whatever file it was
// pointing at, and a name that was in this file and has since been deleted goes
// away with it.
//
// Writing marksOf straight into History.Marks, which is what this replaced,
// lost every mark in every other file on the first exit -- one session editing
// one file emptied the set.
func mergeMarks(old []undofile.Mark, file string, now []undofile.Mark) []undofile.Mark {
	set := make(map[byte]bool, len(now))
	for _, m := range now {
		set[m.Name] = true
	}
	out := make([]undofile.Mark, 0, len(old)+len(now))
	for _, m := range old {
		if set[m.Name] || m.Place.File == file {
			continue
		}
		out = append(out, m)
	}
	return append(out, now...)
}

// markNames is A-Z, in the order ":marks" prints them.
func markNames() []byte {
	var out []byte
	for c := byte('A'); c <= 'Z'; c++ {
		out = append(out, c)
	}
	return out
}

// recordExit puts where the cursor was in file at the front of the file mark
// list, which is what makes it '0 on the next launch and shifts the previous
// '0 along to '1.
//
// One entry per file, vim's rule: an older entry for the same file is dropped
// rather than kept, so a file edited every day does not fill all ten slots by
// itself. The list is oldest first on the disk, because that is how the
// histories are stored and one order for the file is one thing to get wrong.
func recordExit(h undofile.History, file string, at text.Pos) undofile.History {
	if file == "" {
		return h
	}
	kept := h.Files[:0]
	for _, f := range h.Files {
		if f.File != file {
			kept = append(kept, f)
		}
	}
	h.Files = append(kept, undofile.FileMark{
		Place: undofile.Place{File: file, Pos: pos(at)},
		When:  time.Now(),
	})
	return h
}

// pos converts a cursor position on its way to the disk. Two identical structs
// in two packages, because internal/undofile is a leaf and imports nothing in
// this module -- the same reason internal/window has its own Rect.
func pos(p text.Pos) undofile.Pos { return undofile.Pos{Line: p.Line, Col: p.Col} }
