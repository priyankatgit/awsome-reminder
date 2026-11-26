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
	if err := Set(true, exe); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := Set(false, exe); err != nil {
		t.Fatal(err)
	}
	if on, err := Enabled(); err != nil || on {
		t.Fatalf("after Set(false) Enabled = %v, %v", on, err)
	}
}
