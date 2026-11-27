package app

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAutostartDecision(t *testing.T) {
	tmp := filepath.Join("C:", "Users", "me", "AppData", "Local", "Temp")
	exe := filepath.Join("C:", "Program Files", "Awesome Reminder", "awesome-reminder.exe")
	oldExe := filepath.Join("C:", "Users", "me", "Downloads", "awesome-reminder.exe")
	devExe := filepath.Join(tmp, "go-build123", "b001", "exe", "awesome-reminder.exe")
	cases := []struct {
		name      string
		want, has bool
		hasErr    error
		target    string
		exe       string
		set       bool
	}{
		{"enabled matches", true, true, nil, exe, exe, false},
		{"disabled matches", false, false, nil, "", exe, false},
		{"default on, entry missing", true, false, nil, "", exe, true},
		{"off, entry present", false, true, nil, exe, exe, true},
		{"cannot read entry", true, false, errors.New("denied"), "", exe, true},
		{"temp dir build", true, false, nil, "", devExe, false},
		{"temp dir build, off", false, true, nil, exe, devExe, false},
		{"enabled, entry starts an old path", true, true, nil, oldExe, exe, true},
		{"enabled, entry target unknown", true, true, nil, "", exe, true},
		{"off, entry present for an old path", false, true, nil, oldExe, exe, true},
		{"temp dir build, entry for another path", true, true, nil, exe, devExe, false},
	}
	for _, c := range cases {
		set, reason := autostartDecision(c.want, c.has, c.hasErr, c.target, c.exe, tmp)
		if set != c.set || reason == "" {
			t.Errorf("%s: set=%v reason=%q, want set=%v", c.name, set, reason, c.set)
		}
	}
}

// The registered path is compared after resolution, and case-insensitively
// on Windows.
func TestAutostartDecisionSamePathSpelledDifferently(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "App", "awesome-reminder.exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	same := []string{filepath.Join(dir, "App", ".", "awesome-reminder.exe")}
	if runtime.GOOS == "windows" {
		same = append(same, strings.ToUpper(exe))
	}
	for _, target := range same {
		if set, reason := autostartDecision(true, true, nil, target, exe, ""); set {
			t.Errorf("target %q: rewrite (%s)", target, reason)
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
	if set, _ := autostartDecision(true, false, nil, "", exe, os.TempDir()); set {
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
