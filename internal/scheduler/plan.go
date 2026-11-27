// Package scheduler plans and fires reminder alerts.
package scheduler

import (
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"time"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/recur"
)

// missedAfter is how late an alert must be to count as missed.
const missedAfter = 2 * time.Minute

// firstNudge is how long after capture a reply without a due time is first
// nudged, on any day.
const firstNudge = 2 * time.Hour

// Alert kinds.
const (
	KindNudge = "nudge" // an owed reply is still open
	KindSweep = "sweep" // the end-of-day sweep of owed replies
)

// Alert is one notification to show.
type Alert struct {
	Key    string        `json:"key"`    // "<itemID>|<dueUnix>|<leadSeconds>", "<itemID>|snooze|<unix>", "<itemID>|nudge|<unix>" or "sweep|YYYY-MM-DD"
	ItemID string        `json:"itemId"` // "" for a sweep
	Text   string        `json:"text"`
	DueAt  time.Time     `json:"dueAt"`  // occurrence; a nudge's or sweep's own time; a snooze of a reply without a due time: SnoozeUntil
	Lead   time.Duration `json:"lead"`   // 0 = at due time
	Missed bool          `json:"missed"` // fire time was > 2 min ago
	Snooze bool          `json:"snooze"` // fired at the end of a snooze

	Kind         string    `json:"kind"`         // "" normal, "nudge", "sweep"
	WaitingSince time.Time `json:"waitingSince"` // nudge: when the reply was captured
	People       []string  `json:"people"`       // sweep only: who replies are owed to
}

// someone stands in the sweep's People for replies owed to no one in
// particular.
const someone = "Someone"

// Rules are the settings the planner uses.
type Rules struct {
	Leads       []time.Duration         // lead times of items whose LeadTimes is nil
	WorkDays    func(time.Weekday) bool // nudge days; nil = Monday to Friday
	DefaultTime string                  // "HH:MM" of working-day nudges; unparsable = 09:00
	EndOfDay    string                  // "HH:MM" end of the working day (end-of-day sweep)
	Sweep       bool                    // end-of-day sweep on
	Loc         *time.Location          // zone of nudge wall-clock times; nil = time.Local
}

type candidate struct {
	alert Alert
	fire  time.Time
}

