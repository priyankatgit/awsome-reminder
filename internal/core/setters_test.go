package core

import (
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"awesome-reminder/internal/model"
)

func TestSetText(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "tomorrow 5pm deploy #ops")
	got, err := e.c.SetText(it.ID, "  ship it  ")
	if err != nil || got.Text != "ship it" || !reflect.DeepEqual(got.Tags, []string{"ops"}) || got.DueAt == nil {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := e.c.SetText(it.ID, "   "); !errors.Is(err, ErrEmpty) {
		t.Fatalf("err = %v", err)
	}
	if g, _ := e.st.Get(it.ID); g.Text != "ship it" {
		t.Fatalf("stored %q", g.Text)
	}
	// no parsing: tokens stay text
	got, _ = e.c.SetText(it.ID, "call !high #x tomorrow")
	if got.Text != "call !high #x tomorrow" || got.Priority != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestSetPriorityValidates(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "a")
	for _, p := range []string{"high", "medium", "low", ""} {
		got, err := e.c.SetPriority(it.ID, p)
		if err != nil || got.Priority != p {
			t.Fatalf("%q: %+v %v", p, got, err)
		}
	}
	if _, err := e.c.SetPriority(it.ID, "urgent"); !errors.Is(err, ErrBadPriority) {
		t.Fatalf("err = %v", err)
	}
}

func TestSetDueClearsSnoozeAndNilMakesTodo(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "every monday 9am standup")
	if err := e.c.Snooze(it.ID, "1h"); err != nil {
		t.Fatal(err)
	}
	nd := utc(2026, 12, 3, 14, 0)
	got, err := e.c.SetDue(it.ID, &nd)
	if err != nil || got.SnoozeUntil != nil || !got.DueAt.Equal(nd) || got.Kind != model.KindReminder || got.RRule == "" {
		t.Fatalf("got %+v %v", got, err)
	}
	got, err = e.c.SetDue(it.ID, nil)
	if err != nil || got.DueAt != nil || got.Kind != model.KindTodo || got.RRule != "" || got.SnoozeUntil != nil {
		t.Fatalf("got %+v %v", got, err)
	}
	// a to-do gains a due time and becomes a reminder
	got, _ = e.c.SetDue(it.ID, &nd)
	if got.Kind != model.KindReminder {
		t.Fatalf("kind %v", got.Kind)
	}
}

func TestSetRepeat(t *testing.T) {
	e := newEnv(t)
	todo := mustCapture(t, e, "plain")
	if _, err := e.c.SetRepeat(todo.ID, "daily"); !errors.Is(err, ErrNoDue) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.c.SetRepeat(todo.ID, "none"); err != nil {
		t.Fatalf("none on todo: %v", err)
	}
	// Fri 27 Nov 2026 09:00 New York
	fri := time.Date(2026, 11, 27, 9, 0, 0, 0, ny)
	if _, err := e.c.SetDue(todo.ID, &fri); err != nil {
		t.Fatal(err)
	}
	for repeat, want := range map[string]string{
		"daily":    "FREQ=DAILY",
		"weekdays": "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
		"weekly":   "FREQ=WEEKLY;BYDAY=FR",
		"none":     "",
	} {
		got, err := e.c.SetRepeat(todo.ID, repeat)
		if err != nil || got.RRule != want {
			t.Fatalf("%s: %+v %v", repeat, got, err)
		}
	}
	if _, err := e.c.SetRepeat(todo.ID, "monthly"); !errors.Is(err, ErrBadRepeat) {
		t.Fatalf("err = %v", err)
	}
	// weekday is taken in the item's zone: Sat 03:00 UTC is still Friday in New York
	late := utc(2026, 11, 28, 3, 0)
	e.c.SetDue(todo.ID, &late)
	if got, _ := e.c.SetRepeat(todo.ID, "weekly"); got.RRule != "FREQ=WEEKLY;BYDAY=FR" {
		t.Fatalf("rrule %q", got.RRule)
	}
}

func TestSetTagsNormalizes(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "a")
	got, err := e.c.SetTags(it.ID, []string{" #Ops", "ops", "Backend"})
	if err != nil || !reflect.DeepEqual(got.Tags, []string{"ops", "backend"}) {
		t.Fatalf("got %+v %v", got, err)
	}
	got, err = e.c.SetTags(it.ID, []string{})
	if err != nil || !reflect.DeepEqual(got.Tags, []string{"inbox"}) {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := e.c.SetTags(it.ID, []string{"9x"}); !errors.Is(err, ErrBadTag) {
		t.Fatalf("err = %v", err)
	}
	if g, _ := e.st.Get(it.ID); !reflect.DeepEqual(g.Tags, []string{"inbox"}) {
		t.Fatalf("stored %v", g.Tags)
	}
}

