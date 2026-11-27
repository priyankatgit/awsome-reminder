//go:build !windows

package app

import "github.com/wailsapp/wails/v3/pkg/application"

// showNoActivate shows win. Wails v3.1.0 has no way to show a window without
// activating it on this OS, so it may take focus; Focus is never called.
func showNoActivate(win *application.WebviewWindow) {
	win.Show()
}