// Plan is pure. fireNow = alerts to show now (at most one per item); covered = every due,
// un-notified key (including ones collapsed away) to mark notified after firing; next =
// earliest future alert time (zero if none). Lead alerts whose fire time is strictly before
// max(CreatedAt, UpdatedAt) are skipped; the at-time alert is never skipped
// for that reason. A snoozed item also gets an alert at SnoozeUntil, and only
// its lead and at-time alerts that fire after SnoozeUntil are kept.
//
// An open recurring item also gets the lead and at-time alerts of its next
// occurrence after DueAt (evaluated in its TZ, else the local zone). An item
// left un-done keeps its old DueAt until core.AdvanceRecurring moves it at
// that occurrence, and by then the moved item's leads are past its new
// UpdatedAt; looking ahead lets them fire on time. Keys are per occurrence,
// so an alert fired before the move is not fired again after it.
//
// An open reply (Reply with no ReplyOutcome) also gets nudges until it is
// closed; see nudges. Only the latest nudge at or before now and the first
// after it are candidates: the earlier ones are collapsed away for good, as
// time only moves on. Nudges are not leads, so an edit does not skip them;
// a snooze replaces the ones at or before its end.
//
// With r.Sweep on and an open reply anywhere, the end-of-day sweep fires at
// EndOfDay on each working day (see sweep). It belongs to no item.
func Plan(items []model.Item, r Rules, now time.Time, notified func(key string) bool) (fireNow []Alert, covered []string, next time.Time) {
	var shown []candidate
	var replies []model.Item // the open ones, for the sweep
	for _, it := range items {
		owed := it.Reply && it.ReplyOutcome == ""
		if owed && it.Status == model.StatusOpen {
			replies = append(replies, it)
		}
		if it.Status != model.StatusOpen || (it.DueAt == nil && !owed) {
			continue
		}
		var cands []candidate
		ref := it.CreatedAt // leads firing before the item was created or last edited are skipped
		if it.UpdatedAt.After(ref) {
			ref = it.UpdatedAt
		}
		// A snooze adds an alert at its end. Lead and at-time alerts after it
		// still fire, so snoozing before the due time never swallows the
		// due-time alert; the ones at or before it are replaced by it.
		var after time.Time
		if it.SnoozeUntil != nil {
			after = *it.SnoozeUntil
			due := after // a reply without a due time
			if it.DueAt != nil {
				due = *it.DueAt
			}
			cands = append(cands, candidate{
				alert: Alert{Key: fmt.Sprintf("%s|snooze|%d", it.ID, it.SnoozeUntil.Unix()),
					ItemID: it.ID, Text: it.Text, DueAt: due, Snooze: true},
				fire: *it.SnoozeUntil,
			})
		}
		leads := it.LeadTimes
		if leads == nil {
			leads = r.Leads
		}
		all := append([]time.Duration{0}, leads...)
		occurrence := func(due time.Time) {
			seen := map[time.Duration]bool{}
			for _, l := range all {
				if seen[l] {
					continue
				}
				seen[l] = true
				fire := due.Add(-l)
				if l != 0 && fire.Before(ref) {
					continue
				}
				if it.SnoozeUntil != nil && !fire.After(after) {
					continue
				}
				cands = append(cands, candidate{
					alert: Alert{Key: fmt.Sprintf("%s|%d|%d", it.ID, due.Unix(), int64(l/time.Second)),
						ItemID: it.ID, Text: it.Text, DueAt: due, Lead: l},
					fire: fire,
				})
			}
		}
		if it.DueAt != nil {
			occurrence(*it.DueAt)
			if it.RRule != "" {
				// The first occurrence after DueAt; zero if the rule has ended.
				_, nextDue, err := recur.CatchUp(it.RRule, *it.DueAt, *it.DueAt, recur.Loc(it.TZ, time.Local))
				if err != nil {
					log.Printf("scheduler: next occurrence of %s: %v", it.ID, err)
				} else if !nextDue.IsZero() {
					occurrence(nextDue)
				}
			}
		}
		if owed {
			for _, at := range nudges(it, r, now, after) {
				cands = append(cands, candidate{
					alert: Alert{Key: fmt.Sprintf("%s|nudge|%d", it.ID, at.Unix()),
						ItemID: it.ID, Text: it.Text, DueAt: at, Kind: KindNudge, WaitingSince: it.CreatedAt},
					fire: at,
				})
			}
		}

		var latest *candidate
		for i := range cands {
			c := cands[i]
			if notified(c.alert.Key) {
				continue
			}
			if c.fire.After(now) {
				if next.IsZero() || c.fire.Before(next) {
					next = c.fire
				}
				continue
			}
			covered = append(covered, c.alert.Key)
			if latest == nil || c.fire.After(latest.fire) {
				latest = &cands[i]
			}
		}
		if latest != nil {
			latest.alert.Missed = now.Sub(latest.fire) > missedAfter
			shown = append(shown, *latest)
		}
	}
	c, silent, at := sweep(replies, r, now, notified)
	if c != nil {
		covered = append(covered, c.alert.Key)
		if !silent {
			shown = append(shown, *c)
		}
	}
	if !at.IsZero() && (next.IsZero() || at.Before(next)) {
		next = at
	}
	sort.Slice(shown, func(i, j int) bool {
		if !shown[i].fire.Equal(shown[j].fire) {
			return shown[i].fire.Before(shown[j].fire)
		}
		return shown[i].alert.ItemID < shown[j].alert.ItemID
	})
	for _, c := range shown {
		fireNow = append(fireNow, c.alert)
	}
	return fireNow, covered, next
}

// sweep plans the end-of-day sweep of the open replies. If today (in r.Loc)
// is a working day, its sweep time EndOfDay has passed and its key is not
// notified, it returns the sweep to show now (missed if more than 2 minutes
// late). If every open reply was saved at or after the sweep time, the
// sweep would only list replies saved since, so it is returned silent:
// covered without showing, and so not shown later that day either (like the
// lead alerts before an item's creation). It also returns the next sweep
// time to wake for: today's if still ahead, else the next working day's. A sweep for an earlier day is never
// returned: one slept through is dropped. Nothing at all without r.Sweep or
// replies; a reply saved later re-plans anyway.
func sweep(replies []model.Item, r Rules, now time.Time, notified func(string) bool) (c *candidate, silent bool, next time.Time) {
	if !r.Sweep || len(replies) == 0 {
		return nil, false, time.Time{}
	}
	loc := r.loc()
	today := now.In(loc)
	if r.workDay(today.Weekday()) {
		at := r.at(today, r.EndOfDay, 17, 30)
		key := "sweep|" + today.Format(time.DateOnly)
		switch {
		case now.Before(at):
			if !notified(key) {
				return nil, false, at
			}
		case !notified(key):
			silent := !slices.ContainsFunc(replies, func(it model.Item) bool { return it.CreatedAt.Before(at) })
			return &candidate{
				alert: Alert{Key: key, DueAt: at, Kind: KindSweep, People: people(replies),
					Missed: now.Sub(at) > missedAfter},
				fire: at,
			}, silent, r.nextWorkDay(now, r.EndOfDay, 17, 30)
		}
	}
	return nil, false, r.nextWorkDay(now, r.EndOfDay, 17, 30)
}

