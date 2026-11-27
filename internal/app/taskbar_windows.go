//go:build windows

package app

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"awesome-reminder/internal/badge"
)

// The taskbar button: the list and settings windows are hidden from the
// taskbar (HiddenOnTaskbar), and the app keeps one tiny native window, not a
// WebView, that owns the button. It stays minimized and off-screen; clicking
// the button opens the list. The pending count is its overlay icon
// (ITaskbarList3::SetOverlayIcon).
//
// The window lives on its own locked OS thread with its own message loop,
// like the hotkey thread (internal/hotkey/registrar_windows.go), so it never
// waits on the Wails main thread.

// appUserModelID matches the single-instance ID, so a pinned shortcut and
// the running button merge.
const appUserModelID = "dev.awesome-reminder.app"

var (
	tbUser32   = windows.NewLazySystemDLL("user32.dll")
	tbGdi32    = windows.NewLazySystemDLL("gdi32.dll")
	tbOle32    = windows.NewLazySystemDLL("ole32.dll")
	tbShell32  = windows.NewLazySystemDLL("shell32.dll")
	tbKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	tbDwmapi   = windows.NewLazySystemDLL("dwmapi.dll")

	procRegisterClassExW       = tbUser32.NewProc("RegisterClassExW")
	procUnregisterClassW       = tbUser32.NewProc("UnregisterClassW")
	procCreateWindowExW        = tbUser32.NewProc("CreateWindowExW")
	procDestroyWindow          = tbUser32.NewProc("DestroyWindow")
	procDefWindowProcW         = tbUser32.NewProc("DefWindowProcW")
	procTbGetMessageW          = tbUser32.NewProc("GetMessageW")
	procTranslateMessage       = tbUser32.NewProc("TranslateMessage")
	procDispatchMessageW       = tbUser32.NewProc("DispatchMessageW")
	procPostMessageW           = tbUser32.NewProc("PostMessageW")
	procPostQuitMessage        = tbUser32.NewProc("PostQuitMessage")
	procShowWindow             = tbUser32.NewProc("ShowWindow")
	procLoadImageW             = tbUser32.NewProc("LoadImageW")
	procRegisterWindowMessageW = tbUser32.NewProc("RegisterWindowMessageW")
	procCreateIconIndirect     = tbUser32.NewProc("CreateIconIndirect")
	procDestroyIcon            = tbUser32.NewProc("DestroyIcon")
	procGetDpiForWindow        = tbUser32.NewProc("GetDpiForWindow")
	procGetSystemMetrics       = tbUser32.NewProc("GetSystemMetrics")

	procCreateDIBSection = tbGdi32.NewProc("CreateDIBSection")
	procCreateBitmap     = tbGdi32.NewProc("CreateBitmap")
	procDeleteObject     = tbGdi32.NewProc("DeleteObject")

	procCoInitializeEx   = tbOle32.NewProc("CoInitializeEx")
	procCoUninitialize   = tbOle32.NewProc("CoUninitialize")
	procCoCreateInstance = tbOle32.NewProc("CoCreateInstance")

	procSetAppUserModelID = tbShell32.NewProc("SetCurrentProcessExplicitAppUserModelID")
	procGetModuleHandleW  = tbKernel32.NewProc("GetModuleHandleW")

	procDwmSetWindowAttribute = tbDwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	wsOverlapped    = 0x00000000
	wsCaption       = 0x00C00000
	wsSysMenu       = 0x00080000
	wsMinimizeBox   = 0x00020000
	wsExAppWindow   = 0x00040000
	swShowMinNoActv = 7 // SW_SHOWMINNOACTIVE

	wmDestroy    = 0x0002
	wmSize       = 0x0005
	wmClose      = 0x0010
	wmQueryOpen  = 0x0013
	wmSysCommand = 0x0112
	wmTbCount    = 0x8000 + 1 // WM_APP+1: apply the stored count
	wmTbQuit     = 0x8000 + 2 // WM_APP+2: destroy the window

	scMinimize = 0xF020
	scMaximize = 0xF030
	scClose    = 0xF060
	scRestore  = 0xF120

	sizeRestored  = 0
	sizeMaximized = 2

	imageIcon      = 1
	lrShared       = 0x8000
	smCxIcon       = 11
	smCxSmIcon     = 49
	appIconResID   = 3     // wails3 generate syso stores the app icon as RT_ICON (3)
	idiApplication = 32512 // system fallback icon

	coinitApartmentThreaded = 0x2
	clsctxInprocServer      = 0x1

	dwmwaForceIconicRepresentation = 7
	dwmwaDisallowPeek              = 11

	// Activations closer together than this are one click (a click can
	// arrive as more than one message).
	activateDebounce = 400 * time.Millisecond
)

