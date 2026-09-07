//go:build darwin

package gui

import (
	"sync"

	"github.com/ebitengine/purego/objc"
)

// The NSView subclass, registered at run time because there is no compiler here
// to declare one at build time.
//
// EVERY callback below is created once, inside registerViewClass, which runs
// once per process. purego's callback table is 2000 entries wide, is never
// reclaimed, and panics when it fills. Nothing in this package may create a
// callback in response to an event, and the one place that looks like it wants
// to, the CGDataProvider release callback in blit_darwin.go, is guarded by its
// own sync.Once for the same reason.

// current is the one window in this process, reachable from the callbacks.
//
// It is a package-level variable and not a pointer smuggled through the view's
// ivars because handing C a Go pointer to hold is the thing not to do, and
// because a second window would need a second NSApplication's worth of
// rethinking anyway: this editor has tabs, not windows.
var current *window

var (
	viewClassOnce sync.Once
	viewClass     objc.Class
	viewClassErr  error

	delegateClassOnce sync.Once
	delegateClass     objc.Class
	delegateClassErr  error
)

// registerViewClass creates the pvimView class the first time it is asked for.
//
// The class conforms to NSTextInputClient, which is not decoration: -inputContext
// returns nil for a view whose class does not conform, and -interpretKeyEvents:
// on a view with no input context does nothing at all, so dead keys and the
// press-and-hold accent popup would silently not work. A protocol that cannot
// be looked up leaves imeEnabled false and the window takes the ordinary key
// path, which is a window with no dead keys rather than no window.
func registerViewClass() (objc.Class, error) {
	viewClassOnce.Do(func() {
		var protocols []*objc.Protocol
		if p := objc.GetProtocol("NSTextInputClient"); p != nil {
			protocols = append(protocols, p)
			imeEnabled = true
		}
		methods := append(viewMethods(), textInputMethods()...)
		viewClass, viewClassErr = objc.RegisterClass("pvimView", objc.GetClass("NSView"), protocols, nil, methods)
		if viewClassErr != nil {
			imeEnabled = false
		}
	})
	return viewClass, viewClassErr
}

// viewMethods is everything pvimView answers that is not the input-method
// protocol, which lives in ime_darwin.go.
func viewMethods() []objc.MethodDef {
	return []objc.MethodDef{
		// A view that does not accept first responder never sees a key
		// event at all, and the window beeps at every keystroke instead.
		{Cmd: objc.RegisterName("acceptsFirstResponder"), Fn: func(self objc.ID, cmd objc.SEL) bool { return true }},

		// A flipped view has its origin at the top left, which is where a
		// text grid's origin is. Without this every mouse coordinate and
		// every layer offset would need a subtraction from the height, and
		// one of them would eventually be forgotten.
		{Cmd: objc.RegisterName("isFlipped"), Fn: func(self objc.ID, cmd objc.SEL) bool { return true }},

		// wantsUpdateLayer says "do not call drawRect:, call updateLayer".
		// That is the layer-contents blit path rather than the
		// draw-into-a-context path, and it is the reason this editor never
		// builds a CGContext or copies a pixel: the RGBA buffer x/image
		// filled is handed to the window server as it stands.
		{Cmd: objc.RegisterName("wantsUpdateLayer"), Fn: func(self objc.ID, cmd objc.SEL) bool { return true }},
		{Cmd: objc.RegisterName("updateLayer"), Fn: func(self objc.ID, cmd objc.SEL) {
			if current != nil {
				current.blitFront()
			}
		}},

		{Cmd: objc.RegisterName("keyDown:"), Fn: func(self objc.ID, cmd objc.SEL, ev objc.ID) {
			if current != nil {
				current.keyDown(self, ev)
			}
		}},
		// keyUp: is swallowed. Vim has no notion of a key release and
		// letting it fall through to NSResponder produces the system beep.
		{Cmd: objc.RegisterName("keyUp:"), Fn: func(self objc.ID, cmd objc.SEL, ev objc.ID) {}},

		// flagsChanged: is the only way to know Shift is down while the
		// mouse is being dragged, since a drag event's own modifierFlags
		// are reliable but a wheel notch's are not on every input device.
		{Cmd: objc.RegisterName("flagsChanged:"), Fn: func(self objc.ID, cmd objc.SEL, ev objc.ID) {
			if current != nil {
				current.setMods(uint(objc.Send[uint64](ev, selModifierFlags)))
			}
		}},

		// performKeyEquivalent: catches Cmd-anything the menu does not own.
		//
		// The order is the trap and it is the opposite of what the name
		// suggests: NSApplication offers a Command-modified key to the key
		// window's view hierarchy FIRST and to the main menu only if every
		// view refuses it. A view that claimed everything would therefore
		// disable the whole menu bar, Cmd-Q included, which is why
		// menuOwnsKeyEquivalent exists and why this returns false for the
		// five keys the menu defines.
		{Cmd: objc.RegisterName("performKeyEquivalent:"), Fn: func(self objc.ID, cmd objc.SEL, ev objc.ID) bool {
			if current == nil {
				return false
			}
			return current.onKeyEquivalent(ev)
		}},

		{Cmd: objc.RegisterName("mouseDown:"), Fn: mouseIMP(MouseLeft, MousePress)},
		{Cmd: objc.RegisterName("mouseUp:"), Fn: mouseIMP(MouseLeft, MouseRelease)},
		{Cmd: objc.RegisterName("mouseDragged:"), Fn: mouseIMP(MouseLeft, MouseDrag)},
		{Cmd: objc.RegisterName("rightMouseDown:"), Fn: mouseIMP(MouseRight, MousePress)},
		{Cmd: objc.RegisterName("rightMouseUp:"), Fn: mouseIMP(MouseRight, MouseRelease)},
		{Cmd: objc.RegisterName("rightMouseDragged:"), Fn: mouseIMP(MouseRight, MouseDrag)},
		{Cmd: objc.RegisterName("otherMouseDown:"), Fn: mouseIMP(MouseMiddle, MousePress)},
		{Cmd: objc.RegisterName("otherMouseUp:"), Fn: mouseIMP(MouseMiddle, MouseRelease)},
		{Cmd: objc.RegisterName("otherMouseDragged:"), Fn: mouseIMP(MouseMiddle, MouseDrag)},

		{Cmd: objc.RegisterName("scrollWheel:"), Fn: func(self objc.ID, cmd objc.SEL, ev objc.ID) {
			if current != nil {
				current.onScroll(ev)
			}
		}},

		// setFrameSize: is the resize hook. NSView's own implementation has
		// to run, hence the SendSuper, and the recount happens after it so
		// that -bounds already reports the new size.
		{Cmd: objc.RegisterName("setFrameSize:"), Fn: func(self objc.ID, cmd objc.SEL, size NSSize) {
			self.SendSuper(cmd, size)
			if current != nil {
				current.resized()
			}
		}},

		// Dragging the window to a display with a different
		// backingScaleFactor changes the size of every glyph, so the face
		// is rebuilt and the grid recounted.
		{Cmd: objc.RegisterName("viewDidChangeBackingProperties"), Fn: func(self objc.ID, cmd objc.SEL) {
			self.SendSuper(cmd)
			if current != nil {
				current.backingChanged()
			}
		}},

		// The Edit menu's four items target the first responder, which is
		// this view. Implementing them is what makes macOS route Cmd-X, -C,
		// -V and -A here instead of greying the menu out and beeping; they
		// arrive at the editor as the Cmd-modified keys, which is what the
		// vimrc's clipboard maps are written against.
		{Cmd: objc.RegisterName("cut:"), Fn: editIMP(editCut)},
		{Cmd: objc.RegisterName("copy:"), Fn: editIMP(editCopy)},
		{Cmd: objc.RegisterName("paste:"), Fn: editIMP(editPaste)},
		{Cmd: objc.RegisterName("selectAll:"), Fn: editIMP(editSelectAll)},
	}
}

