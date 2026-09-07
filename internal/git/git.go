// Package git is what this editor keeps of fugitive: a status buffer, blame,
// a diff against the index, and ":Git" for everything else.
//
// It shells out to the git binary and reads its plumbing. There is no object
// format here, no pack reader and no go-git: the dependency list has
// four modules on it and none of them is a git implementation, and a second
// implementation of git's index that has to agree with the real one about
// rename detection, .gitattributes, core.autocrlf and submodules would be a
// project rather than a package. What git says is what this editor shows, so
// the two cannot disagree.
//
// There is no oracle for this half of the editor: vim cannot be asked what
// fugitive would have drawn. So the measurements are against the two things
// that can be asked. The git CLI is one -- a blame line here names the commit
// `git blame` names, byte for byte, and a diff hunk here is the hunk
// `git diff` prints, both as tests in a repository the test built in its own
// temporary directory. Fugitive is the other, and it
// was read and run rather than tested against: its status buffer's shape --
// "Head:", "Help: g?", then Untracked, Unstaged and Staged with a count in
// brackets -- is copied from a run of `:Git` in vim 9.2 with fugitive
// installed, and that shape is recorded in status_test.go as
// a literal so that a change to it is a test failure and not a surprise.
//
// Where a behaviour is a judgment call rather than a rule it says so at the
// place it is made. There are four: which sections the status buffer has,
// what "-" does on a section header, what a blame line looks like, and what
// ":Gdiff" names the index buffer.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is one git repository, found from a directory inside it.
type Repo struct {
	// Root is the top of the working tree, which is what every path in a
	// status buffer is relative to and what a ":Git" runs in.
	Root string
	// Dir is the directory the repository was found from. Commands run here
	// and not at the root, so that ":Git add ." adds what a shell in the same
	// place would add.
	Dir string

	// bin is the git binary, resolved once.
	bin string
	// env is the environment git runs under, or nil for this process's own.
	// It exists for the tests, which point HOME and the config files at a
	// temporary directory so that nothing they run can read or write the
	// person's real git configuration.
	env []string
}

// ErrNotARepo is what Find answers outside a repository. It is a sentinel
// because the caller's message depends on it: ":Gstatus" outside a repository
// is a message on the message line and not a stack trace.
var ErrNotARepo = errors.New("not a git repository")

// Find locates the repository containing dir.
//
// It asks git rather than walking up looking for a ".git", because a worktree
// added with `git worktree add` has a ".git" FILE and a submodule's is
// somewhere else entirely, and both are things that exist in a user's
// checkouts.
func Find(dir string) (*Repo, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git: %w", err)
	}
	r := &Repo{Dir: dir, bin: bin}
	out, err := r.output("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, ErrNotARepo
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return nil, ErrNotARepo
	}
	r.Root = root
	return r, nil
}

// SetEnv replaces the environment git runs under. A nil environment means
// this process's own, which is what the editor uses; the tests use it to
// point HOME and GIT_CONFIG_GLOBAL at a directory they made, so that no test
// can read the person's git identity or write their repository.
func (r *Repo) SetEnv(env []string) { r.env = env }

// Run runs a git subcommand and returns its output, standard error included,
// which is what ":Git" puts in a scratch split.
//
// Both streams and not just standard output, because half of what git says
// about a command that half-worked is on the other one, and a scratch split
// showing an empty buffer after a failed push would be a worse answer than
// the one git gave.
func (r *Repo) Run(args ...string) ([]byte, error) {
	cmd := r.cmd(args...)
	out, err := cmd.CombinedOutput()
	return out, err
}

// output is Run for a command whose output is going to be parsed: standard
// output only, with standard error folded into the error so that a parse
// never sees a warning git printed.
func (r *Repo) output(args ...string) (string, error) {
	cmd := r.cmd(args...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			return "", err
		}
		return "", errors.New(msg)
	}
	return string(out), nil
}

