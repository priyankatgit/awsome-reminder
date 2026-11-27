// Package recur evaluates the RRULEs the parser produces. It is pure and
// shared by core (marking done, catching up) and the scheduler (planning the
// next occurrence's alerts).
package recur

import (
	"fmt"
	"time"

	"github.com/teambition/rrule-go"
)

// build parses rule in loc with DTSTART = dtstart, so the wall-clock time is
// preserved across DST changes.
func build(rule string, dtstart time.Time, loc *time.Location) (*rrule.RRule, error) {
	opt, err := rrule.StrToROptionInLocation(rule, loc)
	if err != nil {
		return nil, fmt.Errorf("rrule %q: %w", rule, err)
	}
	opt.Dtstart = dtstart.In(loc)
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, fmt.Errorf("rrule %q: %w", rule, err)
	}
	return r, nil
}

// Next returns the first occurrence of rule strictly after max(now, dtstart).
// The rule is evaluated in loc with DTSTART = dtstart, so the wall-clock time
// is preserved across DST changes. It is an error if the rule has ended.
func Next(rule string, dtstart, now time.Time, loc *time.Location) (time.Time, error) {
	r, err := build(rule, dtstart, loc)
	if err != nil {
		return time.Time{}, err
	}
	after := now
	if dtstart.After(after) {
		after = dtstart
	}
	next := r.After(after, false)
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("rrule %q: no further occurrence", rule)
	}
	return next.UTC(), nil
}

// CatchUp evaluates rule from dtstart (an item's DueAt) at now. latest is the
// last occurrence at or before now if it is later than dtstart (zero
// otherwise); next is the first occurrence after max(dtstart, latest) (zero
// if the rule has ended).
func CatchUp(rule string, dtstart, now time.Time, loc *time.Location) (latest, next time.Time, err error) {
	r, err := build(rule, dtstart, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	from := dtstart
	if l := r.Before(now, true); l.After(dtstart) {
		latest = l.UTC()
		from = l
	}
	if n := r.After(from, false); !n.IsZero() {
		next = n.UTC()
	}
	return latest, next, nil
}

// Loc is the zone an item's rule is evaluated in: its TZ, or fallback when
// TZ is empty or unknown.
func Loc(tz string, fallback *time.Location) *time.Location {
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			return l
		}
	}
	return fallback
}
