package core

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"awesome-reminder/internal/model"
)

// Errors returned by the detail setters.
var (
	ErrNoDue       = errors.New("Set a due time first")
	ErrBadPriority = errors.New("unknown priority")
	ErrBadRepeat   = errors.New("unknown repeat")
	ErrBadTag      = errors.New("tags start with a letter and use a-z, 0-9, - or _")
)

var tagRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

var weekdayCodes = [...]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

// SetText replaces the item's text. It is trimmed and not parsed, so tags and
// the due time stay as they are.
func (c *Core) SetText(id, text string) (model.Item, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return model.Item{}, ErrEmpty
	}
	return c.mutate(id, func(it *model.Item, _ time.Time) error {
		it.Text = text
		return nil
	})
}

// SetPriority sets the priority: "", "low", "medium" or "high".
func (c *Core) SetPriority(id, p string) (model.Item, error) {
	if !model.ValidPriority(p) {
		return model.Item{}, ErrBadPriority
	}
	return c.mutate(id, func(it *model.Item, _ time.Time) error {
		it.Priority = p
		return nil
	})
}

// SetDue sets the due time and clears any snooze. A recurring item keeps its
// rule, with the new due time as the series anchor; a single-day weekly rule
// re-anchors to the weekday of the new due time. nil makes the item a
// to-do and clears its recurrence (and, like StopRepeating, the DoneAt an
// open item kept from being marked done while recurring).
func (c *Core) SetDue(id string, due *time.Time) (model.Item, error) {
	var d *time.Time
	if due != nil {
		u := due.UTC()
		d = &u
	}
	return c.mutate(id, func(it *model.Item, _ time.Time) error {
		it.DueAt, it.SnoozeUntil, it.Kind = d, nil, kindFor(d)
		switch {
		case d == nil:
			it.RRule = ""
			clearStaleDone(it)
		case isSingleDayWeekly(it.RRule):
			it.RRule = c.weeklyRule(*it)
		}
		return nil
	})
}

// isSingleDayWeekly reports whether rule is exactly FREQ=WEEKLY;BYDAY=<one
// day>, the form SetRepeat("weekly") writes.
func isSingleDayWeekly(rule string) bool {
	code, ok := strings.CutPrefix(rule, "FREQ=WEEKLY;BYDAY=")
	if !ok {
		return false
	}
	for _, w := range weekdayCodes {
		if code == w {
			return true
		}
	}
	return false
}

// weeklyRule is the weekly rule on the weekday of the item's due time in the
// item's zone. The item must have a due time.
func (c *Core) weeklyRule(it model.Item) string {
	return "FREQ=WEEKLY;BYDAY=" + weekdayCodes[it.DueAt.In(c.itemLoc(it)).Weekday()]
}

// SetRepeat sets the recurrence: "none", "daily", "weekdays" or "weekly"
// (on the weekday of the due time in the item's zone). Anything but "none"
// needs a due time. "none" is StopRepeating: an open item also drops the
// DoneAt it kept from being marked done while recurring.
func (c *Core) SetRepeat(id, repeat string) (model.Item, error) {
	switch repeat {
	case "none", "daily", "weekdays", "weekly":
	default:
		return model.Item{}, ErrBadRepeat
	}
	return c.mutate(id, func(it *model.Item, _ time.Time) error {
		if repeat == "none" {
			it.RRule = ""
			clearStaleDone(it)
			return nil
		}
		if it.DueAt == nil {
			return ErrNoDue
		}
		switch repeat {
		case "daily":
			it.RRule = "FREQ=DAILY"
		case "weekdays":
			it.RRule = "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"
		case "weekly":
			it.RRule = c.weeklyRule(*it)
		}
		return nil
	})
}

// SetTags replaces the tags: '#' stripped, lowercased, deduplicated and
// validated. An empty list becomes ["inbox"].
func (c *Core) SetTags(id string, tags []string) (model.Item, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#")))
		if t == "" || seen[t] {
			continue
		}
		if !tagRe.MatchString(t) {
			return model.Item{}, ErrBadTag
		}
		seen[t] = true
		out = append(out, t)
	}
	out = withInbox(out)
	return c.mutate(id, func(it *model.Item, _ time.Time) error {
		it.Tags = out
		return nil
	})
}

// PendingCount is the number of open items.
func (c *Core) PendingCount() (int, error) {
	open, err := c.st.ListOpen()
	return len(open), err
}
