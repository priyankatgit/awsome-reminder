package scheduler

import (
	"strconv"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // Asia/Kolkata on systems without zoneinfo

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

var rules = Rules{Leads: leads}

func TestPlanFutureOnly(t *testing.T) {
	due := t0.Add(3 * time.Hour)
	fire, cov, next := Plan([]model.Item{rem("a", due, t0.Add(-24*time.Hour))}, rules, t0, none)
	if len(fire) != 0 || len(cov) != 0 {
		t.Fatalf("expected nothing, got %v %v", fire, cov)
	}
	if !next.Equal(due.Add(-time.Hour)) {
		t.Fatalf("next=%v", next)
	}
}

func TestPlanFiresDueLead(t *testing.T) {
	due := t0.Add(time.Hour)
	fire, _, _ := Plan([]model.Item{rem("a", due, t0.Add(-24*time.Hour))}, rules, t0, none)
	if len(fire) != 1 || fire[0].Lead != time.Hour || fire[0].Missed {
		t.Fatalf("got %+v", fire)
	}
}

func TestPlanSkipsLeadsBeforeCreation(t *testing.T) {
	due := t0.Add(5 * time.Minute)
	fire, _, next := Plan([]model.Item{rem("a", due, t0)}, rules, t0, none)
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
	fire, cov, _ := Plan([]model.Item{rem("a", due, due.Add(-24*time.Hour))}, rules, now, none)
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
	fire, _, next := Plan([]model.Item{it}, rules, t0.Add(time.Hour), none)
	if len(fire) != 1 || fire[0].Key != "a|snooze|"+strconv.FormatInt(sn.Unix(), 10) || fire[0].Lead != 0 || !fire[0].Snooze {
		t.Fatalf("got %+v", fire)
	}
	_, _, next = Plan([]model.Item{it}, rules, t0, none)
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

	fire, cov, next := Plan([]model.Item{it}, rules, snoozedAt, seen)
	if len(fire) != 0 || !next.Equal(sn) {
		t.Fatalf("before snooze end: fire=%+v next=%v", fire, next)
	}
	fire, cov, next = Plan([]model.Item{it}, rules, sn, seen)
	if len(fire) != 1 || !fire[0].Snooze || fire[0].Key != "a|snooze|"+strconv.FormatInt(sn.Unix(), 10) {
		t.Fatalf("at snooze end: %+v", fire)
	}
	mark(cov)
	if !next.Equal(due.Add(-10 * time.Minute)) {
		t.Fatalf("after snooze alert next=%v, want the 10m lead", next)
	}
	fire, cov, next = Plan([]model.Item{it}, rules, due.Add(-10*time.Minute), seen)
	if len(fire) != 1 || fire[0].Lead != 10*time.Minute || fire[0].Snooze {
		t.Fatalf("10m lead: %+v", fire)
	}
	mark(cov)
	if !next.Equal(due) {
		t.Fatalf("next=%v, want due", next)
	}
	fire, _, _ = Plan([]model.Item{it}, rules, due, seen)
	if len(fire) != 1 || fire[0].Lead != 0 || fire[0].Snooze || fire[0].Key != "a|"+strconv.FormatInt(due.Unix(), 10)+"|0" {
		t.Fatalf("at due: %+v", fire)
	}
}

func TestPlanSkipsNotified(t *testing.T) {
	due := t0
	it := rem("a", due, due.Add(-24*time.Hour))
	fire, cov, _ := Plan([]model.Item{it}, rules, due, func(string) bool { return true })
	if len(fire) != 0 || len(cov) != 0 {
		t.Fatalf("got %v %v", fire, cov)
	}
}

func TestPlanTodoNoAlerts(t *testing.T) {
	it := model.Item{ID: "a", Kind: model.KindTodo, Status: model.StatusOpen, CreatedAt: t0}
	fire, cov, next := Plan([]model.Item{it}, rules, t0, none)
	if len(fire) != 0 || len(cov) != 0 || !next.IsZero() {
		t.Fatal("expected nothing")
	}
}

func TestPlanSkipsLeadsBeforeEdit(t *testing.T) {
	due := t0.Add(5 * time.Minute)
	it := rem("a", due, t0.Add(-24*time.Hour))
	it.UpdatedAt = t0
	fire, cov, next := Plan([]model.Item{it}, rules, t0, none)
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

	_, _, next := Plan([]model.Item{it}, rules, yesterday.Add(time.Minute), seen)
	if !next.Equal(tomorrow.Add(-time.Hour)) {
		t.Fatalf("next=%v, want the 1h lead of the next occurrence", next)
	}
	for _, l := range leads {
		fire, cov, _ := Plan([]model.Item{it}, rules, tomorrow.Add(-l), seen)
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
	fire, cov, _ := Plan([]model.Item{it}, rules, today, func(k string) bool { return notified[k] })
	if len(fire) != 1 || fire[0].Lead != 0 || len(cov) != 1 {
		t.Fatalf("got %+v covered %v", fire, cov)
	}

	one := rem("o", today, today.AddDate(0, 0, -2)) // one-off: nothing after its due time
	if _, _, next := Plan([]model.Item{one}, rules, today.Add(time.Minute), func(string) bool { return true }); !next.IsZero() {
		t.Fatalf("one-off next=%v", next)
	}
}

func key(id string, due time.Time, lead time.Duration) string {
	return id + "|" + strconv.FormatInt(due.Unix(), 10) + "|" + strconv.FormatInt(int64(lead/time.Second), 10)
}

func kolkata(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func weekdays(d time.Weekday) bool { return d != time.Saturday && d != time.Sunday }

func replyRules(loc *time.Location, leads []time.Duration) Rules {
	return Rules{Leads: leads, WorkDays: weekdays, DefaultTime: "09:00", EndOfDay: "17:30", Loc: loc}
}

// 2026-12-02 is a Wednesday.
func wed(loc *time.Location, h, m int) time.Time { return time.Date(2026, 12, 2, h, m, 0, 0, loc) }

func reply(id string, created time.Time) model.Item {
	return model.Item{ID: id, Kind: model.KindTodo, Text: "t-" + id, Reply: true, Status: model.StatusOpen,
		CreatedAt: created.UTC(), UpdatedAt: created.UTC()}
}

func nudgeKey(id string, at time.Time) string {
	return id + "|nudge|" + strconv.FormatInt(at.Unix(), 10)
}

// walk steps the plan from `from` through each successive alert of it,
// marking what fired as notified, and returns the fire times and alerts.
func walk(t *testing.T, it model.Item, r Rules, from time.Time, n int) ([]time.Time, []Alert) {
	t.Helper()
	notified := map[string]bool{}
	seen := func(k string) bool { return notified[k] }
	now := from
	var times []time.Time
	var alerts []Alert
	for range n {
		_, _, next := Plan([]model.Item{it}, r, now, seen)
		if next.IsZero() {
			t.Fatalf("no next alert after %v (got %v)", now, times)
		}
		fire, cov, _ := Plan([]model.Item{it}, r, next, seen)
		if len(fire) != 1 || fire[0].Missed {
			t.Fatalf("at %v: fire=%+v", next, fire)
		}
		for _, k := range cov {
			notified[k] = true
		}
		times = append(times, next)
		alerts = append(alerts, fire[0])
		now = next
	}
	return times, alerts
}

func TestNudgeSchedule(t *testing.T) {
	loc := kolkata(t)
	day := func(d, h, m int) time.Time { return time.Date(2026, 12, d, h, m, 0, 0, loc) }
	// Wed 2, Thu 3, Fri 4, Sat 5, Mon 7 December 2026.
	rows := []struct {
		name    string
		created time.Time
		want    []time.Time
	}{
		{"wed morning", day(2, 10, 0), []time.Time{day(2, 12, 0), day(3, 9, 0), day(4, 9, 0)}},
		{"fri afternoon", day(4, 16, 0), []time.Time{day(4, 18, 0), day(7, 9, 0)}},
		{"saturday", day(5, 11, 0), []time.Time{day(5, 13, 0), day(7, 9, 0)}},
		{"wed late night", day(2, 23, 30), []time.Time{day(3, 1, 30), day(4, 9, 0)}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			it := reply("r", row.created)
			times, alerts := walk(t, it, replyRules(loc, nil), row.created, len(row.want))
			for i, want := range row.want {
				if !times[i].Equal(want) {
					t.Fatalf("nudge %d at %v, want %v", i, times[i].In(loc), want)
				}
				a := alerts[i]
				if a.Kind != "nudge" || a.Key != nudgeKey("r", want) || !a.WaitingSince.Equal(it.CreatedAt) || a.ItemID != "r" {
					t.Fatalf("nudge %d: %+v", i, a)
				}
			}
		})
	}
}

func TestNudgeAfterDue(t *testing.T) {
	loc := kolkata(t)
	due := wed(loc, 17, 0)
	it := reply("r", due.AddDate(0, 0, -2))
	it.Kind = model.KindReminder
	d := due.UTC()
	it.DueAt = &d
	want := []time.Time{wed(loc, 16, 0), due, time.Date(2026, 12, 3, 9, 0, 0, 0, loc)}
	kinds := []string{"", "", "nudge"}
	times, alerts := walk(t, it, replyRules(loc, []time.Duration{time.Hour}), it.CreatedAt, len(want))
	for i := range want {
		if !times[i].Equal(want[i]) || alerts[i].Kind != kinds[i] {
			t.Fatalf("alert %d at %v kind %q, want %v kind %q", i, times[i].In(loc), alerts[i].Kind, want[i], kinds[i])
		}
	}
}

func TestNudgesCollapseWhenMissed(t *testing.T) {
	loc := kolkata(t)
	created := time.Date(2026, 11, 30, 10, 0, 0, 0, loc) // Monday
	now := time.Date(2026, 12, 3, 15, 0, 0, 0, loc)      // Thursday
	it := reply("r", created)
	fire, _, next := Plan([]model.Item{it}, replyRules(loc, nil), now, none)
	thu9 := time.Date(2026, 12, 3, 9, 0, 0, 0, loc)
	if len(fire) != 1 || fire[0].Key != nudgeKey("r", thu9) || !fire[0].Missed || fire[0].Kind != "nudge" {
		t.Fatalf("got %+v", fire)
	}
	if fri9 := time.Date(2026, 12, 4, 9, 0, 0, 0, loc); !next.Equal(fri9) {
		t.Fatalf("next=%v, want %v", next, fri9)
	}
}

func TestNoNudgeWhenClosed(t *testing.T) {
	loc := kolkata(t)
	created := wed(loc, 10, 0)
	done := reply("d", created)
	done.Status, done.ReplyOutcome = model.StatusDone, model.ReplyReplied
	dropped := reply("x", created)
	dropped.Status, dropped.ReplyOutcome = model.StatusDone, model.ReplyDropped
	deleted := reply("y", created)
	deleted.Status, deleted.ReplyOutcome = model.StatusDeleted, model.ReplyDropped
	for _, now := range []time.Time{created, created.AddDate(0, 0, 3)} {
		fire, cov, next := Plan([]model.Item{done, dropped, deleted}, replyRules(loc, nil), now, none)
		if len(fire) != 0 || len(cov) != 0 || !next.IsZero() {
			t.Fatalf("at %v: fire=%+v cov=%v next=%v", now, fire, cov, next)
		}
	}
}

// An edit must not suppress later nudges, and a snooze replaces the nudges
// at or before its end.
func TestNudgeEditAndSnooze(t *testing.T) {
	loc := kolkata(t)
	created := wed(loc, 10, 0)
	it := reply("r", created)
	it.UpdatedAt = wed(loc, 13, 0).UTC() // after the first nudge
	thu9 := time.Date(2026, 12, 3, 9, 0, 0, 0, loc)
	if _, _, next := Plan([]model.Item{it}, replyRules(loc, nil), wed(loc, 13, 0), func(k string) bool { return k == nudgeKey("r", wed(loc, 12, 0)) }); !next.Equal(thu9) {
		t.Fatalf("after edit next=%v, want %v", next, thu9)
	}

	sn := time.Date(2026, 12, 3, 9, 30, 0, 0, loc).UTC()
	it.SnoozeUntil = &sn
	fire, _, next := Plan([]model.Item{it}, replyRules(loc, nil), sn, func(k string) bool { return k == nudgeKey("r", wed(loc, 12, 0)) })
	if len(fire) != 1 || !fire[0].Snooze || !fire[0].DueAt.Equal(sn) {
		t.Fatalf("at snooze end: %+v", fire)
	}
	if fri9 := time.Date(2026, 12, 4, 9, 0, 0, 0, loc); !next.Equal(fri9) {
		t.Fatalf("after snooze next=%v, want %v", next, fri9)
	}
}

// Nudge times are wall-clock times in the rules' zone across a DST change.
func TestNudgeDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 10, 30, 10, 0, 0, 0, ny) // Friday; DST ends Sunday 1 Nov
	times, _ := walk(t, reply("r", created), replyRules(ny, nil), created, 3)
	want := []time.Time{created.Add(2 * time.Hour), time.Date(2026, 11, 2, 9, 0, 0, 0, ny), time.Date(2026, 11, 3, 9, 0, 0, 0, ny)}
	for i := range want {
		if !times[i].Equal(want[i]) {
			t.Fatalf("nudge %d at %v, want %v", i, times[i].In(ny), want[i])
		}
	}
}

