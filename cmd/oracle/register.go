package main

// The register: every place pvim is allowed to disagree with vim, decided on
// purpose rather than discovered.
//
// It is a table in the harness and not a file beside it, so that a difference
// cannot be registered without a code change and a reason in the same commit.
// The rule it enforces is one sentence: an unregistered difference fails the
// run, and a registered one passes with a tilde and its id on stdout, so it
// stays visible rather than becoming a quiet exception.
//
// Fewer than ten rows, or the register is doing work the code should be doing.

// registered is the whole register.
//
// keys names the case a row covers: a bare name covers it under both option
// profiles, "name@profile" covers one, and an empty string is a decision with
// no case behind it, which registers nothing and fails nothing.
var registered = []difference{
	{
		id:   "D-001",
		what: "`:help` opens vim's installed documentation read-only, and so does vim",
		why: "Registered as expected-identical, which is the point: the day the " +
			"runtime moves, this turns into a failure with a name on it rather " +
			"than a help window that is empty for a week",
	},
	{
		id:   "D-002",
		what: "An unimplemented E-code prints vim's code with different wording",
		why: "The code is what scripts and habits key off; the sentence after it " +
			"is not, and matching vim's sentence for an error that is otherwise " +
			"unimplemented is copying a string to pass a test",
	},
	{
		id:   "D-003",
		what: "`:!` puts output in a scratch split and does not wait for Enter; vim puts it on the command line and waits",
		why: "'cmdheight' exists to get rid of the press-enter prompt, so " +
			"reproducing the prompt would reproduce the thing the option is " +
			"there to avoid",
	},
	{
		id:   "D-004",
		what: "The GUI cursor does not blink; vim's does",
		why:  "Nobody wants it",
	},
	{
		id:   "D-005",
		keys: "hash_over_cjk_word",
		what: "`\\<` and `\\>` find nothing beside a keyword character above Latin-1, so `*` and `#` over a CJK word report E486 where vim jumps to the next one",
		why: "Patterns are translated into Go's regexp rather than matched by a " +
			"backtracking engine, and `\\<`/`\\>` become `\\b`, which is not " +
			"'iskeyword'-aware. RE2 has no lookbehind and no Unicode word " +
			"boundary, so there is nothing to translate into; dropping the " +
			"boundary instead would silently match a word inside a longer one, " +
			"which is worse than finding nothing",
	},
	{
		id:   "D-006",
		keys: "equals_runs_the_c_indent",
		what: "`=` indents by 'autoindent' plus 'smartindent'; vim runs its C indenter whatever the filetype is",
		why: "Filetype indent scripts are out of scope: 'autoindent' plus " +
			"'smartindent' and 'cinwords' is the whole indent engine. vim " +
			"reaches its C indenter only because 'equalprg' is empty, there is " +
			"no 'indentexpr' and 'lisp' is off, which is fifteen hundred lines " +
			"of C with 'cinoptions' behind it",
	},
	{
		id:   "D-007",
		what: `An undo message says "0 seconds ago" whatever the clock says; vim reports the real elapsed time`,
		why: "The undo tree keeps a timestamp per step but nothing reads it back " +
			"into the message yet. vim's number is a reading of the wall clock " +
			"rather than anything an editor decided, so the harness compares the " +
			"clause for shape and not for value: a run that spawns a subprocess " +
			"lands either side of a second boundary and the same vim disagrees " +
			"with itself, which is a nondeterministic harness and not a difference",
	},
}

// loadRegister builds the lookup over registered.
//
// It cannot fail, which is the reason the table moved into Go: a register in a
// file could be missing, half-written or edited by hand between two runs, and
// each of those made an unregistered difference look like a passing one.
func loadRegister() *register {
	r := &register{byKeys: map[string][]difference{}}
	for _, d := range registered {
		r.all = append(r.all, d)
		if d.keys != "" {
			r.byKeys[d.keys] = append(r.byKeys[d.keys], d)
		}
	}
	return r
}