// people lists who the replies are owed to, once each (matched trimmed and
// case-insensitively, spelled as the oldest reply spells it), ordered by each
// one's oldest reply; replies owed to no one show as "Someone".
func people(replies []model.Item) []string {
	sorted := append([]model.Item(nil), replies...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.Before(sorted[j].CreatedAt) })
	out := []string{}
	seen := map[string]bool{}
	for _, it := range sorted {
		p := strings.TrimSpace(it.OwedTo)
		key := strings.ToLower(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		if p == "" {
			p = someone
		}
		out = append(out, p)
	}
	return out
}

// loc is the zone of the rules' wall-clock times.
func (r Rules) loc() *time.Location {
	if r.Loc == nil {
		return time.Local
	}
	return r.Loc
}

// workDay reports whether d is a working day (Monday to Friday if unset).
func (r Rules) workDay(d time.Weekday) bool {
	if r.WorkDays == nil {
		return d != time.Saturday && d != time.Sunday
	}
	return r.WorkDays(d)
}

// at is the wall-clock time clock ("HH:MM"; unparsable = hh:mm) on day's
// date, in r.Loc.
func (r Rules) at(day time.Time, clock string, hh, mm int) time.Time {
	if t, err := time.Parse("15:04", clock); err == nil {
		hh, mm = t.Hour(), t.Minute()
	}
	return time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, r.loc())
}

// NextWorkDay returns clock ("HH:MM"; unparsable = 09:00) on the first
// working day after now's date in r.Loc; zero if there are no working days.
func (r Rules) NextWorkDay(now time.Time, clock string) time.Time {
	return r.nextWorkDay(now, clock, 9, 0)
}

// nextWorkDay is NextWorkDay with hh:mm for an unparsable clock.
func (r Rules) nextWorkDay(now time.Time, clock string, hh, mm int) time.Time {
	d := now.In(r.loc())
	// Step through calendar dates in UTC so a DST change can't skip or
	// repeat a day.
	day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	for range 7 {
		day = day.AddDate(0, 0, 1)
		if r.workDay(day.Weekday()) {
			return r.at(day, clock, hh, mm)
		}
	}
	return time.Time{}
}

// nudges returns the times of an open reply's latest nudge at or before now
// and of its first nudge after now (either may be missing), leaving out the
// ones before CreatedAt or at or before snoozed (zero if not snoozed).
//
// Without a due time the first nudge is CreatedAt+2h on any day; with one,
// the item's normal alerts come first. Then it is nudged at DefaultTime on
// each working day after the date of that first nudge (or of the due time).
// Dates and wall-clock times are in r.Loc.
func nudges(it model.Item, r Rules, now, snoozed time.Time) []time.Time {
	loc := r.loc()

	var last time.Time
	keep := func(at time.Time) bool { // false once past now; then at is the next nudge
		if at.Before(it.CreatedAt) || (!snoozed.IsZero() && !at.After(snoozed)) {
			return true
		}
		if at.After(now) {
			return false
		}
		last = at
		return true
	}
	var base time.Time // the date the working-day nudges follow
	if it.DueAt != nil {
		base = it.DueAt.In(loc)
	} else {
		first := it.CreatedAt.Add(firstNudge)
		if !keep(first) {
			return compact(last, first)
		}
		base = first.In(loc)
	}
	// Step through calendar dates in UTC so a DST change can't skip or
	// repeat a day; each nudge is built in loc at its wall-clock time.
	day := time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, time.UTC)
	for {
		found := false
		for range 7 {
			day = day.AddDate(0, 0, 1)
			if r.workDay(day.Weekday()) {
				found = true
				break
			}
		}
		if !found { // no working days: nothing after the first nudge
			return compact(last, time.Time{})
		}
		at := r.at(day, r.DefaultTime, 9, 0)
		if !keep(at) {
			return compact(last, at)
		}
	}
}

// compact returns the non-zero times of ts.
func compact(ts ...time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if !t.IsZero() {
			out = append(out, t)
		}
	}
	return out
}
