package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAutostartDecision(t *testing.T) {
	tmp := filepath.Join("C:", "Users", "me", "AppData", "Local", "Temp")
	exe := filepath.Join("C:", "Program Files", "Awesome Reminder", "awesome-reminder.exe")
	devExe := filepath.Join(tmp, "go-build123", "b001", "exe", "awesome-reminder.exe")
	cases := []struct {
		name      string
		want, has bool
		hasErr    error
		exe       string
		set       bool
	}{
		{"enabled matches", true, true, nil, exe, false},
		{"disabled matches", false, false, nil, exe, false},
		{"default on, entry missing", true, false, nil, exe, true},
		{"off, entry present", false, true, nil, exe, true},
		{"cannot read entry", true, false, errors.New("denied"), exe, true},
		{"temp dir build", true, false, nil, devExe, false},
		{"temp dir build, off", false, true, nil, devExe, false},
	}
	for _, c := range cases {
		set, reason := autostartDecision(c.want, c.has, c.hasErr, c.exe, tmp)
		if set != c.set || reason == "" {
			t.Errorf("%s: set=%v reason=%q, want set=%v", c.name, set, reason, c.set)
		}
	}
}

// go test binaries run from a go-build dir under the temp dir, like go run:
// the real os.Executable/os.TempDir pair must be detected as a dev build.
func TestAutostartSkipsRealTempExecutable(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !underDir(exe, os.TempDir()) {
		t.Skipf("test binary %s is not under %s (GOTMPDIR set?)", exe, os.TempDir())
	}
	if set, _ := autostartDecision(true, false, nil, exe, os.TempDir()); set {
		t.Fatal("dev build would be registered")
	}
}

func TestUnderDir(t *testing.T) {
	dir := t.TempDir()
	if !underDir(filepath.Join(dir, "a", "b.exe"), dir) {
		t.Error("child not under dir")
	}
	if underDir(filepath.Join(dir+"x", "b.exe"), dir) || underDir(filepath.Dir(dir), dir) || underDir("b.exe", "") {
		t.Error("false positive")
	}
}
