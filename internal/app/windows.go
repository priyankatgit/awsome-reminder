package app

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

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

// Alert window geometry in DIPs. It sits at the bottom-right of the primary
// screen's work area and its height follows its cards (ResizeAlerts).
const (
	alertWidth         = 380
	alertInitialHeight = 160
	alertMinHeight     = 64
	alertMaxHeight     = 420
	alertInset         = 16
	alertWindowName    = "alert"
	// alertReadyFallback shows the alert window even if its page never
	// reports a height.
	alertReadyFallback = 2 * time.Second
)

// Settings window geometry in DIPs.
const (
	settingsWidth      = 520
	settingsHeight     = 560
	settingsMinWidth   = 440
	settingsMinHeight  = 400
	settingsWindowName = "settings"
)

// Framed window background before the page paints, matching --bg in
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
	settings  *onDemand[*application.WebviewWindow]
	alert     *alertPopup

	// Set by Run before the app starts.
	onAlertsClosed   func() // the user closed the alert window (Alt+F4)
	onSettingsClosed func() // the settings window closed
}

// popup is the alert window as alertPopup drives it.
type popup interface {
	place(height int) // size it and move it to the bottom-right corner
	show()            // show without taking keyboard focus
	hide()
	destroy() // close the window for good; its close hook must not report it
}

// alertPopup keeps at most one alert window. It is created when an alert
// arrives and destroyed when the queue empties, so its WebView2 renderer
// process does not sit in memory between alerts (Task 12b: about 26 MB
// private, 66 MB working set); the next alert creates a new one. Every time
// it goes from hidden to shown it waits until the page has rendered the
// queue and reported its height (ResizeAlerts), so it never appears empty or
// with stale cards; a timer shows it anyway if the page stays silent. Calls
// arrive on binding and scheduler goroutines; mu orders them. Nothing on the
// main thread takes mu, except the close hook after the user closed the
// window (Alt+F4).
type alertPopup struct {
	mu     sync.Mutex
	create func() popup
	after  func(time.Duration, func()) // time.AfterFunc

	cur     popup
	ready   bool // the page reported its height since Show (or the fallback fired)
	want    bool // there are alerts to show
	visible bool
	height  int
	wait    int // counts waits for the page, so a stale fallback is ignored
}

// Show asks for the window to be visible, creating it if needed. The page
// is told about the new queue right after this call.
func (a *alertPopup) Show() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.want = true
	if a.cur == nil {
		a.cur, a.visible, a.height = a.create(), false, alertInitialHeight
	}
	if a.visible {
		return
	}
	a.ready = false
	a.wait++
	p, wait := a.cur, a.wait
	a.after(alertReadyFallback, func() { a.fallback(p, wait) })
}

// showLocked places and shows the window if it is wanted, ready and hidden.
// It re-places on every show: the work area may have changed meanwhile.
func (a *alertPopup) showLocked() {
	if a.cur == nil || !a.ready || !a.want || a.visible {
		return
	}
	a.cur.place(a.height)
	a.cur.show()
	a.visible = true
}

// Resize records the page's content height and shows the window if it is
// waiting for the page. The page reports after every render, even when the
// height is unchanged.
func (a *alertPopup) Resize(height int) {
	height = min(max(height, alertMinHeight), alertMaxHeight)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur == nil {
		return
	}
	a.ready = true
	if height != a.height {
		a.height = height
		if a.visible {
			a.cur.place(height)
		}
	}
	a.showLocked()
}

func (a *alertPopup) fallback(p popup, wait int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur != p || a.wait != wait || a.ready {
		return
	}
	a.ready = true
	a.showLocked()
}

// Dismiss hides the window (handing focus back first) and destroys it.
func (a *alertPopup) Dismiss() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.want = false
	if a.cur == nil {
		return
	}
	if a.visible {
		a.cur.hide()
	}
	a.cur.destroy()
	a.cur, a.ready, a.visible = nil, false, false
}

// Closed forgets p after its window was destroyed.
func (a *alertPopup) Closed(p popup) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur == p {
		a.cur, a.ready, a.visible = nil, false, false
	}
}

// wailsPopup is the real alert window. Its methods are called under
// alertPopup.mu.
type wailsPopup struct {
	app  *application.App
	win  *application.WebviewWindow
	prev uintptr // the foreground window when it was last shown
	// destroying is set before destroy closes the window, so the close hook
	// (on the main thread, while destroy waits) knows it was not the user and
	// takes no lock.
	destroying atomic.Bool
}