var (
	clsidTaskbarList = windows.GUID{Data1: 0x56FDF344, Data2: 0xFD6D, Data3: 0x11D0, Data4: [8]byte{0x95, 0x8A, 0x00, 0x60, 0x97, 0xC9, 0xA0, 0x90}}
	iidITaskbarList3 = windows.GUID{Data1: 0xEA1AFB91, Data2: 0x9E28, Data3: 0x4B86, Data4: [8]byte{0x90, 0xE9, 0x9E, 0x9F, 0x8A, 0x5E, 0xEF, 0xAF}}
)

// ITaskbarList3 vtable slots (IUnknown, ITaskbarList, ITaskbarList2, then
// ITaskbarList3 in declaration order).
const (
	vtRelease        = 2
	vtHrInit         = 3
	vtSetOverlayIcon = 18
)

// setAppUserModelID gives the process the app's AppUserModelID. It must run
// before any window is created.
func setAppUserModelID() {
	id, _ := windows.UTF16PtrFromString(appUserModelID)
	if hr, _, _ := procSetAppUserModelID.Call(uintptr(unsafe.Pointer(id))); hr != 0 {
		log.Printf("taskbar: SetCurrentProcessExplicitAppUserModelID: 0x%08x", uint32(hr))
	}
}

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       windows.Handle
	cursor     windows.Handle
	background windows.Handle
	menuName   *uint16
	className  *uint16
	iconSm     windows.Handle
}

type tbMsg struct {
	hwnd    windows.HWND
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
	private uint32
}

type bitmapInfoHeader struct {
	size          uint32
	width         int32
	height        int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       uint32
	clrImportant  uint32
}

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  windows.Handle
	hbmColor windows.Handle
}

// winTaskbar is the native taskbar window.
type winTaskbar struct {
	onActivate func()

	hwnd  windows.HWND
	count atomic.Int64 // latest SetCount; applied on the thread
	done  chan struct{}
	once  sync.Once

	// Only touched on the window's thread.
	list          unsafe.Pointer // *ITaskbarList3, nil if unavailable
	buttonCreated uint32         // "TaskbarButtonCreated" message
	ready         bool           // initial show done (WM_SIZE before it is creation)
	lastActivate  time.Time
}

// The window procedure is one callback for the process (callbacks are never
// freed); it finds the window through this.
var (
	tbCurrent     atomic.Pointer[winTaskbar]
	tbWndProc     = windows.NewCallback(taskbarWndProc)
	tbClassName   = windows.StringToUTF16Ptr("AwesomeReminderTaskbar")
	tbWindowTitle = windows.StringToUTF16Ptr(appName)
)

// newTaskbar creates the taskbar window. onActivate runs on its own
// goroutine when the user clicks the button. On failure it logs and returns
// a taskbar that does nothing.
func newTaskbar(onActivate func()) taskbar {
	t := &winTaskbar{onActivate: onActivate, done: make(chan struct{})}
	if !tbCurrent.CompareAndSwap(nil, t) {
		log.Printf("taskbar: already created")
		return noTaskbar{}
	}
	ready := make(chan error, 1)
	go t.loop(ready)
	if err := <-ready; err != nil {
		log.Printf("taskbar: %v", err)
		<-t.done
		tbCurrent.Store(nil)
		return noTaskbar{}
	}
	return t
}

type noTaskbar struct{}

func (noTaskbar) SetCount(int) {}
func (noTaskbar) Close()       {}

// SetCount shows n on the button (0 clears it).
func (t *winTaskbar) SetCount(n int) {
	t.count.Store(int64(n))
	procPostMessageW.Call(uintptr(t.hwnd), wmTbCount, 0, 0)
}

// Close destroys the window and waits for its thread to end.
func (t *winTaskbar) Close() {
	t.once.Do(func() {
		if r, _, err := procPostMessageW.Call(uintptr(t.hwnd), wmTbQuit, 0, 0); r == 0 {
			log.Printf("taskbar: close: %v", err)
			return
		}
		<-t.done
		tbCurrent.CompareAndSwap(t, nil)
	})
}

