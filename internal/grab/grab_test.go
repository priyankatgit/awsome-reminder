package grab

import (
	"strings"
	"testing"
	"time"
)

// Review Focus 2: an image or files on the clipboard means no copy is
// injected, so the clipboard is never touched.
func TestCanRestoreRejectsImagesAndFiles(t *testing.T) {
	all := func(uint32) bool { return true }
	base := []uint32{13 /* CF_UNICODETEXT */, 0xC0A1 /* a registered format */}
	if !canRestore(base, all) {
		t.Fatalf("canRestore(%v) = false, want true", base)
	}
	for _, f := range []uint32{15 /* HDROP */, 2 /* BITMAP */, 8 /* DIB */, 17 /* DIBV5 */, 14 /* ENHMETAFILE */, 3 /* METAFILEPICT */} {
		formats := append(append([]uint32{}, base...), f)
		if canRestore(formats, all) {
			t.Errorf("canRestore(%v) = true, want false (format %d)", formats, f)
		}
	}
	unreadable := func(f uint32) bool { return f != 0xC0A1 }
	if canRestore(base, unreadable) {
		t.Errorf("canRestore with an unreadable format = true, want false")
	}
	if !canRestore(nil, all) {
		t.Errorf("canRestore(empty clipboard) = false, want true")
	}
}

func TestFallbackQuote(t *testing.T) {
	if q, ok := fallbackQuote(""); ok || q != "" {
		t.Errorf(`fallbackQuote("") = %q, %v; want "", false`, q, ok)
	}
	s2000 := strings.Repeat("a", 2000)
	if q, ok := fallbackQuote(s2000); !ok || q != s2000 {
		t.Errorf("fallbackQuote(2,000 chars) = %d chars, %v; want the text, true", len(q), ok)
	}
	if q, ok := fallbackQuote(s2000 + "b"); ok || q != "" {
		t.Errorf(`fallbackQuote(2,001 chars) = %d chars, %v; want "", false`, len(q), ok)
	}
	// Characters, not bytes: 2,000 two-byte runes still fit.
	wide := strings.Repeat("é", 2000)
	if _, ok := fallbackQuote(wide); !ok {
		t.Errorf("fallbackQuote(2,000 non-ASCII chars) = false, want true")
	}
}

// Review Focus 1: the hotkey's own modifiers must be up before Ctrl+C is
// injected, or the source app sees Ctrl+Shift+C.
func TestWaitForReleaseSendsKeyUps(t *testing.T) {
	const (
		shift   Key = 0x10
		control Key = 0x11
	)
	var now time.Duration
	sleep := func(d time.Duration) { now += d }
	down := func(k Key) bool {
		switch k {
		case shift:
			return true // held for the whole timeout
		case control:
			return now < 30*time.Millisecond
		}
		return false
	}
	ups := map[Key]int{}
	keyUp := func(k Key) { ups[k]++ }

	timeout := 300 * time.Millisecond
	waitForRelease(down, keyUp, []Key{control, shift}, timeout, sleep)

	if ups[shift] != 1 {
		t.Errorf("key-ups for Shift = %d, want 1", ups[shift])
	}
	if ups[control] != 0 {
		t.Errorf("key-ups for Ctrl = %d, want 0 (released after 30 ms)", ups[control])
	}
	if now > timeout {
		t.Errorf("waited %v, want at most %v", now, timeout)
	}
	if now < timeout {
		t.Errorf("waited %v, want the full %v while Shift is held", now, timeout)
	}
}

func TestWaitForReleaseReturnsOnceAllUp(t *testing.T) {
	var now time.Duration
	sleep := func(d time.Duration) { now += d }
	down := func(Key) bool { return now < 20*time.Millisecond }
	calls := 0
	waitForRelease(down, func(Key) { calls++ }, []Key{0x11, 0x12}, 300*time.Millisecond, sleep)
	if calls != 0 {
		t.Errorf("key-ups = %d, want 0", calls)
	}
	if now != 20*time.Millisecond {
		t.Errorf("waited %v, want 20ms (polled every 10 ms)", now)
	}
}

func TestWaitForReleaseNoMods(t *testing.T) {
	slept := false
	waitForRelease(func(Key) bool { return true }, func(Key) { t.Error("keyUp called") }, nil,
		300*time.Millisecond, func(time.Duration) { slept = true })
	if slept {
		t.Error("slept with no modifiers")
	}
}
