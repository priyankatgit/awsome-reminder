package app

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"awesome-reminder/internal/badge"
	"awesome-reminder/internal/cloudsync"
)

type fakeBadgeTray struct {
	mu      sync.Mutex
	icons   [3][]byte
	tooltip string
	sets    int
	tips    int // SetTooltip calls
}

func (f *fakeBadgeTray) SetIcons(light, dark, template []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.icons = [3][]byte{light, dark, template}
	f.sets++
}

func (f *fakeBadgeTray) SetTooltip(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tooltip = s
	f.tips++
}

func (f *fakeBadgeTray) tooltipSets() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tips
}

func (f *fakeBadgeTray) state() ([3][]byte, string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.icons, f.tooltip, f.sets
}

type fakeTaskbar struct {
	counts chan int
	closed chan struct{}
}

func (f *fakeTaskbar) SetCount(n int) { f.counts <- n }
func (f *fakeTaskbar) Close()         { close(f.closed) }

func testPNG(t *testing.T, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func nextCount(t *testing.T, tb *fakeTaskbar) int {
	t.Helper()
	select {
	case n := <-tb.counts:
		return n
	case <-time.After(2 * time.Second):
		t.Fatal("no SetCount call")
		return -1
	}
}

func TestBadgeWiringUpdatesOnChange(t *testing.T) {
	plain := trayIcons{
		Light:    testPNG(t, color.NRGBA{0, 0, 0, 255}),
		Dark:     testPNG(t, color.NRGBA{255, 255, 255, 255}),
		Template: testPNG(t, color.NRGBA{0, 0, 0, 255}),
	}
	var mu sync.Mutex
	count, show, hkErr := 2, true, ""
	tray := &fakeBadgeTray{}
	tb := &fakeTaskbar{counts: make(chan int, 10), closed: make(chan struct{})}
	w := newBadgeWiring(badgeDeps{
		Count: func() (int, error) { mu.Lock(); defer mu.Unlock(); return count, nil },
		Show:  func() bool { mu.Lock(); defer mu.Unlock(); return show },
		HotkeyErr: func() string {
			mu.Lock()
			defer mu.Unlock()
			return hkErr
		},
		Icons:    plain,
		Tray:     tray,
		Debounce: 10 * time.Millisecond,
	})
	set := func(n int, on bool) {
		mu.Lock()
		count, show = n, on
		mu.Unlock()
	}

	w.Start(tb)
	if n := nextCount(t, tb); n != 2 {
		t.Fatalf("start count %d, want 2", n)
	}
	icons, tip, _ := tray.state()
	if tip != "Awesome Reminder — 2 pending" {
		t.Fatalf("tooltip %q", tip)
	}
	for i := range icons {
		if bytes.Equal(icons[i], [][]byte{plain.Light, plain.Dark, plain.Template}[i]) {
			t.Fatalf("icon %d not badged", i)
		}
	}
	badged2 := icons

	set(3, true)
	w.Changed()
	if n := nextCount(t, tb); n != 3 {
		t.Fatalf("count %d, want 3", n)
	}
	if _, tip, _ := tray.state(); tip != "Awesome Reminder — 3 pending" {
		t.Fatalf("tooltip %q", tip)
	}
	if icons, _, _ := tray.state(); bytes.Equal(icons[0], badged2[0]) {
		t.Fatal("icon for 3 equals icon for 2")
	}

	set(0, true)
	w.Changed()
	if n := nextCount(t, tb); n != 0 {
		t.Fatalf("count %d, want 0", n)
	}
	icons, tip, _ = tray.state()
	if tip != "Awesome Reminder — 0 pending" {
		t.Fatalf("tooltip %q", tip)
	}
	if !bytes.Equal(icons[0], plain.Light) || !bytes.Equal(icons[1], plain.Dark) || !bytes.Equal(icons[2], plain.Template) {
		t.Fatal("count 0 should show the plain icons")
	}

	// A burst of changes is debounced into one refresh.
	_, _, before := tray.state()
	set(5, true)
	for range 5 {
		w.Changed()
	}
	if n := nextCount(t, tb); n != 5 {
		t.Fatalf("count %d, want 5", n)
	}
	time.Sleep(50 * time.Millisecond)
	if _, _, after := tray.state(); after != before+1 {
		t.Fatalf("burst gave %d icon updates, want 1", after-before)
	}
	select {
	case n := <-tb.counts:
		t.Fatalf("extra SetCount(%d) after a debounced burst", n)
	default:
	}

	// A hotkey error is appended; StatusChanged does not re-read the store.
	mu.Lock()
	hkErr = "Ctrl+Shift+Space is already used by another app"
	mu.Unlock()
	w.StatusChanged()
	if _, tip, _ := tray.state(); tip != "Awesome Reminder — 5 pending — hotkey error: Ctrl+Shift+Space is already used by another app" {
		t.Fatalf("tooltip %q", tip)
	}
	mu.Lock()
	hkErr = ""
	mu.Unlock()

	// Setting off: plain icons, plain tooltip, no overlay.
	set(4, false)
	w.Changed()
	if n := nextCount(t, tb); n != 0 {
		t.Fatalf("setting off: count %d, want 0", n)
	}
	icons, tip, _ = tray.state()
	if tip != "Awesome Reminder" {
		t.Fatalf("setting off: tooltip %q", tip)
	}
	if !bytes.Equal(icons[0], plain.Light) || !bytes.Equal(icons[1], plain.Dark) || !bytes.Equal(icons[2], plain.Template) {
		t.Fatal("setting off should show the plain icons")
	}

	// Back on: the badge returns.
	set(4, true)
	w.Changed()
	if n := nextCount(t, tb); n != 4 {
		t.Fatalf("setting on again: count %d, want 4", n)
	}

	w.Stop()
	select {
	case <-tb.closed:
	default:
		t.Fatal("Stop did not close the taskbar")
	}
	// Nothing runs after Stop.
	w.Changed()
	w.StatusChanged()
	time.Sleep(50 * time.Millisecond)
	select {
	case n := <-tb.counts:
		t.Fatalf("SetCount(%d) after Stop", n)
	default:
	}
}

// A failed count read leaves the last shown badge alone.
func TestBadgeWiringCountErrorKeepsLast(t *testing.T) {
	plain := trayIcons{Light: testPNG(t, color.NRGBA{0, 0, 0, 255}), Dark: testPNG(t, color.NRGBA{0, 0, 0, 255}), Template: testPNG(t, color.NRGBA{0, 0, 0, 255})}
	var mu sync.Mutex
	fail := false
	tray := &fakeBadgeTray{}
	tb := &fakeTaskbar{counts: make(chan int, 10), closed: make(chan struct{})}
	w := newBadgeWiring(badgeDeps{
		Count: func() (int, error) {
			mu.Lock()
			defer mu.Unlock()
			if fail {
				return 0, errors.New("closed")
			}
			return 7, nil
		},
		Show:      func() bool { return true },
		HotkeyErr: func() string { return "" },
		Icons:     plain,
		Tray:      tray,
		Debounce:  time.Millisecond,
	})
	w.Start(tb)
	if n := nextCount(t, tb); n != 7 {
		t.Fatalf("count %d", n)
	}
	mu.Lock()
	fail = true
	mu.Unlock()
	w.Changed()
	time.Sleep(50 * time.Millisecond)
	if _, tip, _ := tray.state(); tip != "Awesome Reminder — 7 pending" {
		t.Fatalf("tooltip %q", tip)
	}
	select {
	case n := <-tb.counts:
		t.Fatalf("SetCount(%d) after a failed read", n)
	default:
	}
	w.Stop()
}

func TestBadgeWiringStopWithoutStart(t *testing.T) {
	w := newBadgeWiring(badgeDeps{Tray: &fakeBadgeTray{}})
	w.Changed()
	w.Stop() // no taskbar: must not panic
	// OnChange and StatusChanged can fire before Run assigns the wiring.
	var nilWiring *badgeWiring
	nilWiring.Changed()
	nilWiring.StatusChanged()
	nilWiring.Stop()
}

// A count that fails from the start still shows the name and a hotkey error.
func TestBadgeWiringCountErrorAtStartSetsTooltip(t *testing.T) {
	tray := &fakeBadgeTray{}
	w := newBadgeWiring(badgeDeps{
		Count:     func() (int, error) { return 0, errors.New("closed") },
		Show:      func() bool { return true },
		HotkeyErr: func() string { return "taken" },
		Tray:      tray,
		Debounce:  time.Millisecond,
	})
	w.Start(nil)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, tip, _ := tray.state(); tip == "Awesome Reminder — hotkey error: taken" {
			break
		}
		if time.Now().After(deadline) {
			_, tip, _ := tray.state()
			t.Fatalf("tooltip %q", tip)
		}
		time.Sleep(5 * time.Millisecond)
	}
	w.Stop()
}

