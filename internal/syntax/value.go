package syntax

import (
	"strconv"
	"strings"
)

// The value type of the vimscript subset this package evaluates.
//
// Three kinds and no more: a number, a string, and a list. Syntax files use
// dictionaries in exactly one shape -- `get(g:, 'name', default)`, where g: is
// the dictionary -- so g: is a Go map in the interpreter and never a value,
// and nothing here can build one. A funcref is not a value either; a call is
// resolved by name at the point it is written.

type valueKind int

const (
	vNumber valueKind = iota
	vString
	vList
)

type value struct {
	kind valueKind
	num  int
	str  string
	list []value
}

func num(n int) value    { return value{kind: vNumber, num: n} }
func str(s string) value { return value{kind: vString, str: s} }
func boolean(b bool) value {
	if b {
		return num(1)
	}
	return num(0)
}

// truthy is vim's rule: a number is true when it is not zero, and a string is
// true when the number at its front is not zero, which is almost never.
func (v value) truthy() bool {
	switch v.kind {
	case vNumber:
		return v.num != 0
	case vString:
		n, _ := leadingNumber(v.str)
		return n != 0
	}
	return len(v.list) != 0
}

// text is vim's string coercion.
func (v value) text() string {
	switch v.kind {
	case vNumber:
		return strconv.Itoa(v.num)
	case vString:
		return v.str
	}
	parts := make([]string, len(v.list))
	for i, e := range v.list {
		parts[i] = "'" + e.text() + "'"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// number is vim's number coercion.
func (v value) number() int {
	switch v.kind {
	case vNumber:
		return v.num
	case vString:
		n, _ := leadingNumber(v.str)
		return n
	}
	return 0
}

// leadingNumber reads the number at the front of a string, which is what vim
// does whenever a string is used as one.
func leadingNumber(s string) (int, bool) {
	s = strings.TrimLeft(s, " \t")
	i := 0
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		i++
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == i {
		return 0, false
	}
	n, err := strconv.Atoi(s[:j])
	if err != nil {
		return 0, false
	}
	return n, true
}
