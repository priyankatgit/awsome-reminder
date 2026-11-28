package wakehelper

import "testing"

const rate = 16000

// block returns n samples alternating between +amp and -amp, so its RMS is
// exactly amp/32768.
func block(n int, amp int16) []int16 {
	b := make([]int16, n)
	for i := range b {
		if i%2 == 0 {
			b[i] = amp
		} else {
			b[i] = -amp
		}
	}
	return b
}

const win = rate * 30 / 1000 // one 30 ms window

func TestGateQuietFeedsNothing(t *testing.T) {
	g := NewGate(rate)
	for i := 0; i < 2*rate/win; i++ {
		feed, reset := g.Push(block(win, 50))
		if len(feed) != 0 || reset {
			t.Fatalf("block %d: fed %d samples, reset %v; want nothing", i, len(feed), reset)
		}
	}
}

func TestGateOpensWithPreRoll(t *testing.T) {
	g := NewGate(rate)
	for i := 0; i < 600/30; i++ { // 0.6 s quiet
		if feed, _ := g.Push(block(win, 50)); len(feed) != 0 {
			t.Fatalf("quiet block %d fed %d samples", i, len(feed))
		}
	}
	loud := block(win, 10000)
	feed, reset := g.Push(loud)
	if reset {
		t.Fatal("reset on opening")
	}
	if want := rate/2 + win; len(feed) != want {
		t.Fatalf("first feed has %d samples, want %d (500 ms pre-roll + block)", len(feed), want)
	}
	for i, s := range feed[:rate/2] {
		if s != 50 && s != -50 {
			t.Fatalf("pre-roll sample %d = %d, want ±50", i, s)
		}
	}
	for i, s := range feed[rate/2:] {
		if s != loud[i] {
			t.Fatalf("block sample %d = %d, want %d", i, s, loud[i])
		}
	}
}

func TestGateClosesAfterQuietAndResets(t *testing.T) {
	g := NewGate(rate)
	if feed, _ := g.Push(block(win, 10000)); len(feed) == 0 {
		t.Fatal("loud block not fed")
	}
	resets := 0
	resetAt := -1
	for i := 0; i < 3*rate/win; i++ { // 3 s of quiet
		feed, reset := g.Push(block(win, 50))
		if reset {
			resets++
			resetAt = i
		}
		quietSoFar := (i + 1) * win
		switch {
		case quietSoFar < rate*3/2 && len(feed) != win:
			t.Fatalf("quiet block %d (%d ms after speech) fed %d samples, want %d (still open)", i, quietSoFar*1000/rate, len(feed), win)
		case resetAt >= 0 && i > resetAt && len(feed) != 0:
			t.Fatalf("quiet block %d fed %d samples after the reset", i, len(feed))
		}
	}
	if resets != 1 {
		t.Fatalf("%d resets, want exactly 1", resets)
	}
	if got := (resetAt + 1) * win; got != rate*3/2 {
		t.Fatalf("reset after %d ms of quiet, want 1500", got*1000/rate)
	}
}

// Blocks that do not line up with the 30 ms windows (the mic delivers 100 ms)
// still open the gate, and a short pause inside speech keeps it open.
func TestGateUnalignedBlocksAndPause(t *testing.T) {
	g := NewGate(rate)
	for i := 0; i < 10; i++ {
		g.Push(block(1600, 50))
	}
	feed, _ := g.Push(block(1600, 10000))
	if len(feed) != rate/2+1600 {
		t.Fatalf("first feed %d samples, want %d", len(feed), rate/2+1600)
	}
	for i := 0; i < 10; i++ { // 1 s pause: still open
		if feed, reset := g.Push(block(1600, 50)); len(feed) != 1600 || reset {
			t.Fatalf("pause block %d: fed %d, reset %v", i, len(feed), reset)
		}
	}
	if feed, reset := g.Push(block(1600, 10000)); len(feed) != 1600 || reset {
		t.Fatalf("speech after pause: fed %d, reset %v", len(feed), reset)
	}
}

func TestZeroWatch(t *testing.T) {
	const blk = rate / 10 // 100 ms, the mic's block
	z := NewZeroWatch(rate)
	for i := 1; i < 100; i++ { // 9.9 s of exact zeros from the start
		if z.Push(make([]int16, blk)) {
			t.Fatalf("silent after %d ms, want 10 s", i*100)
		}
	}
	if z.Push(nil) {
		t.Fatal("an empty block counted")
	}
	if !z.Push(make([]int16, blk)) {
		t.Fatal("10 s of exact zeros from the start: want silent")
	}

	// A mic that ever produced a sound never trips, however long the
	// exact silence after it (a noise-gated or virtual mic).
	z = NewZeroWatch(rate)
	for i := 0; i < 50; i++ {
		z.Push(make([]int16, blk))
	}
	b := make([]int16, blk)
	b[blk-1] = 1
	if z.Push(b) {
		t.Fatal("a block with one non-zero sample counted as silent")
	}
	for i := 0; i < 600; i++ { // 60 s of exact zeros afterwards
		if z.Push(make([]int16, blk)) {
			t.Fatalf("silent %d ms after a sound; want never", (i+1)*100)
		}
	}

	// A quiet room (low noise) never trips it.
	z = NewZeroWatch(rate)
	for i := 0; i < 600; i++ {
		if z.Push(block(blk, 3)) {
			t.Fatal("low noise counted as silent")
		}
	}
}
