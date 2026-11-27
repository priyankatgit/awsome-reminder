package app

import (
	"errors"
	"fmt"
	"testing"

	"awesome-reminder/internal/voice"
)

func TestWakeClickOutcome(t *testing.T) {
	cases := []struct {
		name         string
		want         bool
		err          error
		cur          bool
		checked, set bool
	}{
		{"enable", true, nil, false, true, false},
		{"disable", false, nil, true, false, false},
		{"not installed", true, voice.ErrWakeNotInstalled, false, false, true},
		{"wrapped not installed", true, fmt.Errorf("x: %w", voice.ErrWakeNotInstalled), false, false, true},
		{"other error keeps state", true, errors.New("boom"), false, false, false},
		{"other error keeps on", false, errors.New("boom"), true, true, false},
	}
	for _, c := range cases {
		ch, open := wakeClickOutcome(c.want, c.err, c.cur)
		if ch != c.checked || open != c.set {
			t.Errorf("%s: got (%v,%v), want (%v,%v)", c.name, ch, open, c.checked, c.set)
		}
	}
}

func TestWakeSubsFanOut(t *testing.T) {
	var s wakeSubs
	n := 0
	s.add(func(voice.WakeStatus) { n++ })
	s.add(func(st voice.WakeStatus) {
		if st.Enabled {
			n += 10
		}
	})
	s.emit(voice.WakeStatus{Enabled: true})
	if n != 11 {
		t.Fatalf("n = %d", n)
	}
}