// fakeTicker lets a test fire ticks by hand and see when the wiring stops it.
type fakeTicker struct {
	mu      sync.Mutex
	c       chan time.Time
	started int
	stopped int
}

func (f *fakeTicker) New(time.Duration) (<-chan time.Time, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started++
	return f.c, func() { f.mu.Lock(); f.stopped++; f.mu.Unlock() }
}

func (f *fakeTicker) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started, f.stopped
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// The reply hotkey's error follows the capture hotkey's in the tooltip,
// after the sync text; each is left out when empty.
func TestBadgeWiringReplyHotkeyErr(t *testing.T) {
	for _, tc := range []struct {
		n                     int
		sync, hk, reply, want string
	}{
		{3, "", "", "busy", "Awesome Reminder — 3 pending — reply hotkey error: busy"},
		{3, "Syncing…", "taken", "busy", "Awesome Reminder — 3 pending — Syncing… — hotkey error: taken — reply hotkey error: busy"},
		{-1, "", "taken", "", "Awesome Reminder — hotkey error: taken"},
	} {
		if got := pendingTooltip(tc.n, tc.sync, tc.hk, tc.reply); got != tc.want {
			t.Errorf("pendingTooltip(%d, %q, %q, %q) = %q, want %q", tc.n, tc.sync, tc.hk, tc.reply, got, tc.want)
		}
	}

	last := time.Date(2026, 11, 28, 11, 58, 0, 0, time.UTC)
	tray := &fakeBadgeTray{}
	w := newBadgeWiring(badgeDeps{
		Count:          func() (int, error) { return 3, nil },
		Show:           func() bool { return true },
		HotkeyErr:      func() string { return "taken" },
		ReplyHotkeyErr: func() string { return "busy" },
		Sync:           func() cloudsync.Status { return cloudsync.Status{State: cloudsync.StateSynced, LastSyncAt: &last} },
		Now:            func() time.Time { return last.Add(2 * time.Minute) },
		Icons: trayIcons{
			Light:    testPNG(t, color.NRGBA{0, 0, 0, 255}),
			Dark:     testPNG(t, color.NRGBA{255, 255, 255, 255}),
			Template: testPNG(t, color.NRGBA{0, 0, 0, 255}),
		},
		Tray:     tray,
		Debounce: time.Millisecond,
	})
	w.Start(nil)
	defer w.Stop()
	waitFor(t, "first refresh", func() bool { _, tip, _ := tray.state(); return tip != "" })
	if _, tip, _ := tray.state(); tip != "Awesome Reminder — 3 pending — Synced · 2 min ago — hotkey error: taken — reply hotkey error: busy" {
		t.Fatalf("tooltip %q", tip)
	}
}

