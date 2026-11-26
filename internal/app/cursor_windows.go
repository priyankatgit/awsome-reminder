package app

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// cursorScreen returns the screen under the mouse cursor, or nil.
func cursorScreen(app *application.App) *application.Screen {
	x, y, ok := w32.GetCursorPos()
	if !ok {
		return nil
	}
	return app.Screen.ScreenNearestPhysicalPoint(application.Point{X: x, Y: y})
}
