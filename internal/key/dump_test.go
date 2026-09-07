package key

import (
	"fmt"
	"os"
	"testing"
)

// TestDumpBytes is a scaffold, not an assertion: with PVIM_KEY_DUMP set it
// writes the ToBytes encoding of a table of notations so that the bytes can be
// fed to vim and checked against a mapping. It is skipped otherwise.
func TestDumpBytes(t *testing.T) {
	if os.Getenv("PVIM_KEY_DUMP") == "" {
		t.Skip("set PVIM_KEY_DUMP to dump the byte table")
	}
	for _, n := range []string{"<C-x>", "<C-a>", "<C-?>", "<C-\\>", "<C-]>", "<C-^>", "<C-_>",
		"<Nul>", "<Esc>", "<CR>", "<NL>", "<Tab>", "<BS>", "<Space>", "<lt>", "<Bar>",
		"<M-x>", "<C-M-x>", "<M-Space>", "<S-Tab>", "<Up>", "<Down>", "<Left>", "<Right>",
		"<Home>", "<End>", "<PageUp>", "<PageDown>", "<Insert>", "<Del>",
		"<F1>", "<F4>", "<F5>", "<F12>", "<C-Right>", "<S-Left>", "<M-Right>",
		"<C-Space>", "<C-1>", "<D-x>", "<C-F5>", "<S-F5>", "a", "é"} {
		ks, err := Parse(n, ",")
		if err != nil {
			t.Fatal(err)
		}
		out := ""
		for _, c := range Bytes(ks) {
			out += fmt.Sprintf("\\x%02x", c)
		}
		fmt.Printf("%s\t%s\n", n, out)
	}
}
