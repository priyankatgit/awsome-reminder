package scheduler

import (
	"slices"
	"sort"
	"time"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/recur"
)

// Occurrence kinds.
const (
	OccReminder = "reminder"
	OccNudge    = "nudge"
	OccSweep    = "sweep"
)

// Occurrence is one future alert time of the kind the phone is pushed for.
type Occurrence struct {
	Kind   string    // OccReminder, OccNudge, OccSweep
	ItemID string    // "" for a sweep
	At     time.Time // the occurrence (due time, snooze end, nudge time, sweep time); push adds grace
	Due    time.Time // the item's occurrence due time (zero for a reply without due / sweep)
	People []string  // sweep only
}

// Upcoming is pure: the occurrences with At in [from, to], sorted by At, then
// ItemID. It follows Plan's rules (leads aside), so each card Plan shows maps
// to the occurrence whose At is the card's DueAt (a snooze card: the snooze
// end; a nudge or sweep: its own time).
//
// An open item with a due time yields it and, if recurring, each later
// occurrence (in its TZ, else r.Loc). A snooze ending at or after the due
// time replaces that occurrence's time with the snooze end, and later
// occurrences at or before the snooze end are dropped; a snoozed owed reply
// comes back as a nudge, with or without a due time. An open owed reply
// yields its nudges (see nudgeTimes), and with r.Sweep on each working day's
// EndOfDay sweep that some open reply was saved before (see sweep).
func Upcoming(items []model.Item, r Rules, from, to time.Time) []Occurrence {
	var out []Occurrence
	var replies []model.Item // the open ones, for the sweep
	for _, it := range items {
		if it.Status != model.StatusOpen {
			continue
		}
		owed := it.Reply && it.ReplyOutcome == ""
		if owed {
			replies = append(replies, it)
		}
		add := func(kind string, at, due time.Time) {
			if !at.Before(from) && !at.After(to) {
				out = append(out, Occurrence{Kind: kind, ItemID: it.ID, At: at, Due: due})
			}
		}
		snoozeKind := OccReminder
		if owed {
			snoozeKind = OccNudge
		}
		var snoozed, due time.Time
		if it.SnoozeUntil != nil {
			snoozed = *it.SnoozeUntil
		}
		if it.DueAt != nil {
			due = *it.DueAt
			if !snoozed.IsZero() && !snoozed.Before(due) {
				add(snoozeKind, snoozed, due)
			} else {
				add(OccReminder, due, due)
			}
			if it.RRule != "" {
				loc := recur.Loc(it.TZ, r.loc())
				cursor := from.Add(-time.Nanosecond) // so an occurrence at from is included
				for {
					next, err := recur.Next(it.RRule, due, cursor, loc)
					if err != nil || next.After(to) { // an error: the rule has ended
						break
					}
					cursor = next
					if !snoozed.IsZero() && !next.After(snoozed) {
						continue
					}
					add(OccReminder, next, next)
				}
			}
		} else if owed && !snoozed.IsZero() {
			add(OccNudge, snoozed, time.Time{})
		}
		if owed {
			nudgeTimes(it, r, snoozed, func(at time.Time) bool {
				if at.After(to) {
					return false
				}
				add(OccNudge, at, due)
				return true
			})
		}
	}
	if r.Sweep && len(replies) > 0 {
		who := people(replies)
		d := from.In(r.loc())
		// Step through calendar dates in UTC so a DST change can't skip or
		// repeat a day.
		for day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC); ; day = day.AddDate(0, 0, 1) {
			at := r.at(day, r.EndOfDay, 17, 30)
			if at.After(to) {
				break
			}
			if at.Before(from) || !r.workDay(day.Weekday()) || !sweepShows(replies, at) {
				continue
			}
			out = append(out, Occurrence{Kind: OccSweep, At: at, People: slices.Clone(who)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].ItemID < out[j].ItemID
	})
	return out
}
