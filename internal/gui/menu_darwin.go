//go:build darwin

package gui

import "github.com/ebitengine/purego/objc"

// buildMenu installs the menu bar.
//
// A macOS application with no main menu never receives Cmd-Q, Cmd-C, Cmd-V or
// any other key equivalent: the OS looks for a matching menu item, finds no
// menu, and beeps. The menu is not decoration and it is not there for anyone to
// use with a mouse; it is the registration mechanism for those keys. It is
// therefore the smallest menu that makes them work and it gains an item the day
// a key needs one.
//
// The application menu's own title is ignored by AppKit: the first item of the
// main menu is always drawn with the process name, which for a bare binary with
// no .app bundle is "pvim" because that is what the executable is called.
func buildMenu(app objc.ID, delegate objc.ID) {
	nsMenu := objc.GetClass("NSMenu")
	nsMenuItem := objc.GetClass("NSMenuItem")

	newMenu := func(title string) objc.ID {
		m := objc.ID(nsMenu).Send(selAlloc)
		return m.Send(selInitWithTitle, nsString(title))
	}
	newItem := func(title string, action objc.SEL, keyEquiv string) objc.ID {
		it := objc.ID(nsMenuItem).Send(selAlloc)
		return it.Send(selInitWithTitleActionKey, nsString(title), action, nsString(keyEquiv))
	}
	attach := func(bar objc.ID, title string) objc.ID {
		holder := objc.ID(nsMenuItem).Send(selAlloc).Send(selInit)
		bar.Send(selAddItem, holder)
		sub := newMenu(title)
		holder.Send(selSetSubmenu, sub)
		return sub
	}

	add := func(menu objc.ID, it menuItem, target objc.ID) {
		item := newItem(it.title, objc.RegisterName(it.selector), string(it.key))
		if target != 0 {
			item.Send(selSetTarget, target)
		}
		menu.Send(selAddItem, item)
	}

	bar := newMenu("")

	// The application menu. Quit goes to pvim's own window delegate, which
	// pushes a CloseEvent and lets the editor decide.
	appMenu := attach(bar, "pvim")
	for _, it := range appMenuItems {
		add(appMenu, it, delegate)
	}

	// The Edit menu. Target is nil, so AppKit walks the responder chain, finds
	// the four selectors on pvimView, enables the items and delivers the keys
	// there. Without these items macOS beeps at Cmd-C and the editor never sees
	// it, which is the one thing that would make the window unusable next to
	// every other Mac application.
	//
	// Both loops read the tables in keymap.go, which is also where the view
	// decides which Command keys to refuse. One table, so the menu and the
	// refusal cannot drift apart.
	editMenu := attach(bar, "Edit")
	for _, it := range editMenuItems {
		if it.selector == "selectAll:" {
			editMenu.Send(selAddItem, objc.ID(nsMenuItem).Send(selSeparatorItem))
		}
		add(editMenu, it, 0)
	}

	app.Send(selSetMainMenu, bar)
}
