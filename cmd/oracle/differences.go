package main

import "strings"

// difference is one registered disagreement with vim. See register.go.
type difference struct {
	id   string // D-001
	keys string // case name, case@profile, or empty for a decision with no case
	what string
	why  string
}

// register is the lookup over the table in register.go.
//
// An unregistered difference fails the run and a registered one passes with a
// tilde, which is what keeps every disagreement between the two editors named
// rather than quietly tolerated.
type register struct {
	byKeys map[string][]difference
	all    []difference
}

// splitRow cuts a markdown table row into its cells.
func splitRow(row string) []string {
	row = strings.Trim(row, "|")
	cells := strings.Split(row, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// lookup finds the entry that covers a case under a profile, if there is one.
//
// A row may name the case alone, in which case both profiles are covered, or
// name it as case@profile when only one of them disagrees. Nothing matches "-",
// which is what an entry that has no case file yet is written with.
func (r *register) lookup(caseName, profileName string) (difference, bool) {
	for _, key := range []string{caseName + "@" + profileName, caseName} {
		if ds := r.byKeys[key]; len(ds) > 0 {
			return ds[0], true
		}
	}
	return difference{}, false
}
