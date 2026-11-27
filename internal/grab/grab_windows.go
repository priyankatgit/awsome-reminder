//go:build windows

package grab

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows the grab injects Ctrl+C into the foreground app with
// SendInput and reads CF_UNICODETEXT. Every clipboard format is saved into
// memory first and written back afterwards, marked so clipboard history and
// cloud clipboard skip the restore. If the clipboard holds something that
// cannot be put back byte for byte (files, an image, a handle format, or a
// format that cannot be read), nothing is injected and the clipboard is
// never touched. Nothing is injected into a terminal or IDE either (Ctrl+C
// would interrupt the running process), or while a hotkey key that is not a
// modifier is still held.
//
// A copy that lands after the 150 ms wait (a busy Outlook or Excel) is
// still put back: Grab returns the fallback on time, and a background
// goroutine waits up to 1 s more for the copy, then restores.
//
// The restore needs a window: with OpenClipboard(NULL), EmptyClipboard
// leaves the clipboard without an owner and SetClipboardData then fails. A
// message-only window lives on the goroutine's locked OS thread for the
// length of the grab.

var (
	grUser32   = windows.NewLazySystemDLL("user32.dll")
	grKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard              = grUser32.NewProc("OpenClipboard")
	procCloseClipboard             = grUser32.NewProc("CloseClipboard")
	procEmptyClipboard             = grUser32.NewProc("EmptyClipboard")
	procEnumClipboardFormats       = grUser32.NewProc("EnumClipboardFormats")
	procGetClipboardData           = grUser32.NewProc("GetClipboardData")
	procSetClipboardData           = grUser32.NewProc("SetClipboardData")
	procGetClipboardSequenceNumber = grUser32.NewProc("GetClipboardSequenceNumber")
	procRegisterClipboardFormatW   = grUser32.NewProc("RegisterClipboardFormatW")
	procSendInput                  = grUser32.NewProc("SendInput")
	procGetAsyncKeyState           = grUser32.NewProc("GetAsyncKeyState")
	procMapVirtualKeyW             = grUser32.NewProc("MapVirtualKeyW")
	procCreateWindowExW            = grUser32.NewProc("CreateWindowExW")
	procDestroyWindow              = grUser32.NewProc("DestroyWindow")
	procGetWindowTextW             = grUser32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW       = grUser32.NewProc("GetWindowTextLengthW")

	procGlobalAlloc  = grKernel32.NewProc("GlobalAlloc")
	procGlobalFree   = grKernel32.NewProc("GlobalFree")
	procGlobalLock   = grKernel32.NewProc("GlobalLock")
	procGlobalUnlock = grKernel32.NewProc("GlobalUnlock")
	procGlobalSize   = grKernel32.NewProc("GlobalSize")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	inputKeyboard       = 1
	keyeventfExtendedKy = 0x0001
	keyeventfKeyUp      = 0x0002
	mapvkVkToVsc        = 0

	vkControl = 0x11
	vkC       = 0x43
	// vkMask is unassigned. Tapping it before a lone Alt or Win key-up stops
	// the release from opening the menu bar or the Start menu.
	vkMask = 0xE8

	hwndMessage = ^uintptr(2) // HWND_MESSAGE, (HWND)-3
)

// The formats that keep the restore write out of clipboard history and the
// cloud clipboard.
const (
	fmtExcludeMonitor = "ExcludeClipboardContentFromMonitorProcessing"
	fmtCanHistory     = "CanIncludeInClipboardHistory"
	fmtCanCloud       = "CanUploadToCloudClipboard"
)

// keybdInput is KEYBDINPUT. Natural alignment puts the 4 bytes of padding
// before extraInfo on 64-bit.
type keybdInput struct {
	vk        uint16
	scan      uint16
	flags     uint32
	time      uint32
	extraInfo uintptr
}

// input is INPUT with the keyboard member of the union: the union is as big
// as MOUSEINPUT, 8 bytes more than KEYBDINPUT on every architecture. 40
// bytes on amd64 and arm64 (type, 4 bytes of padding, 32-byte union), 28 on
// 386.
type input struct {
	typ uint32
	ki  keybdInput
	_   [8]byte
}

