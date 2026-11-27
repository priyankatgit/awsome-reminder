package app

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"awesome-reminder/internal/autostart"
)

// autostartDecision decides at startup whether the login entry must be
// written so it matches the setting. want is settings.Autostart; has/hasErr
// come from autostart.Enabled() and target from autostart.Target() (the
// executable the entry starts; "" if unknown). An enabled entry that starts
// another executable than exe (the app was moved or reinstalled elsewhere) is
// rewritten. Executables under tmpDir (go run, dev builds) are never
// registered or unregistered.
func autostartDecision(want, has bool, hasErr error, target, exe, tmpDir string) (set bool, reason string) {
	if underDir(exe, tmpDir) {
		return false, "skipped: executable is in the temp dir"
	}
	if hasErr != nil {
		return true, "entry unreadable (" + hasErr.Error() + "), rewriting"
	}
	if has != want {
		return true, "out of sync, updating"
	}
	if want && !samePath(target, exe) {
		return true, "entry starts " + quoteOrUnknown(target) + ", updating"
	}
	return false, "already in sync"
}

func quoteOrUnknown(p string) string {
	if p == "" {
		return "an unknown program"
	}
	return `"` + p + `"`
}

// samePath reports whether a and b name the same file after resolving
// symlinks and short names (case-insensitively on Windows).
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, rb := resolve(a), resolve(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ra, rb)
	}
	return ra == rb
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

// syncAutostart makes the OS login entry match want and start this
// executable. It logs the outcome.
func syncAutostart(want bool) {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("autostart: executable path: %v", err)
		return
	}
	has, hasErr := autostart.Enabled()
	target, err := autostart.Target()
	if err != nil {
		log.Printf("autostart: %v", err)
	}
	set, reason := autostartDecision(want, has, hasErr, target, exe, os.TempDir())
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
