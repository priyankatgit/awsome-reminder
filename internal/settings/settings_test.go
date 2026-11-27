package settings

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	want := Settings{
		Hotkey:           "Ctrl+Shift+Space",
		LeadTimes:        []time.Duration{time.Hour, 10 * time.Minute},
		DefaultTime:      "09:00",
		NextWeekDay:      "monday",
		NextWeekTime:     "09:00",
		ExportPath:       "",
		Autostart:        true,
		ShowPendingCount: true,
	}
	if got := Defaults("windows"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if got := Defaults("darwin").Hotkey; got != "Cmd+Shift+Space" {
		t.Fatalf("darwin hotkey %q", got)
	}
	if err := Defaults("linux").Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(s *Settings){
		"bad default time":  func(s *Settings) { s.DefaultTime = "25:00" },
		"bad nextweek time": func(s *Settings) { s.NextWeekTime = "9am" },
		"bad weekday":       func(s *Settings) { s.NextWeekDay = "funday" },
		"zero lead":         func(s *Settings) { s.LeadTimes = []time.Duration{0} },
		"negative lead":     func(s *Settings) { s.LeadTimes = []time.Duration{-time.Minute} },
		"lead > 7d":         func(s *Settings) { s.LeadTimes = []time.Duration{8 * 24 * time.Hour} },
		"six leads": func(s *Settings) {
			s.LeadTimes = []time.Duration{time.Minute, 2 * time.Minute, 3 * time.Minute, 4 * time.Minute, 5 * time.Minute, 6 * time.Minute}
		},
	}
	for name, mut := range cases {
		s := Defaults("windows")
		mut(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	s := Defaults("windows")
	s.LeadTimes = []time.Duration{7 * 24 * time.Hour}
	if err := s.Validate(); err != nil {
		t.Errorf("7d should be valid: %v", err)
	}
}

func TestParserConfig(t *testing.T) {
	s := Defaults("windows")
	s.DefaultTime = "08:30"
	s.NextWeekDay = "friday"
	s.NextWeekTime = "17:05"
	loc := time.UTC
	c := s.ParserConfig(loc)
	if c.Loc != loc || c.DefaultTime.Hour != 8 || c.DefaultTime.Minute != 30 ||
		c.NextWeekDay != time.Friday || c.NextWeekTime.Hour != 17 || c.NextWeekTime.Minute != 5 {
		t.Fatalf("%+v", c)
	}
}

func TestJSONShape(t *testing.T) {
	b, err := json.Marshal(Defaults("windows"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	leads := m["leadTimes"].([]any)
	if len(leads) != 2 || leads[0] != "1h0m0s" || leads[1] != "10m0s" {
		t.Fatalf("leadTimes = %v", leads)
	}
	var back Settings
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, Defaults("windows")) {
		t.Fatalf("round trip %+v", back)
	}
	if err := json.Unmarshal([]byte(`{"leadTimes":["bogus"]}`), &back); err == nil {
		t.Fatal("expected error for bad duration")
	}
}

// M7: a relative export path would resolve against the process's working
// directory (System32 when started at login).
func TestValidateExportPath(t *testing.T) {
	s := Defaults(runtime.GOOS)
	for _, p := range []string{"todo.md", `notes\todo.md`, "./todo.md"} {
		s.ExportPath = p
		err := s.Validate()
		if err == nil {
			t.Errorf("%q: expected error", p)
			continue
		}
		if !strings.HasPrefix(err.Error(), "exportPath: export path must be a full path, e.g. ") {
			t.Errorf("%q: error %q", p, err)
		}
	}
	for _, p := range []string{"", filepath.Join(t.TempDir(), "todo.md")} {
		s.ExportPath = p
		if err := s.Validate(); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
}

// Review Focus 4: settings saved before Phase 1.6 have no showPendingCount
// field; the badge is on for them.
func TestLoadSettingsMissingShowPendingCountDefaultsTrue(t *testing.T) {
	old := `{"hotkey":"Ctrl+Shift+Space","leadTimes":["1h0m0s"],"defaultTime":"09:00",` +
		`"nextWeekDay":"monday","nextWeekTime":"09:00","exportPath":"","autostart":false}`
	var s Settings
	if err := json.Unmarshal([]byte(old), &s); err != nil {
		t.Fatal(err)
	}
	if !s.ShowPendingCount {
		t.Fatal("ShowPendingCount = false for settings saved without the field")
	}
	if s.Autostart {
		t.Fatal("other fields must still load")
	}
	if !Defaults("windows").ShowPendingCount {
		t.Fatal("default ShowPendingCount = false")
	}
	// An explicit false survives a round trip.
	s.ShowPendingCount = false
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back Settings
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.ShowPendingCount {
		t.Fatalf("explicit false lost: %s", b)
	}
}