func TestCapturePriority(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "deploy !high")
	if it.Priority != model.PriorityHigh || it.Text != "deploy" {
		t.Fatalf("got %+v", it)
	}
	if g, _ := e.st.Get(it.ID); g.Priority != model.PriorityHigh {
		t.Fatalf("stored %+v", g)
	}
	if p := e.c.Preview("deploy !med"); p.Priority != model.PriorityMedium {
		t.Fatalf("preview %+v", p)
	}
	up, err := e.c.Update(it.ID, "deploy !low")
	if err != nil || up.Priority != model.PriorityLow {
		t.Fatalf("update %+v %v", up, err)
	}
	if txt, _ := e.c.EditText(it.ID); txt != "deploy !low" {
		t.Fatalf("edit text %q", txt)
	}
}

func TestViewSortsTagGroupsByPriority(t *testing.T) {
	e := newEnv(t)
	a := mustCapture(t, e, "a #x")
	b := mustCapture(t, e, "b #x !low")
	c := mustCapture(t, e, "c #x !high")
	d := mustCapture(t, e, "d #x !med")
	f := mustCapture(t, e, "e #x")
	v, err := e.c.View()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range v.Tags[0].Items {
		ids = append(ids, it.ID)
	}
	if want := []string{c.ID, d.ID, b.ID, a.ID, f.ID}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("order %v want %v", ids, want)
	}
}

func TestPendingCount(t *testing.T) {
	e := newEnv(t)
	a := mustCapture(t, e, "a")
	b := mustCapture(t, e, "b")
	mustCapture(t, e, "c")
	if n, err := e.c.PendingCount(); err != nil || n != 3 {
		t.Fatalf("n=%d %v", n, err)
	}
	e.c.Done(a.ID)
	e.c.Delete(b.ID)
	if n, _ := e.c.PendingCount(); n != 1 {
		t.Fatalf("n=%d", n)
	}
}

func TestSettersNotifyOnceAndStamp(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "tomorrow 5pm a")
	e.now = e.now.Add(time.Hour)
	nd := utc(2026, 12, 5, 10, 0)
	calls := map[string]func() error{
		"text":     func() error { _, err := e.c.SetText(it.ID, "b"); return err },
		"priority": func() error { _, err := e.c.SetPriority(it.ID, "high"); return err },
		"due":      func() error { _, err := e.c.SetDue(it.ID, &nd); return err },
		"repeat":   func() error { _, err := e.c.SetRepeat(it.ID, "daily"); return err },
		"tags":     func() error { _, err := e.c.SetTags(it.ID, []string{"z"}); return err },
	}
	for name, fn := range calls {
		var n atomic.Int32
		e.c.OnChange(func() { n.Add(1) })
		if err := fn(); err != nil || n.Load() != 1 {
			t.Fatalf("%s: err=%v changes=%d", name, err, n.Load())
		}
		g, _ := e.st.Get(it.ID)
		if !g.UpdatedAt.Equal(e.now.UTC()) || g.UpdatedBy != e.st.DeviceID() {
			t.Fatalf("%s: stamp %v %q", name, g.UpdatedAt, g.UpdatedBy)
		}
		e.now = e.now.Add(time.Minute)
	}
}

func TestSetDueReanchorsSingleDayWeekly(t *testing.T) {
	thu := utc(2026, 12, 3, 14, 0) // Thursday 09:00 in New York
	for _, tc := range []struct{ in, want string }{
		{"every monday 9am standup", "FREQ=WEEKLY;BYDAY=TH"},
		{"every weekday 9am standup", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"},
		{"every day 9am standup", "FREQ=DAILY"},
	} {
		e := newEnv(t)
		it := mustCapture(t, e, tc.in)
		got, err := e.c.SetDue(it.ID, &thu)
		if err != nil || got.RRule != tc.want {
			t.Fatalf("%q: rrule %q err %v, want %q", tc.in, got.RRule, err, tc.want)
		}
	}
	// multi-day weekly is left alone
	e := newEnv(t)
	it := mustCapture(t, e, "tomorrow 9am standup")
	e.c.mutate(it.ID, func(i *model.Item, _ time.Time) error { i.RRule = "FREQ=WEEKLY;BYDAY=MO,WE"; return nil })
	got, err := e.c.SetDue(it.ID, &thu)
	if err != nil || got.RRule != "FREQ=WEEKLY;BYDAY=MO,WE" {
		t.Fatalf("rrule %q err %v", got.RRule, err)
	}
}
