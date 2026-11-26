package parser

import (
	"regexp"
	"strconv"
	"time"

	"github.com/teambition/rrule-go"
)

// byDayCodes is indexed by time.Weekday (Sunday = 0).
var byDayCodes = [...]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

var rruleDays = [...]rrule.Weekday{rrule.SU, rrule.MO, rrule.TU, rrule.WE, rrule.TH, rrule.FR, rrule.SA}

const weekdayRRule = "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"

var weekdayNames = map[string]time.Weekday{
	"sun": time.Sunday, "sunday": time.Sunday,
	"mon": time.Monday, "monday": time.Monday,
	"tue": time.Tuesday, "tues": time.Tuesday, "tuesday": time.Tuesday,
	"wed": time.Wednesday, "wednesday": time.Wednesday,
	"thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday, "thursday": time.Thursday,
	"fri": time.Friday, "friday": time.Friday,
	"sat": time.Saturday, "saturday": time.Saturday,
}

var monthNames = map[string]time.Month{
	"jan": time.January, "january": time.January,
	"feb": time.February, "february": time.February,
	"mar": time.March, "march": time.March,
	"apr": time.April, "april": time.April,
	"may": time.May,
	"jun": time.June, "june": time.June,
	"jul": time.July, "july": time.July,
	"aug": time.August, "august": time.August,
	"sep": time.September, "sept": time.September, "september": time.September,
	"oct": time.October, "october": time.October,
	"nov": time.November, "november": time.November,
	"dec": time.December, "december": time.December,
}

var (
	isoRe     = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	dayNumRe  = regexp.MustCompile(`^(\d{1,2})(?:st|nd|rd|th)?$`)
	countRe   = regexp.MustCompile(`^\d{1,4}$`)
	tonightAt = Clock{20, 0}
)

type dateKind int

const (
	dkToday dateKind = iota
	dkTomorrow
	dkTonight
	dkWeekday     // bare weekday: today if still ahead, else the next one
	dkNextWeekday // "next friday": strictly after today
	dkNextWeek
	dkAbs     // explicit calendar date (year may be unset)
	dkRelDays // "in N days/weeks": keeps the current clock
	dkRelDur  // "in N minutes/hours": absolute offset from now
)

type datePart struct {
	kind  dateKind
	wd    time.Weekday
	month time.Month
	day   int
	year  int // 0 = pick the next occurrence
	days  int
	dur   time.Duration
}

// recurPart is a recognised "every ..." phrase.
type recurPart struct {
	rule string
	days []time.Weekday // empty = every day
}

// parseWhen finds the first time phrase in tokens and returns the due time
// (UTC), RRULE and the remaining tokens. Quoted tokens are never consumed.
func parseWhen(tokens []token, now time.Time, cfg Config) (due *time.Time, rrule string, rest []token) {
	loc := cfg.Loc
	if loc == nil {
		loc = time.UTC
	}
	now = now.In(loc)
	for i := range tokens {
		if tokens[i].Quoted {
			continue
		}
		rec, date, clock, n := matchPhrase(tokens, i, now, cfg)
		if n == 0 {
			continue
		}
		start := i
		if start > 0 && !tokens[start-1].Quoted && isConnector(tokens[start-1].Text) {
			start--
		}
		rest = append(rest, tokens[:start]...)
		rest = append(rest, tokens[i+n:]...)
		if rest == nil {
			rest = []token{}
		}

		var t time.Time
		switch {
		case rec != nil:
			c := cfg.DefaultTime
			if clock != nil {
				c = *clock
			}
			t = firstOccurrence(*rec, c, now, loc)
			rrule = rec.rule
		case date != nil:
			t = resolveDate(*date, clock, now, cfg, loc)
		default:
			t = clockOnly(*clock, now, loc)
		}
		u := t.UTC()
		return &u, rrule, rest
	}
	return nil, "", nil
}

