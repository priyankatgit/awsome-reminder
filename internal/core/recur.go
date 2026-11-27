package core

import "strings"

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
