package scheduler

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"awesome-reminder/internal/model"
)

// 2026-11-25 is a Wednesday.
func nov(day, h, m int) time.Time { return time.Date(2026, 11, day, h, m, 0, 0, time.UTC) }

var upNow = nov(25, 10, 0)

func upRules(sweep bool) Rules {
	return Rules{DefaultTime: "09:00", EndOfDay: "17:30", Sweep: sweep, Loc: time.UTC}
}

func TestUpcoming(t *testing.T) {
	created := nov(24, 8, 0)
	due17 := rem("a", nov(25, 17, 0), created)

	daily := rem("d", nov(25, 9, 0), created)
	daily.RRule, daily.TZ = "FREQ=DAILY", "UTC"

	snoozed := rem("s", nov(25, 17, 0), created)
	snoozed.SnoozeUntil = ptrT(nov(25, 18, 0))

	done := rem("x", nov(25, 17, 0), created)
	done.Status = model.StatusDone

	todo := model.Item{ID: "t", Kind: model.KindTodo, Status: model.StatusOpen, CreatedAt: created}

	r1 := reply("r", upNow)
	r1.OwedTo = "Priya"

	snoozedReply := reply("q", upNow)
	snoozedReply.SnoozeUntil = ptrT(nov(26, 11, 0))

	lateReply := reply("l", nov(25, 17, 45))

	dailyLater := rem("e", nov(25, 17, 0), created) // due inside the window
	dailyLater.RRule, dailyLater.TZ = "FREQ=DAILY", "UTC"

	snoozedDueReply := reply("p", upNow)
	snoozedDueReply.DueAt = ptrT(nov(25, 15, 0))
	snoozedDueReply.SnoozeUntil = ptrT(nov(25, 16, 0))

	atFrom := rem("f", upNow, created)
	atTo := rem("g", nov(26, 10, 0), created)
	afterTo := rem("h", nov(26, 10, 0).Add(time.Second), created)

	sat10 := nov(28, 10, 0)
	rem1 := func(id string, at time.Time) Occurrence {
		return Occurrence{Kind: OccReminder, ItemID: id, At: at, Due: at}
	}
	nudge := func(id string, at time.Time) Occurrence {
		return Occurrence{Kind: OccNudge, ItemID: id, At: at}
	}
	sweepAt := func(at time.Time, people ...string) Occurrence {
		return Occurrence{Kind: OccSweep, At: at, People: people}
	}

	cases := []struct {
		name  string
		items []model.Item
		rules Rules
		to    time.Time
		want  []Occurrence
	}{
		{"reminder due today", []model.Item{due17}, upRules(false), upNow.Add(71 * time.Hour),
			[]Occurrence{rem1("a", nov(25, 17, 0))}},
		{"daily recurring", []model.Item{daily}, upRules(false), upNow.Add(71 * time.Hour),
			[]Occurrence{rem1("d", nov(26, 9, 0)), rem1("d", nov(27, 9, 0)), rem1("d", nov(28, 9, 0))}},
		{"daily recurring due in the window lists it once", []model.Item{dailyLater}, upRules(false), upNow.Add(71 * time.Hour),
			[]Occurrence{rem1("e", nov(25, 17, 0)), rem1("e", nov(26, 17, 0)), rem1("e", nov(27, 17, 0))}},
		{"snooze replaces due time", []model.Item{snoozed}, upRules(false), upNow.Add(71 * time.Hour),
			[]Occurrence{{Kind: OccReminder, ItemID: "s", At: nov(25, 18, 0), Due: nov(25, 17, 0), Snooze: true}}},
		{"snoozed reply with a due time is a snooze nudge", []model.Item{snoozedDueReply}, upRules(false), nov(25, 23, 0),
			[]Occurrence{{Kind: OccNudge, ItemID: "p", At: nov(25, 16, 0), Due: nov(25, 15, 0), Snooze: true}}},
		{"done and undated todo", []model.Item{done, todo}, upRules(false), upNow.Add(71 * time.Hour), nil},
		{"reply nudges", []model.Item{r1}, upRules(false), sat10,
			[]Occurrence{nudge("r", nov(25, 12, 0)), nudge("r", nov(26, 9, 0)), nudge("r", nov(27, 9, 0))}},
		{"sweeps", []model.Item{r1}, upRules(true), sat10,
			[]Occurrence{
				nudge("r", nov(25, 12, 0)), sweepAt(nov(25, 17, 30), "Priya"),
				nudge("r", nov(26, 9, 0)), sweepAt(nov(26, 17, 30), "Priya"),
				nudge("r", nov(27, 9, 0)), sweepAt(nov(27, 17, 30), "Priya"),
			}},
		{"snoozed reply comes back as a nudge", []model.Item{snoozedReply}, upRules(false), sat10,
			[]Occurrence{{Kind: OccNudge, ItemID: "q", At: nov(26, 11, 0), Snooze: true}, nudge("q", nov(27, 9, 0))}},
		{"no sweep for replies saved after it", []model.Item{lateReply}, upRules(true), nov(26, 12, 0),
			[]Occurrence{nudge("l", nov(25, 19, 45)), nudge("l", nov(26, 9, 0))}},
		{"window bounds", []model.Item{afterTo, atTo, atFrom}, upRules(false), nov(26, 10, 0),
			[]Occurrence{rem1("f", upNow), rem1("g", nov(26, 10, 0))}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Upcoming(c.items, c.rules, upNow, c.to)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got\n%v\nwant\n%v", got, c.want)
			}
		})
	}
}

