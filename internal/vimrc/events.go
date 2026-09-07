package vimrc

import "strings"

// eventNames is every autocommand event vim 9.2.0321 knows, read out of the
// installed binary with
//
//	:call writefile(getcompletion('', 'event'), 'out')
//
// and not out of the documentation, because the list is what decides whether
// the first word after ":autocmd" is an event or an augroup name. A name
// missing here turns ":au FocusLost * ..." into an autocommand in a group
// called FocusLost, silently, which is the worst failure this file can have.
var eventNames = []string{
	"BufAdd", "BufCreate", "BufDelete", "BufEnter", "BufFilePost", "BufFilePre",
	"BufHidden", "BufLeave", "BufNew", "BufNewFile", "BufRead", "BufReadCmd",
	"BufReadPost", "BufReadPre", "BufUnload", "BufWinEnter", "BufWinLeave",
	"BufWipeout", "BufWrite", "BufWriteCmd", "BufWritePost", "BufWritePre",
	"CmdUndefined", "CmdlineChanged", "CmdlineEnter", "CmdlineLeave",
	"CmdlineLeavePre", "CmdwinEnter", "CmdwinLeave", "ColorScheme",
	"ColorSchemePre", "CompleteChanged", "CompleteDone", "CompleteDonePre",
	"CursorHold", "CursorHoldI", "CursorMoved", "CursorMovedC", "CursorMovedI",
	"DiffUpdated", "DirChanged", "DirChangedPre", "EncodingChanged", "ExitPre",
	"FileAppendCmd", "FileAppendPost", "FileAppendPre", "FileChangedRO",
	"FileChangedShell", "FileChangedShellPost", "FileEncoding", "FileReadCmd",
	"FileReadPost", "FileReadPre", "FileType", "FileWriteCmd", "FileWritePost",
	"FileWritePre", "FilterReadPost", "FilterReadPre", "FilterWritePost",
	"FilterWritePre", "FocusGained", "FocusLost", "FuncUndefined", "GUIEnter",
	"GUIFailed", "InsertChange", "InsertCharPre", "InsertEnter", "InsertLeave",
	"InsertLeavePre", "KeyInputPre", "MenuPopup", "ModeChanged",
	"OSAppearanceChanged", "OptionSet", "QuickFixCmdPost", "QuickFixCmdPre",
	"QuitPre", "RemoteReply", "SafeState", "SafeStateAgain", "SessionLoadPost",
	"SessionLoadPre", "SessionWritePost", "ShellCmdPost", "ShellFilterPost",
	"SigUSR1", "SourceCmd", "SourcePost", "SourcePre", "SpellFileMissing",
	"StdinReadPost", "StdinReadPre", "SwapExists", "Syntax", "TabClosed",
	"TabClosedPre", "TabEnter", "TabLeave", "TabNew", "TermChanged",
	"TermResponse", "TermResponseAll", "TerminalOpen", "TerminalWinOpen",
	"TextChanged", "TextChangedI", "TextChangedP", "TextChangedT",
	"TextYankPost", "User", "VimEnter", "VimLeave", "VimLeavePre", "VimResized",
	"VimResume", "VimSuspend", "WinClosed", "WinEnter", "WinLeave", "WinNew",
	"WinNewPre", "WinResized", "WinScrolled",
}

// eventAliases maps the four events vim spells two ways onto the spelling it
// prints, which is the one this package stores.
//
// It matters for matching and not only for printing: the vimrc registers
// BufWritePre and BufRead, and a table that held those under one name and
// looked them up under the other would fire nothing. Measured
// ":autocmd BufReadPost *.go" lists the vimrc's autocommand under the heading
// "BufRead", and ":autocmd BufWritePre" lists the nested one under
// "BufWrite".
var eventAliases = map[string]string{
	"bufcreate":    "BufAdd",
	"bufreadpost":  "BufRead",
	"bufwritepre":  "BufWrite",
	"fileencoding": "EncodingChanged",
}

// events resolves a lower-cased event name to vim's spelling of it. Built once
// rather than searched, because ":autocmd" asks per line at startup and
// Config.Match asks per event per buffer write forever.
var events = func() map[string]string {
	m := make(map[string]string, len(eventNames))
	for _, name := range eventNames {
		m[strings.ToLower(name)] = name
	}
	for typed, canonical := range eventAliases {
		m[typed] = canonical
	}
	return m
}()

// canonicalEvent returns vim's spelling of an event name, matched without
// regard to case as vim matches it, and reports whether it is one at all.
//
// "Filetype", which is how the vimrc's line 183 spells it, is FileType.
func canonicalEvent(name string) (string, bool) {
	got, ok := events[strings.ToLower(name)]
	return got, ok
}
