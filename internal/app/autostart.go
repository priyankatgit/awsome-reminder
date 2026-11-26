package app

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"awesome-reminder/internal/autostart"
)

// autostartDecision decides at startup whether the login entry must be
// written so it matches the setting. want is settings.Autostart; has/hasErr
// come from autostart.Enabled(). Executables under tmpDir (go run, dev
// builds) are never registered or unregistered.
func autostartDecision(want, has bool, hasErr error, exe, tmpDir string) (set bool, reason string) {
	if underDir(exe, tmpDir) {
		return false, "skipped: executable is in the temp dir"
	}
	if hasErr != nil {
		return true, "entry unreadable (" + hasErr.Error() + "), rewriting"
	}
	if has == want {
		return false, "already in sync"
	}
	return true, "out of sync, updating"
}

// underDir reports whether path is inside dir (case-insensitive on Windows).
// Both are resolved first so a short (8.3) or symlinked TEMP still matches.
func underDir(path, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(resolve(dir), resolve(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// syncAutostart makes the OS login entry match want. It logs the outcome.
func syncAutostart(want bool) {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("autostart: executable path: %v", err)
		return
	}
	has, hasErr := autostart.Enabled()
	set, reason := autostartDecision(want, has, hasErr, exe, os.TempDir())
	if !set {
		log.Printf("autostart: %s (setting=%v, registered=%v)", reason, want, has)
		return
	}
	if err := autostart.Set(want, exe); err != nil {
		log.Printf("autostart: %s: set %v failed: %v", reason, want, err)
		return
	}
	log.Printf("autostart: %s: set %v for %s", reason, want, exe)
}

// resolve returns p with its longest existing prefix resolved (symlinks and
// Windows short names), so paths that do not exist yet still compare.
func resolve(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for cur := p; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
