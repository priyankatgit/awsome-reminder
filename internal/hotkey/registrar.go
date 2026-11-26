package hotkey

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	lib "golang.design/x/hotkey"
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

func register(c Combo, onPress func()) (func(), error) {
	if err := validateFor(goos, c); err != nil {
		return nil, err
	}
	var mods []lib.Modifier
	for _, m := range c.Mods {
		lm, ok := libMods[m]
		if !ok {
			return nil, fmt.Errorf("%s is not supported on this OS", m)
		}
		mods = append(mods, lm)
	}
	key, ok := libKeys[c.Key]
	if !ok {
		return nil, fmt.Errorf("unknown key %q", c.Key)
	}
	hk := lib.New(mods, key)
	var err error
	onMain(func() { err = hk.Register() })
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already") {
			return nil, fmt.Errorf("%s is already used by another app", c)
		}
		return nil, fmt.Errorf("register %s: %w", c, err)
	}
	stop := make(chan struct{})
	go pumpPresses(hk.Keydown(), stop, onPress)
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop) // never waits on a running handler
			onMain(func() { _ = hk.Unregister() })
		})
	}, nil
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
