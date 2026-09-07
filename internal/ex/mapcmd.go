package ex

// The ":map" family: ":map", ":nmap", ":vmap", ":xmap", ":omap", ":imap",
// ":lmap", ":cmap", the "nore" spellings of each, the ":unmap" spellings and
// the ":mapclear" ones.
//
// None of it is resolved here. A mapping is keys in and keys out, the table
// that holds them is the frontend's and the parser that reads one is
// internal/vimrc's, so what this file does is decide that the command IS a map
// command and hand the line to Context.Map. That is the whole of it, and it is
// worth its own file only because the alternative was thirty-five entries in
// handlers.go pointing at a function in windows.go.
//
// Why the line is rebuilt rather than passed through as typed: the name is
// resolved. ":nn" and ":nnoremap" are the same command and a parser on the far
// side of the hook should not have to abbreviate-match a second time, so what
// crosses is always the full spelling.

// exMap is every command of the map family.
func exMap(c *Context, cmd Cmd) error {
	if c == nil || c.Map == nil {
		return withName(ErrNotImplemented, cmd.Name)
	}
	line := cmd.Name
	if cmd.Bang {
		line += "!"
	}
	if cmd.Args != "" {
		line += " " + cmd.Args
	}
	return c.Map(line)
}

// mapCommands is every name exMap is attached to, which is also the list
// cmdEnd reads: none of them has vim's EX_TRLBAR, so a bar in a right-hand
// side is part of the mapping and not the start of another command.
var mapCommands = []string{
	"map", "mapclear", "noremap", "unmap",
	"nmap", "nmapclear", "nnoremap", "nunmap",
	"vmap", "vmapclear", "vnoremap", "vunmap",
	"xmap", "xmapclear", "xnoremap", "xunmap",
	"omap", "omapclear", "onoremap", "ounmap",
	"imap", "imapclear", "inoremap", "iunmap",
	"lmap", "lmapclear", "lnoremap", "lunmap",
	"cmap", "cmapclear", "cnoremap", "cunmap",
}

// isMapCommand reports whether a resolved command name is one of them.
func isMapCommand(name string) bool {
	for _, n := range mapCommands {
		if n == name {
			return true
		}
	}
	return false
}
