//go:build windows

package app

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// showNoActivate shows win without activating it, so the user's foreground
// app keeps keyboard focus.
//
// Wails v3.1.0 has no such option: WebviewWindow.Show ends in
// ShowWindow(SW_SHOW), which activates a hidden window, and its
// WS_EX_NOACTIVATE support was removed (webview_window_windows.go). Show is
// still needed, because it also makes the WebView2 controller visible. So
// the window is first made visible with SW_SHOWNOACTIVATE; Wails' SW_SHOW
// on an already visible window then changes nothing, and only its WebView
// part takes effect.
func showNoActivate(win *application.WebviewWindow) {
	if h := win.NativeWindow(); h != nil {
		application.InvokeSync(func() {
			w32.ShowWindow(w32.HWND(h), w32.SW_SHOWNOACTIVATE)
		})
	}
	win.Show()
}
