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