func sweepRules(loc *time.Location) Rules {
	r := replyRules(loc, nil)
	r.Sweep = true
	return r
}

// sweeps returns the sweep alerts of fire.
func sweeps(fire []Alert) []Alert {
	var out []Alert
	for _, a := range fire {
		if a.Kind == KindSweep {
			out = append(out, a)
		}
	}
	return out
}

func TestSweepAtEndOfDay(t *testing.T) {
	loc := kolkata(t)
	priya := reply("p", wed(loc, 10, 0))
	priya.OwedTo = "Priya"
	anon := reply("s", wed(loc, 11, 0))
	priyaAgain := reply("p2", wed(loc, 12, 0))
	priyaAgain.OwedTo = " priya "
	items := []model.Item{anon, priyaAgain, priya}

	allNudges := func(k string) bool { return strings.Contains(k, "|nudge|") }
	if _, _, next := Plan(items, sweepRules(loc), wed(loc, 17, 30), allNudges); !next.Equal(time.Date(2026, 12, 3, 17, 30, 0, 0, loc)) {
		t.Fatalf("firing: next=%v, want Thursday's sweep", next.In(loc))
	}
	fire, cov, _ := Plan(items, sweepRules(loc), wed(loc, 17, 30), none)
	sw := sweeps(fire)
	if len(sw) != 1 {
		t.Fatalf("sweeps %+v", sw)
	}
	a := sw[0]
	if a.Key != "sweep|2026-12-02" || a.ItemID != "" || a.Missed || !a.DueAt.Equal(wed(loc, 17, 30)) {
		t.Fatalf("sweep %+v", a)
	}
	if len(a.People) != 2 || a.People[0] != "Priya" || a.People[1] != "Someone" {
		t.Fatalf("people %q, want [Priya Someone]", a.People)
	}
	found := false
	for _, k := range cov {
		found = found || k == a.Key
	}
	if !found {
		t.Fatalf("sweep key not covered: %v", cov)
	}

	fire, _, next := Plan(items, sweepRules(loc), wed(loc, 17, 29), none)
	if sw := sweeps(fire); len(sw) != 0 {
		t.Fatalf("17:29: %+v", sw)
	}
	if !next.Equal(wed(loc, 17, 30)) {
		t.Fatalf("17:29: next=%v, want the sweep at 17:30", next.In(loc))
	}

	sat := time.Date(2026, 12, 5, 17, 30, 0, 0, loc)
	if fire, _, _ := Plan(items, sweepRules(loc), sat, none); len(sweeps(fire)) != 0 {
		t.Fatalf("saturday: %+v", sweeps(fire))
	}
	if fire, _, _ := Plan(items, replyRules(loc, nil), wed(loc, 17, 30), none); len(sweeps(fire)) != 0 {
		t.Fatalf("sweep off: %+v", sweeps(fire))
	}

	// No open replies: no sweep, and no wake for one.
	done := priya
	done.Status, done.ReplyOutcome = model.StatusDone, model.ReplyReplied
	other := rem("r", wed(loc, 20, 0), wed(loc, 9, 0))
	fire, cov, next = Plan([]model.Item{done, other}, sweepRules(loc), wed(loc, 17, 30), none)
	if len(fire) != 0 || len(cov) != 0 || !next.Equal(wed(loc, 20, 0)) {
		t.Fatalf("no replies: fire=%+v cov=%v next=%v", fire, cov, next.In(loc))
	}
	if _, _, next := Plan([]model.Item{done}, sweepRules(loc), wed(loc, 17, 0), none); !next.IsZero() {
		t.Fatalf("no replies: next=%v, want none", next.In(loc))
	}
}

