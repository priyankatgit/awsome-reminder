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
	if !waitForRelease(down, keyUp, []Key{control, shift}, timeout, sleep) {
		t.Errorf("waitForRelease = false, want true (only modifiers held)")
	}

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
	if !waitForRelease(down, func(Key) { calls++ }, []Key{0x11, 0x12, 0x52}, 300*time.Millisecond, sleep) {
		t.Errorf("waitForRelease = false, want true (all released)")
	}
	if calls != 0 {
		t.Errorf("key-ups = %d, want 0", calls)
	}
	if now != 20*time.Millisecond {
		t.Errorf("waited %v, want 20ms (polled every 10 ms)", now)
	}
}

func TestWaitForReleaseNoMods(t *testing.T) {
	slept := false
	ok := waitForRelease(func(Key) bool { return true }, func(Key) { t.Error("keyUp called") }, nil,
		300*time.Millisecond, func(time.Duration) { slept = true })
	if !ok {
		t.Error("waitForRelease(no keys) = false, want true")
	}
	if slept {
		t.Error("slept with no modifiers")
	}
}

// Ruling R6: a main key still held would auto-repeat into the selection
// once its modifiers were released, so nothing is injected and nothing is
// released.
func TestWaitForReleaseMainKeyHeld(t *testing.T) {
	const keyR Key = 0x52
	var now time.Duration
	sleep := func(d time.Duration) { now += d }
	down := func(Key) bool { return true } // Ctrl+Shift+R held throughout
	var ups []Key
	timeout := 300 * time.Millisecond
	if waitForRelease(down, func(k Key) { ups = append(ups, k) }, []Key{KeyControl, KeyShift, keyR}, timeout, sleep) {
		t.Errorf("waitForRelease = true, want false (don't inject)")
	}
	for _, k := range ups {
		if k == keyR {
			t.Errorf("key-up sent for the main key")
		}
	}
	if len(ups) != 0 {
		t.Errorf("key-ups = %v, want none when not injecting", ups)
	}
	if now != timeout {
		t.Errorf("waited %v, want %v", now, timeout)
	}

	// The main key let go in time: only the held modifier is released.
	now = 0
	ups = nil
	down = func(k Key) bool { return k == KeyShift || now < 50*time.Millisecond }
	if !waitForRelease(down, func(k Key) { ups = append(ups, k) }, []Key{KeyShift, keyR}, timeout, sleep) {
		t.Errorf("waitForRelease = false, want true (main key released after 50 ms)")
	}
	if len(ups) != 1 || ups[0] != KeyShift {
		t.Errorf("key-ups = %v, want [Shift]", ups)
	}
}

func TestIsModifier(t *testing.T) {
	for _, k := range []Key{KeyShift, KeyControl, KeyAlt, KeyWin, 0x5C, 0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5} {
		if !isModifier(k) {
			t.Errorf("isModifier(%#x) = false, want true", k)
		}
	}
	for _, k := range []Key{0x52 /* R */, 0x20 /* Space */, 0x70 /* F1 */, 0x43 /* C */} {
		if isModifier(k) {
			t.Errorf("isModifier(%#x) = true, want false", k)
		}
	}
}

