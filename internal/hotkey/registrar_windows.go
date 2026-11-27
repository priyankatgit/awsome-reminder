//go:build windows

package hotkey

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows the hotkeys are registered on one dedicated OS thread that
// blocks in GetMessage until a WM_HOTKEY (or a request from register) is
// posted to it, so the hotkey costs nothing while the app is idle.
//
// golang.design/x/hotkey, which macOS and Linux still use, polls its message
// queue on Windows with a 10 ms ticker on a locked OS thread: 100 wake-ups a
// second, each a thread hand-off plus PeekMessage/GetAsyncKeyState calls. In
// a CPU profile of the idle app that loop and the scheduler wake-ups it
// caused were the Go process's CPU (1.3 s per 120 s, about 1% of a core);
// with this loop the same profile had no samples (Task 12b).

var (
	user32                = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey    = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey  = user32.NewProc("UnregisterHotKey")
	procGetMessageW       = user32.NewProc("GetMessageW")
	procPeekMessageW      = user32.NewProc("PeekMessageW")
	procPostThreadMessage = user32.NewProc("PostThreadMessageW")
)

const (
	wmHotkey  = 0x0312
	wmApp     = 0x8000              // WM_APP: "run the queued requests"
	modNoRpt  = 0x4000              // MOD_NOREPEAT: holding the combo fires once
	errHkUsed = windows.Errno(1409) // ERROR_HOTKEY_ALREADY_REGISTERED
	// maxHotkeyID is the last ID an application may use (0x0000-0xBFFF).
	maxHotkeyID = 0xBFFF
)

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
	private uint32
}

// hotkeyThread is the OS thread that owns every registration.
type hotkeyThread struct {
	tid uint32

	mu   sync.Mutex
	reqs []func() // run on the thread, in order

	// Only touched on the thread.
	presses map[uintptr]chan struct{}
	nextID  uintptr
}

var (
	hkOnce   sync.Once
	hkThread *hotkeyThread
	hkErr    error
)

// thread starts the hotkey thread on first use.
func thread() (*hotkeyThread, error) {
	hkOnce.Do(func() {
		t := &hotkeyThread{presses: map[uintptr]chan struct{}{}}
		ready := make(chan error, 1)
		go t.loop(ready)
		if hkErr = <-ready; hkErr == nil {
			hkThread = t
		}
	})
	return hkThread, hkErr
}

func (t *hotkeyThread) loop(ready chan<- error) {
	runtime.LockOSThread() // registrations belong to this thread for good
	var m winMsg
	// Any message call creates the thread's queue, which PostThreadMessage
	// needs before it can succeed.
	procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 0)
	t.tid = windows.GetCurrentThreadId()
	ready <- nil
	for {
		r, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(r) {
		case 0: // WM_QUIT: nobody posts it; the thread lives as long as the app
			return
		case -1:
			// Only fails for invalid arguments. Later requests then fail
			// in PostThreadMessage instead of hanging.
			log.Printf("hotkey: GetMessage: %v", err)
			return
		}
		switch m.message {
		case wmHotkey:
			if ch, ok := t.presses[m.wParam]; ok {
				select {
				case ch <- struct{}{}:
				default: // a press is already pending
				}
			}
		case wmApp:
			t.mu.Lock()
			reqs := t.reqs
			t.reqs = nil
			t.mu.Unlock()
			for _, f := range reqs {
				f()
			}
		}
	}
}

// do runs f on the hotkey thread and waits for it.
func (t *hotkeyThread) do(f func()) error {
	done := make(chan struct{})
	t.mu.Lock()
	t.reqs = append(t.reqs, func() { defer close(done); f() })
	t.mu.Unlock()
	if r, _, err := procPostThreadMessage.Call(uintptr(t.tid), wmApp, 0, 0); r == 0 {
		return fmt.Errorf("post to hotkey thread: %w", err)
	}
	<-done
	return nil
}

func register(c Combo, onPress func()) (func(), error) {
	if err := validateFor(goos, c); err != nil {
		return nil, err
	}
	mods := uintptr(modNoRpt)
	for _, m := range c.Mods {
		lm, ok := libMods[m]
		if !ok {
			return nil, fmt.Errorf("%s is not supported on this OS", m)
		}
		mods |= uintptr(lm)
	}
	key, ok := libKeys[c.Key]
	if !ok {
		return nil, fmt.Errorf("unknown key %q", c.Key)
	}
	t, err := thread()
	if err != nil {
		return nil, err
	}
	events := make(chan struct{}, 1)
	var id uintptr
	var regErr error
	if err := t.do(func() {
		t.nextID = t.nextID%maxHotkeyID + 1
		id = t.nextID
		if r, _, e := procRegisterHotKey.Call(0, id, mods, uintptr(key)); r == 0 {
			regErr = e
			return
		}
		t.presses[id] = events
	}); err != nil {
		return nil, err
	}
	if regErr != nil {
		if errors.Is(regErr, errHkUsed) {
			return nil, fmt.Errorf("%s is already used by another app", c)
		}
		return nil, fmt.Errorf("register %s: %w", c, regErr)
	}
	stop := make(chan struct{})
	go pumpPresses((<-chan struct{})(events), stop, onPress)
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop) // never waits on a running handler
			_ = t.do(func() {
				procUnregisterHotKey.Call(0, id)
				delete(t.presses, id)
			})
		})
	}, nil
}
