package recur

import (
	"testing"
	"time"
)

var ny, _ = time.LoadLocation("America/New_York")

func TestNextKeepsWallClockAcrossDST(t *testing.T) {
	// 09:00 New York on Sat 31 Oct 2026 (EDT); DST ends Sun 1 Nov.
	due := time.Date(2026, 10, 31, 9, 0, 0, 0, ny)
	got, err := Next("FREQ=DAILY", due, due, ny)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 1, 9, 0, 0, 0, ny); !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("got %v, want %v in UTC", got, want.UTC())
	}
	if _, err := Next("FREQ=DAILY;COUNT=1", due, due, ny); err == nil {
		t.Fatal("ended rule: want an error")
	}
}

func TestCatchUp(t *testing.T) {
	mon := time.Date(2026, 11, 30, 10, 0, 0, 0, ny) // a Monday
	latest, next, err := CatchUp("FREQ=WEEKLY;BYDAY=MO", mon, mon.AddDate(0, 0, 16), ny)
	if err != nil || !latest.Equal(mon.AddDate(0, 0, 14)) || !next.Equal(mon.AddDate(0, 0, 21)) {
		t.Fatalf("latest %v next %v err %v", latest, next, err)
	}
	// Not reached yet: no latest, next is the first occurrence after dtstart.
	latest, next, err = CatchUp("FREQ=WEEKLY;BYDAY=MO", mon, mon, ny)
	if err != nil || !latest.IsZero() || !next.Equal(mon.AddDate(0, 0, 7)) {
		t.Fatalf("latest %v next %v err %v", latest, next, err)
	}
	// An ended rule has no next, and that is not an error.
	if _, next, err := CatchUp("FREQ=DAILY;COUNT=1", mon, mon, ny); err != nil || !next.IsZero() {
		t.Fatalf("ended: next %v err %v", next, err)
	}
	if _, _, err := CatchUp("FREQ=NOPE", mon, mon, ny); err == nil {
		t.Fatal("invalid rule: want an error")
	}
}

func TestLoc(t *testing.T) {
	if l := Loc("America/New_York", time.UTC); l.String() != "America/New_York" {
		t.Fatalf("got %v", l)
	}
	for _, tz := range []string{"", "Not/AZone"} {
		if l := Loc(tz, ny); l != ny {
			t.Fatalf("%q: got %v, want the fallback", tz, l)
		}
	}
}
