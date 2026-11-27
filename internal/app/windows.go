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

// List window geometry in DIPs.
const (
	listWidth      = 900
	listHeight     = 640
	listMinWidth   = 480
	listMinHeight  = 360
	listWindowName = "list"
)

// List window background before the page paints, matching --bg in
// frontend/src/tokens.css so opening it doesn't flash white.
var (
	listBgDark  = application.NewRGB(0x0F, 0x17, 0x2A)
	listBgLight = application.NewRGB(0xF8, 0xFA, 0xFC)
)

// wailsWindows implements Windows on top of a running Wails application.
type wailsWindows struct {
	app       *application.App
	capture   *application.WebviewWindow
	size      *heightKeeper
	list      *onDemand[*application.WebviewWindow]
	listFocus focusSlot
}

// onDemand holds a window that is created when first needed and forgotten
// when it closes, so the next Get creates a fresh one. Wails destroys a
// normal window on close (its default WindowClosing listener), so a stale
// pointer must never be reused. create runs under mu; it may wait for the
// main thread, which never takes mu.
type onDemand[W comparable] struct {
	mu     sync.Mutex
	cur    W
	create func() W
}

// Get returns the current window, creating one if there is none; created
// reports whether it is new.
func (o *onDemand[W]) Get() (w W, created bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var zero W
	if o.cur != zero {
		return o.cur, false
	}
	o.cur = o.create()
	return o.cur, true
}

// Drop forgets w if it is still the current window.
func (o *onDemand[W]) Drop(w W) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cur == w {
		var zero W
		o.cur = zero
	}
}

// focusSlot holds the item the list window should scroll to. A freshly
// created list window takes it once its page has loaded (an event emitted
// before then would be lost); an open one is nudged with EventListFocus and
// takes it too.
type focusSlot struct {
	mu sync.Mutex
	id string
}

// Set records id as the item to focus.
func (f *focusSlot) Set(id string) {
	f.mu.Lock()
	f.id = id
	f.mu.Unlock()
}

// Take returns the pending item ID ("" if none) and clears it.
func (f *focusSlot) Take() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id
	f.id = ""
	return id
}

// heightKeeper applies capture-window heights in order. Each binding call
// runs on its own goroutine, so the native resize happens under mu: the
// recorded height always equals the last height applied. Nothing on the main
// thread takes mu, so holding it across set (which waits for the main
// thread) cannot deadlock.
type heightKeeper struct {
	mu     sync.Mutex
	height int
	set    func(height int)
}

// Resize clamps height and applies it unless it is already the current one.
func (k *heightKeeper) Resize(height int) {
	height = min(max(height, captureMinHeight), captureMaxHeight)
	k.mu.Lock()
	defer k.mu.Unlock()
	if height == k.height {
		return
	}
	k.set(height)
	k.height = height
}

// newWindows pre-creates the hidden capture window.
func newWindows(app *application.App) *wailsWindows {
	w := &wailsWindows{app: app}
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
	w.size = &heightKeeper{height: captureHeight, set: func(h int) { w.capture.SetSize(captureWidth, h) }}
	w.capture.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		w.HideCapture()
	})
	w.list = &onDemand[*application.WebviewWindow]{create: w.newList}
	return w
}

// newList creates and shows the list window. It is destroyed when closed;
// a hook (run synchronously, before Wails' own destroy listener) drops our
// reference so the next OpenList builds a new one.
func (w *wailsWindows) newList() *application.WebviewWindow {
	bg := listBgLight
	if w.app.Env.IsDarkMode() {
		bg = listBgDark
	}
	win := w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             listWindowName,
		Title:            appName,
		Width:            listWidth,
		Height:           listHeight,
		MinWidth:         listMinWidth,
		MinHeight:        listMinHeight,
		URL:              "/#list",
		InitialPosition:  application.WindowCentered,
		BackgroundColour: bg,
	})
	win.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		w.list.Drop(win)
		log.Printf("list: closed")
	})
	return win
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
	w.size.Resize(height)
}

// OpenList shows the list window, creating it if needed, and asks it to
// scroll to focusItemID when that is not "".
func (w *wailsWindows) OpenList(focusItemID string) {
	if focusItemID != "" {
		w.listFocus.Set(focusItemID)
	}
	win, created := w.list.Get()
	if created {
		win.Focus()
		log.Printf("list: created (focus %q)", focusItemID)
		return
	}
	win.UnMinimise()
	win.Show()
	win.Focus()
	if focusItemID != "" {
		w.app.Event.Emit(EventListFocus, focusItemID)
	}
	log.Printf("list: focused (focus %q)", focusItemID)
}

// TakePendingFocus returns the item the list window should scroll to and
// clears it.
func (w *wailsWindows) TakePendingFocus() string {
	return w.listFocus.Take()
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
