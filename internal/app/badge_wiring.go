package app

import (
	"bytes"
	"image"
	"image/png"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

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
	// ReplyHotkeyErr is appended to the tooltip after HotkeyErr; nil = none.
	ReplyHotkeyErr func() string
	Icons          trayIcons // plain icons
	Tray           badgeTray
	Debounce       time.Duration
	Sync           func() cloudsync.Status // nil = sync off
	Now            func() time.Time        // nil = time.Now
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
	// iconCache holds the composed icons per dot style for the count
	// iconCount, so the syncing animation's two frames are decoded and
	// encoded once, not on every tick. A new count empties it.
	iconCache map[badge.DotStyle][3][]byte
	iconCount int
	tip       string // the tooltip last set
	tipSet    bool

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
				w.setTooltip(w.tooltip(w.syncStatus()))
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
	replyErr := ""
	if w.d.ReplyHotkeyErr != nil {
		replyErr = w.d.ReplyHotkeyErr()
	}
	return pendingTooltip(w.shown, syncTooltip(st, now()), w.d.HotkeyErr(), replyErr)
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
	w.setTooltip(w.tooltip(st))
}

// setTooltip sets the tray tooltip unless it already shows text (each tick
// of the syncing animation re-renders with the same text). Call with
// refreshMu held.
func (w *badgeWiring) setTooltip(text string) {
	if w.tipSet && text == w.tip {
		return
	}
	w.d.Tray.SetTooltip(text)
	w.tip, w.tipSet = text, true
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
// the plain ones when there is neither (or an icon cannot be decoded). Call
// with refreshMu held (it fills iconCache).
func (w *badgeWiring) icons(label string, n int, dot badge.DotStyle) (light, dark, tmpl []byte) {
	p := w.d.Icons
	if label == "" && dot == badge.DotNone {
		return p.Light, p.Dark, p.Template
	}
	if w.iconCache == nil || w.iconCount != n {
		w.iconCache, w.iconCount = map[badge.DotStyle][3][]byte{}, n
	}
	if c, ok := w.iconCache[dot]; ok {
		return c[0], c[1], c[2]
	}
	light, dark, tmpl = composeIcons(p, n, dot)
	w.iconCache[dot] = [3][]byte{light, dark, tmpl}
	return light, dark, tmpl
}

func composeIcons(p trayIcons, n int, dot badge.DotStyle) (light, dark, tmpl []byte) {
	return composeBadge(p.Light, n, dot, false), composeBadge(p.Dark, n, dot, false), composeBadge(p.Template, n, dot, true)
}

// composeBadge is withBadge; a variable so tests can count compositions.
var composeBadge = withBadge

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

// maxTooltip is the longest tray tooltip in UTF-16 code units: Wails cuts
// the Windows tooltip at 127.
const maxTooltip = 127

// pendingTooltip is the tray tooltip: "Awesome Reminder — N pending", or
// just the name when the count is off (n < 0), then the sync text and any
// capture and reply hotkey errors, each skipped when empty. A tooltip over
// maxTooltip puts the hotkey errors ahead of the sync text, so the cut falls
// on the sync text, and ends in an ellipsis.
func pendingTooltip(n int, syncText, hotkeyErr, replyHotkeyErr string) string {
	s := appName
	if n >= 0 {
		s += " — " + strconv.Itoa(n) + " pending"
	}
	errs := []string{}
	if hotkeyErr != "" {
		errs = append(errs, "hotkey error: "+hotkeyErr)
	}
	if replyHotkeyErr != "" {
		errs = append(errs, "reply hotkey error: "+replyHotkeyErr)
	}
	var parts []string
	if syncText != "" {
		parts = append(parts, syncText)
	}
	parts = append(parts, errs...)
	if full := joinTooltip(s, parts); utf16Len(full) <= maxTooltip {
		return full
	}
	if syncText != "" {
		errs = append(errs, syncText)
	}
	return truncateUTF16(joinTooltip(s, errs), maxTooltip)
}

func joinTooltip(s string, parts []string) string {
	for _, p := range parts {
		s += " — " + p
	}
	return s
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// truncateUTF16 cuts s to at most max UTF-16 units, the last one an
// ellipsis, never splitting a surrogate pair.
func truncateUTF16(s string, max int) string {
	if utf16Len(s) <= max {
		return s
	}
	n := 0
	for i, r := range s {
		if n+utf16.RuneLen(r) > max-1 {
			return strings.TrimRight(s[:i], " —") + "…"
		}
		n += utf16.RuneLen(r)
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
