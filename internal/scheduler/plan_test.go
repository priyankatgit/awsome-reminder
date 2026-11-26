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
	if len(fire) != 1 || fire[0].Key != "a|snooze|"+strconv.FormatInt(sn.Unix(), 10) || fire[0].Lead != 0 {
		t.Fatalf("got %+v", fire)
	}
	_, _, next = Plan([]model.Item{it}, leads, t0, none)
	if !next.Equal(sn) {
		t.Fatalf("next=%v", next)
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
