package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/screen"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/tree"
)

// The gate for the finder and tree halves, run through the whole editor
// rather than through the two packages:
//
//	CTRL-P then "hueveri" then CR opens hue_verify.go
//	"," shows the tree, dotfiles included
//	a file inside .git is not in the finder
//
// The keys go in through editor.key, which is the function both frontends call
// and the only way in that a person has.

// project builds a repository to search: a .git with a file in it, the file
// the gate names, and enough neighbours that the ranking has to choose.
func project(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{
		".git/HEAD",
		".gitignore",
		"internal/hue/hue_verify.go",
		"internal/hue/hue_verifier_registry_internal.go",
		"internal/mode/mode.go",
		"cmd/pvim/main.go",
		"README.md",
		"build/out.o",
	} {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package hue\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// editorOn builds the editor a person gets, with the vimrc this project exists
// to run, opened on a file in dir.
func editorOn(t *testing.T, dir, file string) *editor {
	t.Helper()
	if _, err := os.Stat(realVimrc); err != nil {
		t.Skipf("no vimrc at %s", realVimrc)
	}
	t.Setenv("MYVIMRC", realVimrc)
	e, err := newFrontendEditor(config{file: filepath.Join(dir, file), rows: 40, cols: 120}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { uninstallPlugins(e) })
	return e
}

func press(e *editor, s string) {
	for _, r := range s {
		e.key(key.Rune(r))
	}
}

func bufferText(b *text.Buffer) string {
	var sb strings.Builder
	for i := 1; i <= b.LineCount(); i++ {
		sb.Write(b.Line(i))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func TestTheFinderGate(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")

	p := pluginsFor(e)
	if p == nil {
		t.Fatal("newFrontendEditor did not install the plugins")
	}
	if p.root != dir {
		t.Fatalf("the project root is %q, want %q", p.root, dir)
	}
	if p.maxHeight != 40 {
		t.Errorf("g:ctrlp_max_height came through as %d, want 40", p.maxHeight)
	}

	e.key(key.Ctrl('p'))
	if p.find == nil {
		t.Fatal("CTRL-P did not open the finder")
	}
	if len(e.tabs.Current().Windows()) != 2 {
		t.Fatalf("the match window is not a split: %d windows", len(e.tabs.Current().Windows()))
	}
	// A file inside .git is not in the finder, and neither is the .o that
	// 'wildignore' names.
	for _, f := range p.files {
		if strings.HasPrefix(f, ".git/") {
			t.Errorf("a file inside .git is in the finder: %q", f)
		}
		if strings.HasSuffix(f, ".o") {
			t.Errorf("wildignore did not keep %q out", f)
		}
	}

	press(e, "hueveri")
	if got := e.message(); !strings.Contains(got, ">>> hueveri") {
		t.Errorf("the prompt line is %q", got)
	}
	sel, ok := p.find.Selected()
	if !ok || sel.Display != "internal/hue/hue_verify.go" {
		t.Fatalf("hueveri selected %+v", sel)
	}

	e.key(key.Key{Special: key.KeyCR})
	if p.find != nil {
		t.Error("CR left the finder open")
	}
	want := filepath.Join(dir, "internal/hue/hue_verify.go")
	if e.file != want {
		t.Fatalf("CR opened %q, want %q", e.file, want)
	}
	if len(e.tabs.Current().Windows()) != 1 {
		t.Errorf("the match window is still there: %d windows", len(e.tabs.Current().Windows()))
	}
	if e.ed.Buffer() != e.cur().Text {
		t.Error("the mode machine and the buffer list disagree after an open")
	}
}

// TestTheFinderDrawsAMatchWindow is the frame a person actually sees, taken
// off the same Screen internal/screen hands a frontend: the match window is a
// bottom split of the height g:ctrlp_max_height asked for, the best match is
// on its LAST line with ">" against it, and the prompt is on the message line
// under it, which is where ctrlp echoes it.
func TestTheFinderDrawsAMatchWindow(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	// The screen the frame is worked out for and the screen it is drawn on
	// have to be the same one, or the layout is a 40-row layout clipped to 24.
	e.resize(24, 70)
	e.key(key.Ctrl('p'))
	press(e, "hue")

	rows := screenRows(e.draw(24, 70))
	// 24 rows: one command line, the match window, its status line, and what
	// is left of the window it split.
	// Two files match "hue", so the match window is two rows: ctrlp sizes the
	// window to the results and g:ctrlp_max_height is the cap and not the
	// height.
	if got := pluginsFor(e).findWin.View.Height; got != 2 {
		t.Errorf("the match window is %d rows for two matches, want 2", got)
	}
	last := rows[len(rows)-1]
	if !strings.HasPrefix(last, ">>> hue") {
		t.Errorf("the prompt line is %q", last)
	}
	// The line above the match window's status line is the best match.
	best := ""
	for i, r := range rows {
		if strings.HasPrefix(r, "ControlP") {
			best = rows[i-1]
			break
		}
	}
	if !strings.HasPrefix(best, "> internal/hue/hue_verify.go") {
		t.Errorf("the line nearest the prompt is %q, want the best match marked", best)
	}
	e.key(key.Key{Special: key.KeyEsc})
	if got := e.message(); got != "" {
		t.Errorf("escape left %q on the message line", got)
	}
}

// TestTheTreeDrawsALeftSplit is the same for the tree: 80 columns on the left,
// nerdtree's header, its arrows and its two-space indent.
func TestTheTreeDrawsALeftSplit(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	e.resize(24, 120)
	press(e, ",,")

	rows := screenRows(e.draw(24, 120))
	if !strings.HasPrefix(rows[0], `" Press ? for help`) {
		t.Errorf("the first row is %q", rows[0])
	}
	if !strings.HasPrefix(rows[2], ".. (up a dir)") {
		t.Errorf("the third row is %q", rows[2])
	}
	// The root line is the project path, clipped to the window's 80 columns.
	root := dir
	if len(root) > 80 {
		root = root[:80]
	}
	if !strings.HasPrefix(rows[3], root) {
		t.Errorf("the root row is %q, want %q", rows[3], root)
	}
	want := map[string]bool{"▸ .git/": true, "  README.md": true}
	for _, r := range rows {
		for w := range want {
			if strings.HasPrefix(r, w) {
				delete(want, w)
			}
		}
	}
	for w := range want {
		t.Errorf("no row starts with %q", w)
	}
	// The tree is 80 columns wide, so the window beside it starts at 81.
	for _, r := range rows[:4] {
		if len(r) > 81 && r[80] != '|' && strings.TrimSpace(r[80:81]) != "" {
			t.Errorf("column 81 of %q is not the separator", r)
		}
	}
}

// screenRows is the drawn grid as strings, trailing blanks kept off.
func screenRows(s *screen.Screen) []string {
	out := make([]string, 0, s.Grid.Rows)
	for r := 0; r < s.Grid.Rows; r++ {
		var b strings.Builder
		for c := 0; c < s.Grid.Cols; c++ {
			ch := s.Grid.At(r, c).Rune
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

func TestFinderEscapeGoesBack(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	before := e.file

	e.key(key.Ctrl('p'))
	press(e, "hue")
	e.key(key.Key{Special: key.KeyEsc})

	if pluginsFor(e).find != nil {
		t.Error("escape left the finder open")
	}
	if e.file != before {
		t.Errorf("escape moved the editor to %q", e.file)
	}
	if n := len(e.tabs.Current().Windows()); n != 1 {
		t.Errorf("%d windows after escape, want 1", n)
	}
}

func TestFinderOpensInATabAndASplit(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")

	e.key(key.Ctrl('p'))
	press(e, "hueveri")
	e.key(key.Ctrl('t'))
	if n := len(e.tabs.Pages); n != 2 {
		t.Fatalf("CTRL-T made %d tabs, want 2", n)
	}
	if !strings.HasSuffix(e.file, "hue_verify.go") {
		t.Errorf("the new tab is on %q", e.file)
	}

	e.key(key.Ctrl('p'))
	press(e, "modemode")
	e.key(key.Ctrl('v'))
	if n := len(e.tabs.Current().Windows()); n != 2 {
		t.Fatalf("CTRL-V made %d windows, want 2", n)
	}
	if !strings.HasSuffix(e.file, "mode.go") {
		t.Errorf("the vsplit is on %q", e.file)
	}
}

func TestFinderModesReachBuffersAndMRU(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	p := pluginsFor(e)

	// Open a second file so there is something in both the buffer list and
	// the MRU list, and one key after it so the poll notices.
	if err := e.ctx.RunLine("edit " + escapeExArg(filepath.Join(dir, "cmd/pvim/main.go"))); err != nil {
		t.Fatal(err)
	}
	e.key(key.Rune('l'))

	e.key(key.Ctrl('p'))
	e.key(key.Ctrl('f'))
	if got := p.find.Mode().String(); got != "buffers" {
		t.Fatalf("CTRL-F went to %q", got)
	}
	if len(p.find.Matches()) == 0 {
		t.Error("buffer mode has nothing in it")
	}
	e.key(key.Ctrl('f'))
	if got := p.find.Mode().String(); got != "mru files" {
		t.Fatalf("a second CTRL-F went to %q", got)
	}
	found := false
	for _, m := range p.find.Matches() {
		if strings.HasSuffix(m.Path, "README.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("the file the session started on is not in the MRU list: %v", p.find.Matches())
	}
	e.key(key.Key{Special: key.KeyEsc})
}

func TestTheTreeGate(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	p := pluginsFor(e)

	// The vimrc's own mapping is `nnoremap <leader>, :NERDTreeToggle<CR>` with
	// mapleader ",", so this is the key sequence a person types.
	press(e, ",,")
	if p.treeWin == nil {
		t.Fatal(",, did not open the tree")
	}
	if p.treeSize != 80 {
		t.Errorf("g:NERDTreeWinSize came through as %d, want 80", p.treeSize)
	}
	if !p.showHidden {
		t.Error("g:NERDTreeShowHidden came through false")
	}
	if got := p.treeWin.View.Width; got != 80 {
		t.Errorf("the tree window is %d columns, want 80", got)
	}
	if p.treeWin != e.tabs.Current().Windows()[0] {
		t.Error("the tree is not the leftmost window")
	}

	got := bufferText(p.treeBuf.Text)
	for _, want := range []string{".gitignore", ".git/", "internal/", "README.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not in the tree:\n%s", want, got)
		}
	}

	// And "," again closes it, which is the whole of ":NERDTreeToggle".
	press(e, ",,")
	if p.treeWin != nil {
		t.Error("a second ,, did not close the tree")
	}
	if n := len(e.tabs.Current().Windows()); n != 1 {
		t.Errorf("%d windows after closing the tree, want 1", n)
	}
}

func TestTreeOpensAFileInTheOtherWindow(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	p := pluginsFor(e)
	press(e, ",,")

	readme := p.tree.Find(filepath.Join(dir, "README.md"))
	if readme == nil {
		t.Fatal("README.md is not in the tree")
	}
	e.ed.SetCursor(text.Pos{Line: p.tree.LineOf(readme)})
	e.key(key.Rune('o'))

	if !strings.HasSuffix(e.file, "README.md") {
		t.Fatalf("o opened %q", e.file)
	}
	if p.treeWin == nil {
		t.Error("o closed the tree")
	}
	if e.ctx.Window() == p.treeWin {
		t.Error("the file was opened into the tree's own window")
	}
}

func TestTreeUnboundKeysStillMoveTheCursor(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	press(e, ",,")

	before := e.ed.Cursor().Line
	e.key(key.Rune('j'))
	if e.ed.Cursor().Line != before+1 {
		t.Errorf("j in the tree moved the cursor from %d to %d", before, e.ed.Cursor().Line)
	}
	e.key(key.Rune('k'))
	if e.ed.Cursor().Line != before {
		t.Errorf("k did not come back: line %d", e.ed.Cursor().Line)
	}
}

func TestTreeMenuAddsAFile(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	p := pluginsFor(e)
	press(e, ",,")
	e.ed.SetCursor(text.Pos{Line: p.tree.LineOf(p.tree.Root())})

	e.key(key.Rune('m'))
	if p.ask == nil {
		t.Fatal("m did not put the menu up")
	}
	if !strings.Contains(e.message(), "(a)dd a childnode") {
		t.Errorf("the menu line is %q", e.message())
	}
	e.key(key.Rune('a'))
	if p.ask == nil {
		t.Fatal("a did not ask for a name")
	}
	// The prompt is seeded with the directory, which is nerdtree's pre-filled
	// input; CTRL-U clears it and the name is typed whole.
	e.key(key.Ctrl('u'))
	press(e, "fresh.go")
	e.key(key.Key{Special: key.KeyCR})

	if _, err := os.Stat(filepath.Join(dir, "fresh.go")); err != nil {
		t.Fatalf("the file was not made: %v", err)
	}
	if !strings.Contains(bufferText(p.treeBuf.Text), "fresh.go") {
		t.Error("the new file is not in the tree")
	}
}

func TestTreeMenuDeleteWantsAYes(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	p := pluginsFor(e)
	press(e, ",,")

	readme := p.tree.Find(filepath.Join(dir, "README.md"))
	e.ed.SetCursor(text.Pos{Line: p.tree.LineOf(readme)})
	e.key(key.Rune('m'))
	e.key(key.Rune('d'))
	if !strings.Contains(e.message(), "(yN)") {
		t.Errorf("the delete prompt is %q", e.message())
	}
	e.key(key.Rune('n'))
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Fatal("answering n deleted the file anyway")
	}

	e.key(key.Rune('m'))
	e.key(key.Rune('d'))
	e.key(key.Rune('y'))
	if _, err := os.Stat(filepath.Join(dir, "README.md")); !os.IsNotExist(err) {
		t.Error("answering y did not delete the file")
	}
}

func TestTreeMenuRenameFollowsTheBuffer(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	p := pluginsFor(e)
	press(e, ",,")

	readme := p.tree.Find(filepath.Join(dir, "README.md"))
	e.ed.SetCursor(text.Pos{Line: p.tree.LineOf(readme)})
	e.key(key.Rune('m'))
	e.key(key.Rune('m'))
	e.key(key.Ctrl('u'))
	press(e, filepath.Join(dir, "NOTES.md"))
	e.key(key.Key{Special: key.KeyCR})

	if _, err := os.Stat(filepath.Join(dir, "NOTES.md")); err != nil {
		t.Fatalf("the rename did not happen: %v", err)
	}
	for _, b := range e.ctx.Bufs.Bufs {
		if strings.HasSuffix(b.Name, "README.md") {
			t.Errorf("buffer %d still calls itself %q", b.Num, b.Name)
		}
	}
}

// TestCtrlPFromTheTreeWindow: the finder is reachable from inside the tree,
// and it does not split the tree to get there.
func TestCtrlPFromTheTreeWindow(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	p := pluginsFor(e)
	press(e, ",,")
	if e.ctx.Window() != p.treeWin {
		t.Fatal(",, did not leave the cursor in the tree")
	}
	e.key(key.Ctrl('p'))
	if p.find == nil {
		t.Fatal("CTRL-P in the tree window did not open the finder")
	}
	if p.findWin.View.Width == p.treeWin.View.Width {
		t.Errorf("the match window is the tree's width (%d); it split the tree", p.findWin.View.Width)
	}
	press(e, "hueveri")
	e.key(key.Key{Special: key.KeyCR})
	if !strings.HasSuffix(e.file, "hue_verify.go") {
		t.Errorf("CR opened %q", e.file)
	}
	if p.treeWin == nil {
		t.Error("the tree closed")
	}
}

func TestTreeZZDoesNotWriteTheTreeOut(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	press(e, ",,")

	e.key(key.Rune('Z'))
	if !strings.Contains(e.message(), "E382") {
		t.Errorf("Z in the tree said %q", e.message())
	}
	if _, err := os.Stat("NERD_tree_1"); err == nil {
		os.Remove("NERD_tree_1")
		t.Error("Z wrote the tree buffer to a file")
	}
}

func TestEditADirectoryOpensTheTree(t *testing.T) {
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	p := pluginsFor(e)

	if err := e.sess.runLine("edit " + escapeExArg(filepath.Join(dir, "internal"))); err != nil {
		t.Fatal(err)
	}
	if p.treeWin == nil {
		t.Fatal(":e <dir> did not open the tree")
	}
	if p.tree.Root().Path != filepath.Join(dir, "internal") {
		t.Errorf("the tree is rooted at %q", p.tree.Root().Path)
	}
	if e.message() != "" && strings.Contains(e.message(), "E484") {
		t.Errorf(":e <dir> said %q", e.message())
	}
}

func TestPvimOnADirectoryOpensTheTree(t *testing.T) {
	dir := project(t)
	if _, err := os.Stat(realVimrc); err != nil {
		t.Skipf("no vimrc at %s", realVimrc)
	}
	t.Setenv("MYVIMRC", realVimrc)

	e, err := newFrontendEditor(config{file: dir, rows: 40, cols: 120}, false)
	if err != nil {
		t.Fatalf("pvim <dir> = %v", err)
	}
	defer uninstallPlugins(e)
	p := pluginsFor(e)
	if p.treeWin == nil {
		t.Fatal("pvim <dir> did not open the tree")
	}
	if p.tree.Root().Path != dir {
		t.Errorf("the tree is rooted at %q, want %q", p.tree.Root().Path, dir)
	}
}

// TestNetrwHistoryIsNeverWritten is the other half of "the tree replaces
// netrw": nothing in this program writes ~/.vim/.netrwhist, and the file it
// would be is not created by opening a directory.
func TestNetrwHistoryIsNeverWritten(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := project(t)
	e := editorOn(t, dir, "README.md")
	press(e, ",,")
	if _, err := os.Stat(filepath.Join(home, ".vim", ".netrwhist")); !os.IsNotExist(err) {
		t.Errorf("something wrote a netrw history: %v", err)
	}
}

// TestTheOracleHasNoFinder is the guard on the 809 graded cases: an editor
// built the way --oracle builds one has no plugins, so CTRL-P is whatever the
// mode machine says it is and not a match window.
func TestTheOracleHasNoFinder(t *testing.T) {
	e, err := newEditor([]byte("alpha\n"), "f.txt", 24, 80, "")
	if err != nil {
		t.Fatal(err)
	}
	if pluginsFor(e) != nil {
		t.Fatal("newEditor installed the plugins")
	}
	if pluginKey(e, key.Ctrl('p')) {
		t.Error("CTRL-P was taken by a finder that does not exist")
	}
}

func TestTreeDefaultsWithoutAVimrc(t *testing.T) {
	dir := project(t)
	e, err := newFrontendEditor(config{file: filepath.Join(dir, "README.md"), rows: 40, cols: 120, clean: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer uninstallPlugins(e)
	p := pluginsFor(e)
	if p.treeSize != tree.DefaultWinSize {
		t.Errorf("--clean tree width is %d, want nerdtree's own %d", p.treeSize, tree.DefaultWinSize)
	}
	if p.showHidden {
		t.Error("--clean shows hidden files; nerdtree's default is not to")
	}
	if p.maxHeight != 40 {
		t.Errorf("--clean match window height is %d", p.maxHeight)
	}
}
