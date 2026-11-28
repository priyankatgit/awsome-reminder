// Package settings defines the user-configurable settings and their validation.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"awesome-reminder/internal/hotkey"
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
	// ReplyHotkey is the per-device global hotkey that marks the selected
	// message as owing a reply ("Ctrl+Alt+R", "Ctrl+Option+R" on darwin).
	ReplyHotkey string `json:"replyHotkey"`
	// WorkDays are the lowercase weekday names that count as working days, in
	// Monday..Sunday order. Shared.
	WorkDays []string `json:"workDays"`
	// EndOfDay is the "HH:MM" end of the working day. Shared.
	EndOfDay string `json:"endOfDay"`
	// EndOfDaySweep turns the end-of-day sweep of owed replies on. Shared.
	EndOfDaySweep bool `json:"endOfDaySweep"`
	// SharedUpdatedAt and SharedUpdatedBy stamp the last change to the shared
	// fields (see Shared), for last-writer-wins sync. JSON: unix ms and the
	// device id; missing means zero and "".
	SharedUpdatedAt time.Time `json:"sharedUpdatedAt"`
	SharedUpdatedBy string    `json:"sharedUpdatedBy"`
}

// Shared is the subset of Settings that syncs between devices: the reminder
// defaults. The hotkey, export path, autostart and pending-count flag stay
// per machine.
type Shared struct {
	LeadTimes                              []time.Duration
	DefaultTime, NextWeekDay, NextWeekTime string
	WorkDays                               []string
	EndOfDay                               string
	EndOfDaySweep                          bool
}

// Shared returns the synced fields.
func (s Settings) Shared() Shared {
	return Shared{append([]time.Duration(nil), s.LeadTimes...), s.DefaultTime, s.NextWeekDay, s.NextWeekTime,
		slices.Clone(s.WorkDays), s.EndOfDay, s.EndOfDaySweep}
}

// WithShared returns s with only the shared fields replaced by p.
func (s Settings) WithShared(p Shared) Settings {
	s.LeadTimes = append([]time.Duration(nil), p.LeadTimes...)
	s.DefaultTime, s.NextWeekDay, s.NextWeekTime = p.DefaultTime, p.NextWeekDay, p.NextWeekTime
	s.WorkDays, s.EndOfDay, s.EndOfDaySweep = slices.Clone(p.WorkDays), p.EndOfDay, p.EndOfDaySweep
	return s
}

// Equal reports whether p and q hold the same values; the order of WorkDays
// does not matter.
func (p Shared) Equal(q Shared) bool {
	if len(p.LeadTimes) != len(q.LeadTimes) {
		return false
	}
	for i := range p.LeadTimes {
		if p.LeadTimes[i] != q.LeadTimes[i] {
			return false
		}
	}
	return p.DefaultTime == q.DefaultTime && p.NextWeekDay == q.NextWeekDay && p.NextWeekTime == q.NextWeekTime &&
		slices.Equal(canonDays(p.WorkDays), canonDays(q.WorkDays)) && p.EndOfDay == q.EndOfDay && p.EndOfDaySweep == q.EndOfDaySweep
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
	// Pointers/nil so settings saved before the replies-owed feature get the
	// defaults.
	ReplyHotkey   *string  `json:"replyHotkey"`
	WorkDays      []string `json:"workDays"`
	EndOfDay      *string  `json:"endOfDay"`
	EndOfDaySweep *bool    `json:"endOfDaySweep"`
	// Unix ms; a pointer so a missing field decodes to the zero time.
	SharedUpdatedAt *int64 `json:"sharedUpdatedAt"`
	SharedUpdatedBy string `json:"sharedUpdatedBy"`
}

