package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLegacyVoiceDirUnderUserCache(t *testing.T) {
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
	dir, err := LegacyVoiceDir()
	if err != nil {
		t.Fatalf("LegacyVoiceDir: %v", err)
	}
	if !strings.HasPrefix(dir, tmp) || filepath.Base(dir) != "voice" || filepath.Base(filepath.Dir(dir)) != "awesome-reminder" {
		t.Errorf("LegacyVoiceDir = %q, want <cache>/awesome-reminder/voice under %q", dir, tmp)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("LegacyVoiceDir must not create %q", dir)
	}
}