// sides are the left and right keys behind each modifier.
var sides = map[Key][2]uint16{
	KeyShift:   {0xA0, 0xA1}, // VK_LSHIFT, VK_RSHIFT
	KeyControl: {0xA2, 0xA3}, // VK_LCONTROL, VK_RCONTROL
	KeyAlt:     {0xA4, 0xA5}, // VK_LMENU, VK_RMENU
	KeyWin:     {0x5B, 0x5C}, // VK_LWIN, VK_RWIN
}

// extended keys need KEYEVENTF_EXTENDEDKEY to be told apart from their
// left-hand twins.
var extended = map[uint16]bool{0xA3: true, 0xA5: true, 0x5B: true, 0x5C: true}

type savedFormat struct {
	format uint32
	data   []byte
}

// restoreMu is held from the save until the clipboard is put back,
// including by a background restore after Grab has returned. A grab that
// finds it held does not inject: its save would capture the previous grab's
// copy instead of the user's clipboard.
var restoreMu sync.Mutex

// Grab returns the text selected in the foreground app, and the app's
// process name and window title. keys are all the hotkey's keys (modifiers
// and main key), released before the copy is injected. It never panics: a
// Win32 failure is logged and Grab returns what it has.
func Grab(ctx context.Context, keys []Key) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("grab: panic: %v", r)
		}
	}()
	runtime.LockOSThread() // the window and the open clipboard belong to this thread
	defer runtime.UnlockOSThread()

	res.Process, res.Title = windowInfo(windows.GetForegroundWindow())
	switch {
	case ctx.Err() != nil:
		return res
	case noInject(res.Process):
		res.Text = fallbackText()
		return res
	case !waitForRelease(keyDown, modUp, keys, releaseTimeout, time.Sleep):
		log.Printf("grab: a hotkey key is still held; not injecting")
		res.Text = fallbackText()
		return res
	case !restoreMu.TryLock():
		log.Printf("grab: the last grab's restore is still pending; not injecting")
		res.Text = fallbackText()
		return res
	}
	res.Text, res.Grabbed = copySelection(ctx)
	return res
}

// copySelection injects Ctrl+C and returns the copied text, or the fallback
// quote with grabbed false. The caller holds restoreMu; copySelection
// unlocks it, or hands it to the background restore.
func copySelection(ctx context.Context) (text string, grabbed bool) {
	handedOff := false
	defer func() {
		if !handedOff {
			restoreMu.Unlock()
		}
	}()
	hwnd := messageWindow()
	if hwnd == 0 { // the restore would fail: don't inject
		return fallbackText(), false
	}
	defer procDestroyWindow.Call(hwnd)

	var saved []savedFormat
	var original string
	ok := false
	if !withClipboard(hwnd, openTimeout, func() { saved, original, ok = saveClipboard() }) {
		return "", false
	}
	fallback, _ := fallbackQuote(original)
	if !ok || ctx.Err() != nil {
		return fallback, false
	}

	seq := sequenceNumber()
	if !sendCopy() {
		return fallback, false
	}
	// From here the clipboard must go back, whatever ctx says.
	changed := waitForChange(sequenceNumber, seq, seqTimeout, time.Now, time.Sleep)
	restored := changed && withClipboard(hwnd, openTimeout, func() {
		text = readText()
		restore(saved)
	})
	switch afterCopy(changed, restored) {
	case stepWatchLate:
		handedOff = true
		go restoreLater(saved, seq, true)
	case stepRestoreLater:
		log.Printf("grab: clipboard busy after the copy; restoring in the background")
		handedOff = true
		go restoreLater(saved, seq, false)
	}
	if text != "" {
		return text, true
	}
	return fallback, false
}

// restoreLater puts the saved clipboard back after Grab has returned. With
// watch, it first waits up to 1 s for a late copy and does nothing if none
// lands (the clipboard was never touched). It owns restoreMu and unlocks it.
func restoreLater(saved []savedFormat, seq uint32, watch bool) {
	defer restoreMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("grab: background restore: panic: %v", r)
		}
	}()
	runtime.LockOSThread() // its own window and open clipboard
	defer runtime.UnlockOSThread()

	if watch && !waitForChange(sequenceNumber, seq, lateTimeout, time.Now, time.Sleep) {
		return
	}
	hwnd := messageWindow()
	if hwnd == 0 {
		log.Printf("grab: background restore: no window; clipboard not restored")
		return
	}
	defer procDestroyWindow.Call(hwnd)
	if !withClipboard(hwnd, restoreOpenTimeout, func() { restore(saved) }) {
		log.Printf("grab: background restore: clipboard busy; not restored")
	}
}

