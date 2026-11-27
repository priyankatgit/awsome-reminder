package parser

import (
	"reflect"
	"strings"
	"testing"
)

func TestRenderRoundTrip(t *testing.T) {
	now, cfg := testNow(t)
	for _, c := range whenCases {
		if c.due == "" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			r := Parse(c.in, now, cfg)
			line := Render(r.Text, r.Tags, r.DueAt, r.RRule, r.Priority, cfg.Loc)
			r2 := Parse(line, now, cfg)
			if r2.Text != r.Text {
				t.Errorf("line %q: Text = %q, want %q", line, r2.Text, r.Text)
			}
			if !reflect.DeepEqual(r2.Tags, r.Tags) {
				t.Errorf("line %q: Tags = %#v, want %#v", line, r2.Tags, r.Tags)
			}
			if r2.RRule != r.RRule {
				t.Errorf("line %q: RRule = %q, want %q", line, r2.RRule, r.RRule)
			}
			if r2.DueAt == nil || !r2.DueAt.Equal(*r.DueAt) {
				t.Errorf("line %q: DueAt = %v, want %v", line, r2.DueAt, r.DueAt)
			}
		})
	}
}

func TestRenderForms(t *testing.T) {
	now, cfg := testNow(t)
	check := func(in, want string) {
		t.Helper()
		r := Parse(in, now, cfg)
		if got := Render(r.Text, r.Tags, r.DueAt, r.RRule, r.Priority, cfg.Loc); got != want {
			t.Errorf("Render(%q) = %q, want %q", in, got, want)
		}
	}
	check("tomorrow 5pm check deploy #backend", "check deploy #backend 2026-11-26 17:00")
	check("every monday 10am standup", "standup every monday 10:00")
	check("every day at 9am vitamins", "vitamins every day 09:00")
	check("every weekday 9:30 standup", "standup every weekday 09:30")
	check("buy milk #home", "buy milk #home")
}

func TestRenderQuotesWhenNeeded(t *testing.T) {
	_, cfg := testNow(t)
	got := Render("fix 5pm cron job", nil, nil, "", "", cfg.Loc)
	if !strings.Contains(got, `"fix 5pm cron job"`) {
		t.Errorf("got %q, want quoted text", got)
	}
	if got := Render("plain text", []string{"a"}, nil, "", "", cfg.Loc); got != "plain text #a" {
		t.Errorf("got %q", got)
	}
	if got := Render("see #notatag", nil, nil, "", "", cfg.Loc); got != `"see #notatag"` {
		t.Errorf("got %q", got)
	}
}
