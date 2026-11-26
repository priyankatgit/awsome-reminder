package parser

import (
	"reflect"
	"testing"
	"time"
)

type whenCase struct {
	name, in, text string
	tags           []string
	due            string // local "2006-01-02 15:04", "" = nil
	rrule          string
}

var whenCases = []whenCase{
	{"tomorrow+time", "remind me tomorrow 5pm check deploy #backend", "check deploy", []string{"backend"}, "2026-11-26 17:00", ""},
	{"today at", "set me reminder for standup notes today at 4 pm", "standup notes", nil, "2026-11-25 16:00", ""},
	{"today no time", "call bob today", "call bob", nil, "2026-11-25 11:00", ""},
	{"date only", "tomorrow pay rent", "pay rent", nil, "2026-11-26 09:00", ""},
	{"next week", "next week plan sprint", "plan sprint", nil, "2026-11-30 09:00", ""},
	{"in hours", "in 2 hours check build", "check build", nil, "2026-11-25 12:00", ""},
	{"in minutes", "in 30 min stretch", "stretch", nil, "2026-11-25 10:30", ""},
	{"in an hour", "in an hour coffee", "coffee", nil, "2026-11-25 11:00", ""},
	{"in days", "in 3 days follow up", "follow up", nil, "2026-11-28 10:00", ""},
	{"weekday", "on friday 3pm demo", "demo", nil, "2026-11-27 15:00", ""},
	{"same weekday passed", "wednesday 9am retro", "retro", nil, "2026-12-02 09:00", ""},
	{"same weekday ahead", "wednesday 4pm retro", "retro", nil, "2026-11-25 16:00", ""},
	{"next weekday", "next wednesday sync", "sync", nil, "2026-12-02 09:00", ""},
	{"day month", "28 nov 3pm review", "review", nil, "2026-11-28 15:00", ""},
	{"month day", "nov 28 at 15:30 review", "review", nil, "2026-11-28 15:30", ""},
	{"past date next year", "1 jan taxes", "taxes", nil, "2027-01-01 09:00", ""},
	{"iso", "2026-12-01 17:00 release", "release", nil, "2026-12-01 17:00", ""},
	{"bare hour pm", "at 5 deploy", "deploy", nil, "2026-11-25 17:00", ""},
	{"bare time passed", "at 9 standup", "standup", nil, "2026-11-26 09:00", ""},
	{"24h", "at 17:30 ship", "ship", nil, "2026-11-25 17:30", ""},
	{"noon", "lunch at noon", "lunch", nil, "2026-11-25 12:00", ""},
	{"eod", "eod push branch", "push branch", nil, "2026-11-25 17:00", ""},
	{"tonight", "tonight backup", "backup", nil, "2026-11-25 20:00", ""},
	{"case-insensitive", "Tomorrow 5PM Call", "Call", nil, "2026-11-26 17:00", ""},
	{"weekly", "every monday 10am standup #team", "standup", []string{"team"}, "2026-11-30 10:00", "FREQ=WEEKLY;BYDAY=MO"},
	{"daily", "every day at 9am vitamins", "vitamins", nil, "2026-11-26 09:00", "FREQ=DAILY"},
	{"weekdays", "every weekday 9:30 standup", "standup", nil, "2026-11-26 09:30", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"},
	{"quoted literal", `"fix 5pm cron job" tomorrow #ops`, "fix 5pm cron job", []string{"ops"}, "2026-11-26 09:00", ""},
	{"no time", "refactor auth #backend", "refactor auth", []string{"backend"}, "", ""},
	// extras beyond the contract table
	{"short weekday", "fri 5 pm pizza", "pizza", nil, "2026-11-27 17:00", ""},
	{"5:30pm", "tomorrow 5:30pm gym", "gym", nil, "2026-11-26 17:30", ""},
	{"midnight", "midnight backup", "backup", nil, "2026-11-26 00:00", ""},
	{"connector kept elsewhere", "look at the logs on friday", "look at the logs", nil, "2026-11-27 09:00", ""},
	{"ordinal", "dec 3rd dentist", "dentist", nil, "2026-12-03 09:00", ""},
	{"in a week", "in 1 week review", "review", nil, "2026-12-02 10:00", ""},
	{"not a time", "buy 5 apples", "buy 5 apples", nil, "", ""},
}

func localDue(t *testing.T, s string, loc *time.Location) time.Time {
	t.Helper()
	d, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return d.UTC()
}

func TestParseWhen(t *testing.T) {
	now, cfg := testNow(t)
	for _, c := range whenCases {
		t.Run(c.name, func(t *testing.T) {
			r := Parse(c.in, now, cfg)
			if r.Text != c.text {
				t.Errorf("Text = %q, want %q", r.Text, c.text)
			}
			wantTags := c.tags
			if wantTags == nil {
				wantTags = []string{}
			}
			if !reflect.DeepEqual(r.Tags, wantTags) {
				t.Errorf("Tags = %#v, want %#v", r.Tags, wantTags)
			}
			if r.RRule != c.rrule {
				t.Errorf("RRule = %q, want %q", r.RRule, c.rrule)
			}
			if c.due == "" {
				if r.DueAt != nil {
					t.Errorf("DueAt = %v, want nil", r.DueAt)
				}
				return
			}
			if r.DueAt == nil {
				t.Fatalf("DueAt = nil, want %s", c.due)
			}
			want := localDue(t, c.due, cfg.Loc)
			if !r.DueAt.Equal(want) || r.DueAt.Location() != time.UTC {
				t.Errorf("DueAt = %v, want %v (UTC)", r.DueAt, want)
			}
		})
	}
}

func TestParseWhenPastWarning(t *testing.T) {
	now, cfg := testNow(t)
	r := Parse("today 9am x", now, cfg)
	if r.DueAt == nil || !r.DueAt.Equal(localDue(t, "2026-11-25 09:00", cfg.Loc)) {
		t.Fatalf("DueAt = %v", r.DueAt)
	}
	if !reflect.DeepEqual(r.Warnings, []string{"time is in the past"}) {
		t.Errorf("Warnings = %#v", r.Warnings)
	}
	if r := Parse("tomorrow 9am x", now, cfg); len(r.Warnings) != 0 {
		t.Errorf("unexpected warnings %#v", r.Warnings)
	}
}

func TestParseWhenDST(t *testing.T) {
	_, cfg := testNow(t)
	now := time.Date(2027, 3, 13, 10, 0, 0, 0, cfg.Loc)
	r := Parse("tomorrow 9am x", now, cfg)
	if r.DueAt == nil {
		t.Fatal("DueAt nil")
	}
	want := time.Date(2027, 3, 14, 13, 0, 0, 0, time.UTC)
	if !r.DueAt.Equal(want) {
		t.Errorf("DueAt = %v, want %v", r.DueAt, want)
	}
}
