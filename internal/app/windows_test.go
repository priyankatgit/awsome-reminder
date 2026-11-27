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