// fallbackText is the fallback quote from the clipboard's current text.
func fallbackText() string {
	var text string
	withClipboard(0, openTimeout, func() { text = readText() })
	q, _ := fallbackQuote(text)
	return q
}

// windowInfo is the base name of hwnd's process image and its title.
func windowInfo(hwnd windows.HWND) (process, title string) {
	if hwnd == 0 {
		return "", ""
	}
	if n, _, _ := procGetWindowTextLengthW.Call(uintptr(hwnd)); n > 0 {
		buf := make([]uint16, n+1)
		r, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		title = windows.UTF16ToString(buf[:r])
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil || pid == 0 {
		log.Printf("grab: GetWindowThreadProcessId: %v", err)
		return "", title
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		log.Printf("grab: OpenProcess(%d): %v", pid, err)
		return "", title
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		log.Printf("grab: QueryFullProcessImageName: %v", err)
		return "", title
	}
	return filepath.Base(windows.UTF16ToString(buf[:size])), title
}

// messageWindow creates a message-only window to own the clipboard during
// the restore, or returns 0.
func messageWindow() uintptr {
	class, _ := windows.UTF16PtrFromString("STATIC")
	h, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), 0, 0,
		0, 0, 0, 0, hwndMessage, 0, 0, 0)
	if h == 0 {
		log.Printf("grab: CreateWindowEx: %v", err)
	}
	return h
}

// withClipboard opens the clipboard for hwnd, retrying every 10 ms for up
// to timeout while another app has it open, runs f and closes it, even if f
// panics. It reports whether the clipboard could be opened.
func withClipboard(hwnd uintptr, timeout time.Duration, f func()) bool {
	deadline := time.Now().Add(timeout)
	for {
		r, _, err := procOpenClipboard.Call(hwnd)
		if r != 0 {
			break
		}
		if !time.Now().Before(deadline) {
			log.Printf("grab: OpenClipboard: %v", err)
			return false
		}
		time.Sleep(pollInterval)
	}
	defer procCloseClipboard.Call()
	f()
	return true
}

// saveClipboard copies every format on the open clipboard into memory, and
// returns its CF_UNICODETEXT for the fallback. ok is false when the
// clipboard cannot be put back byte for byte; saved is then nil.
func saveClipboard() (saved []savedFormat, text string, ok bool) {
	var formats []uint32
	for f := uintptr(0); ; {
		f, _, _ = procEnumClipboardFormats.Call(f)
		if f == 0 {
			break
		}
		formats = append(formats, uint32(f))
	}
	// Reject files and images before reading anything: a read can make the
	// source app render a delayed format.
	if !canRestore(formats, func(uint32) bool { return true }) {
		return nil, readText(), false
	}
	handles := map[uint32]uintptr{}
	readable := func(f uint32) bool {
		h, _, _ := procGetClipboardData.Call(uintptr(f))
		handles[f] = h
		return h != 0
	}
	if !canRestore(formats, readable) {
		return nil, readText(), false
	}
	for _, f := range formats {
		data, err := globalBytes(handles[f])
		if err != nil {
			log.Printf("grab: save format %d: %v", f, err)
			return nil, readText(), false
		}
		saved = append(saved, savedFormat{format: f, data: data})
		if f == cfUnicodeText {
			text = utf16Text(data)
		}
	}
	return saved, text, true
}

// globalBytes copies the memory behind an HGLOBAL.
func globalBytes(h uintptr) ([]byte, error) {
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		return nil, fmt.Errorf("GlobalLock: %w", err)
	}
	defer procGlobalUnlock.Call(h)
	n, _, err := procGlobalSize.Call(h)
	if n == 0 {
		return nil, fmt.Errorf("GlobalSize: %w", err)
	}
	src := unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), n)
	return append([]byte(nil), src...), nil
}

// readText is the open clipboard's CF_UNICODETEXT, or "".
func readText() string {
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return ""
	}
	data, err := globalBytes(h)
	if err != nil {
		log.Printf("grab: read text: %v", err)
		return ""
	}
	return utf16Text(data)
}

// utf16Text decodes NUL-terminated UTF-16 from clipboard bytes.
func utf16Text(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return windows.UTF16ToString(u)
}

