// Package grab reads the text the user has selected in another app, for the
// "reply owed" hotkey: it injects a copy and puts the clipboard back the way
// it was.
//
// Grab does its work only when called (on the key press): nothing here polls
// the clipboard or the foreground window.
package grab

import (
	"time"
	"unicode/utf8"
)

// Key is a modifier key as a Windows virtual-key code. macOS and Linux
// ignore it.
type Key uint16

// The modifier keys a hotkey can hold.
const (
	KeyShift   Key = 0x10 // VK_SHIFT
	KeyControl Key = 0x11 // VK_CONTROL
	KeyAlt     Key = 0x12 // VK_MENU
	KeyWin     Key = 0x5B // VK_LWIN
)

// Result is what Grab found. Grabbed is false when Text came from the
// fallback (the clipboard's own text) or is empty.
type Result struct {
	Text, Process, Title string
	Grabbed              bool
}

const (
	// releaseTimeout bounds the wait for the hotkey's modifiers to be let go.
	releaseTimeout = 300 * time.Millisecond
	// pollInterval is the step of every wait.
	pollInterval = 10 * time.Millisecond
	// seqTimeout bounds the wait for the injected copy to reach the clipboard.
	seqTimeout = 150 * time.Millisecond
	// openTimeout bounds the retries while another app has the clipboard open.
	openTimeout = 100 * time.Millisecond
	// maxFallback is the longest clipboard text used as the fallback quote.
	maxFallback = 2000
)

// unrestorable are clipboard formats that are not plain memory, so they
// cannot be saved and put back byte for byte: files, bitmaps and metafiles
// (the Global Constraints list), plus the other GDI-handle and
// owner-managed formats.
var unrestorable = map[uint32]bool{
	2:    true, // CF_BITMAP
	3:    true, // CF_METAFILEPICT
	8:    true, // CF_DIB
	14:   true, // CF_ENHMETAFILE
	15:   true, // CF_HDROP
	17:   true, // CF_DIBV5
	9:    true, // CF_PALETTE (an HPALETTE)
	0x80: true, // CF_OWNERDISPLAY
	0x82: true, // CF_DSPBITMAP
	0x83: true, // CF_DSPMETAFILEPICT
	0x8E: true, // CF_DSPENHMETAFILE
}

// canRestore reports whether every format on the clipboard can be saved and
// put back: none is a file list, image or other handle format, and each one
// is readable. When it is false no copy is injected.
func canRestore(formats []uint32, readable func(uint32) bool) bool {
	for _, f := range formats {
		// CF_PRIVATEFIRST..CF_PRIVATELAST and CF_GDIOBJFIRST..CF_GDIOBJLAST
		// carry handles the system knows nothing about.
		if unrestorable[f] || (f >= 0x200 && f <= 0x3FF) {
			return false
		}
		if !readable(f) {
			return false
		}
	}
	return true
}

// fallbackQuote is the quote to use when nothing was grabbed: the
// clipboard's own text, if it is non-empty and at most 2,000 characters.
func fallbackQuote(text string) (string, bool) {
	if text == "" || utf8.RuneCountInString(text) > maxFallback {
		return "", false
	}
	return text, true
}

// waitForRelease waits for the hotkey's modifiers to be let go, polling
// every 10 ms for up to timeout, then sends a key-up for any still down, so
// the injected Ctrl+C does not arrive as Ctrl+Shift+C or Ctrl+Alt+C.
func waitForRelease(down func(Key) bool, keyUp func(Key), mods []Key, timeout time.Duration, sleep func(time.Duration)) {
	anyDown := func() bool {
		for _, m := range mods {
			if down(m) {
				return true
			}
		}
		return false
	}
	for waited := time.Duration(0); anyDown(); waited += pollInterval {
		if waited >= timeout {
			sent := map[Key]bool{}
			for _, m := range mods {
				if !sent[m] && down(m) {
					sent[m] = true
					keyUp(m)
				}
			}
			return
		}
		sleep(pollInterval)
	}
}
