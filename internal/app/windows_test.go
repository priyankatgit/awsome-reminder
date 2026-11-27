package app

import (
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

// Concurrent resizes (each binding call runs on its own goroutine) must leave
// the native size equal to the recorded height, so later calls are not
// wrongly skipped.
func TestHeightKeeperConcurrentResizeStaysConsistent(t *testing.T) {
	for round := range 20 {
		var mu sync.Mutex
		native, calls := captureHeight, 0
		k := &heightKeeper{height: captureHeight, set: func(h int) {
			time.Sleep(time.Duration(rand.IntN(200)) * time.Microsecond)
			mu.Lock()
			native, calls = h, calls+1
			mu.Unlock()
		}}
		var wg sync.WaitGroup
		for i := range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				k.Resize(150 + 10*(i%4))
			}()
		}
		wg.Wait()
		k.mu.Lock()
		recorded := k.height
		k.mu.Unlock()
		if native != recorded {
			t.Fatalf("round %d: native height %d, recorded %d", round, native, recorded)
		}
		before := calls
		k.Resize(captureHeight)
		if calls != before+1 || native != captureHeight {
			t.Fatalf("round %d: resize back to %d skipped (native %d)", round, captureHeight, native)
		}
	}
}

func TestHeightKeeperClampsAndSkipsSame(t *testing.T) {
	var got []int
	k := &heightKeeper{height: captureHeight, set: func(h int) { got = append(got, h) }}
	k.Resize(captureHeight)
	k.Resize(10)
	k.Resize(10_000)
	k.Resize(10_000)
	want := []int{captureMinHeight, captureMaxHeight}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("set calls %v, want %v", got, want)
	}
}

type fakeWin struct{ n int }

func TestOnDemandCreatesOnceAndRecreatesAfterDrop(t *testing.T) {
	made := 0
	o := &onDemand[*fakeWin]{create: func() *fakeWin { made++; return &fakeWin{n: made} }}
	a, created := o.Get()
	if !created || a.n != 1 {
		t.Fatalf("first Get: %+v created=%v", a, created)
	}
	if b, created := o.Get(); created || b != a {
		t.Fatalf("second Get should reuse: %+v created=%v", b, created)
	}
	o.Drop(a)
	c, created := o.Get()
	if !created || c == a || c.n != 2 {
		t.Fatalf("after Drop: %+v created=%v", c, created)
	}
	o.Drop(a) // stale close of the old window must not forget the new one
	if d, created := o.Get(); created || d != c {
		t.Fatalf("stale Drop cleared current: %+v created=%v", d, created)
	}
}

func TestOnDemandConcurrentGetCreatesOne(t *testing.T) {
	var made int
	o := &onDemand[*fakeWin]{create: func() *fakeWin {
		time.Sleep(time.Millisecond)
		made++
		return &fakeWin{n: made}
	}}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() { o.Get() })
	}
	wg.Wait()
	if made != 1 {
		t.Fatalf("created %d windows, want 1", made)
	}
}

func TestFocusSlotTakeClears(t *testing.T) {
	var f focusSlot
	if got := f.Take(); got != "" {
		t.Fatalf("empty slot: %q", got)
	}
	f.Set("a")
	f.Set("b")
	if got := f.Take(); got != "b" {
		t.Fatalf("take: %q", got)
	}
	if got := f.Take(); got != "" {
		t.Fatalf("second take: %q", got)
	}
}

type fakePopup struct {
	n         int
	placed    []int
	shows     int
	hides     int
	destroyed int
	calls     []string // call order: place, show, hide, destroy
}

func (p *fakePopup) place(h int) { p.placed = append(p.placed, h); p.calls = append(p.calls, "place") }
func (p *fakePopup) show()       { p.shows++; p.calls = append(p.calls, "show") }
func (p *fakePopup) hide()       { p.hides++; p.calls = append(p.calls, "hide") }
func (p *fakePopup) destroy()    { p.destroyed++; p.calls = append(p.calls, "destroy") }

func newTestPopup() (*alertPopup, *[]*fakePopup, *[]func()) {
	var made []*fakePopup
	var timers []func()
	a := &alertPopup{
		create: func() popup { p := &fakePopup{n: len(made) + 1}; made = append(made, p); return p },
		after:  func(_ time.Duration, f func()) { timers = append(timers, f) },
	}
	return a, &made, &timers
}

