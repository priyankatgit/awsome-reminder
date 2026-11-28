package settings

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
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
		ReplyHotkey:      "Ctrl+Alt+R",
		WorkDays:         []string{"monday", "tuesday", "wednesday", "thursday", "friday"},
		EndOfDay:         "17:30",
		EndOfDaySweep:    true,
		PushServer:       "https://ntfy.sh",
		PushReplies:      true,
		PushGrace:        2 * time.Minute,
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

func TestWithSharedKeepsLocalFields(t *testing.T) {
	s := Defaults("windows")
	s.Hotkey = "Ctrl+Alt+K"
	s.ExportPath = `C:\todo.md`
	s.Autostart = false
	s.ShowPendingCount = false
	p := Shared{LeadTimes: []time.Duration{2 * time.Hour}, DefaultTime: "08:30", NextWeekDay: "friday", NextWeekTime: "07:15"}
	got := s.WithShared(p)
	if got.Hotkey != "Ctrl+Alt+K" || got.ExportPath != `C:\todo.md` || got.Autostart || got.ShowPendingCount {
		t.Fatalf("local fields changed: %+v", got)
	}
	if !got.Shared().Equal(p) {
		t.Fatalf("shared not replaced: %+v", got.Shared())
	}
	if s.Shared().Equal(p) {
		t.Fatal("Equal should differ")
	}
}