// mouseIMP builds the callback for one of the nine mouse selectors. It is
// called nine times at registration and never again.
func mouseIMP(button MouseButton, action MouseAction) func(objc.ID, objc.SEL, objc.ID) {
	return func(self objc.ID, cmd objc.SEL, ev objc.ID) {
		if current != nil {
			current.onMouse(ev, button, action)
		}
	}
}

// editIMP builds the callback for one Edit menu selector, which types the keys
// vim binds that menu item to. See menukeys.go.
func editIMP(a editAction) func(objc.ID, objc.SEL, objc.ID) {
	return func(self objc.ID, cmd objc.SEL, sender objc.ID) {
		if current != nil {
			current.onMenuAction(a)
		}
	}
}

// registerDelegateClass creates the NSObject subclass that is the window's
// delegate and the Quit menu item's target.
func registerDelegateClass() (objc.Class, error) {
	delegateClassOnce.Do(func() {
		delegateClass, delegateClassErr = objc.RegisterClass("pvimDelegate", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
			// The red button. Returning NO keeps the window up and hands the
			// decision to the editor, which under 'confirm' has a prompt to put
			// on the command line first.
			{Cmd: objc.RegisterName("windowShouldClose:"), Fn: func(self objc.ID, cmd objc.SEL, sender objc.ID) bool {
				if current != nil {
					current.q.push(CloseEvent{})
				}
				return false
			}},

			// Cmd-Q. Without an NSMenu carrying this item macOS never delivers
			// Cmd-Q at all, which is the entire reason there is a menu.
			{Cmd: objc.RegisterName("pvimQuit:"), Fn: func(self objc.ID, cmd objc.SEL, sender objc.ID) {
				if current != nil {
					current.q.push(CloseEvent{})
				}
			}},

			// The other way out, and the one that would otherwise take the
			// process down where it stands: Quit from the Dock menu, and a
			// logout or restart asking every application to go. Both send
			// -terminate:, which without this method kills the process with a
			// modified buffer still in it and nothing on the command line to
			// say so.
			//
			// NSTerminateCancel, which is 0, refuses and hands the decision to
			// the editor as the same CloseEvent Cmd-Q produces. An editor that
			// then quits does it by returning from its handler, which stops
			// the run loop; one that has an unsaved buffer under 'confirm'
			// prompts instead. That is the same rule as the red button and it
			// is why this file names terminate: in a comment and never sends
			// it.
			{Cmd: objc.RegisterName("applicationShouldTerminate:"), Fn: func(self objc.ID, cmd objc.SEL, sender objc.ID) uint64 {
				if current != nil {
					current.q.push(CloseEvent{})
				}
				return nsTerminateCancel
			}},

			// Focus. vim draws a hollow caret in a window that is not the key
			// window and a solid one in the window that is, which is the only
			// way to tell at a glance which of two editors a keystroke would go
			// to. The editor decides that; this says which it is.
			{Cmd: objc.RegisterName("windowDidBecomeKey:"), Fn: func(self objc.ID, cmd objc.SEL, note objc.ID) {
				if current != nil {
					current.onFocus(true)
				}
			}},
			{Cmd: objc.RegisterName("windowDidResignKey:"), Fn: func(self objc.ID, cmd objc.SEL, note objc.ID) {
				if current != nil {
					current.onFocus(false)
				}
			}},
		})
	})
	return delegateClass, delegateClassErr
}