func (p *wailsPopup) place(height int) {
	s := p.app.Screen.GetPrimary()
	if s == nil {
		return
	}
	wa := s.WorkArea
	p.win.SetBounds(application.Rect{
		X:      wa.X + wa.Width - alertWidth - alertInset,
		Y:      wa.Y + wa.Height - height - alertInset,
		Width:  alertWidth,
		Height: height,
	})
}

func (p *wailsPopup) show() { p.prev = showNoActivate(p.win) }
func (p *wailsPopup) hide() { hideRestoringFocus(p.win, p.prev) }

func (p *wailsPopup) destroy() {
	p.destroying.Store(true)
	p.win.Close()
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
		// Dictation: grant the mic to our own page without a WebView prompt
		// (macOS still asks once at the OS level, see Info.plist).
		Permissions: map[application.PermissionType]application.Permission{
			application.PermissionMicrophone: application.PermissionAllow,
		},
		DefaultContextMenuDisabled: true,
	})
	w.size =&heightKeeper{height: captureHeight, set: func(h int) { w.capture.SetSize(captureWidth, h) }}
	w.capture.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		w.HideCapture()
	})
	w.list = &onDemand[*application.WebviewWindow]{create: w.newList}
	w.settings = &onDemand[*application.WebviewWindow]{create: w.newSettings}
	w.alert = &alertPopup{
		create: w.newAlert,
		after:  func(d time.Duration, f func()) { time.AfterFunc(d, f) },
	}
	return w
}

// pageBackground is the framed windows' background colour for the OS theme.
func (w *wailsWindows) pageBackground() application.RGBA {
	if w.app.Env.IsDarkMode() {
		return listBgDark
	}
	return listBgLight
}

// newList creates and shows the list window. It is destroyed when closed;
// a hook (run synchronously, before Wails' own destroy listener) drops our
// reference so the next OpenList builds a new one.
func (w *wailsWindows) newList() *application.WebviewWindow {
	win := w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             listWindowName,
		Title:            appName,
		Width:            listWidth,
		Height:           listHeight,
		MinWidth:         listMinWidth,
		MinHeight:        listMinHeight,
		URL:              "/#list",
		InitialPosition:  application.WindowCentered,
		BackgroundColour: w.pageBackground(),
	})
	win.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		w.list.Drop(win)
		log.Printf("list: closed")
	})
	return win
}

// newSettings creates and shows the settings window; like the list window it
// is destroyed when closed.
func (w *wailsWindows) newSettings() *application.WebviewWindow {
	win := w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             settingsWindowName,
		Title:            appName + " Settings",
		Width:            settingsWidth,
		Height:           settingsHeight,
		MinWidth:         settingsMinWidth,
		MinHeight:        settingsMinHeight,
		URL:              "/#settings",
		InitialPosition:  application.WindowCentered,
		BackgroundColour: w.pageBackground(),
	})
	win.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		w.settings.Drop(win)
		log.Printf("settings: closed")
		if w.onSettingsClosed != nil {
			w.onSettingsClosed()
		}
	})
	return win
}

// newAlert creates the hidden alert window: frameless, always on top, off
// the taskbar, translucent like the capture box. It is destroyed when the
// queue empties (alertPopup.Dismiss) or when the user closes it.
func (w *wailsWindows) newAlert() popup {
	win := w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             alertWindowName,
		Title:            appName,
		Width:            alertWidth,
		Height:           alertInitialHeight,
		URL:              "/#alert",
		Hidden:           true,
		Frameless:        true,
		AlwaysOnTop:      true,
		DisableResize:    true,
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
	p := &wailsPopup{app: w.app, win: win}
	win.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		if p.destroying.Load() {
			log.Printf("alert: destroyed")
			return
		}
		w.alert.Closed(p)
		log.Printf("alert: closed")
		if w.onAlertsClosed != nil {
			w.onAlertsClosed()
		}
	})
	log.Printf("alert: created")
	return p
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

// OpenSettings shows and focuses the settings window, creating it if needed.
func (w *wailsWindows) OpenSettings() {
	win, created := w.settings.Get()
	if !created {
		win.UnMinimise()
		win.Show()
	}
	win.Focus()
	log.Printf("settings: opened (created=%v)", created)
}

// ShowAlerts shows the alert window without taking focus, creating it on
// the first alert.
func (w *wailsWindows) ShowAlerts() {
	w.alert.Show()
}

// ResizeAlerts fits the alert window to its cards (clamped to 420 DIP; the
// page scrolls beyond that), keeping it in the bottom-right corner.
func (w *wailsWindows) ResizeAlerts(height int) {
	w.alert.Resize(height)
}

// DismissAlerts hides and destroys the alert window.
func (w *wailsWindows) DismissAlerts() {
	w.alert.Dismiss()
	log.Printf("alert: hidden")
}
