//go:build !windows && !darwin && !linux

package app

import "github.com/wailsapp/wails/v3/pkg/application"

// showNoActivate shows win. Wails v3.1.0 has no way to show a window without
// activating it on this OS, so it may take focus; Focus is never called.
func showNoActivate(win *application.WebviewWindow) (prevForeground uintptr) {
	win.Show()
	return 0
}

// restoreActivation does nothing: showNoActivate changed nothing.
func restoreActivation(*application.WebviewWindow) {}

// hideRestoringFocus hides win; the OS picks the next active window.
func hideRestoringFocus(win *application.WebviewWindow, _ uintptr) {
	win.Hide()
}
