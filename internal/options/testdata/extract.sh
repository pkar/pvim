#!/bin/sh
# Extract the option table out of vim's own options.txt.
#
# Every option in that file is introduced by a header of exactly this shape,
# and vim's documentation is generated alongside the C table it describes, so
# it is the closest thing to the source of truth that ships on this machine:
#
#	'tabstop' 'ts'		number	(default 8)
#				local to buffer
#
# The header line gives the long name, the abbreviation when there is one and
# the type; the line under it gives the scope. Everything else in the entry is
# prose. Output is name<TAB>abbrev<TAB>type<TAB>scope, one option per line,
# with the vim patch level in the file name so that a brew upgrade regenerating
# it is a diff somebody reads.
#
# Usage: extract.sh $VIMRUNTIME/doc/options.txt > vim-9.2.0321-options.tsv
awk '
/^'"'"'[a-z_]+'"'"'([ \t]+'"'"'[a-z]+'"'"')?[ \t]+(boolean|number|string)[ \t]/ {
	line = $0
	# name and optional abbreviation, both in single quotes
	n = split(line, f, /[ \t]+/)
	name = f[1]; gsub(/'"'"'/, "", name)
	abbrev = ""
	i = 2
	if (f[i] ~ /^'"'"'/) { abbrev = f[i]; gsub(/'"'"'/, "", abbrev); i++ }
	type = f[i]
	pending = name "\t" abbrev "\t" type
	next
}
pending != "" {
	scope = ""
	if ($0 ~ /global or local to tab page/) scope = "global-tabpage"
	else if ($0 ~ /global or local to buffer/) scope = "global-buffer"
	else if ($0 ~ /global or local to window/) scope = "global-window"
	else if ($0 ~ /local to buffer/) scope = "buffer"
	else if ($0 ~ /local to window/) scope = "window"
	else if ($0 ~ /^[ \t]+global[ \t]*$/ || $0 ~ /^[ \t]+global[ \t]+\|/) scope = "global"
	if (scope != "") { print pending "\t" scope; pending = "" }
	next
}
' "$1" | sort
