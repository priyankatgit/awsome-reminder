package app

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// orderFrontNoActivate puts window on screen, above other apps' windows
// within its level, without making it key and without activating the app.
// Main thread only.
static void orderFrontNoActivate(void *window) {
	[(NSWindow *)window orderFrontRegardless];
}
*/
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

// showNoActivate shows win without activating the app, so the user's
// foreground app keeps keyboard focus.
//
// Wails v3.1.0's Show ends in makeKeyAndOrderFront (windowShow in
// webview_window_darwin.go), and its Focus activates the app; only a
// non-activating NSPanel is ordered front with orderFrontRegardless. So the
// window (NativeWindow is its NSWindow) is ordered front here directly and
// Wails' Show is not called. Wails' visibility stays consistent: IsVisible
// reads the window's occlusion state, Hide is orderOut and Show is
// makeKeyAndOrderFront, all of which work on a window shown this way. The
// one flag Show would have cleared, options.Hidden, only makes
// windowShouldClose ignore a close request, and the windows shown here are
// frameless (no close button). If the native window does not exist yet, it
// falls back to Show; the capture page's keyboard guard then keeps typing
// out of the box until a click, the hotkey or Ctrl+M.
func showNoActivate(win *application.WebviewWindow) (prevForeground uintptr) {
	h := win.NativeWindow()
	if h == nil {
		win.Show()
		return 0
	}
	application.InvokeSync(func() { C.orderFrontNoActivate(h) })
	return 0
}

// restoreActivation undoes what showNoActivate changed before a normal,
// focusing show. Nothing is left changed on macOS.
func restoreActivation(*application.WebviewWindow) {}

// hideRestoringFocus hides win; macOS picks the next active window.
func hideRestoringFocus(win *application.WebviewWindow, _ uintptr) {
	win.Hide()
}
