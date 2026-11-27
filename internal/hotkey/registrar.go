package hotkey

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"
)

var (
	runnerMu   sync.Mutex
	mainRunner func(func())
)

// SetMainThreadRunner sets the function used to run registration on the
// application's main thread. It is required on macOS and ignored elsewhere.
func SetMainThreadRunner(run func(func())) {
	runnerMu.Lock()
	defer runnerMu.Unlock()
	mainRunner = run
}

// onMain runs f on the main thread (macOS) and waits for it to finish.
func onMain(f func()) {
	runnerMu.Lock()
	run := mainRunner
	runnerMu.Unlock()
	if goos != "darwin" || run == nil {
		f()
		return
	}
	done := make(chan struct{})
	run(func() { defer close(done); f() })
	<-done
}

const waylandMsg = `global hotkeys aren't supported on Wayland; bind "awesome-reminder --capture" in your system keyboard settings`

// Platform returns the real Registrar for the current OS.
func Platform() Registrar {
	if goos == "linux" && os.Getenv("XDG_SESSION_TYPE") == "wayland" {
		return func(Combo, func()) (func(), error) { return nil, errors.New(waylandMsg) }
	}
	return register
}

// pumpPresses dispatches each event from events to onPress on its own
// goroutine until stop is closed or events is closed. If a previous handler is
// still running, the new press is dropped, so rapid repeats coalesce. It never
// waits for a handler, so a slow onPress cannot block key events or unregister.
func pumpPresses[T any](events <-chan T, stop <-chan struct{}, onPress func()) {
	var busy atomic.Bool
	for {
		select {
		case <-stop:
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			if !busy.CompareAndSwap(false, true) {
				continue
			}
			go func() {
				defer busy.Store(false)
				onPress()
			}()
		}
	}
}