// cmd builds one git command, in the right directory and with the right
// environment.
func (r *Repo) cmd(args ...string) *exec.Cmd {
	cmd := exec.Command(r.bin, args...)
	cmd.Dir = r.Dir
	if r.env != nil {
		cmd.Env = r.env
	}
	// A git that stops for a password or a passphrase would hang the editor
	// with no way to answer it, because the editor owns the terminal. Asking
	// for no terminal prompt turns that into an error message, which a
	// scratch split can show.
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd
}

// Rel is path as the repository names it: relative to the working tree root,
// with forward slashes, which is what git prints and what a status buffer
// line holds.
func (r *Repo) Rel(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// The root came from git, which resolves symlinks; a path that arrived
	// from a buffer name did not. On macOS /var is a link to /private/var and
	// every temporary directory is under it, so without this every path in a
	// repository under TMPDIR reads as outside the working tree.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	} else if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		abs = filepath.Join(dir, filepath.Base(abs))
	}
	rel, err := filepath.Rel(r.Root, abs)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%s is outside %s", path, r.Root)
	}
	return filepath.ToSlash(rel), nil
}

// Abs is the reverse of Rel: a path git printed, as a path on this machine.
func (r *Repo) Abs(rel string) string {
	return filepath.Join(r.Root, filepath.FromSlash(rel))
}

// IndexBlob is the contents of a path as the index has it, which is the left
// half of ":Gdiff".
//
// A path that is not in the index -- a new file -- is not an error and comes
// back empty, because that is what the diff should show: every line added.
func (r *Repo) IndexBlob(rel string) ([]byte, error) {
	out, err := r.output("show", ":"+rel)
	if err != nil {
		if _, e := r.output("ls-files", "--error-unmatch", "--", rel); e != nil {
			return nil, nil
		}
		return nil, err
	}
	return []byte(out), nil
}

// Blob is the contents of a path at a revision, which is what ":Gdiff HEAD"
// and opening a commit want.
func (r *Repo) Blob(rev, rel string) ([]byte, error) {
	out, err := r.output("show", rev+":"+rel)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// Lines splits a blob into lines the way this editor's buffer holds them: no
// trailing empty line for the newline that ends the last line.
func Lines(b []byte) [][]byte {
	if len(b) == 0 {
		return nil
	}
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.Split(b, []byte("\n"))
}

// Commit runs `git commit` with the given editor, which is how the message
// gets typed in this editor rather than in whatever EDITOR says.
//
// editor is a command line, not a path: "pvim --wait" is what this is for and
// what the instance socket makes work, and git takes the whole string. The
// call BLOCKS until git is done, which means until the tab holding
// COMMIT_EDITMSG is closed, so the caller has to run it off the editor's own
// goroutine or the editor is waiting on a client that is waiting on the
// editor. cmd/pvim/git.go does; there is nothing here that can enforce it.
func (r *Repo) Commit(editor string, args ...string) ([]byte, error) {
	cmd := r.cmd(append([]string{"commit"}, args...)...)
	cmd.Env = append(cmd.Env, "GIT_EDITOR="+editor)
	// EDITOR as well as GIT_EDITOR, because a repository with core.editor set
	// would otherwise win over GIT_EDITOR -- it does not, GIT_EDITOR is above
	// core.editor in git's own order -- but a hook that starts an editor
	// reads EDITOR, and there are hooks like that in use here.
	cmd.Env = append(cmd.Env, "EDITOR="+editor)
	return cmd.CombinedOutput()
}

// Env is a git environment for a test: this process's own with HOME and both
// config paths pointed somewhere harmless.
//
// It is exported and not test-only on purpose. Every test in this tree that
// touches git has to run against a repository it made in its own temporary
// directory -- the repository this editor is written in is the one the work
// is committed from, and a stray `git add` in it destroys somebody's
// afternoon -- and one function that builds the fence is better than five
// copies of it. cmd/pvim's git tests use it too.
func Env(home string) []string {
	env := append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(home, ".gitconfig-system"),
		"GIT_AUTHOR_NAME=pvim test",
		"GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=pvim test",
		"GIT_COMMITTER_EMAIL=test@example.invalid",
	)
	return env
}