func TestBadgeWiringSyncChanged(t *testing.T) {
	plain := trayIcons{
		Light:    testPNG(t, color.NRGBA{0, 0, 0, 255}),
		Dark:     testPNG(t, color.NRGBA{255, 255, 255, 255}),
		Template: testPNG(t, color.NRGBA{0, 0, 0, 255}),
	}
	var mu sync.Mutex
	st := cloudsync.Status{State: cloudsync.StateOff}
	setSt := func(s cloudsync.Status) { mu.Lock(); st = s; mu.Unlock() }
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	tray := &fakeBadgeTray{}
	ft := &fakeTicker{c: make(chan time.Time)}
	reads := 0
	w := newBadgeWiring(badgeDeps{
		Count:     func() (int, error) { mu.Lock(); reads++; mu.Unlock(); return 3, nil },
		Show:      func() bool { return true },
		HotkeyErr: func() string { return "taken" },
		Sync:      func() cloudsync.Status { mu.Lock(); defer mu.Unlock(); return st },
		Now:       func() time.Time { return now },
		NewTicker: ft.New,
		Icons:     plain,
		Tray:      tray,
		Debounce:  time.Millisecond,
	})
	var nilW *badgeWiring
	nilW.SyncChanged()

	w.Start(nil)
	waitFor(t, "first refresh", func() bool { _, tip, _ := tray.state(); return tip != "" })
	offIcons, tip, _ := tray.state()
	if tip != "Awesome Reminder — 3 pending — hotkey error: taken" {
		t.Fatalf("off tooltip %q", tip)
	}
	if s, _ := ft.counts(); s != 0 {
		t.Fatal("ticker started while off")
	}
	mu.Lock()
	readsBefore := reads
	mu.Unlock()

	// Syncing: ticker starts; two ticks alternate two distinct icon sets.
	setSt(cloudsync.Status{State: cloudsync.StateSyncing})
	w.SyncChanged()
	iconsA, tip, _ := tray.state()
	if tip != "Awesome Reminder — 3 pending — Syncing… — hotkey error: taken" {
		t.Fatalf("syncing tooltip %q", tip)
	}
	if bytes.Equal(iconsA[0], offIcons[0]) {
		t.Fatal("syncing icon has no dot")
	}
	if s, _ := ft.counts(); s != 1 {
		t.Fatalf("ticker started %d times, want 1", s)
	}
	ft.c <- now
	var iconsB [3][]byte
	waitFor(t, "frame B", func() bool { iconsB, _, _ = tray.state(); return !bytes.Equal(iconsB[0], iconsA[0]) })
	ft.c <- now
	waitFor(t, "frame A again", func() bool { i, _, _ := tray.state(); return bytes.Equal(i[0], iconsA[0]) })
	w.SyncChanged() // still syncing: the same ticker keeps running
	if s, _ := ft.counts(); s != 1 {
		t.Fatalf("ticker restarted (%d)", s)
	}

	// Synced: the ticker stops, the tooltip carries the age.
	last := now.Add(-2 * time.Minute)
	setSt(cloudsync.Status{State: cloudsync.StateSynced, LastSyncAt: &last})
	w.SyncChanged()
	// The goroutine releases the ticker asynchronously after its stop
	// channel closes; once it has, it can no longer take a tick.
	waitFor(t, "ticker released", func() bool { _, e := ft.counts(); return e == 1 })
	_, tip, sets := tray.state()
	if tip != "Awesome Reminder — 3 pending — Synced · 2 min ago — hotkey error: taken" {
		t.Fatalf("synced tooltip %q", tip)
	}
	select {
	case ft.c <- now:
		t.Fatal("stopped ticker still consumed a tick")
	case <-time.After(30 * time.Millisecond):
	}
	if _, _, after := tray.state(); after != sets {
		t.Fatal("icons changed after the ticker stopped")
	}
	mu.Lock()
	if reads != readsBefore {
		t.Fatalf("SyncChanged read the store (%d reads)", reads-readsBefore)
	}
	mu.Unlock()

	// Syncing again, then Stop joins the ticker: nothing touches the tray after.
	setSt(cloudsync.Status{State: cloudsync.StateSyncing})
	w.SyncChanged()
	w.Stop()
	if s, e := ft.counts(); s != 2 || e != 2 {
		t.Fatalf("ticker started/stopped %d/%d, want 2/2", s, e)
	}
	_, _, before := tray.state()
	w.SyncChanged()
	time.Sleep(30 * time.Millisecond)
	if _, _, after := tray.state(); after != before {
		t.Fatal("tray touched after Stop")
	}
}

