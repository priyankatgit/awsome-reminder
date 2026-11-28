package paths

import (
	"runtime"
	"testing"
)

// isolate points the config and cache dirs at fresh temp dirs and returns them.
func isolate(t *testing.T) (cfgBase, cacheBase string) {
	t.Helper()
	cfg, cache := t.TempDir(), t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", cfg)
		t.Setenv("LOCALAPPDATA", cache)
	case "darwin":
		t.Setenv("HOME", cfg)
		cache = cfg
	default:
		t.Setenv("XDG_CONFIG_HOME", cfg)
		t.Setenv("XDG_CACHE_HOME", cache)
		t.Setenv("HOME", cfg)
	}
	return cfg, cache
}