// restore empties the open clipboard and writes the saved formats back,
// marked to stay out of clipboard history and the cloud clipboard.
func restore(saved []savedFormat) {
	if r, _, err := procEmptyClipboard.Call(); r == 0 {
		log.Printf("grab: EmptyClipboard: %v", err)
		return
	}
	for _, s := range saved {
		setData(s.format, s.data)
	}
	zero := []byte{0, 0, 0, 0} // DWORD 0
	if f := registerFormat(fmtExcludeMonitor); f != 0 {
		setData(f, []byte{0})
	}
	if f := registerFormat(fmtCanHistory); f != 0 {
		setData(f, zero)
	}
	if f := registerFormat(fmtCanCloud); f != 0 {
		setData(f, zero)
	}
}

func registerFormat(name string) uint32 {
	p, _ := windows.UTF16PtrFromString(name)
	f, _, err := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p)))
	if f == 0 {
		log.Printf("grab: RegisterClipboardFormat(%s): %v", name, err)
	}
	return uint32(f)
}

// setData puts a copy of data on the open clipboard as format. The
// clipboard owns the memory once SetClipboardData succeeds.
func setData(format uint32, data []byte) {
	if len(data) == 0 {
		return
	}
	h, _, err := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		log.Printf("grab: GlobalAlloc(%d): %v", len(data), err)
		return
	}
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		log.Printf("grab: GlobalLock: %v", err)
		procGlobalFree.Call(h)
		return
	}
	copy(unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), len(data)), data)
	procGlobalUnlock.Call(h)
	if r, _, err := procSetClipboardData.Call(uintptr(format), h); r == 0 {
		log.Printf("grab: SetClipboardData(%d): %v", format, err)
		procGlobalFree.Call(h)
	}
}

func sequenceNumber() uint32 {
	r, _, _ := procGetClipboardSequenceNumber.Call()
	return uint32(r)
}

func keyInput(vk uint16, flags uint32) input {
	scan, _, _ := procMapVirtualKeyW.Call(uintptr(vk), mapvkVkToVsc)
	if extended[vk] {
		flags |= keyeventfExtendedKy
	}
	return input{typ: inputKeyboard, ki: keybdInput{vk: vk, scan: uint16(scan), flags: flags}}
}

// sendInputs injects in and returns how many events were sent.
func sendInputs(in []input) int {
	if len(in) == 0 {
		return 0
	}
	n, _, err := procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
	if int(n) != len(in) {
		log.Printf("grab: SendInput: %d of %d sent: %v", n, len(in), err)
	}
	return int(n)
}

// sendCopy injects Ctrl down, C down, C up, Ctrl up. It reports false only
// when nothing was sent. If only some events were sent, the copy may still
// happen: the key-ups are sent again so Ctrl and C are not left down, and
// it reports true so the clipboard is watched and put back.
func sendCopy() bool {
	ups := []input{keyInput(vkC, keyeventfKeyUp), keyInput(vkControl, keyeventfKeyUp)}
	in := append([]input{keyInput(vkControl, 0), keyInput(vkC, 0)}, ups...)
	switch n := sendInputs(in); {
	case n == 0:
		return false
	case n < len(in):
		sendInputs(ups)
	}
	return true
}

func vkDown(vk uint16) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return r&0x8000 != 0
}

// vks are the keys to check and release for k: both sides of a modifier,
// or the key itself.
func vks(k Key) []uint16 {
	if s, ok := sides[k]; ok {
		return s[:]
	}
	return []uint16{uint16(k)}
}

// keyDown reports whether k (either side, for a modifier) is held.
func keyDown(k Key) bool {
	for _, vk := range vks(k) {
		if vkDown(vk) {
			return true
		}
	}
	return false
}

// modUp sends a key-up for each side of the modifier that is held.
func modUp(k Key) {
	var in []input
	if k == KeyAlt || k == KeyWin {
		in = append(in, keyInput(vkMask, 0), keyInput(vkMask, keyeventfKeyUp))
	}
	for _, vk := range vks(k) {
		if vkDown(vk) {
			in = append(in, keyInput(vk, keyeventfKeyUp))
		}
	}
	sendInputs(in)
}

// AccessibilityTrusted is always true on Windows: the grab needs no
// permission.
func AccessibilityTrusted() bool { return true }