// A SyncChanged or StatusChanged that passed its stopped check before Stop
// ran must not write to the tray after Stop returns.
func TestBadgeWiringNoTrayWritesAfterStop(t *testing.T) {
	for _, name := range []string{"SyncChanged", "StatusChanged"} {
		t.Run(name, func(t *testing.T) {
			tray := &fakeBadgeTray{}
			w := newBadgeWiring(badgeDeps{
				Count:     func() (int, error) { return 1, nil },
				Show:      func() bool { return true },
				HotkeyErr: func() string { return "" },
				Sync:      func() cloudsync.Status { return cloudsync.Status{State: cloudsync.StateSynced} },
				Tray:      tray,
				Debounce:  time.Millisecond,
			})
			w.Start(nil)
			waitFor(t, "first refresh", func() bool { _, tip, _ := tray.state(); return tip != "" })

			var before int
			w.afterStopCheck = func() {
				// Runs in the window between the stopped check and refreshMu.
				w.afterStopCheck = nil
				w.Stop()
				_, _, before = tray.state()
			}
			if name == "SyncChanged" {
				w.SyncChanged()
			} else {
				w.StatusChanged()
			}
			if _, _, after := tray.state(); after != before {
				t.Fatal("tray written after Stop")
			}
			// And the plain post-Stop no-op.
			w.SyncChanged()
			w.StatusChanged()
			if _, _, after := tray.state(); after != before {
				t.Fatal("tray written after Stop")
			}
		})
	}
}