// A new window is shown only once its page has reported its height, placed
// at that height. When the queue empties it is hidden and destroyed; the
// next alert creates a new one, which again waits for its page.
func TestAlertPopupShowsAfterPageReady(t *testing.T) {
	a, made, _ := newTestPopup()
	a.Show()
	if len(*made) != 1 {
		t.Fatalf("created %d windows", len(*made))
	}
	p := (*made)[0]
	if p.shows != 0 {
		t.Fatal("shown before the page was ready")
	}
	a.Resize(150)
	if p.shows != 1 || len(p.placed) != 1 || p.placed[0] != 150 {
		t.Fatalf("after ready: shows %d placed %v", p.shows, p.placed)
	}
	a.Show() // already visible: nothing
	if p.shows != 1 {
		t.Fatalf("shows %d", p.shows)
	}
	a.Resize(150) // same height: nothing
	a.Resize(10_000)
	if got := p.placed[len(p.placed)-1]; got != alertMaxHeight || len(p.placed) != 2 {
		t.Fatalf("placed %v, want clamp to %d once", p.placed, alertMaxHeight)
	}
	a.Dismiss()
	if p.hides != 1 || p.destroyed != 1 {
		t.Fatalf("dismiss: hides %d destroyed %d", p.hides, p.destroyed)
	}
	// Hidden first (so focus is handed back while the window still exists).
	if got := p.calls[len(p.calls)-2:]; got[0] != "hide" || got[1] != "destroy" {
		t.Fatalf("dismiss order %v, want hide then destroy", p.calls)
	}
	a.Resize(200) // a late report from the destroyed page: ignored
	if p.shows != 1 || len(p.placed) != 2 {
		t.Fatalf("destroyed window touched: shows %d placed %v", p.shows, p.placed)
	}
	a.Dismiss() // nothing left to dismiss
	if p.hides != 1 || p.destroyed != 1 {
		t.Fatalf("second dismiss: hides %d destroyed %d", p.hides, p.destroyed)
	}

	// The next alert creates a fresh window at the initial height, shown
	// only once its page reports.
	a.Show()
	if len(*made) != 2 {
		t.Fatalf("next alert: made %d windows, want 2", len(*made))
	}
	q := (*made)[1]
	if q.shows != 0 || p.shows != 1 {
		t.Fatalf("shown before the new page was ready: new %d old %d", q.shows, p.shows)
	}
	a.Resize(alertInitialHeight) // same as the default height: still counts
	if q.shows != 1 || len(q.placed) != 1 || q.placed[0] != alertInitialHeight {
		t.Fatalf("new window: shows %d placed %v", q.shows, q.placed)
	}
	if p.shows != 1 || p.destroyed != 1 {
		t.Fatalf("old window reused: shows %d destroyed %d", p.shows, p.destroyed)
	}
}

// A fallback timer from an earlier (destroyed) window does not show the
// next one early.
func TestAlertPopupStaleFallbackIgnored(t *testing.T) {
	a, made, timers := newTestPopup()
	a.Show()
	a.Resize(150)
	a.Dismiss()
	a.Show() // new window, second wait: timers[1]
	(*timers)[0]()
	if p := (*made)[1]; p.shows != 0 {
		t.Fatalf("stale fallback showed the new window: shows %d", p.shows)
	}
	(*timers)[1]()
	if p := (*made)[1]; p.shows != 1 || p.placed[0] != alertInitialHeight {
		t.Fatalf("current fallback: shows %d placed %v", p.shows, p.placed)
	}
	if p := (*made)[0]; p.shows != 1 {
		t.Fatalf("old window shown again: %d", p.shows)
	}
}

// Alerts that keep arriving while the window is up reuse it; only an empty
// queue destroys it.
func TestAlertPopupVisibleReusedUntilEmpty(t *testing.T) {
	a, made, _ := newTestPopup()
	a.Show()
	a.Resize(160)
	a.Show() // a second alert while visible
	a.Resize(300)
	if len(*made) != 1 {
		t.Fatalf("made %d", len(*made))
	}
	p := (*made)[0]
	if p.shows != 1 || p.destroyed != 0 || p.placed[len(p.placed)-1] != 300 {
		t.Fatalf("shows %d destroyed %d placed %v", p.shows, p.destroyed, p.placed)
	}
	a.Dismiss()
	if p.destroyed != 1 {
		t.Fatalf("destroyed %d", p.destroyed)
	}
}

// A window dismissed before its page was ready is destroyed without ever
// being shown or hidden, and its late fallback does nothing.
func TestAlertPopupDismissBeforeReadyNeverShows(t *testing.T) {
	a, made, timers := newTestPopup()
	a.Show()
	a.Dismiss()
	a.Resize(150)
	(*timers)[0]()
	if p := (*made)[0]; p.shows != 0 || p.hides != 0 || p.destroyed != 1 {
		t.Fatalf("shows %d hides %d destroyed %d", p.shows, p.hides, p.destroyed)
	}
	if len(*made) != 1 {
		t.Fatalf("made %d", len(*made))
	}
}

// If the page never reports a height the window is shown anyway.
func TestAlertPopupReadyFallback(t *testing.T) {
	a, made, timers := newTestPopup()
	a.Show()
	(*timers)[0]()
	p := (*made)[0]
	if p.shows != 1 || p.placed[0] != alertInitialHeight {
		t.Fatalf("fallback: shows %d placed %v", p.shows, p.placed)
	}
	a.Resize(150)
	if p.shows != 1 || p.placed[len(p.placed)-1] != 150 {
		t.Fatalf("resize after fallback: shows %d placed %v", p.shows, p.placed)
	}
}

// After the window closes, the next Show creates a new one; a stale fallback
// timer or close of the old window does nothing.
func TestAlertPopupClosedRecreates(t *testing.T) {
	a, made, timers := newTestPopup()
	a.Show()
	a.Resize(150)
	old := (*made)[0]
	a.Closed(old)
	a.Show()
	if len(*made) != 2 {
		t.Fatalf("made %d", len(*made))
	}
	(*timers)[0]() // stale timer of the old window
	a.Closed(old)  // stale close
	cur := (*made)[1]
	if cur.shows != 0 {
		t.Fatal("stale timer showed the new window")
	}
	a.Resize(160)
	if cur.shows != 1 {
		t.Fatalf("new window shows %d", cur.shows)
	}
}
