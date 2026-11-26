package app

import (
	"log"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Capture window geometry in DIPs. The frontend reports its content height
// through ResizeCapture; the window keeps its top edge when it grows.
const (
	captureWidth      = 640
	captureHeight     = 138 // input row + one preview line + hint row (style.css)
	captureMinHeight  = 96
	captureMaxHeight  = 480
	captureWindowName = "capture"
)

// wailsWindows implements Windows on top of a running Wails application.
type wailsWindows struct {
	app     *application.App
	capture *application.WebviewWindow

	mu     sync.Mutex
	height int
}

// newWindows pre-creates the hidden capture window.
func newWindows(app *application.App) *wailsWindows {
	w := &wailsWindows{app: app, height: captureHeight}
	w.capture = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             captureWindowName,
		Title:            "Awesome Reminder",
		Width:            captureWidth,
		Height:           captureHeight,
		URL:              "/#capture",
		Hidden:           true,
		Frameless:        true,
		AlwaysOnTop:      true,
		DisableResize:    true,
		InitialPosition:  application.WindowCentered,
		BackgroundType:   application.BackgroundTypeTranslucent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		Windows: application.WindowsWindow{
			BackdropType:    application.Acrylic,
			HiddenOnTaskbar: true,
		},
		Mac: application.MacWindow{
			Backdrop: application.MacBackdropTranslucent,
		},
		DefaultContextMenuDisabled: true,
	})
	w.capture.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		w.HideCapture()
	})
	return w
}

// ShowCapture centers the capture box on the screen under the mouse cursor
// (falling back to the window's current screen), shows and focuses it, and
// tells the frontend to reset and focus its input.
func (w *wailsWindows) ShowCapture() {
	w.app.Event.Emit(EventCaptureOpen)
	if s := cursorScreen(w.app); s != nil {
		w.capture.SetScreen(s)
	} else {
		w.capture.Center()
	}
	w.capture.Show()
	w.capture.Focus()
	log.Printf("capture: shown")
}

// HideCapture hides the capture box.
func (w *wailsWindows) HideCapture() {
	w.capture.Hide()
}

// ResizeCapture sets the capture window height to fit its content.
func (w *wailsWindows) ResizeCapture(height int) {
	height = min(max(height, captureMinHeight), captureMaxHeight)
	w.mu.Lock()
	same := height == w.height
	w.height = height
	w.mu.Unlock()
	if !same {
		w.capture.SetSize(captureWidth, height)
	}
}

// OpenList shows the list window.
func (w *wailsWindows) OpenList(focusItemID string) {
	// TODO(task-10): build the list window and emit EventListFocus.
	log.Printf("open list (focus %q): not implemented yet", focusItemID)
}

// OpenSettings shows the settings window.
func (w *wailsWindows) OpenSettings() {
	// TODO(task-11): build the settings window.
	log.Printf("open settings: not implemented yet")
}

// DismissAlerts hides the alert window.
func (w *wailsWindows) DismissAlerts() {
	// TODO(task-11): build the alert window.
	log.Printf("dismiss alerts: not implemented yet")
}