// The syncing animation composes each frame's icons once per count and dot
// style, then reuses them; an unchanged tooltip is not set again.
func TestBadgeWiringSyncFramesCached(t *testing.T) {
	var composes atomic.Int32
	orig := composeBadge
	composeBadge = func(icon []byte, n int, dot badge.DotStyle, template bool) []byte {
		composes.Add(1)
		return orig(icon, n, dot, template)
	}
	defer func() { composeBadge = orig }()

	var mu sync.Mutex
	count := 3
	tray := &fakeBadgeTray{}
	ft := &fakeTicker{c: make(chan time.Time)}
	w := newBadgeWiring(badgeDeps{
		Count:     func() (int, error) { mu.Lock(); defer mu.Unlock(); return count, nil },
		Show:      func() bool { return true },
		HotkeyErr: func() string { return "" },
		Sync:      func() cloudsync.Status { return cloudsync.Status{State: cloudsync.StateSyncing} },
		NewTicker: ft.New,
		Icons: trayIcons{
			Light:    testPNG(t, color.NRGBA{0, 0, 0, 255}),
			Dark:     testPNG(t, color.NRGBA{255, 255, 255, 255}),
			Template: testPNG(t, color.NRGBA{0, 0, 0, 255}),
		},
		Tray:     tray,
		Debounce: time.Millisecond,
	})
	w.Start(nil)
	defer w.Stop()
	waitFor(t, "first refresh", func() bool { _, _, sets := tray.state(); return sets == 1 })
	frameA, _, _ := tray.state()

	tick := func(i int) [3][]byte {
		t.Helper()
		_, _, before := tray.state()
		ft.c <- time.Now()
		waitFor(t, fmt.Sprintf("tick %d", i), func() bool { _, _, sets := tray.state(); return sets == before+1 })
		icons, _, _ := tray.state()
		return icons
	}
	frameB := tick(0)
	if bytes.Equal(frameA[0], frameB[0]) {
		t.Fatal("frames A and B are the same icon")
	}
	for i := 1; i <= 6; i++ {
		want := frameA
		if i%2 == 0 {
			want = frameB
		}
		if got := tick(i); !bytes.Equal(got[0], want[0]) || !bytes.Equal(got[2], want[2]) {
			t.Fatalf("tick %d shows the wrong frame", i)
		}
	}
	if n := composes.Load(); n != 6 {
		t.Errorf("composed %d icons over 7 ticks, want 6 (3 per frame)", n)
	}
	if n := tray.tooltipSets(); n != 1 {
		t.Errorf("tooltip set %d times, want 1 (it never changed)", n)
	}

	// A new count composes new frames, and the tooltip changes once.
	mu.Lock()
	count = 4
	mu.Unlock()
	w.Changed()
	waitFor(t, "count 4 tooltip", func() bool { _, tip, _ := tray.state(); return strings.Contains(tip, "4 pending") })
	tick(7)
	tick(8)
	if n := composes.Load(); n != 12 {
		t.Errorf("composed %d icons after the count changed, want 12", n)
	}
	if n := tray.tooltipSets(); n != 2 {
		t.Errorf("tooltip set %d times, want 2", n)
	}
}
