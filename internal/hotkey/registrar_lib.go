//go:build !windows

package hotkey

import (
	"fmt"
	"strings"
	"sync"

	lib "golang.design/x/hotkey"
)

// register uses golang.design/x/hotkey (macOS, Linux/X11). Windows has its
// own blocking message loop (registrar_windows.go).
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
