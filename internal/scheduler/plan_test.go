package scheduler

import (
	"strconv"
	"testing"
	"time"

	"awesome-reminder/internal/model"
)

var t0 = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

func rem(id string, due time.Time, created time.Time) model.Item {
	d := due
	return model.Item{ID: id, Kind: model.KindReminder, Text: "t-" + id, DueAt: &d,
		Status: model.StatusOpen, CreatedAt: created}
}

func none(string) bool { return false }

var leads = []time.Duration{time.Hour, 10 * time.Minute}

func TestPlanFutureOnly(t *testing.T) {
	due := t0.Add(3 * time.Hour)
	fire, cov, next := Plan([]model.Item{rem("a", due, t0.Add(-24*time.Hour))}, leads, t0, none)
	if len(fire) != 0 || len(cov) != 0 {
		t.Fatalf("expected nothing, got %v %v", fire, cov)
	}
	if !next.Equal(due.Add(-time.Hour)) {
		t.Fatalf("next=%v", next)
	}
}

func TestPlanFiresDueLead(t *testing.T) {
	due := t0.Add(time.Hour)
	fire, _, _ := Plan([]model.Item{rem("a", due, t0.Add(-24*time.Hour))}, leads, t0, none)
	if len(fire) != 1 || fire[0].Lead != time.Hour || fire[0].Missed {
		t.Fatalf("got %+v", fire)
	}
}

func TestPlanSkipsLeadsBeforeCreation(t *testing.T) {
	due := t0.Add(5 * time.Minute)
	fire, _, next := Plan([]model.Item{rem("a", due, t0)}, leads, t0, none)
	if len(fire) != 0 {
		t.Fatalf("got %+v", fire)
	}
	if !next.Equal(due) {
		t.Fatalf("next=%v", next)
	}
}

func TestPlanMissedCollapsesPerItem(t *testing.T) {
	due := t0
	now := due.Add(3 * time.Hour)
	fire, cov, _ := Plan([]model.Item{rem("a", due, due.Add(-24*time.Hour))}, leads, now, none)
	if len(fire) != 1 || fire[0].Lead != 0 || !fire[0].Missed {
		t.Fatalf("got %+v", fire)
	}
	if len(cov) != 3 {
		t.Fatalf("covered=%v", cov)
	}
}

func TestPlanSnoozeOverrides(t *testing.T) {
	due := t0
	sn := t0.Add(30 * time.Minute)
	it := rem("a", due, t0.Add(-24*time.Hour))
	it.SnoozeUntil = &sn
	fire, _, next := Plan([]model.Item{it}, leads, t0.Add(time.Hour), none)
	if len(fire) != 1 || fire[0].Key != "a|snooze|"+strconv.FormatInt(sn.Unix(), 10) || fire[0].Lead != 0 || !fire[0].Snooze {
		t.Fatalf("got %+v", fire)
	}
	_, _, next = Plan([]model.Item{it}, leads, t0, none)
	if !next.Equal(sn) {
		t.Fatalf("next=%v", next)
	}
}

// A snooze that ends before the due time must not swallow the due-time
// alert (C1): due T, the 1h lead shows at T-1h, the user snoozes 10 min.
func TestPlanSnoozeBeforeDueKeepsAtTime(t *testing.T) {
	due := t0
	snoozedAt := due.Add(-time.Hour)
	sn := due.Add(-50 * time.Minute)
	it := rem("a", due, due.Add(-24*time.Hour))
	it.UpdatedAt = snoozedAt
	it.SnoozeUntil = &sn
	notified := map[string]bool{}
	mark := func(cov []string) {
		for _, k := range cov {
			notified[k] = true
		}
	}
	seen := func(k string) bool { return notified[k] }

	fire, cov, next := Plan([]model.Item{it}, leads, snoozedAt, seen)
	if len(fire) != 0 || !next.Equal(sn) {
		t.Fatalf("before snooze end: fire=%+v next=%v", fire, next)
	}
	fire, cov, next = Plan([]model.Item{it}, leads, sn, seen)
	if len(fire) != 1 || !fire[0].Snooze || fire[0].Key != "a|snooze|"+strconv.FormatInt(sn.Unix(), 10) {
		t.Fatalf("at snooze end: %+v", fire)
	}
	mark(cov)
	if !next.Equal(due.Add(-10 * time.Minute)) {
		t.Fatalf("after snooze alert next=%v, want the 10m lead", next)
	}
	fire, cov, next = Plan([]model.Item{it}, leads, due.Add(-10*time.Minute), seen)
	if len(fire) != 1 || fire[0].Lead != 10*time.Minute || fire[0].Snooze {
		t.Fatalf("10m lead: %+v", fire)
	}
	mark(cov)
	if !next.Equal(due) {
		t.Fatalf("next=%v, want due", next)
	}
	fire, _, _ = Plan([]model.Item{it}, leads, due, seen)
	if len(fire) != 1 || fire[0].Lead != 0 || fire[0].Snooze || fire[0].Key != "a|"+strconv.FormatInt(due.Unix(), 10)+"|0" {
		t.Fatalf("at due: %+v", fire)
	}
}

