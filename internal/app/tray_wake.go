package app

import (
	"errors"
	"log"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"awesome-reminder/internal/voice"
)

const wakeTrayLabel = `Listen for "Hey Awesome"`

// wakeClickOutcome decides what a tray click leaves behind: want is the state
// the click asked for, err what SetWakeEnabled returned and current the
// wake word's state before. A wake word that is not installed opens Settings
// (where the download lives) and stays unchecked; any other failure keeps the
// previous state.
func wakeClickOutcome(want bool, err error, current bool) (checked, openSettings bool) {
	switch {
	case err == nil:
		return want, false
	case errors.Is(err, voice.ErrWakeNotInstalled):
		return false, true
	default:
		return current, false
	}
}

// wakeSubs fans one wake status out to several listeners (the frontend event
// and the tray). Calls may come from any goroutine.
type wakeSubs struct {
	mu  sync.Mutex
	fns []func(voice.WakeStatus)
}

func (s *wakeSubs) add(fn func(voice.WakeStatus)) {
	s.mu.Lock()
	s.fns = append(s.fns, fn)
	s.mu.Unlock()
}

func (s *wakeSubs) emit(st voice.WakeStatus) {
	s.mu.Lock()
	fns := append([](func(voice.WakeStatus)){}, s.fns...)
	s.mu.Unlock()
	for _, fn := range fns {
		fn(st)
	}
}

// addWakeTray adds the quick wake word switch to menu and keeps it in step
// with the wake word's status.
func addWakeTray(menu *application.Menu, win *wailsWindows, v *VoiceService) {
	item := menu.AddCheckbox(wakeTrayLabel, v.WakeStatus().Enabled)
	set := func(checked bool) {
		application.InvokeAsync(func() { item.SetChecked(checked) })
	}
	v.wakeSubs.add(func(s voice.WakeStatus) { set(s.Enabled) })
	item.OnClick(func(*application.Context) {
		go func() {
			cur := v.WakeStatus().Enabled
			// The click may already have flipped the box; the wake word is the truth.
			want := !cur
			err := v.SetWakeEnabled(want)
			checked, open := wakeClickOutcome(want, err, cur)
			if err != nil && !open {
				log.Printf("wake: tray toggle: %v", err)
			}
			set(checked)
			if open {
				win.OpenSettings()
			}
		}()
	})
}
