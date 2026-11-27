package model

import (
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	base := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		a    time.Time
		aBy  string
		b    time.Time
		bBy  string
		want bool
	}{
		{"later wins", base.Add(time.Second), "A", base, "Z", true},
		{"earlier loses", base, "Z", base.Add(time.Second), "A", false},
		{"same ms is a tie, greater device wins", base.Add(400 * time.Microsecond), "B", base.Add(100 * time.Microsecond), "A", true},
		{"same ms is a tie, smaller device loses", base.Add(400 * time.Microsecond), "A", base.Add(100 * time.Microsecond), "B", false},
		{"identical stamps are not newer", base, "A", base, "A", false},
		{"1ms later wins despite smaller device", base.Add(time.Millisecond), "A", base, "B", true},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.aBy, c.b, c.bBy); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