func TestPlanSkipsNotified(t *testing.T) {
	due := t0
	it := rem("a", due, due.Add(-24*time.Hour))
	fire, cov, _ := Plan([]model.Item{it}, leads, due, func(string) bool { return true })
	if len(fire) != 0 || len(cov) != 0 {
		t.Fatalf("got %v %v", fire, cov)
	}
}

func TestPlanTodoNoAlerts(t *testing.T) {
	it := model.Item{ID: "a", Kind: model.KindTodo, Status: model.StatusOpen, CreatedAt: t0}
	fire, cov, next := Plan([]model.Item{it}, leads, t0, none)
	if len(fire) != 0 || len(cov) != 0 || !next.IsZero() {
		t.Fatal("expected nothing")
	}
}

func TestPlanSkipsLeadsBeforeEdit(t *testing.T) {
	due := t0.Add(5 * time.Minute)
	it := rem("a", due, t0.Add(-24*time.Hour))
	it.UpdatedAt = t0
	fire, cov, next := Plan([]model.Item{it}, leads, t0, none)
	if len(fire) != 0 || len(cov) != 0 {
		t.Fatalf("got %+v %v", fire, cov)
	}
	if !next.Equal(due) {
		t.Fatalf("next=%v", next)
	}
}

func TestWallJumped(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		elapsed time.Duration
		want    bool
	}{
		{"normal", 30 * time.Second, false},
		{"small drift", 33 * time.Second, false},
		{"forward jump", 10 * time.Minute, true},
		{"backward jump", -time.Hour, true},
		{"slightly short", 20 * time.Second, true},
	}
	for _, c := range cases {
		if got := wallJumped(base, base.Add(c.elapsed), 30*time.Second); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

// An open recurring item left un-done keeps yesterday's DueAt until its next
// occurrence arrives (AdvanceRecurring), so the plan must look ahead to that
// occurrence for its lead alerts.
func TestPlanRecurringNextOccurrenceLeads(t *testing.T) {
	yesterday := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	tomorrow := yesterday.AddDate(0, 0, 1)
	it := rem("r", yesterday, yesterday.AddDate(0, 0, -1))
	it.RRule, it.TZ, it.UpdatedAt = "FREQ=DAILY", "UTC", yesterday
	notified := map[string]bool{key(it.ID, yesterday, 0): true}
	seen := func(k string) bool { return notified[k] }

	_, _, next := Plan([]model.Item{it}, leads, yesterday.Add(time.Minute), seen)
	if !next.Equal(tomorrow.Add(-time.Hour)) {
		t.Fatalf("next=%v, want the 1h lead of the next occurrence", next)
	}
	for _, l := range leads {
		fire, cov, _ := Plan([]model.Item{it}, leads, tomorrow.Add(-l), seen)
		if len(fire) != 1 || fire[0].Lead != l || !fire[0].DueAt.Equal(tomorrow) || fire[0].Missed ||
			fire[0].Key != key(it.ID, tomorrow, l) {
			t.Fatalf("lead %v: got %+v", l, fire)
		}
		for _, k := range cov {
			notified[k] = true
		}
	}
}

// The look-ahead is only for recurring items, and never repeats a lead that
// the advanced item already fired (same key).
func TestPlanRecurringLookAheadIdempotent(t *testing.T) {
	today := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	it := rem("r", today, today.AddDate(0, 0, -2))
	it.RRule, it.TZ, it.UpdatedAt = "FREQ=DAILY", "UTC", today // just advanced
	notified := map[string]bool{key(it.ID, today, time.Hour): true, key(it.ID, today, 10*time.Minute): true}
	fire, cov, _ := Plan([]model.Item{it}, leads, today, func(k string) bool { return notified[k] })
	if len(fire) != 1 || fire[0].Lead != 0 || len(cov) != 1 {
		t.Fatalf("got %+v covered %v", fire, cov)
	}

	one := rem("o", today, today.AddDate(0, 0, -2)) // one-off: nothing after its due time
	if _, _, next := Plan([]model.Item{one}, leads, today.Add(time.Minute), func(string) bool { return true }); !next.IsZero() {
		t.Fatalf("one-off next=%v", next)
	}
}

func key(id string, due time.Time, lead time.Duration) string {
	return id + "|" + strconv.FormatInt(due.Unix(), 10) + "|" + strconv.FormatInt(int64(lead/time.Second), 10)
}
