package parser

import (
	"testing"
	"time"
)

func TestParsePriority(t *testing.T) {
	now, cfg := testNow(t)
	cases := []struct{ in, text, prio string }{
		{"fix prod !high", "fix prod", "high"},
		{"!h fix prod", "fix prod", "high"},
		{"fix prod !!!", "fix prod", "high"},
		{"review PR !med tomorrow 5pm", "review PR", "medium"},
		{"!m x", "x", "medium"},
		{"!medium x", "x", "medium"},
		{"tidy desk !l", "tidy desk", "low"},
		{"!low tidy desk", "tidy desk", "low"},
		{"call Bob !", "call Bob !", ""},
		{"wow!! ship it", "wow!! ship it", ""},
		{"read !important doc", "read !important doc", ""},
		{`"!h" literally`, "!h literally", ""},
		{"a !h b !l", "a b", "high"},
		{"remind me tomorrow 5pm to call Sam !h", "call Sam", "high"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			r := Parse(c.in, now, cfg)
			if r.Text != c.text || r.Priority != c.prio {
				t.Errorf("got (%q, %q), want (%q, %q)", r.Text, r.Priority, c.text, c.prio)
			}
		})
	}
	r := Parse("review PR !med tomorrow 5pm", now, cfg)
	if r.DueAt == nil || !r.DueAt.Equal(time.Date(2026, 11, 26, 17, 0, 0, 0, cfg.Loc)) {
		t.Errorf("DueAt = %v", r.DueAt)
	}
}

func TestRenderRoundTripPriority(t *testing.T) {
	now, cfg := testNow(t)
	for _, in := range []string{"fix prod !high", "review PR !med tomorrow 5pm #ops", "tidy desk !l", "every monday 10am standup !h"} {
		r := Parse(in, now, cfg)
		line := Render(r.Text, r.Tags, r.DueAt, r.RRule, r.Priority, cfg.Loc)
		r2 := Parse(line, now, cfg)
		if r2.Text != r.Text || r2.Priority != r.Priority || r2.RRule != r.RRule || (r.DueAt == nil) != (r2.DueAt == nil) || (r.DueAt != nil && !r2.DueAt.Equal(*r.DueAt)) {
			t.Errorf("%q -> %q -> %+v, want %+v", in, line, r2, r)
		}
		if r.Priority == "" {
			t.Errorf("%q: no priority parsed", in)
		}
	}
	if got := Render("deploy", []string{"ops"}, nil, "", "high", cfg.Loc); got != "deploy #ops !high" {
		t.Errorf("got %q", got)
	}
	if got := Render("a !h b", nil, nil, "", "", cfg.Loc); got != `"a !h b"` {
		t.Errorf("got %q", got)
	}
}
