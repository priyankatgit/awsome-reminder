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
	"awesome-reminder/internal/cloudsync"
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
	Sync      func() cloudsync.Status // nil = sync off
	Now       func() time.Time        // nil = time.Now
	// NewTicker starts the syncing-animation ticker (nil = time.NewTicker).
	// The returned func releases it.
	NewTicker func(d time.Duration) (<-chan time.Time, func())
}

// syncFrame is the syncing dot's animation period.
const syncFrame = 500 * time.Millisecond

func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
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
	dot       badge.DotStyle // dot on the tray icons
	frame     bool           // syncing animation frame (false = A)
	tickStop  chan struct{}  // non-nil while the syncing ticker runs
	tickWG    sync.WaitGroup // ticker goroutines

	afterStopCheck func() // test hook: runs between the stopped check and refreshMu
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
				w.d.Tray.SetTooltip(w.tooltip(w.syncStatus()))
			}
			return
		}
		n = c
	}
	w.shown = n
	w.render()
	if tb != nil {
		tb.SetCount(max(n, 0))
	}
}

// syncStatus is the current sync status (zero value = off).
func (w *badgeWiring) syncStatus() cloudsync.Status {
	if w.d.Sync == nil {
		return cloudsync.Status{}
	}
	return w.d.Sync()
}

func (w *badgeWiring) tooltip(st cloudsync.Status) string {
	now := time.Now
	if w.d.Now != nil {
		now = w.d.Now
	}
	return pendingTooltip(w.shown, syncTooltip(st, now()), w.d.HotkeyErr())
}

// render shows the last count and the current sync status on the tray: the
// icons when the badge or the dot changed, and the tooltip. It starts or
// stops the syncing ticker. Call with refreshMu held.
func (w *badgeWiring) render() {
	// A caller may have passed its stopped check before Stop ran; Stop takes
	// refreshMu after setting stopped, so this check closes that window.
	w.mu.Lock()
	stopped := w.stopped
	w.mu.Unlock()
	if stopped {
		return
	}
	st := w.syncStatus()
	w.syncTicker(st.State == cloudsync.StateSyncing)
	dot := dotFor(st, w.frame)
	label := badge.Label(w.shown)
	if !w.iconsSet || label != w.label || dot != w.dot {
		light, dark, tmpl := w.icons(label, w.shown, dot)
		w.d.Tray.SetIcons(light, dark, tmpl)
		w.label, w.dot, w.iconsSet = label, dot, true
	}
	w.d.Tray.SetTooltip(w.tooltip(st))
}

// syncTicker runs the syncing-animation ticker while on. Call with refreshMu
// held. A ticker goroutine ends itself once its stop channel is closed; Stop
// joins them.
func (w *badgeWiring) syncTicker(on bool) {
	if !on {
		if w.tickStop != nil {
			close(w.tickStop)
			w.tickStop = nil
		}
		w.frame = false
		return
	}
	if w.tickStop != nil {
		return
	}
	w.mu.Lock()
	stopped := w.stopped
	w.mu.Unlock()
	if stopped {
		return
	}
	newTicker := w.d.NewTicker
	if newTicker == nil {
		newTicker = realTicker
	}
	c, release := newTicker(syncFrame)
	stop := make(chan struct{})
	w.tickStop, w.frame = stop, false
	w.tickWG.Add(1)
	go func() {
		defer w.tickWG.Done()
		defer release()
		for {
			select {
			case <-stop:
				return
			case <-c:
			}
			w.refreshMu.Lock()
			w.mu.Lock()
			stopped := w.stopped
			w.mu.Unlock()
			select {
			case <-stop:
				stopped = true
			default:
			}
			if stopped {
				w.refreshMu.Unlock()
				return
			}
			w.frame = !w.frame
			w.render()
			w.refreshMu.Unlock()
		}
	}()
}

// icons returns the tray icons with the badge for n and the status dot, or
// the plain ones when there is neither (or an icon cannot be decoded).
func (w *badgeWiring) icons(label string, n int, dot badge.DotStyle) (light, dark, tmpl []byte) {
	p := w.d.Icons
	if label == "" && dot == badge.DotNone {
		return p.Light, p.Dark, p.Template
	}
	return withBadge(p.Light, n, dot, false), withBadge(p.Dark, n, dot, false), withBadge(p.Template, n, dot, true)
}

// withBadge composes the badge and then the status dot onto a PNG; on any
// error it returns the PNG unchanged.
func withBadge(icon []byte, n int, dot badge.DotStyle, template bool) []byte {
	base, err := png.Decode(bytes.NewReader(icon))
	if err != nil {
		return icon
	}
	var img image.Image
	if template {
		img = badge.DotTemplate(badge.ComposeTemplate(base, n), dot)
	} else {
		img = badge.Dot(badge.Compose(base, n), dot)
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
	if w.afterStopCheck != nil {
		w.afterStopCheck()
	}
	w.refreshMu.Lock()
	defer w.refreshMu.Unlock()
	w.render()
}

// SyncChanged re-renders the icons and tooltip with the current sync status
// and the last count (no store read). It does nothing before Start, after
// Stop, or on a nil wiring (the sync controller reports before it exists).
func (w *badgeWiring) SyncChanged() {
	if w == nil {
		return
	}
	w.mu.Lock()
	skip := w.stopped || !w.started
	w.mu.Unlock()
	if skip {
		return
	}
	if w.afterStopCheck != nil {
		w.afterStopCheck()
	}
	w.refreshMu.Lock()
	defer w.refreshMu.Unlock()
	w.render()
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
	if w.tickStop != nil {
		close(w.tickStop)
		w.tickStop = nil
	}
	w.refreshMu.Unlock()
	w.tickWG.Wait()
	if tb != nil {
		tb.Close()
	}
}

// pendingTooltip is the tray tooltip: "Awesome Reminder — N pending", or
// just the name when the count is off (n < 0), then the sync text and any
// hotkey error, each skipped when empty.
func pendingTooltip(n int, syncText, hotkeyErr string) string {
	s := appName
	if n >= 0 {
		s += " — " + strconv.Itoa(n) + " pending"
	}
	if syncText != "" {
		s += " — " + syncText
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
