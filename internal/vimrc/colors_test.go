package vimrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/screen"
)

// loadScheme runs one of the four nofrils files in testdata.
func loadScheme(t *testing.T, name string) (*Config, *Result) {
	t.Helper()

	src, err := os.ReadFile(filepath.Join("testdata", "colors", name+".vim"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig()
	res := Run(src, name+".vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	return cfg, res
}

// rgb spells a colour the way the colourscheme files do, so an expectation in
// this file can be pasted out of vim's ":hi" output.
func rgb(hex string) screen.RGB {
	got, err := parseHex(hex)
	if err != nil {
		panic(err)
	}
	return got
}

func look(t *testing.T, c *Config, group string) screen.Highlight {
	t.Helper()
	return c.Table.Look(c.Table.ID(group))
}

// TestNofrilsDarkLoads is the gate's colourscheme clause and then
// some: every group here was read out of /opt/homebrew/bin/vim 9.2.0321
// with
//
//	vim --clean -u pre.vim -s keys, pre.vim being
//	 set rtp^=.../nofrils
//	 colorscheme nofrils-dark
//	and keys calling execute('hi Normal') and friends,
//
// and the expected values below are those raw attributes with this package's
// one resolution rule applied: an omitted colour, "NONE", "fg" and "bg" all
// mean Normal's, because screen.Highlight is "what a cell looks like, with
// nothing left to inherit" and a frontend must never have to know what a link
// is.
func TestNofrilsDarkLoads(t *testing.T) {
	cfg, res := loadScheme(t, "nofrils-dark")

	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}

	for _, tc := range []struct {
		group    string
		fg, bg   string
		attr     screen.Attr
		vimSaid  string
		resolved string
	}{
		{group: "Normal", fg: "#eeeeee", bg: "#262626", vimSaid: "guifg=#eeeeee guibg=#262626"},
		{group: "Comment", fg: "#6C6C6C", bg: "#262626", vimSaid: "guifg=#6C6C6C", resolved: "guibg=NONE is Normal's"},
		{group: "LineNr", fg: "#808080", bg: "#262626", vimSaid: "guifg=#808080 guibg=bg", resolved: "guibg=bg is Normal's background"},
		{group: "VertSplit", fg: "#000000", bg: "#6C6C6C", vimSaid: "guifg=black guibg=#6C6C6C", resolved: "black is #000000 in v:colornames"},
		{group: "IncSearch", fg: "#000000", bg: "#00ff00", vimSaid: "guifg=black guibg=green", resolved: "green is #00ff00 in v:colornames"},
		{group: "StatusLine", fg: "#000000", bg: "#eeeeee", vimSaid: "guifg=black guibg=fg", resolved: "guibg=fg is Normal's foreground"},
		{group: "WildMenu", fg: "#eeeeee", bg: "#000000", vimSaid: "guifg=fg guibg=black"},
		{group: "Todo", fg: "#00FF00", bg: "#000000", vimSaid: "guifg=#00FF00 guibg=black"},
		{group: "Visual", fg: "#eeeeee", bg: "#262626", attr: screen.AttrReverse, vimSaid: "gui=reverse"},
		{group: "VisualNOS", fg: "#eeeeee", bg: "#262626", attr: screen.AttrReverse | screen.AttrUnderline, vimSaid: "gui=underline,reverse"},
	} {
		got := look(t, cfg, tc.group)
		// SP falls back to the group's own foreground, because that is the
		// colour vim draws an undercurl in when guisp is unset, and nofrils
		// never writes a guisp.
		want := screen.Highlight{FG: rgb(tc.fg), BG: rgb(tc.bg), SP: rgb(tc.fg), Attr: tc.attr}
		if got != want {
			t.Errorf("%s = %+v, want %+v (vim said %q%s)", tc.group, got, want, tc.vimSaid, note(tc.resolved))
		}
	}
}

func note(s string) string {
	if s == "" {
		return ""
	}
	return "; " + s
}

// TestAllFourSchemesLoad. Each one's Normal was read from the installed vim on
// the same way, and Normal is the value the gate names because
// every other group in the file resolves against it.
func TestAllFourSchemesLoad(t *testing.T) {
	for _, tc := range []struct {
		name, fg, bg string
	}{
		{"nofrils-dark", "#eeeeee", "#262626"},
		{"nofrils-light", "#000000", "#E4E4E4"},
		{"nofrils-sepia", "#000000", "#ffdfaf"},
		{"nofrils-acme", "#000000", "#ffffd7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, res := loadScheme(t, tc.name)
			for _, d := range res.Errors {
				t.Errorf("%v", d)
			}
			got := look(t, cfg, "Normal")
			if got.FG != rgb(tc.fg) || got.BG != rgb(tc.bg) {
				t.Errorf("Normal = %+v, want guifg=%s guibg=%s", got, tc.fg, tc.bg)
			}
			// "guifg=fg" on VertSplit in three of the four is the case that
			// makes resolution order matter: it is Normal's foreground and
			// Normal is defined earlier in the file, so a scheme resolved in
			// file order gets it right and one resolved lazily may not.
			if tc.name != "nofrils-dark" {
				if vs := look(t, cfg, "VertSplit"); vs.FG != rgb(tc.fg) {
					t.Errorf("VertSplit guifg=fg resolved to %+v, want Normal's %s", vs.FG, tc.fg)
				}
			}
		})
	}
}

// TestASchemeThatSetsNormalLastStillResolves is why Config.rebuild finds
// Normal before it walks the file. Nothing in nofrils does this and the next
// colourscheme somebody tries might.
func TestASchemeThatSetsNormalLastStillResolves(t *testing.T) {
	cfg := NewConfig()
	src := []byte("hi Comment guifg=#112233 guibg=NONE\nhi Normal guifg=#eeeeee guibg=#262626\n")
	res := Run(src, "scheme.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}
	if got := look(t, cfg, "Comment"); got.BG != rgb("#262626") {
		t.Errorf("Comment background = %+v, want Normal's #262626", got.BG)
	}
}

// TestHighlightLinkResolves. nofrils has no "hi link" and vim's own defaults
// are full of them, so the first colourscheme that is not nofrils will bring
// one.
func TestHighlightLinkResolves(t *testing.T) {
	cfg := NewConfig()
	src := []byte("hi Normal guifg=#eeeeee guibg=#262626\nhi Comment guifg=#6c6c6c\nhi link SpecialComment Comment\n")
	res := Run(src, "scheme.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}
	if got, want := look(t, cfg, "SpecialComment"), look(t, cfg, "Comment"); got != want {
		t.Errorf("SpecialComment = %+v, want Comment's %+v", got, want)
	}
}

// TestTheSchemesIgnoredList is the other half of the dividing rule for a
// colourscheme: four variables nothing reads, three function definitions, and
// the ":call" at the end of the file.
func TestTheSchemesIgnoredList(t *testing.T) {
	_, res := loadScheme(t, "nofrils-dark")

	want := []string{
		"g:colors_name",
		"g:nofrils_strbackgrounds", "g:nofrils_heavycomments", "g:nofrils_heavylinenumbers",
		"NofrilsFocusComments", "NofrilsFocusCode", "NofrilsNormal",
		"NofrilsNormal", // the ":call" on the last line
	}
	if got := res.IgnoredNames(); !equal(got, want) {
		t.Errorf("ignored list is\n %v\nwant\n %v", got, want)
	}

	reasons := map[IgnoreReason]int{}
	for _, i := range res.Ignored {
		reasons[i.Reason]++
	}
	if reasons[IgnoreFunc] != 3 || reasons[IgnoreCall] != 1 || reasons[IgnoreVar] != 4 {
		t.Errorf("reasons = %v, want 3 functions, 1 call, 4 variables", reasons)
	}
}

// TestACallOfAFunctionNobodyDefinedIsAnError keeps the ":call" refusal from
// being a blanket one. A call of a function this loader stepped over is a
// logged refusal; a call of anything else is E117, which is exactly what vim
// says when the function is not there. Measured
// "E117: Unknown function: NoSuchFunction".
func TestACallOfAFunctionNobodyDefinedIsAnError(t *testing.T) {
	cfg := NewConfig()
	res := Run([]byte("call Frobnicate()\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	if len(res.Errors) != 1 {
		t.Fatalf("errors = %v, want one E117", res.Errors)
	}
	if !strings.Contains(res.Errors[0].Error(), "E117") {
		t.Errorf("error is %v, want E117", res.Errors[0])
	}
	if len(res.Ignored) != 0 {
		t.Errorf("ignored %v; a call of a function nobody defined is not a refusal, it is a mistake", res.Ignored)
	}
}

// TestTheNofrilsCallIsANoOp is the honesty check behind skipping ":function"
// and ":call".
//
// The last line of each nofrils file is "call NofrilsNormal()", and this
// package does not run it. That is only safe because the five ":hi" lines at
// the top of that function's body are byte-identical to the five top-level
// ones for the same groups, and the three ":if"s after them are guarded by
// g:nofrils_strbackgrounds, g:nofrils_heavycomments and
// g:nofrils_heavylinenumbers, all of which the file has just set to 0. So the
// call changes nothing, and the table pvim builds is the table vim builds.
//
// This test reads the FILES rather than the parser, because what it is
// asserting is a property of the files: the day one of them changes so that
// the call does something, this fails and the refusal has to be revisited.
func TestTheNofrilsCallIsANoOp(t *testing.T) {
	for _, name := range []string{"nofrils-dark", "nofrils-light", "nofrils-sepia", "nofrils-acme"} {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("testdata", "colors", name+".vim"))
			if err != nil {
				t.Fatal(err)
			}
			top := map[string]string{}
			body := map[string]string{}
			// fn is the function being read, empty at the top level, and
			// guarded says the reader has passed that function's first ":if",
			// after which every ":hi" is behind a g:nofrils_* the file has
			// just set to 0. All three functions have to be tracked and not
			// only NofrilsNormal: the other two set Comment and Normal to
			// different values, and a reader that saw only NofrilsNormal
			// would take those for top-level definitions.
			fn, guarded := "", false

			for _, line := range strings.Split(string(src), "\n") {
				trimmed := strings.TrimSpace(line)
				switch {
				case strings.HasPrefix(trimmed, "function"):
					name, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(trimmed, "function"), "!"), "(")
					fn, guarded = strings.TrimSpace(name), false
					continue
				case fn != "" && strings.HasPrefix(trimmed, "endfunction"):
					fn = ""
					continue
				case fn != "" && strings.HasPrefix(trimmed, "if "):
					guarded = true
					continue
				}
				if !strings.HasPrefix(trimmed, "hi ") {
					continue
				}
				group, args, ok := strings.Cut(strings.TrimPrefix(trimmed, "hi "), " ")
				if !ok {
					continue
				}
				switch {
				case fn == "NofrilsNormal" && !guarded:
					body[group] = args
				case fn == "":
					top[group] = args
				}
			}

			if len(body) != 5 {
				t.Fatalf("NofrilsNormal defines %d groups before its first if, want 5", len(body))
			}
			for group, args := range body {
				if top[group] != args {
					t.Errorf("%s differs between the top level and NofrilsNormal():\n  top: %s\n  fn : %s\nthe ':call' this package skips is no longer a no-op", group, top[group], args)
				}
			}
		})
	}
}