// matchPhrase greedily takes at most one recurrence-or-date part and one time
// part, in either order, starting at tokens[i]. n is the tokens consumed.
func matchPhrase(toks []token, i int, now time.Time, cfg Config) (rec *recurPart, date *datePart, clock *Clock, n int) {
	pos := i
	for {
		if rec == nil && date == nil {
			if r, k, ok := matchRecur(toks, pos); ok {
				rec, pos = &r, pos+k
				continue
			}
			if d, k, ok := matchDate(toks, pos); ok {
				date, pos = &d, pos+k
				if d.kind == dkRelDur {
					break // an absolute offset takes no time of day
				}
				continue
			}
		}
		if clock == nil && (date == nil || date.kind != dkRelDur) {
			if c, k, ok := matchClock(toks, pos); ok {
				clock, pos = &c, pos+k
				continue
			}
		}
		break
	}
	return rec, date, clock, pos - i
}

func matchRecur(toks []token, i int) (recurPart, int, bool) {
	if w, ok := word(toks, i); !ok || w != "every" {
		return recurPart{}, 0, false
	}
	w, ok := word(toks, i+1)
	if !ok {
		return recurPart{}, 0, false
	}
	switch w {
	case "day":
		return recurPart{rule: "FREQ=DAILY"}, 2, true
	case "weekday", "weekdays":
		return recurPart{rule: weekdayRRule, days: []time.Weekday{1, 2, 3, 4, 5}}, 2, true
	}
	if wd, ok := weekdayNames[w]; ok {
		return recurPart{rule: "FREQ=WEEKLY;BYDAY=" + byDayCodes[wd], days: []time.Weekday{wd}}, 2, true
	}
	return recurPart{}, 0, false
}

func matchDate(toks []token, i int) (datePart, int, bool) {
	w, ok := word(toks, i)
	if !ok {
		return datePart{}, 0, false
	}
	switch w {
	case "today":
		return datePart{kind: dkToday}, 1, true
	case "tomorrow", "tmrw":
		return datePart{kind: dkTomorrow}, 1, true
	case "tonight":
		return datePart{kind: dkTonight}, 1, true
	case "next":
		if n, ok := word(toks, i+1); ok {
			if n == "week" {
				return datePart{kind: dkNextWeek}, 2, true
			}
			if wd, ok := weekdayNames[n]; ok {
				return datePart{kind: dkNextWeekday, wd: wd}, 2, true
			}
		}
		return datePart{}, 0, false
	case "in":
		return matchRelative(toks, i)
	}
	if wd, ok := weekdayNames[w]; ok {
		return datePart{kind: dkWeekday, wd: wd}, 1, true
	}
	if m := isoRe.FindStringSubmatch(w); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		if validDate(y, time.Month(mo), d) {
			return datePart{kind: dkAbs, year: y, month: time.Month(mo), day: d}, 1, true
		}
		return datePart{}, 0, false
	}
	// "28 nov" / "nov 28"
	if m := dayNumRe.FindStringSubmatch(w); m != nil {
		if mon, ok := word(toks, i+1); ok {
			if month, ok := monthNames[mon]; ok {
				d, _ := strconv.Atoi(m[1])
				if validDate(2024, month, d) {
					return datePart{kind: dkAbs, month: month, day: d}, 2, true
				}
			}
		}
		return datePart{}, 0, false
	}
	if month, ok := monthNames[w]; ok {
		if dw, ok := word(toks, i+1); ok {
			if m := dayNumRe.FindStringSubmatch(dw); m != nil {
				d, _ := strconv.Atoi(m[1])
				if validDate(2024, month, d) {
					return datePart{kind: dkAbs, month: month, day: d}, 2, true
				}
			}
		}
	}
	return datePart{}, 0, false
}

// matchRelative handles "in N unit", "in an hour" and "in a minute".
func matchRelative(toks []token, i int) (datePart, int, bool) {
	cnt, ok := word(toks, i+1)
	if !ok {
		return datePart{}, 0, false
	}
	n := 1
	if countRe.MatchString(cnt) {
		n, _ = strconv.Atoi(cnt)
	} else if cnt != "a" && cnt != "an" {
		return datePart{}, 0, false
	}
	unit, ok := word(toks, i+2)
	if !ok {
		return datePart{}, 0, false
	}
	switch unit {
	case "m", "min", "mins", "minute", "minutes":
		return datePart{kind: dkRelDur, dur: time.Duration(n) * time.Minute}, 3, true
	case "h", "hr", "hrs", "hour", "hours":
		return datePart{kind: dkRelDur, dur: time.Duration(n) * time.Hour}, 3, true
	case "d", "day", "days":
		return datePart{kind: dkRelDays, days: n}, 3, true
	case "w", "week", "weeks":
		return datePart{kind: dkRelDays, days: 7 * n}, 3, true
	}
	return datePart{}, 0, false
}

