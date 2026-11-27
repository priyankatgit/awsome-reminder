package core

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/settings"
	"awesome-reminder/internal/store"
)

var ny, _ = time.LoadLocation("America/New_York")

type env struct {
	c   *Core
	st  *store.Store
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{st: st, now: time.Date(2026, 11, 25, 10, 0, 0, 0, ny)}
	e.c = New(st, func() time.Time { return e.now }, ny)
	return e
}

func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

func mustCapture(t *testing.T, e *env, in string) model.Item {
	t.Helper()
	it, err := e.c.Capture(in)
	if err != nil {
		t.Fatalf("capture %q: %v", in, err)
	}
	return it
}

func TestCaptureTodoGetsInbox(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "refactor auth")
	if it.Kind != model.KindTodo || !reflect.DeepEqual(it.Tags, []string{"inbox"}) || it.DueAt != nil {
		t.Fatalf("got %+v", it)
	}
	got, err := e.st.Get(it.ID)
	if err != nil || got.Text != "refactor auth" || got.UpdatedBy != e.st.DeviceID() {
		t.Fatalf("stored %+v %v", got, err)
	}
}

func TestCaptureReminder(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "tomorrow 5pm deploy #ops")
	if it.Kind != model.KindReminder || it.Text != "deploy" || !reflect.DeepEqual(it.Tags, []string{"ops"}) {
		t.Fatalf("got %+v", it)
	}
	if it.DueAt == nil || !it.DueAt.Equal(utc(2026, 11, 26, 22, 0)) || it.TZ != "America/New_York" {
		t.Fatalf("due/tz %v %s", it.DueAt, it.TZ)
	}
	if it.Status != model.StatusOpen || !it.CreatedAt.Equal(e.now) || it.ID == "" {
		t.Fatalf("got %+v", it)
	}
}

func TestCaptureEmpty(t *testing.T) {
	e := newEnv(t)
	for _, in := range []string{"  ", "#ops"} {
		if _, err := e.c.Capture(in); !errors.Is(err, ErrEmpty) {
			t.Errorf("%q: err %v", in, err)
		}
	}
}

func TestDoneRecurringAdvances(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "every monday 10am standup")
	if !it.DueAt.Equal(utc(2026, 11, 30, 15, 0)) {
		t.Fatalf("first due %v", it.DueAt)
	}
	e.now = e.now.Add(5 * 24 * time.Hour) // Mon 11-30 10:00
	got, err := e.c.Done(it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusOpen || !got.DueAt.Equal(utc(2026, 12, 7, 15, 0)) || got.DoneAt == nil {
		t.Fatalf("got %+v", got)
	}
}

func TestDoneRecurringOverdueSkipsToFuture(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "every monday 10am standup")
	e.now = time.Date(2026, 12, 16, 10, 0, 0, 0, ny)
	got, err := e.c.Done(it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.DueAt.Equal(utc(2026, 12, 21, 15, 0)) {
		t.Fatalf("due %v", got.DueAt)
	}
}

func TestDoneOneOff(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "refactor auth")
	got, err := e.c.Done(it.ID)
	if err != nil || got.Status != model.StatusDone || got.DoneAt == nil {
		t.Fatalf("got %+v %v", got, err)
	}
	if err := e.c.Reopen(it.ID); err != nil {
		t.Fatal(err)
	}
	re, _ := e.st.Get(it.ID)
	if re.Status != model.StatusOpen || re.DoneAt != nil {
		t.Fatalf("reopen %+v", re)
	}
}

func TestStopRepeating(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "every monday 10am standup")
	if err := e.c.StopRepeating(it.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := e.st.Get(it.ID)
	if got.RRule != "" || got.Status != model.StatusOpen {
		t.Fatalf("got %+v", got)
	}
}