func (t *winTaskbar) loop(ready chan<- error) {
	defer close(t.done)
	runtime.LockOSThread() // the window and the COM apartment belong to this thread
	defer runtime.UnlockOSThread()

	if hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded); int32(hr) >= 0 {
		defer procCoUninitialize.Call()
	}
	inst, _, _ := procGetModuleHandleW.Call(0)
	msgName, _ := windows.UTF16PtrFromString("TaskbarButtonCreated")
	r, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(msgName)))
	t.buttonCreated = uint32(r)

	wc := wndClassEx{
		wndProc:   tbWndProc,
		instance:  windows.Handle(inst),
		icon:      appIcon(inst, smCxIcon),
		iconSm:    appIcon(inst, smCxSmIcon),
		className: tbClassName,
	}
	wc.size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		ready <- fmt.Errorf("RegisterClassEx: %w", err)
		return
	}
	defer procUnregisterClassW.Call(uintptr(unsafe.Pointer(tbClassName)), inst)

	// Off-screen, 1x1: if the window is ever restored it is not seen.
	pos := int32(offscreen)
	h, _, err := procCreateWindowExW.Call(
		wsExAppWindow,
		uintptr(unsafe.Pointer(tbClassName)),
		uintptr(unsafe.Pointer(tbWindowTitle)),
		wsOverlapped|wsCaption|wsSysMenu|wsMinimizeBox,
		uintptr(pos), uintptr(pos), 1, 1,
		0, 0, inst, 0)
	if h == 0 {
		ready <- fmt.Errorf("CreateWindowEx: %w", err)
		return
	}
	t.hwnd = windows.HWND(h)
	// The hover preview would be an empty window: show the icon instead and
	// never "peek" at it.
	on := int32(1)
	procDwmSetWindowAttribute.Call(h, dwmwaForceIconicRepresentation, uintptr(unsafe.Pointer(&on)), 4)
	procDwmSetWindowAttribute.Call(h, dwmwaDisallowPeek, uintptr(unsafe.Pointer(&on)), 4)

	t.list = newTaskbarList()
	if t.list != nil {
		defer comCall(t.list, vtRelease)
	}
	procShowWindow.Call(h, swShowMinNoActv)
	t.ready = true
	ready <- nil

	var m tbMsg
	for {
		r, _, err := procTbGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(r) {
		case 0: // WM_QUIT, posted by WM_DESTROY
			return
		case -1:
			log.Printf("taskbar: GetMessage: %v", err)
			procDestroyWindow.Call(h)
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// offscreen is the window's position (also its restored position).
const offscreen = -32000

// appIcon loads the exe's icon at the metric's size (shared: never
// destroyed), falling back to the system application icon.
func appIcon(inst uintptr, metric uintptr) windows.Handle {
	sz, _, _ := procGetSystemMetrics.Call(metric)
	if h, _, _ := procLoadImageW.Call(inst, appIconResID, imageIcon, sz, sz, lrShared); h != 0 {
		return windows.Handle(h)
	}
	h, _, _ := procLoadImageW.Call(0, idiApplication, imageIcon, sz, sz, lrShared)
	return windows.Handle(h)
}

// newTaskbarList creates and initialises ITaskbarList3, or returns nil.
func newTaskbarList() unsafe.Pointer {
	var p unsafe.Pointer
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidTaskbarList)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidITaskbarList3)), uintptr(unsafe.Pointer(&p)))
	if hr != 0 || p == nil {
		log.Printf("taskbar: CoCreateInstance(TaskbarList): 0x%08x", uint32(hr))
		return nil
	}
	if hr := comCall(p, vtHrInit); hr != 0 {
		log.Printf("taskbar: HrInit: 0x%08x", uint32(hr))
		comCall(p, vtRelease)
		return nil
	}
	return p
}

// comCall calls vtable slot i of the COM object p with p as "this".
func comCall(p unsafe.Pointer, i int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(p)
	fn := *(*uintptr)(unsafe.Add(vtbl, i*int(unsafe.Sizeof(uintptr(0)))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(p)}, args...)...)
	return r
}

