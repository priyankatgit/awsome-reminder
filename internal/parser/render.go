package parser

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

var renderWeekdays = [...]string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

// Render produces the editable one-line form "<text> #tag1 #tag2 <when>".
// The text is wrapped in double quotes only when it would not survive being
// parsed back unchanged (it contains a time phrase, a tag, a prefix, ...).
func Render(text string, tags []string, due *time.Time, rrule string, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	var rest []string
	for _, t := range tags {
		rest = append(rest, "#"+t)
	}
	if due != nil {
		local := due.In(loc)
		clock := local.Format("15:04")
		if rec := renderRecurrence(rrule); rec != "" {
			rest = append(rest, rec+" "+clock)
		} else {
			rest = append(rest, local.Format("2006-01-02")+" "+clock)
		}
	}
	suffix := strings.Join(rest, " ")
	join := func(t string) string {
		return strings.TrimSpace(t + " " + suffix)
	}

	line := join(text)
	if text != "" && !strings.Contains(text, `"`) && !survives(line, text, tags, due, rrule, loc) {
		line = join(`"` + text + `"`)
	}
	return line
}

// survives reports whether parsing line yields back the given text, tags and
// schedule.
func survives(line, text string, tags []string, due *time.Time, rrule string, loc *time.Location) bool {
	cfg := Config{Loc: loc, DefaultTime: Clock{9, 0}, NextWeekDay: time.Monday, NextWeekTime: Clock{9, 0}}
	r := Parse(line, time.Now().In(loc), cfg)
	if r.Text != text || r.RRule != rrule || !reflect.DeepEqual(r.Tags, nonNil(tags)) {
		return false
	}
	if (r.DueAt == nil) != (due == nil) {
		return false
	}
	return rrule != "" || due == nil || r.DueAt.Equal(*due)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func renderRecurrence(rrule string) string {
	switch rrule {
	case "":
		return ""
	case "FREQ=DAILY":
		return "every day"
	case "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR":
		return "every weekday"
	}
	if day, ok := strings.CutPrefix(rrule, "FREQ=WEEKLY;BYDAY="); ok {
		for i, code := range byDayCodes {
			if code == day {
				return fmt.Sprintf("every %s", renderWeekdays[i])
			}
		}
	}
	return ""
}
