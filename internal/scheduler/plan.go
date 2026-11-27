// Package scheduler plans and fires reminder alerts.
package scheduler

import (
	"fmt"
	"sort"
	"time"

	"awesome-reminder/internal/model"
)

// missedAfter is how late an alert must be to count as missed.
const missedAfter = 2 * time.Minute

// Alert is one notification to show.
type Alert struct {
	Key    string        `json:"key"` // "<itemID>|<dueUnix>|<leadSeconds>" or "<itemID>|snooze|<unix>"
	ItemID string        `json:"itemId"`
	Text   string        `json:"text"`
	DueAt  time.Time     `json:"dueAt"`
	Lead   time.Duration `json:"lead"`   // 0 = at due time
	Missed bool          `json:"missed"` // fire time was > 2 min ago
	Snooze bool          `json:"snooze"` // fired at the end of a snooze
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
func Plan(items []model.Item, defaultLeads []time.Duration, now time.Time, notified func(key string) bool) (fireNow []Alert, covered []string, next time.Time) {
	var shown []candidate
	for _, it := range items {
		if it.Status != model.StatusOpen || it.DueAt == nil {
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
			cands = append(cands, candidate{
				alert: Alert{Key: fmt.Sprintf("%s|snooze|%d", it.ID, it.SnoozeUntil.Unix()),
					ItemID: it.ID, Text: it.Text, DueAt: *it.DueAt, Snooze: true},
				fire: *it.SnoozeUntil,
			})
		}
		leads := it.LeadTimes
		if leads == nil {
			leads = defaultLeads
		}
		all := append([]time.Duration{0}, leads...)
		seen := map[time.Duration]bool{}
		for _, l := range all {
			if seen[l] {
				continue
			}
			seen[l] = true
			fire := it.DueAt.Add(-l)
			if l != 0 && fire.Before(ref) {
				continue
			}
			if it.SnoozeUntil != nil && !fire.After(after) {
				continue
			}
			cands = append(cands, candidate{
				alert: Alert{Key: fmt.Sprintf("%s|%d|%d", it.ID, it.DueAt.Unix(), int64(l/time.Second)),
					ItemID: it.ID, Text: it.Text, DueAt: *it.DueAt, Lead: l},
				fire: fire,
			})
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
