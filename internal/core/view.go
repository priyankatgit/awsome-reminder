package core

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"awesome-reminder/internal/model"
)

const inboxTag = "inbox"

// Preview is the live feedback shown while the user types a capture line.
type Preview struct {
	Kind     model.Kind `json:"kind"`
	Text     string     `json:"text"`
	Tags     []string   `json:"tags"` // "inbox" if none
	DueAt    *time.Time `json:"dueAt"`
	RRule    string     `json:"rrule"`
	Priority string     `json:"priority"`
	Human    string     `json:"human"`  // e.g. "Thu 26 Nov, 17:00 · every Monday"
	Alerts   []string   `json:"alerts"` // e.g. ["1h before", "10m before", "at time"]
	Warnings []string   `json:"warnings"`
}

// TagGroup is the open items carrying one tag.
type TagGroup struct {
	Tag   string       `json:"tag"`
	Items []model.Item `json:"items"`
}

// PersonGroup is the open replies owed to one person.
type PersonGroup struct {
	Person string       `json:"person"`
	Items  []model.Item `json:"items"`
}

// View is the data behind the main window.
type View struct {
	Upcoming []model.Item `json:"upcoming"`
	Tags     []TagGroup   `json:"tags"`
	Done     []model.Item `json:"done"`
	// Waiting lists open replies by person; never null.
	Waiting []PersonGroup `json:"waiting"`
	// Pending lists the items with local changes not yet synced; never null.
	Pending []string `json:"pending"`
}

const (
	upcomingWindow = 7 * 24 * time.Hour
	doneLimit      = 50
)

// effectiveTime is when an item next demands attention: SnoozeUntil while
// it is still ahead of now and earlier than DueAt, otherwise DueAt. A snooze
// that has passed (its alert fired) or that ends after the due time never
// hides the due time. frontend/src/item-row.ts effectiveDue mirrors it.
func effectiveTime(it model.Item, now time.Time) time.Time {
	if s := it.SnoozeUntil; s != nil && s.After(now) && s.Before(*it.DueAt) {
		return *s
	}
	return *it.DueAt
}

// priorityRank orders high before medium before low before none.
func priorityRank(it model.Item) int {
	switch it.Priority {
	case model.PriorityHigh:
		return 0
	case model.PriorityMedium:
		return 1
	case model.PriorityLow:
		return 2
	}
	return 3
}

// buildView groups open items. Upcoming holds open reminders whose effective
// time is before now+7d, sorted ascending (overdue items therefore come
// first). An item snoozed to before its due time sorts by its snooze time
// until the snooze ends.
func buildView(open, done []model.Item, now time.Time) View {
	v := View{Upcoming: []model.Item{}, Tags: []TagGroup{}, Waiting: []PersonGroup{}, Done: done}
	if v.Done == nil {
		v.Done = []model.Item{}
	}
	horizon := now.Add(upcomingWindow)
	groups := map[string][]model.Item{}
	var replies []model.Item
	for _, it := range open {
		if it.Reply {
			replies = append(replies, it)
			continue
		}
		if it.Kind == model.KindReminder && it.DueAt != nil && effectiveTime(it, now).Before(horizon) {
			v.Upcoming = append(v.Upcoming, it)
		}
		tags := it.Tags
		if len(tags) == 0 {
			tags = []string{inboxTag}
		}
		for _, t := range tags {
			groups[t] = append(groups[t], it)
		}
	}
	sort.SliceStable(v.Upcoming, func(i, j int) bool {
		return effectiveTime(v.Upcoming[i], now).Before(effectiveTime(v.Upcoming[j], now))
	})
	names := make([]string, 0, len(groups))
	for t := range groups {
		names = append(names, t)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == inboxTag) != (names[j] == inboxTag) {
			return names[i] == inboxTag
		}
		return names[i] < names[j]
	})
	for _, t := range names {
		items := groups[t] // open is already ordered by created asc
		sort.SliceStable(items, func(i, j int) bool { return priorityRank(items[i]) < priorityRank(items[j]) })
		v.Tags = append(v.Tags, TagGroup{Tag: t, Items: items})
	}
	v.Waiting = groupReplies(replies)
	return v
}

// groupReplies groups open replies by person (case-insensitive, trimmed).
// Items run oldest first within a group, groups by their oldest item, and
// the group with no person last. A group shows the person as its oldest
// item spelled it.
func groupReplies(replies []model.Item) []PersonGroup {
	out := []PersonGroup{}
	if len(replies) == 0 {
		return out
	}
	sorted := append([]model.Item(nil), replies...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.Before(sorted[j].CreatedAt) })
	idx := map[string]int{}
	for _, it := range sorted {
		person := strings.TrimSpace(it.OwedTo)
		key := strings.ToLower(person)
		i, ok := idx[key]
		if !ok {
			i = len(out)
			idx[key] = i
			out = append(out, PersonGroup{Person: person})
		}
		out[i].Items = append(out[i].Items, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return (out[i].Person == "") != (out[j].Person == "") && out[j].Person == "" })
	return out
}

// formatLead renders a lead time with the largest whole unit (d, h, m).
func formatLead(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd before", int(d/(24*time.Hour)))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh before", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dm before", int(d/time.Minute))
	}
}

// alertLabels lists the alerts that will still fire for an item due at due.
func alertLabels(due, now time.Time, leads []time.Duration) []string {
	out := []string{}
	for _, l := range leads {
		if due.Add(-l).After(now) {
			out = append(out, formatLead(l))
		}
	}
	if due.After(now) {
		out = append(out, "at time")
	}
	return out
}

// humanDue formats "Mon 2 Jan, 15:04" plus the recurrence, in loc.
func humanDue(due time.Time, rrule string, loc *time.Location) string {
	s := due.In(loc).Format("Mon 2 Jan, 15:04")
	if r := describeRRule(rrule); r != "" {
		s += " · " + r
	}
	return s
}