func ptrT(t time.Time) *time.Time { return &t }

// TestUpcomingMatchesPlanCards checks that each card Plan shows maps back to
// an occurrence: an at-time or missed card's DueAt, a snooze card's snooze
// time, and a nudge's or sweep's DueAt equal the occurrence's At.
func TestUpcomingMatchesPlanCards(t *testing.T) {
	created := nov(24, 8, 0)
	noLeads := []time.Duration{}
	r := upRules(true)

	due := rem("a", nov(25, 17, 0), created)
	due.LeadTimes = noLeads
	snoozed := rem("s", nov(25, 17, 0), created)
	snoozed.LeadTimes = noLeads
	snoozed.SnoozeUntil = ptrT(nov(25, 18, 0))
	r1 := reply("r", upNow)
	r1.OwedTo = "Priya"

	occs := Upcoming([]model.Item{due, snoozed, r1}, r, upNow, nov(27, 23, 0))
	plan := func(it model.Item, at time.Time) []Alert {
		fire, _, _ := Plan([]model.Item{it}, r, at, none)
		return fire
	}
	var sawReminder, sawSnooze, sawNudge, sawSweep bool
	for _, o := range occs {
		switch {
		case o.Kind == OccSweep:
			sawSweep = true
			var sw *Alert
			for _, a := range plan(r1, o.At) {
				if a.Kind == KindSweep {
					sw = &a
				}
			}
			if sw == nil || !sw.DueAt.Equal(o.At) || !reflect.DeepEqual(sw.People, o.People) {
				t.Fatalf("sweep %v: plan %+v", o, sw)
			}
		case o.ItemID == "r":
			sawNudge = true
			fire := plan(r1, o.At)
			if len(fire) == 0 || fire[0].Kind != KindNudge || !fire[0].DueAt.Equal(o.At) {
				t.Fatalf("nudge %v: plan %+v", o, fire)
			}
		case o.ItemID == "s":
			sawSnooze = true
			fire := plan(snoozed, o.At)
			want := fmt.Sprintf("s|snooze|%d", o.At.Unix())
			if len(fire) != 1 || !fire[0].Snooze || fire[0].Key != want || !snoozed.SnoozeUntil.Equal(o.At) ||
				!fire[0].DueAt.Equal(o.Due) {
				t.Fatalf("snooze %v: plan %+v", o, fire)
			}
		case o.ItemID == "a":
			sawReminder = true
			for _, late := range []time.Duration{0, 5 * time.Minute} { // at-time, then missed
				fire := plan(due, o.At.Add(late))
				if len(fire) != 1 || fire[0].Lead != 0 || fire[0].Missed != (late > 0) || !fire[0].DueAt.Equal(o.At) {
					t.Fatalf("reminder %v late %v: plan %+v", o, late, fire)
				}
			}
		}
	}
	if !sawReminder || !sawSnooze || !sawNudge || !sawSweep {
		t.Fatalf("missing kinds in %v", occs)
	}
}