// Ruling R7: Ctrl+C in a terminal interrupts the running process.
func TestNoInject(t *testing.T) {
	for _, p := range []string{
		"WindowsTerminal.exe", "OpenConsole.exe", "conhost.exe", "cmd.exe",
		"powershell.exe", "pwsh.exe", "wsl.exe", "bash.exe", "mintty.exe",
		"alacritty.exe", "wezterm-gui.exe", "Code.exe", "Cursor.exe",
		"idea64.exe", "goland64.exe", "pycharm64.exe", "webstorm64.exe",
		"rider64.exe", "clion64.exe", "devenv.exe",
		"windowsterminal.EXE", "CODE.EXE", "PWSH.exe", // case-insensitive
		"", // unknown process: be safe
	} {
		if !noInject(p) {
			t.Errorf("noInject(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"OUTLOOK.EXE", "olk.exe", "ms-teams.exe", "slack.exe", "chrome.exe", "msedge.exe", "Notepad.exe", "code-helper.exe"} {
		if noInject(p) {
			t.Errorf("noInject(%q) = true, want false", p)
		}
	}
}

// fakeClock is a clock that only moves when slept on.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time                   { return c.t }
func (c *fakeClock) sleep(d time.Duration)            { c.t = c.t.Add(d) }
func (c *fakeClock) since(t0 time.Time) time.Duration { return c.t.Sub(t0) }

// Finding 1: a copy that lands after the 150 ms wait is still caught by the
// 1 s background watch, so the clipboard gets put back.
func TestWaitForChangeLateCopy(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	start := clk.now()
	seqAt := func(changeAfter time.Duration) func() uint32 {
		return func() uint32 {
			if clk.since(start) >= changeAfter {
				return 8
			}
			return 7
		}
	}

	seq := seqAt(400 * time.Millisecond) // Outlook under load
	if waitForChange(seq, 7, seqTimeout, clk.now, clk.sleep) {
		t.Fatalf("changed within %v, want not yet", seqTimeout)
	}
	if got := clk.since(start); got < seqTimeout || got > seqTimeout+pollInterval {
		t.Errorf("first wait took %v, want about %v", got, seqTimeout)
	}
	if afterCopy(false, false) != stepWatchLate {
		t.Fatalf("afterCopy(not changed) = %v, want stepWatchLate", afterCopy(false, false))
	}
	t1 := clk.now()
	if !waitForChange(seq, 7, lateTimeout, clk.now, clk.sleep) {
		t.Fatalf("late copy at 400 ms not seen within %v", lateTimeout)
	}
	if got := clk.since(t1); got > lateTimeout {
		t.Errorf("late wait took %v, want at most %v", got, lateTimeout)
	}

	// Never copied: the watch gives up after 1 s and nothing is restored.
	clk = &fakeClock{t: time.Unix(2000, 0)}
	start = clk.now()
	seq = seqAt(time.Hour)
	if waitForChange(seq, 7, lateTimeout, clk.now, clk.sleep) {
		t.Errorf("waitForChange saw a change that never happened")
	}
	if got := clk.since(start); got < lateTimeout || got > lateTimeout+pollInterval {
		t.Errorf("watch took %v, want about %v", got, lateTimeout)
	}

	// An immediate change returns without sleeping.
	clk = &fakeClock{t: time.Unix(3000, 0)}
	start = clk.now()
	if !waitForChange(func() uint32 { return 9 }, 7, seqTimeout, clk.now, clk.sleep) || clk.since(start) != 0 {
		t.Errorf("immediate change: slept %v", clk.since(start))
	}
}

func TestAfterCopy(t *testing.T) {
	for _, c := range []struct {
		changed, restored bool
		want              afterCopyStep
	}{
		{true, true, stepDone},          // copied, read and restored on time
		{true, false, stepRestoreLater}, // copied, clipboard busy: restore in the background
		{false, false, stepWatchLate},   // no copy yet: watch 1 s for a late one
	} {
		if got := afterCopy(c.changed, c.restored); got != c.want {
			t.Errorf("afterCopy(changed=%v, restored=%v) = %v, want %v", c.changed, c.restored, got, c.want)
		}
	}
}

// Ruling R1: on macOS, files and images on the pasteboard, or a type that
// cannot be read, mean no Cmd+C is injected.
func TestCanRestoreTypes(t *testing.T) {
	all := func(string) bool { return true }
	base := []string{"public.utf8-plain-text", "public.html", "com.apple.webarchive"}
	if !canRestoreTypes(base, all) {
		t.Fatalf("canRestoreTypes(%v) = false, want true", base)
	}
	for _, typ := range []string{"public.file-url", "public.tiff", "public.png"} {
		types := append(append([]string{}, base...), typ)
		if canRestoreTypes(types, all) {
			t.Errorf("canRestoreTypes(%v) = true, want false (type %s)", types, typ)
		}
	}
	unreadable := func(s string) bool { return s != "public.html" }
	if canRestoreTypes(base, unreadable) {
		t.Errorf("canRestoreTypes with an unreadable type = true, want false")
	}
	if !canRestoreTypes(nil, all) {
		t.Errorf("canRestoreTypes(empty pasteboard) = false, want true")
	}
}

// Cmd+C in a macOS terminal or IDE terminal can interrupt the running
// process.
func TestNoInjectMac(t *testing.T) {
	for _, app := range []string{
		"Terminal", "iTerm2", "Alacritty", "WezTerm", "kitty", "Warp", "Hyper",
		"Ghostty", "Code", "Visual Studio Code", "Cursor", "IntelliJ IDEA",
		"GoLand", "PyCharm", "WebStorm", "Rider", "CLion",
		"terminal", "ITERM2", "visual studio code", "KITTY", // case-insensitive
		"", // unknown app: be safe
	} {
		if !noInjectMac(app) {
			t.Errorf("noInjectMac(%q) = false, want true", app)
		}
	}
	for _, app := range []string{"Mail", "Microsoft Outlook", "Slack", "Safari", "Google Chrome", "Notes", "Code Helper", "Terminal Notes"} {
		if noInjectMac(app) {
			t.Errorf("noInjectMac(%q) = true, want false", app)
		}
	}
}