// MarshalJSON encodes LeadTimes as duration strings such as "1h0m0s".
func (s Settings) MarshalJSON() ([]byte, error) {
	w := wire{s.Hotkey, make([]string, len(s.LeadTimes)), s.DefaultTime, s.NextWeekDay, s.NextWeekTime, s.ExportPath, s.Autostart, &s.ShowPendingCount,
		&s.ReplyHotkey, slices.Clone(s.WorkDays), &s.EndOfDay, &s.EndOfDaySweep, nil, s.SharedUpdatedBy}
	if w.WorkDays == nil {
		w.WorkDays = []string{}
	}
	if !s.SharedUpdatedAt.IsZero() {
		ms := s.SharedUpdatedAt.UnixMilli()
		w.SharedUpdatedAt = &ms
	}
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
	var at time.Time
	if w.SharedUpdatedAt != nil {
		at = time.UnixMilli(*w.SharedUpdatedAt)
	}
	d := Defaults(runtime.GOOS)
	reply, eod, sweep, days := d.ReplyHotkey, d.EndOfDay, d.EndOfDaySweep, d.WorkDays
	if w.ReplyHotkey != nil {
		reply = *w.ReplyHotkey
	} else {
		reply = defaultReplyHotkey(runtime.GOOS, w.Hotkey)
	}
	if w.EndOfDay != nil {
		eod = *w.EndOfDay
	}
	if w.EndOfDaySweep != nil {
		sweep = *w.EndOfDaySweep
	}
	if w.WorkDays != nil {
		days = canonDays(w.WorkDays)
	}
	*s = Settings{w.Hotkey, leads, w.DefaultTime, w.NextWeekDay, w.NextWeekTime, w.ExportPath, w.Autostart, show,
		reply, days, eod, sweep, at, w.SharedUpdatedBy}
	return nil
}

// Defaults returns the default settings for the given GOOS.
func Defaults(goos string) Settings {
	hk, rhk := "Ctrl+Shift+Space", "Ctrl+Alt+R"
	if goos == "darwin" {
		hk, rhk = "Cmd+Shift+Space", "Ctrl+Option+R"
	}
	return Settings{
		Hotkey:       hk,
		LeadTimes:    []time.Duration{time.Hour, 10 * time.Minute},
		DefaultTime:  "09:00",
		NextWeekDay:  "monday",
		NextWeekTime: "09:00",
		Autostart:    true,

		ShowPendingCount: true,
		ReplyHotkey:      rhk,
		WorkDays:         []string{"monday", "tuesday", "wednesday", "thursday", "friday"},
		EndOfDay:         "17:30",
		EndOfDaySweep:    true,
	}
}

// weekOrder is the canonical Monday..Sunday order of WorkDays.
var weekOrder = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

// canonDays returns days sorted Monday..Sunday, with duplicates kept and
// unknown names last (Validate rejects both).
func canonDays(days []string) []string {
	out := slices.Clone(days)
	rank := func(d string) int {
		if i := slices.Index(weekOrder, d); i >= 0 {
			return i
		}
		return len(weekOrder)
	}
	slices.SortStableFunc(out, func(a, b string) int { return rank(a) - rank(b) })
	return out
}

// IsWorkDay reports whether d is one of the working days.
func (s Settings) IsWorkDay(d time.Weekday) bool {
	for _, n := range s.WorkDays {
		if wd, ok := weekdays[n]; ok && wd == d {
			return true
		}
	}
	return false
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
	if _, err := parseClock(s.EndOfDay); err != nil {
		return fmt.Errorf("endOfDay: %w", err)
	}
	if len(s.WorkDays) == 0 {
		return fmt.Errorf("workDays: pick at least one working day")
	}
	seen := map[string]bool{}
	for _, d := range s.WorkDays {
		if _, ok := weekdays[d]; !ok {
			return fmt.Errorf("workDays: unknown weekday %q", d)
		}
		if seen[d] {
			return fmt.Errorf("workDays: %q listed twice", d)
		}
		seen[d] = true
	}
	rc, err := hotkey.ParseCombo(s.ReplyHotkey)
	if err != nil {
		return fmt.Errorf("replyHotkey: %w", err)
	}
	if cc, err := hotkey.ParseCombo(s.Hotkey); err == nil && cc.String() == rc.String() {
		return errors.New("Pick a different key from the capture hotkey")
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

// defaultReplyHotkey is the reply hotkey for settings saved without one: the
// platform default, or the Shift variant when the default would collide with
// the user's existing capture hotkey (compared as parsed combos).
func defaultReplyHotkey(goos, capture string) string {
	def, alt := "Ctrl+Alt+R", "Ctrl+Alt+Shift+R"
	if goos == "darwin" {
		def, alt = "Ctrl+Option+R", "Ctrl+Option+Shift+R"
	}
	dc, err1 := hotkey.ParseCombo(def)
	cc, err2 := hotkey.ParseCombo(capture)
	if err1 == nil && err2 == nil && dc.String() == cc.String() {
		return alt
	}
	return def
}
