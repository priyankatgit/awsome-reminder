package autostart

import (
	"strings"
	"testing"
)

func TestAutostartToggle(t *testing.T) {
	setupTest(t)
	exe := `C:\Apps\Awesome Reminder\awesome-reminder.exe`
	if runtimeIsUnix() {
		exe = "/opt/awesome reminder/awesome-reminder"
	}
	if on, err := Enabled(); err != nil || on {
		t.Fatalf("initially Enabled = %v, %v", on, err)
	}
	if err := Set(false, exe); err != nil { // absent: no error
		t.Fatalf("Set(false) when absent: %v", err)
	}
	if err := Set(true, exe); err != nil {
		t.Fatal(err)
	}
	if on, err := Enabled(); err != nil || !on {
		t.Fatalf("after Set(true) Enabled = %v, %v", on, err)
	}
	if e := readEntry(t); !strings.Contains(e, exe) {
		t.Fatalf("entry %q does not contain %q", e, exe)
	}
	if got, err := Target(); err != nil || got != exe {
		t.Fatalf("Target = %q, %v; want %q", got, err, exe)
	}
	if err := Set(true, exe); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := Set(false, exe); err != nil {
		t.Fatal(err)
	}
	if on, err := Enabled(); err != nil || on {
		t.Fatalf("after Set(false) Enabled = %v, %v", on, err)
	}
	if got, err := Target(); err != nil || got != "" {
		t.Fatalf("Target with no entry = %q, %v", got, err)
	}
}

// Target round-trips paths that need quoting or escaping in the entry.
func TestTargetRoundTrip(t *testing.T) {
	setupTest(t)
	paths := []string{`C:\Users\Zoë & Co\100% "fun"\awesome-reminder.exe`, `D:\a\b.exe`}
	if runtimeIsUnix() {
		paths = []string{"/opt/a b/100% \"fun\" $HOME `x` \\ <&>/awesome-reminder", "/usr/bin/awesome-reminder"}
	}
	for _, exe := range paths {
		if err := Set(true, exe); err != nil {
			t.Fatal(err)
		}
		if got, err := Target(); err != nil || got != exe {
			t.Errorf("Target = %q, %v; want %q", got, err, exe)
		}
	}
}
