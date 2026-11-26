//go:build !windows

package app

import "github.com/wailsapp/wails/v3/pkg/application"

// cursorScreen is not implemented off Windows; the window is centered on its
// current screen instead.
func cursorScreen(*application.App) *application.Screen { return nil }