func TestSettingsJSONMissingSharedStamp(t *testing.T) {
	var s Settings
	old := `{"hotkey":"Ctrl+Shift+Space","leadTimes":["1h0m0s"],"defaultTime":"09:00","nextWeekDay":"monday","nextWeekTime":"09:00","exportPath":"","autostart":true,"showPendingCount":true}`
	if err := json.Unmarshal([]byte(old), &s); err != nil {
		t.Fatal(err)
	}
	if !s.SharedUpdatedAt.IsZero() || s.SharedUpdatedBy != "" {
		t.Fatalf("got %v %q", s.SharedUpdatedAt, s.SharedUpdatedBy)
	}
	s.SharedUpdatedAt = time.UnixMilli(1764000000123)
	s.SharedUpdatedBy = "dev1"
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"sharedUpdatedAt":1764000000123`) {
		t.Fatalf("json %s", b)
	}
	var back Settings
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !back.SharedUpdatedAt.Equal(s.SharedUpdatedAt) || back.SharedUpdatedBy != "dev1" {
		t.Fatalf("round trip %v %q", back.SharedUpdatedAt, back.SharedUpdatedBy)
	}
}

func TestDefaultsReplySettings(t *testing.T) {
	w := Defaults("windows")
	if w.ReplyHotkey != "Ctrl+Alt+R" {
		t.Fatalf("windows reply hotkey %q", w.ReplyHotkey)
	}
	if got := Defaults("darwin").ReplyHotkey; got != "Ctrl+Option+R" {
		t.Fatalf("darwin reply hotkey %q", got)
	}
	if !reflect.DeepEqual(w.WorkDays, []string{"monday", "tuesday", "wednesday", "thursday", "friday"}) {
		t.Fatalf("work days %v", w.WorkDays)
	}
	if w.EndOfDay != "17:30" || !w.EndOfDaySweep {
		t.Fatalf("eod %q sweep %v", w.EndOfDay, w.EndOfDaySweep)
	}
	if err := Defaults("darwin").Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSettingsMissingReplyFieldsGetDefaults(t *testing.T) {
	old := `{"hotkey":"Ctrl+Shift+Space","leadTimes":["1h0m0s"],"defaultTime":"09:00","nextWeekDay":"monday","nextWeekTime":"09:00","exportPath":"","autostart":true,"showPendingCount":true}`
	var s Settings
	if err := json.Unmarshal([]byte(old), &s); err != nil {
		t.Fatal(err)
	}
	d := Defaults(runtime.GOOS)
	if s.ReplyHotkey != d.ReplyHotkey || !reflect.DeepEqual(s.WorkDays, d.WorkDays) || s.EndOfDay != "17:30" || !s.EndOfDaySweep {
		t.Fatalf("got %+v", s)
	}
	// Explicit values survive a round trip, including sweep=false.
	s.EndOfDaySweep = false
	s.EndOfDay = "16:00"
	s.WorkDays = []string{"sunday", "monday"}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back Settings
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.EndOfDaySweep || back.EndOfDay != "16:00" || !reflect.DeepEqual(back.WorkDays, []string{"monday", "sunday"}) {
		t.Fatalf("round trip %+v (%s)", back, b)
	}
}

func TestValidateReplyHotkeySameAsCapture(t *testing.T) {
	s := Defaults("windows")
	s.ReplyHotkey = "ctrl+shift+space"
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "Pick a different key from the capture hotkey") {
		t.Fatalf("got %v", err)
	}
	s = Defaults("windows")
	s.Hotkey, s.ReplyHotkey = "Ctrl+Alt+R", "ctrl+alt+r"
	if err := s.Validate(); err == nil {
		t.Fatal("normalised equal combos accepted")
	}
	s.ReplyHotkey = "nonsense"
	if err := s.Validate(); err == nil {
		t.Fatal("bad combo accepted")
	}
}

func TestValidateEndOfDayAndWorkDays(t *testing.T) {
	cases := map[string]func(s *Settings){
		"bad eod":   func(s *Settings) { s.EndOfDay = "25:00" },
		"no days":   func(s *Settings) { s.WorkDays = []string{} },
		"nil days":  func(s *Settings) { s.WorkDays = nil },
		"bad day":   func(s *Settings) { s.WorkDays = []string{"funday"} },
		"duplicate": func(s *Settings) { s.WorkDays = []string{"monday", "monday"} },
	}
	for name, mut := range cases {
		s := Defaults("windows")
		mut(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSharedIncludesReplySchedule(t *testing.T) {
	s := Defaults("windows")
	base := s.Shared()
	for name, mut := range map[string]func(*Settings){
		"eod":   func(s *Settings) { s.EndOfDay = "18:00" },
		"days":  func(s *Settings) { s.WorkDays = []string{"monday"} },
		"sweep": func(s *Settings) { s.EndOfDaySweep = false },
	} {
		c := s
		mut(&c)
		if c.Shared().Equal(base) {
			t.Errorf("%s change not in Shared", name)
		}
	}
	c := s
	c.ReplyHotkey = "Ctrl+Alt+Y"
	if !c.Shared().Equal(base) {
		t.Error("ReplyHotkey must not be shared")
	}
	p := Shared{LeadTimes: []time.Duration{time.Hour}, DefaultTime: "09:00", NextWeekDay: "monday", NextWeekTime: "09:00",
		WorkDays: []string{"saturday"}, EndOfDay: "12:00", EndOfDaySweep: false}
	got := s.WithShared(p)
	if got.EndOfDay != "12:00" || got.EndOfDaySweep || !reflect.DeepEqual(got.WorkDays, []string{"saturday"}) || got.ReplyHotkey != s.ReplyHotkey {
		t.Fatalf("WithShared %+v", got)
	}
}

func TestIsWorkDay(t *testing.T) {
	s := Defaults("windows")
	if !s.IsWorkDay(time.Wednesday) || s.IsWorkDay(time.Saturday) || s.IsWorkDay(time.Sunday) {
		t.Fatal("default work days wrong")
	}
	s.WorkDays = []string{"sunday"}
	if !s.IsWorkDay(time.Sunday) || s.IsWorkDay(time.Monday) {
		t.Fatal("custom work days wrong")
	}
}

func TestMissingReplyHotkeyAvoidsCaptureCollision(t *testing.T) {
	old := `{"hotkey":"ctrl+alt+r","leadTimes":["1h0m0s"],"defaultTime":"09:00","nextWeekDay":"monday","nextWeekTime":"09:00","exportPath":"","autostart":true,"showPendingCount":true}`
	var s Settings
	if err := json.Unmarshal([]byte(old), &s); err != nil {
		t.Fatal(err)
	}
	want := "Ctrl+Alt+Shift+R"
	if runtime.GOOS == "darwin" {
		want = "Ctrl+Option+Shift+R"
		s.Hotkey = "Ctrl+Option+R"
	}
	if runtime.GOOS == "darwin" {
		_ = json.Unmarshal([]byte(strings.Replace(old, "ctrl+alt+r", "Ctrl+Option+R", 1)), &s)
	}
	if s.ReplyHotkey != want {
		t.Fatalf("reply hotkey %q want %q", s.ReplyHotkey, want)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := defaultReplyHotkey("darwin", "Ctrl+Option+R"); got != "Ctrl+Option+Shift+R" {
		t.Fatalf("darwin collision %q", got)
	}
	if got := defaultReplyHotkey("darwin", "Cmd+Shift+Space"); got != "Ctrl+Option+R" {
		t.Fatalf("darwin default %q", got)
	}
	if got := defaultReplyHotkey("windows", "Ctrl+Shift+Space"); got != "Ctrl+Alt+R" {
		t.Fatalf("windows default %q", got)
	}
}

// WorkDays order does not make two Shared values differ, so saving the same
// days in another order does not re-stamp the shared settings.
func TestSharedEqualIgnoresWorkDaysOrder(t *testing.T) {
	p := Defaults("windows").Shared()
	q := p
	q.WorkDays = []string{"friday", "monday", "wednesday", "tuesday", "thursday"}
	if !p.Equal(q) {
		t.Errorf("Equal false for the same days in another order")
	}
	q.WorkDays = []string{"monday", "tuesday"}
	if p.Equal(q) {
		t.Errorf("Equal true for different days")
	}
}

func TestDefaultsPush(t *testing.T) {
	d := Defaults("windows")
	if d.PushEnabled || d.PushServer != "https://ntfy.sh" || d.PushTopic != "" || d.PushGrace != 2*time.Minute {
		t.Fatalf("push defaults: %+v", d)
	}
}

func TestValidatePush(t *testing.T) {
	cases := []struct {
		name string
		mut  func(s *Settings)
		bad  bool
	}{
		{"defaults", func(s *Settings) {}, false},
		{"ftp server", func(s *Settings) { s.PushServer = "ftp://x" }, true},
		{"empty server", func(s *Settings) { s.PushServer = "" }, true},
		{"trailing slash ok", func(s *Settings) { s.PushServer = "https://ntfy.sh/" }, false},
		{"topic with space", func(s *Settings) { s.PushTopic = "has space" }, true},
		{"topic 64 ok", func(s *Settings) { s.PushTopic = strings.Repeat("a", 64) }, false},
		{"topic 65", func(s *Settings) { s.PushTopic = strings.Repeat("a", 65) }, true},
		{"grace 3m", func(s *Settings) { s.PushGrace = 3 * time.Minute }, true},
		{"grace 0 ok", func(s *Settings) { s.PushGrace = 0 }, false},
		{"enabled without topic", func(s *Settings) { s.PushEnabled = true }, true},
		{"enabled with topic", func(s *Settings) { s.PushEnabled, s.PushTopic = true, "ar-x" }, false},
	}
	for _, c := range cases {
		s := Defaults("windows")
		c.mut(&s)
		if err := s.Validate(); (err != nil) != c.bad {
			t.Errorf("%s: err=%v, want bad=%v", c.name, err, c.bad)
		}
	}
}

func TestNewPushTopic(t *testing.T) {
	re := regexp.MustCompile(`^ar-[a-z2-7]{26}$`)
	a, b := NewPushTopic(), NewPushTopic()
	if !re.MatchString(a) || !re.MatchString(b) || a == b {
		t.Fatalf("topics %q %q", a, b)
	}
}

func TestUnmarshalOldSettingsPushDefaults(t *testing.T) {
	var s Settings
	old := `{"hotkey":"Ctrl+Shift+Space","leadTimes":["1h0m0s"],"defaultTime":"09:00","nextWeekDay":"monday","nextWeekTime":"09:00"}`
	if err := json.Unmarshal([]byte(old), &s); err != nil {
		t.Fatal(err)
	}
	if s.PushEnabled || s.PushServer != DefaultPushServer || s.PushGrace != 2*time.Minute || !s.PushReplies || s.PushTopic != "" {
		t.Fatalf("push defaults: %+v", s)
	}
}

func TestPushJSONRoundTrip(t *testing.T) {
	s := Defaults("windows")
	s.PushEnabled, s.PushTopic, s.PushHideText, s.PushReplies, s.PushGrace = true, "ar-abc", true, false, 10*time.Minute
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"pushGrace":"10m0s"`) {
		t.Fatalf("grace not a duration string: %s", b)
	}
	var back Settings
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, s) {
		t.Fatalf("got %+v want %+v", back, s)
	}
}

func TestSharedRoundTripPush(t *testing.T) {
	s := Defaults("windows")
	s.PushEnabled, s.PushServer, s.PushTopic, s.PushReplies, s.PushHideText, s.PushGrace =
		true, "https://ntfy.example.com/x", "ar-abc", false, true, 5*time.Minute
	got := Defaults("windows").WithShared(s.Shared())
	if got.PushEnabled != true || got.PushServer != s.PushServer || got.PushTopic != "ar-abc" ||
		got.PushReplies || !got.PushHideText || got.PushGrace != 5*time.Minute {
		t.Fatalf("push fields lost: %+v", got)
	}
	o := s
	o.PushTopic = "ar-other"
	if s.Shared().Equal(o.Shared()) {
		t.Fatal("Equal ignores the topic")
	}
}
