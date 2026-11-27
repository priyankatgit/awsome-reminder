// Package grab reads the text the user has selected in another app, for the
// "reply owed" hotkey: it injects a copy and puts the clipboard back the way
// it was.
//
// Grab does its work only when called (on the key press): nothing here polls
// the clipboard or the foreground window.
package grab

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Key is one of the hotkey's keys as a Windows virtual-key code: a modifier
// or the main key. macOS and Linux ignore it.
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
	// lateTimeout bounds the background wait for a copy that missed
	// seqTimeout (a busy Outlook or Excel), so the clipboard still goes back.
	lateTimeout = 1 * time.Second
	// restoreOpenTimeout bounds the retries to open the clipboard for the
	// restore, which must happen once the copy has landed.
	restoreOpenTimeout = 500 * time.Millisecond
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

// isModifier reports whether k is a modifier key: Shift, Ctrl, Alt or Win,
// generic or sided.
func isModifier(k Key) bool {
	switch k {
	case KeyShift, KeyControl, KeyAlt, KeyWin, 0x5C, // VK_RWIN
		0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5: // VK_LSHIFT..VK_RMENU
		return true
	}
	return false
}

// waitForRelease waits for the hotkey's keys (modifiers and main key) to be
// let go, polling every 10 ms for up to timeout, so the injected Ctrl+C does
// not arrive as Ctrl+Shift+C or Ctrl+Alt+C. At the timeout it sends a key-up
// for each modifier still down, unless a non-modifier is still down: its
// auto-repeat would then type into the selection, so it sends nothing and
// returns false (don't inject).
func waitForRelease(down func(Key) bool, keyUp func(Key), keys []Key, timeout time.Duration, sleep func(time.Duration)) bool {
	anyDown := func() bool {
		for _, k := range keys {
			if down(k) {
				return true
			}
		}
		return false
	}
	for waited := time.Duration(0); anyDown(); waited += pollInterval {
		if waited < timeout {
			sleep(pollInterval)
			continue
		}
		for _, k := range keys {
			if !isModifier(k) && down(k) {
				return false
			}
		}
		sent := map[Key]bool{}
		for _, k := range keys {
			if !sent[k] && down(k) {
				sent[k] = true
				keyUp(k)
			}
		}
		return true
	}
	return true
}

// noInjectProcesses are terminals and IDEs with a terminal: with nothing
// selected, Ctrl+C there interrupts the running process.
var noInjectProcesses = []string{
	"WindowsTerminal.exe", "OpenConsole.exe", "conhost.exe", "cmd.exe",
	"powershell.exe", "pwsh.exe", "wsl.exe", "bash.exe", "mintty.exe",
	"alacritty.exe", "wezterm-gui.exe", "Code.exe", "Cursor.exe",
	"idea64.exe", "goland64.exe", "pycharm64.exe", "webstorm64.exe",
	"rider64.exe", "clion64.exe", "devenv.exe",
}

// noInject reports whether Ctrl+C must not be sent to the process (its
// image base name): a terminal or IDE, or an unknown process.
func noInject(process string) bool {
	if process == "" {
		return true
	}
	for _, p := range noInjectProcesses {
		if strings.EqualFold(process, p) {
			return true
		}
	}
	return false
}

// waitForChange polls seq every 10 ms for up to timeout and reports whether
// it moved away from from.
func waitForChange(seq func() uint32, from uint32, timeout time.Duration, now func() time.Time, sleep func(time.Duration)) bool {
	deadline := now().Add(timeout)
	for {
		if seq() != from {
			return true
		}
		if !now().Before(deadline) {
			return false
		}
		sleep(pollInterval)
	}
}

// afterCopyStep is what is left to do once the injected copy's wait is over.
type afterCopyStep int

const (
	// stepDone: the copy landed and the clipboard was read and put back.
	stepDone afterCopyStep = iota
	// stepRestoreLater: the copy landed but the clipboard was busy, so it is
	// put back in the background (reopen retried for up to 500 ms).
	stepRestoreLater
	// stepWatchLate: no copy within 150 ms. Grab returns the fallback; the
	// background waits up to 1 s for a late copy and then puts the clipboard
	// back. If none comes, the clipboard was never touched.
	stepWatchLate
)

func (s afterCopyStep) String() string {
	switch s {
	case stepDone:
		return "stepDone"
	case stepRestoreLater:
		return "stepRestoreLater"
	case stepWatchLate:
		return "stepWatchLate"
	}
	return "afterCopyStep(?)"
}

// afterCopy decides the next step from whether the clipboard sequence
// changed within the wait and whether the clipboard was then restored.
func afterCopy(changed, restored bool) afterCopyStep {
	switch {
	case !changed:
		return stepWatchLate
	case !restored:
		return stepRestoreLater
	}
	return stepDone
}

// unrestorableTypes are macOS pasteboard types that are files or images:
// the Global Constraints list, as for unrestorable on Windows.
var unrestorableTypes = map[string]bool{
	"public.file-url": true,
	"public.tiff":     true,
	"public.png":      true,
}

// canRestoreTypes is canRestore for macOS pasteboard types: every type can
// be saved and put back when none is a file or image and each one is
// readable. When it is false no Cmd+C is injected.
func canRestoreTypes(types []string, readable func(string) bool) bool {
	for _, t := range types {
		if unrestorableTypes[t] || !readable(t) {
			return false
		}
	}
	return true
}

// noInjectApps are macOS terminals and IDEs with a terminal (their
// localized app names): with nothing selected, Cmd+C there can interrupt
// the running process.
var noInjectApps = []string{
	"Terminal", "iTerm2", "Alacritty", "WezTerm", "kitty", "Warp", "Hyper",
	"Ghostty", "Code", "Visual Studio Code", "Cursor", "IntelliJ IDEA",
	"GoLand", "PyCharm", "WebStorm", "Rider", "CLion",
}

// noInjectMac reports whether Cmd+C must not be sent to the frontmost app
// (its localized name): a terminal or IDE, or an unknown app.
func noInjectMac(app string) bool {
	if app == "" {
		return true
	}
	for _, a := range noInjectApps {
		if strings.EqualFold(app, a) {
			return true
		}
	}
	return false
}
