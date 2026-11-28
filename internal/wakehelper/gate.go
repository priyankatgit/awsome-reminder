package wakehelper

import "math"

// The speech test is the one the capture box uses (frontend/src/voice.ts).
const (
	gateWindowMs   = 30    // one RMS step
	gatePreRollMs  = 500   // kept while quiet, fed in front of speech
	gateHangMs     = 1500  // fed after the last speech before closing
	gateSpeechMin  = 0.015 // RMS (of samples in [-1, 1]) that always counts as speech
	gateOverFloor  = 3     // ... or this many times the noise floor
	gateFloorStart = 0.003 // seeded at a quiet room level
)

// Gate decides which mic audio reaches the keyword spotter, so a quiet room
// costs almost no CPU. While quiet it feeds nothing and keeps the last
// 500 ms. On speech it feeds that pre-roll plus the block, then every block
// until 1.5 s without speech, and then asks once for a stream reset.
type Gate struct {
	window  int // samples per RMS window
	hang    int // samples of quiet that close the gate
	floor   float64
	acc     float64 // sum of squares of the current window
	accN    int
	open    bool
	quiet   int // samples since the last speech window, while open
	ring    []int16
	ringPos int // next write index
	ringN   int // valid samples in ring
	out     []int16
}

// NewGate returns a closed gate for audio at rate samples per second.
func NewGate(rate int) *Gate {
	return &Gate{
		window: rate * gateWindowMs / 1000,
		hang:   rate * gateHangMs / 1000,
		floor:  gateFloorStart,
		ring:   make([]int16, rate*gatePreRollMs/1000),
	}
}

// Push takes the next block of mono samples. feed is what to give the
// spotter (nil while quiet); it is valid until the next Push and may share
// memory with block. When reset is true the caller feeds feed first, then
// resets the keyword stream.
func (g *Gate) Push(block []int16) (feed []int16, reset bool) {
	speech, closeAt := false, false
	for _, s := range block {
		f := float64(s) / 32768
		g.acc += f * f
		g.accN++
		if g.accN < g.window {
			continue
		}
		rms := math.Sqrt(g.acc / float64(g.accN))
		g.acc, g.accN = 0, 0
		// Noise floor: follows quiet levels at once, louder ones only slowly.
		if rms < g.floor {
			g.floor = rms
		} else {
			g.floor = g.floor*0.995 + rms*0.005
		}
		if rms > math.Max(gateSpeechMin, g.floor*gateOverFloor) {
			speech, closeAt = true, false
			g.quiet = 0
		} else if g.open || speech {
			g.quiet += g.window
			if g.quiet >= g.hang {
				closeAt = true
			}
		}
	}

	switch {
	case !g.open && speech:
		g.open = true
		g.out = g.preRoll(g.out[:0])
		g.out = append(g.out, block...)
		feed = g.out
	case g.open:
		feed = block
	}
	if g.open && closeAt {
		g.open, g.quiet, reset = false, 0, true
	}
	g.remember(block)
	return feed, reset
}

// preRoll appends the ring's samples, oldest first, to dst.
func (g *Gate) preRoll(dst []int16) []int16 {
	start := g.ringPos - g.ringN
	if start < 0 {
		start += len(g.ring)
		dst = append(dst, g.ring[start:]...)
		start = 0
	}
	return append(dst, g.ring[start:g.ringPos]...)
}

// remember keeps the last len(g.ring) samples pushed.
func (g *Gate) remember(block []int16) {
	if len(block) >= len(g.ring) {
		copy(g.ring, block[len(block)-len(g.ring):])
		g.ringPos, g.ringN = 0, len(g.ring)
		return
	}
	n := copy(g.ring[g.ringPos:], block)
	if n < len(block) {
		copy(g.ring, block[n:])
	}
	g.ringPos = (g.ringPos + len(block)) % len(g.ring)
	g.ringN = min(g.ringN+len(block), len(g.ring))
}

// zeroLimitMs of nothing but exact-zero samples since the helper started
// means the mic delivers no sound. Windows hands a microphone blocked in
// privacy settings (or muted) to desktop apps as digital silence from the
// first block, not as an error. A mic that ever produced a non-zero sample
// is working (a noise-gated or virtual mic may go exactly silent later) and
// never trips it.
const zeroLimitMs = 10000

// ZeroWatch tells when the mic has delivered only exact zeros, from its
// first block, for zeroLimitMs.
type ZeroWatch struct {
	limit int  // samples
	run   int  // samples seen, all zero so far
	heard bool // a non-zero sample was seen: never trips
}

// NewZeroWatch returns a watch for audio at rate samples per second.
func NewZeroWatch(rate int) *ZeroWatch {
	return &ZeroWatch{limit: rate * zeroLimitMs / 1000}
}

// Push takes the next block and reports whether every sample since the
// start, zeroLimitMs of them or more, was exactly zero. Once any non-zero
// sample has been seen it reports false for good; an empty block changes
// nothing.
func (z *ZeroWatch) Push(block []int16) bool {
	if z.heard {
		return false
	}
	for _, s := range block {
		if s != 0 {
			z.heard = true
			return false
		}
	}
	z.run += len(block)
	return z.run >= z.limit
}
