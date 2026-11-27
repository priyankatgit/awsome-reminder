// Package settings defines the user-configurable settings and their validation.
package settings

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"awesome-reminder/internal/parser"
)

// Settings are the user preferences.
//
// JSON shape (durations are Go duration strings so the frontend can show and
// edit them):
//
//	{"hotkey":"Ctrl+Shift+Space","leadTimes":["1h0m0s","10m0s"],
//	 "defaultTime":"09:00","nextWeekDay":"monday","nextWeekTime":"09:00",
//	 "exportPath":"","autostart":true,"showPendingCount":true}
type Settings struct {
	Hotkey       string          `json:"hotkey"`       // "Ctrl+Shift+Space" ("Cmd+Shift+Space" on darwin)
	LeadTimes    []time.Duration `json:"leadTimes"`    // [1h, 10m]
	DefaultTime  string          `json:"defaultTime"`  // "09:00"
	NextWeekDay  string          `json:"nextWeekDay"`  // "monday"
	NextWeekTime string          `json:"nextWeekTime"` // "09:00"
	ExportPath   string          `json:"exportPath"`   // "" = off
	Autostart    bool            `json:"autostart"`    // true
	// ShowPendingCount shows the open-item count on the tray icon, the
	// Windows taskbar button and the macOS Dock (true; also for settings
	// saved before the field existed).
	ShowPendingCount bool `json:"showPendingCount"`
}

// wire mirrors Settings with lead times as duration strings.
type wire struct {
	Hotkey       string   `json:"hotkey"`
	LeadTimes    []string `json:"leadTimes"`
	DefaultTime  string   `json:"defaultTime"`
	NextWeekDay  string   `json:"nextWeekDay"`
	NextWeekTime string   `json:"nextWeekTime"`
	ExportPath   string   `json:"exportPath"`
	Autostart    bool     `json:"autostart"`
	// A pointer so a missing field (settings saved before Phase 1.6) can
	// default to true.
	ShowPendingCount *bool `json:"showPendingCount"`
}

// MarshalJSON encodes LeadTimes as duration strings such as "1h0m0s".
func (s Settings) MarshalJSON() ([]byte, error) {
	w := wire{s.Hotkey, make([]string, len(s.LeadTimes)), s.DefaultTime, s.NextWeekDay, s.NextWeekTime, s.ExportPath, s.Autostart, &s.ShowPendingCount}
	for i, d := range s.LeadTimes {
		w.LeadTimes[i] = d.String()
	}
	return json.Marshal(w)
}

// UnmarshalJSON decodes LeadTimes from duration strings. A missing
// showPendingCount means true.
func (s *Settings) UnmarshalJSON(b []byte) error {
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	leads := make([]time.Duration, len(w.LeadTimes))
	for i, str := range w.LeadTimes {
		d, err := time.ParseDuration(str)
		if err != nil {
			return fmt.Errorf("leadTimes[%d]: %w", i, err)
		}
		leads[i] = d
	}
	show := w.ShowPendingCount == nil || *w.ShowPendingCount
	*s = Settings{w.Hotkey, leads, w.DefaultTime, w.NextWeekDay, w.NextWeekTime, w.ExportPath, w.Autostart, show}
	return nil
}

// Defaults returns the default settings for the given GOOS.
func Defaults(goos string) Settings {
	hk := "Ctrl+Shift+Space"
	if goos == "darwin" {
		hk = "Cmd+Shift+Space"
	}
	return Settings{
		Hotkey:       hk,
		LeadTimes:    []time.Duration{time.Hour, 10 * time.Minute},
		DefaultTime:  "09:00",
		NextWeekDay:  "monday",
		NextWeekTime: "09:00",
		Autostart:    true,

		ShowPendingCount: true,
	}
}

const (
	maxLeads = 5
	maxLead  = 7 * 24 * time.Hour
)

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday, "friday": time.Friday,
	"saturday": time.Saturday,
}

// parseClock parses strict "HH:MM" (24h).
func parseClock(v string) (parser.Clock, error) {
	h, m, ok := strings.Cut(v, ":")
	if !ok || len(h) != 2 || len(m) != 2 {
		return parser.Clock{}, fmt.Errorf("invalid time %q, want HH:MM", v)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return parser.Clock{}, fmt.Errorf("invalid time %q, want HH:MM", v)
	}
	return parser.Clock{Hour: hh, Minute: mm}, nil
}

// exportExample is the sample path in the "full path" error.
func exportExample() string {
	if runtime.GOOS == "windows" {
		return `C:\Users\you\todo.md`
	}
	return "/home/you/todo.md"
}

// Validate checks times, weekday name, lead times and that a non-empty
// export path is absolute (a relative one would resolve against the working
// directory, which is System32 when the app starts at login).
func (s Settings) Validate() error {
	if _, err := parseClock(s.DefaultTime); err != nil {
		return fmt.Errorf("defaultTime: %w", err)
	}
	if _, err := parseClock(s.NextWeekTime); err != nil {
		return fmt.Errorf("nextWeekTime: %w", err)
	}
	if _, ok := weekdays[s.NextWeekDay]; !ok {
		return fmt.Errorf("nextWeekDay: unknown weekday %q", s.NextWeekDay)
	}
	if len(s.LeadTimes) > maxLeads {
		return fmt.Errorf("leadTimes: at most %d allowed", maxLeads)
	}
	for _, d := range s.LeadTimes {
		if d <= 0 || d > maxLead {
			return fmt.Errorf("leadTimes: %v must be > 0 and <= 7 days", d)
		}
	}
	if s.ExportPath != "" && !filepath.IsAbs(s.ExportPath) {
		return fmt.Errorf("exportPath: export path must be a full path, e.g. %s", exportExample())
	}
	return nil
}

// ParserConfig builds the parser configuration. Settings must be valid;
// invalid values fall back to the zero value of the field.
func (s Settings) ParserConfig(loc *time.Location) parser.Config {
	dt, _ := parseClock(s.DefaultTime)
	nt, _ := parseClock(s.NextWeekTime)
	return parser.Config{Loc: loc, DefaultTime: dt, NextWeekDay: weekdays[s.NextWeekDay], NextWeekTime: nt}
}
