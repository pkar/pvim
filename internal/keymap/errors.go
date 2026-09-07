package keymap

import (
	"errors"
	"fmt"
)

// The errors this package returns.
//
// Three of them are vim's own codes with vim's own wording, measured out of
// 9.2 and quoted exactly, because a script and a habit both key off the code
// and these are errors pvim really does implement. The other two are pvim's
// own and are numbered above E1820, which is the highest internal/regex has
// taken, which is itself above E1575, the highest vim 9.2 uses. A pvim code can
// therefore never collide with a real one.
var (
	// ErrRecursive is vim's E223, raised when a mapping chain is deeper than
	// MaxDepth. Measured on `nmap a b` plus `nmap b a`: "E223: Recursive
	// mapping" and the half-typed command thrown away.
	ErrRecursive = errors.New("E223: Recursive mapping")

	// ErrNoMapping is vim's E31, raised by Unmap for a left-hand side that is
	// not mapped in the mode asked for. Measured: ":nunmap zz" on a fresh vim,
	// and ":vunmap ab" when ab is mapped with :nnoremap.
	ErrNoMapping = errors.New("E31: No such mapping")
)

// UniqueError is vim's E227: a <unique> mapping whose left-hand side is
// already mapped in one of the modes asked for.
//
// It is an exact test on the left-hand side and not an overlap test. Measured:
// ":nnoremap <unique> ab bar" fails when ab is mapped and succeeds when only
// abc is.
type UniqueError struct{ LHS string }

func (e *UniqueError) Error() string {
	return "E227: Mapping already exists for " + e.LHS
}

// ExprError is the refusal of <expr>.
//
// The right-hand side of an <expr> mapping is a vimscript expression evaluated
// afresh on every keystroke, and an expression evaluator is out of scope.
// Refused when the mapping is made, not when it is pressed,
// because there is nothing about the keys that could ever work and a mapping
// that swallowed a key silently would be worse than one that never existed.
type ExprError struct{ LHS string }

func (e *ExprError) Error() string {
	return fmt.Sprintf("E1821: <expr> mapping is not supported: %s", e.LHS)
}

// ScriptIDError is the refusal of a <SID> right-hand side at the moment its
// keys would be fed back into the editor.
//
// <SID> is the script-local function prefix, and resolving it needs a script
// context: which file defined the mapping and what number that file was given.
// pvim's vimrc subset has no script-local functions to name, so the five
// characters are parsed and stored and there is nothing behind them. Refused on
// use rather than on definition, because a mapping the vimrc makes and nobody
// presses must not put anything on the message line at startup.
type ScriptIDError struct{ LHS string }

func (e *ScriptIDError) Error() string {
	return fmt.Sprintf("E1822: <SID> in a mapping has no script context: %s", e.LHS)
}
