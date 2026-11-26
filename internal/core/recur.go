package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// nextOccurrence returns the first occurrence of rule strictly after
// max(now, dtstart). The rule is evaluated in loc with DTSTART = dtstart, so
// the wall-clock time is preserved across DST changes.
func nextOccurrence(rule string, dtstart, now time.Time, loc *time.Location) (time.Time, error) {
	opt, err := rrule.StrToROptionInLocation(rule, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("rrule %q: %w", rule, err)
	}
	opt.Dtstart = dtstart.In(loc)
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return time.Time{}, fmt.Errorf("rrule %q: %w", rule, err)
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

var weekdayNames = map[string]string{
	"MO": "Monday", "TU": "Tuesday", "WE": "Wednesday", "TH": "Thursday",
	"FR": "Friday", "SA": "Saturday", "SU": "Sunday",
}

// describeRRule renders the parser's RRULEs for humans ("every Monday").
func describeRRule(rule string) string {
	switch rule {
	case "":
		return ""
	case "FREQ=DAILY":
		return "every day"
	case "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR":
		return "every weekday"
	}
	if code, ok := strings.CutPrefix(rule, "FREQ=WEEKLY;BYDAY="); ok {
		if n, ok := weekdayNames[code]; ok {
			return "every " + n
		}
	}
	return "repeats"
}
