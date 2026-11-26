package hotkey

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

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
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			case _, ok := <-hk.Keydown():
				if !ok {
					return
				}
				onPress()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-stopped
			onMain(func() { _ = hk.Unregister() })
		})
	}, nil
}