// Once today's sweep is done, the next wake for it is the next working day's
// end of day.
func TestSweepNextWorkingDay(t *testing.T) {
	loc := kolkata(t)
	fri := func(h, m int) time.Time { return time.Date(2026, 12, 4, h, m, 0, 0, loc) }
	it := reply("r", fri(10, 0))
	seen := func(k string) bool { return strings.Contains(k, "|nudge|") || k == "sweep|2026-12-04" }
	fire, cov, next := Plan([]model.Item{it}, sweepRules(loc), fri(18, 0), seen)
	if len(fire) != 0 || len(cov) != 0 {
		t.Fatalf("fire=%+v cov=%v", fire, cov)
	}
	if mon := time.Date(2026, 12, 7, 17, 30, 0, 0, loc); !next.Equal(mon) {
		t.Fatalf("next=%v, want %v", next.In(loc), mon)
	}
}

// Review Focus 4: a sweep missed while asleep fires late the same day only.
func TestSweepSameDayOnly(t *testing.T) {
	loc := kolkata(t)
	it := reply("r", wed(loc, 10, 0))
	it.OwedTo = "Priya"
	items := []model.Item{it}
	notified := map[string]bool{}
	seen := func(k string) bool { return notified[k] }
	_, cov, next := Plan(items, sweepRules(loc), wed(loc, 17, 0), seen)
	for _, k := range cov {
		notified[k] = true
	}
	if !next.Equal(wed(loc, 17, 30)) {
		t.Fatalf("at 17:00 next=%v", next.In(loc))
	}

	thu8 := time.Date(2026, 12, 3, 8, 0, 0, 0, loc)
	fire, cov, next := Plan(items, sweepRules(loc), thu8, seen)
	if sw := sweeps(fire); len(sw) != 0 {
		t.Fatalf("thursday morning: %+v", sw)
	}
	for _, k := range cov {
		if strings.HasPrefix(k, "sweep|") {
			t.Fatalf("thursday morning covered %q", k)
		}
	}
	if thu9 := time.Date(2026, 12, 3, 9, 0, 0, 0, loc); !next.Equal(thu9) {
		t.Fatalf("thursday morning next=%v", next.In(loc))
	}

	fire, _, _ = Plan(items, sweepRules(loc), wed(loc, 22, 0), seen)
	sw := sweeps(fire)
	if len(sw) != 1 || !sw[0].Missed || sw[0].Key != "sweep|2026-12-02" {
		t.Fatalf("wed 22:00: %+v", sw)
	}
}
