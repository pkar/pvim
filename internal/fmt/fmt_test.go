package fmt

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTableIsTheTwoPluginsAndNothingElse: the table has exactly the filetypes
// the vimrc's plugins formatted, and a filetype outside it is silent.
func TestTableIsTheTwoPluginsAndNothingElse(t *testing.T) {
	tab := New(nil)
	for _, ft := range []string{"go", "terraform", "tf", "hcl"} {
		if !tab.Formats(ft) {
			t.Errorf("%q is not formatted on save; the vimrc's plugins formatted it", ft)
		}
	}
	// The vimrc has a BufWritePre whitespace strip for python, java and
	// javascript and no formatter for any of them, and markdown gets neither.
	for _, ft := range []string{"python", "java", "javascript", "markdown", "text", ""} {
		if tab.Formats(ft) {
			t.Errorf("%q is formatted on save and no plugin in this vimrc formatted it", ft)
		}
	}
	if _, err := tab.Format(context.Background(), "markdown", "/x/a.md", []byte("x")); !errors.Is(err, ErrNoFormatter) {
		t.Errorf("a markdown save answered %v, want ErrNoFormatter", err)
	}
}

// TestSwitchesAreTheVimrcsLets: g:go_fmt_autosave and g:terraform_fmt_on_save,
// each of which turns its own row off and leaves the other alone.
func TestSwitchesAreTheVimrcsLets(t *testing.T) {
	tab := New(nil)
	tab.GoEnabled = false
	if tab.Formats("go") {
		t.Error("g:go_fmt_autosave = 0 did not turn the go row off")
	}
	if !tab.Formats("terraform") {
		t.Error("turning the go row off took the terraform row with it")
	}

	tab = New(nil)
	tab.TerraformEnabled = false
	if tab.Formats("terraform") || tab.Formats("tf") || tab.Formats("hcl") {
		t.Error("g:terraform_fmt_on_save = 0 did not turn all three terraform rows off")
	}
	if !tab.Formats("go") {
		t.Error("turning the terraform rows off took the go row with it")
	}
}