func validDate(y int, m time.Month, d int) bool {
	if m < time.January || m > time.December || d < 1 {
		return false
	}
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return t.Month() == m && t.Day() == d
}

func at(loc *time.Location, y int, m time.Month, d int, c Clock) time.Time {
	return time.Date(y, m, d, c.Hour, c.Minute, 0, 0, loc)
}

// clockOnly resolves a bare time of day: today if still ahead, else tomorrow.
func clockOnly(c Clock, now time.Time, loc *time.Location) time.Time {
	y, m, d := now.Date()
	t := at(loc, y, m, d, c)
	if !t.After(now) {
		t = at(loc, y, m, d+1, c)
	}
	return t
}

func resolveDate(dp datePart, clock *Clock, now time.Time, cfg Config, loc *time.Location) time.Time {
	y, m, d := now.Date()
	pick := func(def Clock) Clock {
		if clock != nil {
			return *clock
		}
		return def
	}
	switch dp.kind {
	case dkRelDur:
		return at(loc, y, m, d, Clock{now.Hour(), now.Minute()}).Add(dp.dur)
	case dkRelDays:
		return at(loc, y, m, d+dp.days, pick(Clock{now.Hour(), now.Minute()}))
	case dkToday:
		if clock != nil {
			return at(loc, y, m, d, *clock)
		}
		next := at(loc, y, m, d, Clock{now.Hour() + 1, 0})
		if def := at(loc, y, m, d, cfg.DefaultTime); def.After(next) {
			return def
		}
		return next
	case dkTomorrow:
		return at(loc, y, m, d+1, pick(cfg.DefaultTime))
	case dkTonight:
		return at(loc, y, m, d, pick(tonightAt))
	case dkWeekday:
		c := pick(cfg.DefaultTime)
		off := (int(dp.wd) - int(now.Weekday()) + 7) % 7
		t := at(loc, y, m, d+off, c)
		if !t.After(now) {
			t = at(loc, y, m, d+off+7, c)
		}
		return t
	case dkNextWeekday:
		off := (int(dp.wd) - int(now.Weekday()) + 7) % 7
		if off == 0 {
			off = 7
		}
		return at(loc, y, m, d+off, pick(cfg.DefaultTime))
	case dkNextWeek:
		off := (int(cfg.NextWeekDay) - int(now.Weekday()) + 7) % 7
		if off == 0 {
			off = 7
		}
		return at(loc, y, m, d+off, pick(cfg.NextWeekTime))
	default: // dkAbs
		c := pick(cfg.DefaultTime)
		if dp.year != 0 {
			return at(loc, dp.year, dp.month, dp.day, c)
		}
		today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		if time.Date(y, dp.month, dp.day, 0, 0, 0, 0, time.UTC).Before(today) {
			y++
		}
		return at(loc, y, dp.month, dp.day, c)
	}
}

// firstOccurrence returns the first occurrence of the recurrence strictly
// after now, with DTSTART = today at the given clock in loc.
func firstOccurrence(rec recurPart, c Clock, now time.Time, loc *time.Location) time.Time {
	y, m, d := now.Date()
	opt := rrule.ROption{Freq: rrule.DAILY, Dtstart: at(loc, y, m, d, c)}
	if len(rec.days) > 0 {
		opt.Freq = rrule.WEEKLY
		for _, wd := range rec.days {
			opt.Byweekday = append(opt.Byweekday, rruleDays[wd])
		}
	}
	r, err := rrule.NewRRule(opt)
	if err != nil {
		return at(loc, y, m, d+1, c)
	}
	t := r.After(now, false)
	if t.IsZero() {
		return at(loc, y, m, d+1, c)
	}
	return t.In(loc)
}
