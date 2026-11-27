package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDataDirUnderUserConfig(t *testing.T) {
	tmp := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", tmp)
	case "darwin":
		t.Setenv("HOME", tmp)
	default:
		t.Setenv("XDG_CONFIG_HOME", tmp)
		t.Setenv("HOME", tmp)
	}

	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if filepath.Base(dir) != "awesome-reminder" {
		t.Errorf("DataDir = %q, want suffix awesome-reminder", dir)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Errorf("DataDir %q should exist as a directory (err=%v)", dir, err)
	}

	db, err := DBPath()
	if err != nil {
		t.Fatalf("DBPath: %v", err)
	}
	if filepath.Base(db) != "awesome.db" || filepath.Dir(db) != dir {
		t.Errorf("DBPath = %q, want awesome.db inside %q", db, dir)
	}

	lg, err := LogPath()
	if err != nil {
		t.Fatalf("LogPath: %v", err)
	}
	if filepath.Base(lg) != "awesome.log" || filepath.Dir(lg) != dir {
		t.Errorf("LogPath = %q, want awesome.log inside %q", lg, dir)
	}
}

func TestVoiceDirUnderUserCache(t *testing.T) {
	tmp := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("LOCALAPPDATA", tmp)
	case "darwin":
		t.Setenv("HOME", tmp)
	default:
		t.Setenv("XDG_CACHE_HOME", tmp)
		t.Setenv("HOME", tmp)
	}
	dir, err := VoiceDir()
	if err != nil {
		t.Fatalf("VoiceDir: %v", err)
	}
	if !strings.HasPrefix(dir, tmp) || filepath.Base(dir) != "voice" || filepath.Base(filepath.Dir(dir)) != "awesome-reminder" {
		t.Errorf("VoiceDir = %q, want <cache>/awesome-reminder/voice under %q", dir, tmp)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Errorf("VoiceDir should exist (err=%v)", err)
	}
}
