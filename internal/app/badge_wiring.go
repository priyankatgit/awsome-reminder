package app

import (
	"bytes"
	"image"
	"image/png"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"awesome-reminder/internal/badge"
)

// badgeDebounce coalesces a burst of item changes into one count refresh.
const badgeDebounce = 250 * time.Millisecond

// taskbar shows the pending count next to the app outside the tray: the
// Windows taskbar button overlay, the macOS Dock badge, nothing elsewhere.
// SetCount(0) clears it. Both methods may be called from any goroutine and
// must not block on the Wails main thread.
type taskbar interface {
	SetCount(n int)
	Close()
}

// trayIcons are the tray PNGs (see Options).
type trayIcons struct{ Light, Dark, Template []byte }

// badgeTray is what the badge wiring needs from the tray.
type badgeTray interface {
	SetIcons(light, dark, template []byte)
	SetTooltip(text string)
}

type badgeDeps struct {
	Count     func() (int, error) // open items
	Show      func() bool         // the "Show pending count" setting
	HotkeyErr func() string       // appended to the tooltip
	Icons     trayIcons           // plain icons
	Tray      badgeTray
	Debounce  time.Duration
}

// badgeWiring keeps the tray icon, its tooltip and the taskbar badge in step
// with the number of open items.
type badgeWiring struct {
	d badgeDeps

	mu      sync.Mutex
	tb      taskbar // nil until Start
	started bool
	stopped bool
	timer   *time.Timer
	running sync.WaitGroup // refreshes in flight

	refreshMu sync.Mutex // one refresh at a time; guards the fields below
	shown     int        // count in the tooltip; -1 = count off
	label     string     // badge label on the tray icons ("" = plain)
	iconsSet  bool
}

func newBadgeWiring(d badgeDeps) *badgeWiring {
	return &badgeWiring{d: d, shown: -1}
}

// Start shows the current count now and lets Changed refresh it. tb may be
// nil.
func (w *badgeWiring) Start(tb taskbar) {
	w.mu.Lock()
	if w.stopped || w.started {
		w.mu.Unlock()
		if tb != nil && w.tb != tb {
			tb.Close()
		}
		return
	}
	w.tb, w.started = tb, true
	w.mu.Unlock()
	w.schedule(0)
}

// Changed refreshes the count after the debounce delay; a burst of calls
// gives one refresh. It does nothing before Start or after Stop, or on a
// nil wiring (OnChange and StatusChanged are hooked up before it exists).
func (w *badgeWiring) Changed() {
	if w == nil {
		return
	}
	w.schedule(w.d.Debounce)
}

func (w *badgeWiring) schedule(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started || w.stopped {
		return
	}
	if w.timer == nil {
		w.timer = time.AfterFunc(d, w.run)
		return
	}
	w.timer.Reset(d)
}

func (w *badgeWiring) run() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.running.Add(1)
	tb := w.tb
	w.mu.Unlock()
	defer w.running.Done()
	w.refresh(tb)
}

// refresh reads the count and setting and updates the tray and taskbar. A
// failed read leaves what is shown.
func (w *badgeWiring) refresh(tb taskbar) {
	w.refreshMu.Lock()
	defer w.refreshMu.Unlock()
	n := -1
	if w.d.Show() {
		c, err := w.d.Count()
		if err != nil {
			log.Printf("badge: pending count: %v", err)
			if !w.iconsSet {
				// Nothing shown yet: still show the name and any hotkey
				// error.
				w.d.Tray.SetTooltip(pendingTooltip(w.shown, w.d.HotkeyErr()))
			}
			return
		}
		n = c
	}
	label := badge.Label(n)
	if !w.iconsSet || label != w.label {
		light, dark, tmpl := w.icons(label, n)
		w.d.Tray.SetIcons(light, dark, tmpl)
		w.label, w.iconsSet = label, true
	}
	w.shown = n
	w.d.Tray.SetTooltip(pendingTooltip(n, w.d.HotkeyErr()))
	if tb != nil {
		tb.SetCount(max(n, 0))
	}
}

// icons returns the tray icons with the badge for n, or the plain ones when
// label is "" (or an icon cannot be decoded).
func (w *badgeWiring) icons(label string, n int) (light, dark, tmpl []byte) {
	p := w.d.Icons
	if label == "" {
		return p.Light, p.Dark, p.Template
	}
	return withBadge(p.Light, n, false), withBadge(p.Dark, n, false), withBadge(p.Template, n, true)
}

// withBadge composes the badge onto a PNG; on any error it returns the PNG
// unchanged.
func withBadge(icon []byte, n int, template bool) []byte {
	base, err := png.Decode(bytes.NewReader(icon))
	if err != nil {
		return icon
	}
	var img image.Image
	if template {
		img = badge.ComposeTemplate(base, n)
	} else {
		img = badge.Compose(base, n)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return icon
	}
	return b.Bytes()
}

// StatusChanged re-renders the tooltip with the current hotkey error and
// the last count (no store read). It does nothing on a nil wiring.
func (w *badgeWiring) StatusChanged() {
	if w == nil {
		return
	}
	w.mu.Lock()
	stopped := w.stopped
	w.mu.Unlock()
	if stopped {
		return
	}
	w.refreshMu.Lock()
	defer w.refreshMu.Unlock()
	w.d.Tray.SetTooltip(pendingTooltip(w.shown, w.d.HotkeyErr()))
}

// Stop cancels a pending refresh, waits for one in flight and closes the
// taskbar. Nothing reads the store or touches the tray after it returns.
func (w *badgeWiring) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	if w.timer != nil {
		w.timer.Stop()
	}
	tb := w.tb
	w.mu.Unlock()
	w.running.Wait()
	w.refreshMu.Lock() // a StatusChanged in flight
	w.refreshMu.Unlock()
	if tb != nil {
		tb.Close()
	}
}

// pendingTooltip is the tray tooltip: "Awesome Reminder — N pending", or
// just the name when the count is off (n < 0), with any hotkey error
// appended.
func pendingTooltip(n int, hotkeyErr string) string {
	s := appName
	if n >= 0 {
		s += " — " + strconv.Itoa(n) + " pending"
	}
	if hotkeyErr != "" {
		s += " — hotkey error: " + hotkeyErr
	}
	return s
}

// wailsBadgeTray sets the Wails tray from the badge wiring. It hands the
// change to the main thread without waiting, so a refresh never blocks on
// it (a blocked InvokeSync after the main loop has ended would hang Stop),
// and skips it once the app is shutting down (the tray is destroyed then).
type wailsBadgeTray struct {
	app  *application.App
	tray *application.SystemTray
}

func (t wailsBadgeTray) SetIcons(light, dark, template []byte) {
	application.InvokeAsync(func() {
		if t.app.Context().Err() != nil {
			return
		}
		setTrayIcons(t.tray, Options{TrayIconLight: light, TrayIconDark: dark, TrayIconTemplate: template})
	})
}

func (t wailsBadgeTray) SetTooltip(text string) {
	application.InvokeAsync(func() {
		if t.app.Context().Err() != nil {
			return
		}
		t.tray.SetTooltip(text)
	})
}