func taskbarWndProc(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	t := tbCurrent.Load()
	if t == nil || (t.hwnd != 0 && t.hwnd != hwnd) {
		r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
		return r
	}
	switch msg {
	case wmTbCount:
		t.applyCount(hwnd)
		return 0
	case wmTbQuit:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	case wmClose:
		return 0 // only Close ends the window
	case wmQueryOpen:
		// Restoring the minimized window: open the list instead and stay
		// minimized (FALSE), so the window itself is never shown.
		t.activate()
		return 0
	case wmSysCommand:
		switch wParam & 0xFFF0 {
		case scRestore, scMaximize, scMinimize:
			// A click on the button: SC_RESTORE normally, SC_MINIMIZE if
			// this window had become the active one.
			t.activate()
			return 0
		case scClose:
			return 0
		}
	case wmSize:
		if t.ready && (wParam == sizeRestored || wParam == sizeMaximized) {
			// Restored some other way (e.g. Win+Shift+M): minimize again.
			procShowWindow.Call(uintptr(hwnd), swShowMinNoActv)
			t.activate()
			return 0
		}
	}
	if t.buttonCreated != 0 && msg == t.buttonCreated {
		// The button exists now (or again, after Explorer restarted): an
		// overlay set before this is lost.
		t.applyCount(hwnd)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func (t *winTaskbar) activate() {
	now := time.Now()
	if now.Sub(t.lastActivate) < activateDebounce {
		return
	}
	t.lastActivate = now
	if t.onActivate != nil {
		go t.onActivate()
	}
}

// applyCount sets the overlay for the stored count; runs on the thread.
func (t *winTaskbar) applyCount(hwnd windows.HWND) {
	if t.list == nil {
		return
	}
	n := int(t.count.Load())
	if badge.Label(n) == "" {
		comCall(t.list, vtSetOverlayIcon, uintptr(hwnd), 0, 0)
		return
	}
	size := 16
	if procGetDpiForWindow.Find() != nil {
		// GetDpiForWindow needs Windows 10 1607: keep 16 px.
	} else if dpi, _, _ := procGetDpiForWindow.Call(uintptr(hwnd)); dpi >= 144 {
		size = 32 // scaled down by the shell to its 20-24 px overlay
	}
	icon, err := hiconFromImage(badge.Overlay(size, n))
	if err != nil {
		log.Printf("taskbar: overlay icon: %v", err)
		return
	}
	// The taskbar keeps its own copy, so the icon is freed right away.
	defer procDestroyIcon.Call(uintptr(icon))
	desc, _ := windows.UTF16PtrFromString(fmt.Sprintf("%d pending", n))
	hr := comCall(t.list, vtSetOverlayIcon, uintptr(hwnd), uintptr(icon), uintptr(unsafe.Pointer(desc)))
	runtime.KeepAlive(desc) // comCall's uintptr args do not keep it alive
	if hr != 0 {
		log.Printf("taskbar: SetOverlayIcon: 0x%08x", uint32(hr))
	}
}

// hiconFromImage builds a 32-bit icon with alpha from img. The caller
// destroys it.
func hiconFromImage(img image.Image) (windows.Handle, error) {
	if img == nil {
		return 0, errors.New("no image")
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	hdr := bitmapInfoHeader{width: int32(w), height: -int32(h), planes: 1, bitCount: 32} // top-down BGRA
	hdr.size = uint32(unsafe.Sizeof(hdr))
	var bits unsafe.Pointer
	hbm, _, err := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&hdr)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 || bits == nil {
		return 0, fmt.Errorf("CreateDIBSection: %w", err)
	}
	defer procDeleteObject.Call(hbm)
	px := unsafe.Slice((*byte)(bits), w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := nrgbaAt(img, b.Min.X+x, b.Min.Y+y)
			i := (y*w + x) * 4
			px[i], px[i+1], px[i+2], px[i+3] = c.B, c.G, c.R, c.A
		}
	}
	// A 32-bit colour bitmap carries its own alpha; the mask is unused but
	// required. It is all zeros (opaque), not left undefined: rows are
	// WORD-aligned.
	maskBits := make([]byte, (w+15)/16*2*h)
	mask, _, err := procCreateBitmap.Call(uintptr(w), uintptr(h), 1, 1, uintptr(unsafe.Pointer(&maskBits[0])))
	if mask == 0 {
		return 0, fmt.Errorf("CreateBitmap: %w", err)
	}
	defer procDeleteObject.Call(mask)
	ii := iconInfo{fIcon: 1, hbmMask: windows.Handle(mask), hbmColor: windows.Handle(hbm)}
	icon, _, err := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	if icon == 0 {
		return 0, fmt.Errorf("CreateIconIndirect: %w", err)
	}
	return windows.Handle(icon), nil
}

func nrgbaAt(img image.Image, x, y int) color.NRGBA {
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}
