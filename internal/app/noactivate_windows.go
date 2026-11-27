//go:build windows

package app

import (
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

var procGetAncestor = windows.NewLazySystemDLL("user32.dll").NewProc("GetAncestor")

const gaRoot = 2 // GA_ROOT

// rootOf returns the top-level window containing h. The WebView2 content is
// a child window owned by another process, and it is what
// GetForegroundWindow reports after a click into the page.
func rootOf(h w32.HWND) w32.HWND {
	r, _, _ := procGetAncestor.Call(uintptr(h), gaRoot)
	return w32.HWND(r)
}

// showNoActivate shows win without activating it, so the user's foreground
// app keeps keyboard focus. It returns that app's window, for
// hideRestoringFocus.
//
// Wails v3.1.0 has no such option: WebviewWindow.Show ends in
// ShowWindow(SW_SHOW), which activates a hidden window, and its
// WS_EX_NOACTIVATE support was removed (webview_window_windows.go). Show is
// still needed, because it also makes the WebView2 controller visible. So
// the window is first made visible with SW_SHOWNOACTIVATE; Wails' SW_SHOW
// on an already visible window then changes nothing, and only its WebView
// part takes effect.
func showNoActivate(win *application.WebviewWindow) (prevForeground uintptr) {
	h := win.NativeWindow()
	if h != nil {
		application.InvokeSync(func() {
			if fg := w32.GetForegroundWindow(); fg != w32.HWND(h) {
				prevForeground = uintptr(fg)
			}
			w32.ShowWindow(w32.HWND(h), w32.SW_SHOWNOACTIVATE)
		})
	}
	win.Show()
	return prevForeground
}

// restoreActivation undoes what showNoActivate changed before a normal,
// focusing show. Nothing is left changed on Windows.
func restoreActivation(*application.WebviewWindow) {}

// hideRestoringFocus hides win. If the user had clicked into it (so it is
// the foreground window), hiding it would leave the hidden window active and
// the keyboard going nowhere, so activation is first handed back to prev,
// the window that was in front when the alert appeared, if it still exists.
// This must happen before the hide: Windows refuses SetForegroundWindow once
// the active window is hidden.
func hideRestoringFocus(win *application.WebviewWindow, prev uintptr) {
	h := win.NativeWindow()
	if h != nil && prev != 0 {
		application.InvokeSync(func() {
			fg := w32.GetForegroundWindow()
			if fg == 0 || (fg != w32.HWND(h) && rootOf(fg) != w32.HWND(h)) {
				return
			}
			p := w32.HWND(prev)
			if w32.IsWindow(p) && w32.IsWindowVisible(p) {
				ok := w32.SetForegroundWindow(p) != 0
				log.Printf("alert: focus back to the previous window: %v", ok)
			}
		})
	}
	win.Hide()
}