// TestGoGoesThroughTheServer, and a Go file with no server says so rather than
// being silently unformatted.
func TestGoGoesThroughTheServer(t *testing.T) {
	var gotPath string
	tab := New(FormatterFunc(func(_ context.Context, path string, src []byte) ([]byte, error) {
		gotPath = path
		return append(src, []byte("// formatted\n")...), nil
	}))
	out, err := tab.Format(context.Background(), "go", "/x/a.go", []byte("package a\n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "package a\n// formatted\n" {
		t.Fatalf("out = %q", out)
	}
	if gotPath != "/x/a.go" {
		t.Fatalf("the formatter was given path %q", gotPath)
	}

	if _, err := New(nil).Format(context.Background(), "go", "/x/a.go", []byte("package a\n")); !errors.Is(err, ErrNoServer) {
		t.Fatalf("a Go save with no server answered %v, want ErrNoServer", err)
	}
}

// TestTerraformIsTheStdinForm: "terraform fmt -" and not "terraform fmt FILE",
// because the second rewrites the file on disk and leaves the buffer holding
// the text the person typed.
func TestTerraformIsTheStdinForm(t *testing.T) {
	var name string
	var args []string
	var stdin []byte
	tab := New(nil)
	tab.Run = func(_ context.Context, n string, a []string, in []byte) ([]byte, error) {
		name, args, stdin = n, a, in
		return []byte("formatted\n"), nil
	}
	src := []byte("resource \"a\" \"b\" {\nx=1\n}\n")
	out, err := tab.Format(context.Background(), "terraform", "/x/main.tf", src)
	if err != nil {
		t.Fatal(err)
	}
	if name != "terraform" || strings.Join(args, " ") != "fmt -" {
		t.Fatalf("ran %q %v, want terraform fmt -", name, args)
	}
	if !bytes.Equal(stdin, src) {
		t.Fatalf("the buffer did not go to standard input: %q", stdin)
	}
	if string(out) != "formatted\n" {
		t.Fatalf("out = %q", out)
	}
}

// TestAFailedFormatterLeavesTheBufferAlone. A Go file with a syntax error is
// exactly the file most likely to be saved, and an editor whose format-on-save
// emptied the buffer over one would be unusable.
func TestAFailedFormatterLeavesTheBufferAlone(t *testing.T) {
	src := []byte("package a\nfunc (\n")
	tab := New(FormatterFunc(func(context.Context, string, []byte) ([]byte, error) {
		return nil, errors.New("expected ')'")
	}))
	out, err := tab.Format(context.Background(), "go", "/x/a.go", src)
	if err == nil {
		t.Fatal("a formatter that failed was reported as success")
	}
	if !bytes.Equal(out, src) {
		t.Fatalf("the buffer changed on a failed format: %q", out)
	}
}

// TestEmptyOutputIsRefused: a program that exits 0 and prints nothing has
// decided the input is not its language, and applying that answer empties the
// buffer.
func TestEmptyOutputIsRefused(t *testing.T) {
	tab := New(nil)
	tab.Run = func(context.Context, string, []string, []byte) ([]byte, error) { return nil, nil }
	src := []byte("resource \"a\" \"b\" {}\n")
	out, err := tab.Format(context.Background(), "terraform", "/x/main.tf", src)
	if err == nil {
		t.Fatal("a formatter that printed nothing was accepted")
	}
	if !bytes.Equal(out, src) {
		t.Fatalf("the buffer changed: %q", out)
	}
	// An empty buffer formatted to nothing is not an error: there is nothing
	// to lose.
	if _, err := tab.Format(context.Background(), "terraform", "/x/main.tf", nil); err != nil {
		t.Fatalf("an empty buffer answered %v", err)
	}
}

// TestCommandErrorIsOneLine: the message area is one row under this vimrc's
// 'cmdheight', and a multi-line diagnostic there is a press-enter prompt where
// a person expected a saved file.
func TestCommandErrorIsOneLine(t *testing.T) {
	tab := New(nil)
	tab.Run = execRun
	tab.Rules = map[string]Rule{"x": {Kind: KindCommand, Command: "sh", Args: []string{"-c", "echo one >&2; echo two >&2; exit 1"}}}
	_, err := tab.Format(context.Background(), "x", "/x/a", []byte("x"))
	if err == nil {
		t.Fatal("a command that exited 1 was reported as success")
	}
	if strings.Contains(err.Error(), "\n") {
		t.Fatalf("the error is more than one line: %q", err)
	}
	if !strings.Contains(err.Error(), "one") {
		t.Fatalf("the error dropped what the command said: %q", err)
	}
}

// TestTerraformRoundTripsAgainstTheCLI is the gate: a .tf fixture
// formatted on save comes out byte-identical to what the terraform binary
// writes for the same input.
//
// Byte-identical against the CLI's own file-rewriting mode and not against a
// golden, because the golden would be this code's own answer and would pass
// forever after terraform changed its mind about alignment. Skipped when there
// is no terraform, which is the honest thing on a machine that has none.
func TestTerraformRoundTripsAgainstTheCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: this test shells out to terraform")
	}
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("no terraform on $PATH")
	}

	// Deliberately misaligned and misindented, which is the whole of what
	// "terraform fmt" changes: it aligns the "=" of consecutive arguments and
	// fixes indentation, and does not reorder or rewrite anything.
	const src = `resource "aws_instance" "web" {
ami = "ami-0123"
instance_type="t3.micro"
  tags = {
Name = "web"
Environment="prod"
}
}
`
	dir := t.TempDir()
	file := filepath.Join(dir, "main.tf")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("terraform", "fmt", file).CombinedOutput(); err != nil {
		t.Fatalf("terraform fmt %s: %v\n%s", file, err, out)
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) == src {
		t.Fatal("the fixture was already formatted; the test proves nothing")
	}

	got, err := New(nil).Format(context.Background(), "terraform", file, []byte(src))
	if err != nil {
		t.Fatalf("format on save: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("format on save is not the CLI\n got: %q\nwant: %q", got, want)
	}
}