func TestSnoozePresets(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "tomorrow 5pm deploy")
	want := map[string]time.Time{
		"10m":      time.Date(2026, 11, 25, 10, 10, 0, 0, ny),
		"1h":       time.Date(2026, 11, 25, 11, 0, 0, 0, ny),
		"tomorrow": time.Date(2026, 11, 26, 9, 0, 0, 0, ny),
	}
	for p, w := range want {
		if err := e.c.Snooze(it.ID, p); err != nil {
			t.Fatal(p, err)
		}
		got, _ := e.st.Get(it.ID)
		if got.SnoozeUntil == nil || !got.SnoozeUntil.Equal(w) {
			t.Errorf("%s: %v want %v", p, got.SnoozeUntil, w)
		}
	}
	if err := e.c.Snooze(it.ID, "bogus"); err == nil {
		t.Error("want error for bogus preset")
	}
	todo := mustCapture(t, e, "just a todo")
	if err := e.c.Snooze(todo.ID, "1h"); err == nil {
		t.Error("want error snoozing todo")
	}
}

func TestUpdateReparsesKeepsIdentity(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "tomorrow 5pm deploy #ops")
	e.now = e.now.Add(time.Hour)
	got, err := e.c.Update(it.ID, "deploy #ops")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != model.KindTodo || got.DueAt != nil || got.ID != it.ID || !got.CreatedAt.Equal(it.CreatedAt) {
		t.Fatalf("got %+v", got)
	}
	if _, err := e.c.Update(it.ID, "  "); !errors.Is(err, ErrEmpty) {
		t.Fatalf("err %v", err)
	}
	if _, err := e.c.Update("nope", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestEditTextRoundTrip(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "tomorrow 5pm deploy #ops")
	txt, err := e.c.EditText(it.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.c.Update(it.ID, txt)
	if err != nil || !got.DueAt.Equal(*it.DueAt) || got.Text != "deploy" {
		t.Fatalf("%q -> %+v %v", txt, got, err)
	}
}

func TestEditTextOmitsImplicitInbox(t *testing.T) {
	e := newEnv(t)
	plain := mustCapture(t, e, "read rfc")
	txt, err := e.c.EditText(plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(txt, "#inbox") || txt != "read rfc" {
		t.Fatalf("untagged: %q", txt)
	}
	got, err := e.c.Update(plain.ID, txt)
	if err != nil || !reflect.DeepEqual(got.Tags, []string{"inbox"}) {
		t.Fatalf("round trip: %+v %v", got, err)
	}

	tagged := mustCapture(t, e, "deploy #inbox #ops")
	txt, err = e.c.EditText(tagged.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(txt, "#inbox") || !strings.Contains(txt, "#ops") {
		t.Fatalf("tagged: %q", txt)
	}
}

func TestViewGrouping(t *testing.T) {
	e := newEnv(t)
	later := mustCapture(t, e, "2026-11-27 09:00 later #zeta")
	overdue := mustCapture(t, e, "today 11am x #alpha")
	e.now = e.now.Add(2 * time.Hour) // overdue now
	far := mustCapture(t, e, "2026-12-04 09:00 far #alpha")
	todo := mustCapture(t, e, "plain todo")
	gone := mustCapture(t, e, "gone #alpha")
	if err := e.c.Delete(gone.ID); err != nil {
		t.Fatal(err)
	}
	v, err := e.c.View()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range v.Upcoming {
		ids = append(ids, it.ID)
	}
	if !reflect.DeepEqual(ids, []string{overdue.ID, later.ID}) {
		t.Fatalf("upcoming %v", ids)
	}
	var tags []string
	for _, g := range v.Tags {
		tags = append(tags, g.Tag)
	}
	if !reflect.DeepEqual(tags, []string{"inbox", "alpha", "zeta"}) {
		t.Fatalf("tags %v", tags)
	}
	if v.Tags[0].Items[0].ID != todo.ID {
		t.Fatal("inbox content")
	}
	alpha := v.Tags[1].Items
	if len(alpha) != 2 || alpha[0].ID != overdue.ID || alpha[1].ID != far.ID {
		t.Fatalf("alpha %+v", alpha)
	}
	if v.Done == nil {
		t.Fatal("Done nil")
	}
}

func upcomingIDs(t *testing.T, e *env) []string {
	t.Helper()
	v, err := e.c.View()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range v.Upcoming {
		ids = append(ids, it.ID)
	}
	return ids
}

// C1: a snooze sorts the item only while it is pending and before the due
// time; once it has passed the item sorts by its due time again.
func TestViewSnoozeSortsOnlyWhilePending(t *testing.T) {
	e := newEnv(t) // 10:00
	a := mustCapture(t, e, "today 12:00 a")
	b := mustCapture(t, e, "today 11:30 b")
	if err := e.c.Snooze(a.ID, "1h"); err != nil { // until 11:00
		t.Fatal(err)
	}
	if got := upcomingIDs(t, e); !reflect.DeepEqual(got, []string{a.ID, b.ID}) {
		t.Fatalf("while snoozed: %v", got)
	}
	e.now = e.now.Add(70 * time.Minute) // 11:10, snooze over
	if got := upcomingIDs(t, e); !reflect.DeepEqual(got, []string{b.ID, a.ID}) {
		t.Fatalf("after snooze: %v", got)
	}
	it, _ := e.st.Get(a.ID)
	if got := effectiveTime(it, e.now); !got.Equal(*it.DueAt) {
		t.Fatalf("effective %v, want due %v", got, it.DueAt)
	}
	// A snooze past the due time never moves the item later than its due time.
	late := it.DueAt.Add(time.Hour)
	it.SnoozeUntil = &late
	if got := effectiveTime(it, e.now); !got.Equal(*it.DueAt) {
		t.Fatalf("snooze after due: effective %v", got)
	}
}

func TestPreviewAlerts(t *testing.T) {
	e := newEnv(t)
	p := e.c.Preview("in 30 minutes call")
	if !reflect.DeepEqual(p.Alerts, []string{"10m before", "at time"}) {
		t.Fatalf("%+v", p)
	}
	if p.Kind != model.KindReminder {
		t.Fatal(p.Kind)
	}
	p = e.c.Preview("every monday 10am standup #x")
	if p.Human != "Mon 30 Nov, 10:00 · every Monday" {
		t.Fatalf("human %q", p.Human)
	}
	p = e.c.Preview("thing")
	if p.Kind != model.KindTodo || !reflect.DeepEqual(p.Tags, []string{"inbox"}) || p.Alerts == nil || p.Warnings == nil {
		t.Fatalf("%+v", p)
	}
}

func TestFormatLead(t *testing.T) {
	cases := map[time.Duration]string{time.Hour: "1h before", 10 * time.Minute: "10m before",
		24 * time.Hour: "1d before", 90 * time.Minute: "90m before"}
	for d, w := range cases {
		if got := formatLead(d); got != w {
			t.Errorf("%v: %q want %q", d, got, w)
		}
	}
}

func TestOnChangeCalled(t *testing.T) {
	e := newEnv(t)
	n := 0
	e.c.OnChange(func() { n++ })
	it := mustCapture(t, e, "tomorrow 5pm deploy")
	steps := []func() error{
		func() error { return e.c.Snooze(it.ID, "10m") },
		func() error { _, err := e.c.Update(it.ID, "deploy 6pm tomorrow"); return err },
		func() error { _, err := e.c.Done(it.ID); return err },
		func() error { return e.c.Reopen(it.ID) },
		func() error { return e.c.Delete(it.ID) },
		func() error { s, _ := e.c.Settings(); return e.c.SaveSettings(s) },
	}
	want := 1
	for i, f := range steps {
		if err := f(); err != nil {
			t.Fatal(i, err)
		}
		want++
		if n != want {
			t.Fatalf("step %d: n=%d want %d", i, n, want)
		}
	}
	if _, err := e.c.Capture(" "); err == nil || n != want {
		t.Fatal("failed mutation must not notify")
	}
}

func TestSaveSettingsValidates(t *testing.T) {
	e := newEnv(t)
	s, _ := e.c.Settings()
	s.DefaultTime = "bogus"
	if err := e.c.SaveSettings(s); err == nil {
		t.Fatal("want error")
	}
}

func TestWithDue(t *testing.T) {
	e := newEnv(t)
	due := time.Date(2026, 11, 27, 8, 30, 0, 0, ny)
	for _, in := range []string{"tomorrow 5pm deploy #ops", "deploy #ops"} {
		out := e.c.WithDue(in, due)
		r, err := e.c.Capture(out)
		if err != nil {
			t.Fatal(err)
		}
		if r.Text != "deploy" || !reflect.DeepEqual(r.Tags, []string{"ops"}) || r.DueAt == nil || !r.DueAt.Equal(due) {
			t.Fatalf("%q -> %q -> %+v", in, out, r)
		}
	}
	out := e.c.WithDue("refactor auth", due)
	r, _ := e.c.Capture(out)
	if r.Kind != model.KindReminder || r.Text != "refactor auth" || !r.DueAt.Equal(due) {
		t.Fatalf("%q -> %+v", out, r)
	}
}

// R15: a recurring reminder that is never marked done keeps reminding. A
// daily 09:00 item due yesterday, at 10:00 today, moves to today 09:00 and
// its at-time alert fires (late, so "missed"); its leads are skipped.
func TestAdvanceRecurringDaily(t *testing.T) {
	e := newEnv(t) // Wed 25 Nov 10:00 New York
	it := mustCapture(t, e, "every day at 9am vitamins")
	if !it.DueAt.Equal(utc(2026, 11, 26, 14, 0)) {
		t.Fatalf("first due %v", it.DueAt)
	}
	if err := e.c.Snooze(it.ID, "1h"); err != nil {
		t.Fatal(err)
	}
	changes := 0
	e.c.OnChange(func() { changes++ })
	e.now = time.Date(2026, 11, 27, 10, 0, 0, 0, ny) // due yesterday, 10:00 today

	moved, wake, err := e.c.AdvanceRecurring(e.now)
	if err != nil || !reflect.DeepEqual(moved, []string{it.ID}) || changes != 1 {
		t.Fatalf("moved=%v changes=%d err=%v", moved, changes, err)
	}
	got, _ := e.st.Get(it.ID)
	if !got.DueAt.Equal(utc(2026, 11, 27, 14, 0)) || got.SnoozeUntil != nil || got.Status != model.StatusOpen {
		t.Fatalf("after advance %+v", got)
	}
	if !got.UpdatedAt.Equal(e.now) {
		t.Fatalf("updatedAt %v", got.UpdatedAt)
	}
	if !wake.Equal(utc(2026, 11, 28, 14, 0)) {
		t.Fatalf("wake %v, want tomorrow 09:00", wake)
	}
	fire, _, _ := scheduler.Plan([]model.Item{got}, []time.Duration{time.Hour, 10 * time.Minute}, e.now, func(string) bool { return false })
	if len(fire) != 1 || fire[0].Lead != 0 || !fire[0].DueAt.Equal(*got.DueAt) || !fire[0].Missed {
		t.Fatalf("planned %+v", fire)
	}

	// Again with nothing to do: no change, no notification.
	if moved, _, err := e.c.AdvanceRecurring(e.now); len(moved) != 0 || err != nil || changes != 1 {
		t.Fatalf("second pass moved=%v err=%v changes=%d", moved, err, changes)
	}
}

func TestAdvanceRecurringNotYet(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "every day at 9am vitamins") // due Thu 26 Nov 09:00
	changes := 0
	e.c.OnChange(func() { changes++ })
	e.now = time.Date(2026, 11, 27, 8, 59, 0, 0, ny) // next occurrence (27th 09:00) not reached
	moved, wake, err := e.c.AdvanceRecurring(e.now)
	if err != nil || len(moved) != 0 || changes != 0 {
		t.Fatalf("moved=%v changes=%d err=%v", moved, changes, err)
	}
	got, _ := e.st.Get(it.ID)
	if !got.DueAt.Equal(*it.DueAt) || !got.UpdatedAt.Equal(it.UpdatedAt) {
		t.Fatalf("changed: %+v", got)
	}
	if !wake.Equal(utc(2026, 11, 27, 14, 0)) {
		t.Fatalf("wake %v, want the next occurrence", wake)
	}
}

func TestAdvanceRecurringWeeklyCollapses(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "every monday 10am standup") // Mon 30 Nov 10:00
	one := mustCapture(t, e, "tomorrow 5pm one-off")
	e.now = time.Date(2026, 12, 23, 12, 0, 0, 0, ny) // Wed, three Mondays later
	moved, wake, err := e.c.AdvanceRecurring(e.now)
	if err != nil || !reflect.DeepEqual(moved, []string{it.ID}) {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	got, _ := e.st.Get(it.ID)
	if !got.DueAt.Equal(utc(2026, 12, 21, 15, 0)) {
		t.Fatalf("due %v, want Mon 21 Dec 10:00 NY", got.DueAt)
	}
	if !wake.Equal(utc(2026, 12, 28, 15, 0)) {
		t.Fatalf("wake %v", wake)
	}
	if o, _ := e.st.Get(one.ID); !o.DueAt.Equal(*one.DueAt) || !o.UpdatedAt.Equal(one.UpdatedAt) {
		t.Fatalf("one-off touched: %+v", o)
	}
}

// I5: undoing a delete restores the item as it was, unlike Reopen.
func TestUndeleteRestoresState(t *testing.T) {
	e := newEnv(t)
	done := mustCapture(t, e, "refactor auth")
	if _, err := e.c.Done(done.ID); err != nil {
		t.Fatal(err)
	}
	snoozed := mustCapture(t, e, "tomorrow 5pm deploy")
	if err := e.c.Snooze(snoozed.ID, "1h"); err != nil {
		t.Fatal(err)
	}
	rec := mustCapture(t, e, "every monday 10am standup")
	if _, err := e.c.Done(rec.ID); err != nil { // advances, stays open, DoneAt set
		t.Fatal(err)
	}
	before := map[string]model.Item{}
	for _, id := range []string{done.ID, snoozed.ID, rec.ID} {
		before[id], _ = e.st.Get(id)
		if err := e.c.Delete(id); err != nil {
			t.Fatal(err)
		}
		if err := e.c.Undelete(id); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := e.st.Get(done.ID)
	if d.Status != model.StatusDone || d.DoneAt == nil || !d.DoneAt.Equal(*before[done.ID].DoneAt) {
		t.Fatalf("done item: %+v", d)
	}
	s, _ := e.st.Get(snoozed.ID)
	if s.Status != model.StatusOpen || s.SnoozeUntil == nil || !s.SnoozeUntil.Equal(*before[snoozed.ID].SnoozeUntil) {
		t.Fatalf("snoozed item: %+v", s)
	}
	r, _ := e.st.Get(rec.ID)
	if r.Status != model.StatusOpen || !r.DueAt.Equal(*before[rec.ID].DueAt) {
		t.Fatalf("recurring item: %+v", r)
	}
	if err := e.c.Undelete("nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

// A recurring item marked done once carries DoneAt. Once it stops repeating
// it is a plain open item, so the stale DoneAt must go: otherwise Delete then
// Undelete would restore it as done.
func TestStopRepeatingClearsDoneAt(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "every monday 10am standup")
	if _, err := e.c.Done(it.ID); err != nil { // advances, stays open, DoneAt set
		t.Fatal(err)
	}
	if err := e.c.StopRepeating(it.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.st.Get(it.ID); got.DoneAt != nil || got.Status != model.StatusOpen {
		t.Fatalf("after stop repeating: %+v", got)
	}
	if err := e.c.Delete(it.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.c.Undelete(it.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.st.Get(it.ID); got.Status != model.StatusOpen {
		t.Fatalf("after undo delete: status %s, want open", got.Status)
	}
}

func TestUpdateRemovingRuleClearsDoneAt(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "every monday 10am standup")
	if _, err := e.c.Done(it.ID); err != nil {
		t.Fatal(err)
	}
	// Editing a still-recurring item keeps its DoneAt.
	if _, err := e.c.Update(it.ID, "every monday 11am standup"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.st.Get(it.ID); got.DoneAt == nil {
		t.Fatalf("recurring edit dropped DoneAt: %+v", got)
	}
	got, err := e.c.Update(it.ID, "monday 10am standup")
	if err != nil {
		t.Fatal(err)
	}
	if got.RRule != "" || got.DoneAt != nil || got.Status != model.StatusOpen {
		t.Fatalf("after removing the rule: %+v", got)
	}
}

func TestUndeleteNotDeletedIsNoOp(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "tomorrow 5pm deploy")
	changes := 0
	e.c.OnChange(func() { changes++ })
	e.now = e.now.Add(time.Minute) // a save would move UpdatedAt
	if err := e.c.Undelete(it.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := e.st.Get(it.ID)
	if changes != 0 || !got.UpdatedAt.Equal(it.UpdatedAt) || got.Status != model.StatusOpen {
		t.Fatalf("changes=%d item %+v", changes, got)
	}
}

// A daily 09:00 reminder left un-done still gets tomorrow's 1h and 10m lead
// alerts, then tomorrow's at-time alert once AdvanceRecurring moves it, with
// no alert repeated. This runs the scheduler's steps (advance, then plan)
// by hand.
func TestRecurringUndoneKeepsLeadAlerts(t *testing.T) {
	e := newEnv(t) // Wed 25 Nov 10:00 New York
	it := mustCapture(t, e, "every day at 9am vitamins")
	leads := []time.Duration{time.Hour, 10 * time.Minute}
	notified := map[string]bool{}
	step := func(at time.Time) ([]scheduler.Alert, time.Time) {
		t.Helper()
		e.now = at
		if _, _, err := e.c.AdvanceRecurring(at); err != nil {
			t.Fatal(err)
		}
		open, err := e.st.ListOpen()
		if err != nil {
			t.Fatal(err)
		}
		fire, cov, next := scheduler.Plan(open, leads, at, func(k string) bool { return notified[k] })
		for _, k := range cov {
			notified[k] = true
		}
		return fire, next
	}
	at := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, ny) }

	for _, l := range leads { // today's (26th) leads, then its at-time alert
		step(it.DueAt.Add(-l))
	}
	fire, next := step(at(26, 9, 0))
	if len(fire) != 1 || fire[0].Lead != 0 {
		t.Fatalf("26th 09:00: %+v", fire)
	}
	if !next.Equal(at(27, 8, 0)) {
		t.Fatalf("after the 26th, next %v, want 27th 08:00", next.In(ny))
	}
	tomorrow := at(27, 9, 0)
	for _, c := range []struct {
		at   time.Time
		lead time.Duration
	}{{at(27, 8, 0), time.Hour}, {at(27, 8, 50), 10 * time.Minute}, {tomorrow, 0}} {
		fire, _ := step(c.at)
		if len(fire) != 1 || fire[0].Lead != c.lead || !fire[0].DueAt.Equal(tomorrow) || fire[0].Missed {
			t.Fatalf("%v: got %+v, want the %v alert for the 27th", c.at.In(ny), fire, c.lead)
		}
	}
	if fire, _ := step(at(27, 9, 1)); len(fire) != 0 {
		t.Fatalf("repeat: %+v", fire)
	}
}

// Done on the alert for today's occurrence of a daily item left un-done since
// yesterday (its lead alert fires before AdvanceRecurring moves it) completes
// today's occurrence too; plain Done completes only the overdue one.
func TestDoneThroughOccurrence(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "every day at 9am vitamins") // due Thu 26 Nov 09:00
	e.now = time.Date(2026, 11, 27, 8, 30, 0, 0, ny)     // today's 09:00 not reached
	today := utc(2026, 11, 27, 14, 0)

	got, err := e.c.DoneThrough(it.ID, today)
	if err != nil || !got.DueAt.Equal(utc(2026, 11, 28, 14, 0)) || got.DoneAt == nil || got.Status != model.StatusOpen {
		t.Fatalf("done through today: %+v %v", got, err)
	}

	other := mustCapture(t, e, "every day at 9am stretch") // due Fri 27 Nov 09:00 (captured at 08:30)
	if _, err := e.c.DoneThrough(other.ID, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if o, _ := e.st.Get(other.ID); !o.DueAt.Equal(utc(2026, 11, 28, 14, 0)) {
		t.Fatalf("zero through: due %v", o.DueAt)
	}
	one := mustCapture(t, e, "tomorrow 5pm deploy")
	if d, err := e.c.DoneThrough(one.ID, today); err != nil || d.Status != model.StatusDone {
		t.Fatalf("one-off: %+v %v", d, err)
	}
}

func TestSaveSettingsStampsOnlySharedChanges(t *testing.T) {
	e := newEnv(t)
	s, _ := e.c.Settings()
	if err := e.c.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	drain := func() int {
		rows, err := e.st.Outbox(100)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, r := range rows {
			if r.Kind == store.OutboxSettings {
				n++
			}
			if _, err := e.st.Ack(r); err != nil {
				t.Fatal(err)
			}
		}
		return n
	}
	drain()
	before, _ := e.c.Settings()

	s = before
	s.Hotkey = "Ctrl+Alt+H"
	s.SharedUpdatedAt = time.UnixMilli(5)
	s.SharedUpdatedBy = "evil"
	if err := e.c.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	got, _ := e.c.Settings()
	if got.Hotkey != "Ctrl+Alt+H" || !got.SharedUpdatedAt.Equal(before.SharedUpdatedAt) || got.SharedUpdatedBy != before.SharedUpdatedBy {
		t.Fatalf("local change: %+v", got)
	}
	if n := drain(); n != 0 {
		t.Fatalf("outbox rows %d", n)
	}

	s = got
	s.LeadTimes = []time.Duration{3 * time.Hour}
	s.SharedUpdatedAt = time.UnixMilli(5)
	s.SharedUpdatedBy = "evil"
	if err := e.c.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	got, _ = e.c.Settings()
	if !got.SharedUpdatedAt.Equal(e.now) || got.SharedUpdatedAt.UnixMilli() != e.now.UnixMilli() || got.SharedUpdatedBy != e.st.DeviceID() {
		t.Fatalf("stamp %v %q", got.SharedUpdatedAt, got.SharedUpdatedBy)
	}
	if n := drain(); n != 1 {
		t.Fatalf("outbox rows %d", n)
	}
}

func TestSaveSettingsReplyScheduleIsShared(t *testing.T) {
	e := newEnv(t)
	s, _ := e.c.Settings()
	if err := e.c.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	before, _ := e.c.Settings()
	for name, mut := range map[string]func(*settings.Settings){
		"eod":  func(s *settings.Settings) { s.EndOfDay = "18:15" },
		"days": func(s *settings.Settings) { s.WorkDays = []string{"monday", "tuesday"} },
	} {
		s := before
		mut(&s)
		s.SharedUpdatedAt = time.Time{}
		if err := e.c.SaveSettings(s); err != nil {
			t.Fatal(err)
		}
		got, _ := e.c.Settings()
		if got.SharedUpdatedAt.Equal(before.SharedUpdatedAt) || got.SharedUpdatedBy != e.st.DeviceID() {
			t.Fatalf("%s: not stamped %v", name, got.SharedUpdatedAt)
		}
		before = got
	}
}
