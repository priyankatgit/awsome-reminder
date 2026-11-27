//go:build linux

package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// baseDir overrides the XDG config directory (tests).
var baseDir string

func entryPath() string {
	dir := baseDir
	if dir == "" {
		dir = os.Getenv("XDG_CONFIG_HOME")
	}
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "autostart", "awesome-reminder.desktop")
}

// quoteExec quotes a path for the Exec key of a desktop entry.
func quoteExec(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`, `%`, `%%`)
	return `"` + r.Replace(p) + `"`
}

func Set(enabled bool, exePath string) error {
	p := entryPath()
	if !enabled {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("autostart: remove: %w", err)
		}
		return nil
	}
	entry := "[Desktop Entry]\nType=Application\nName=Awesome Reminder\nExec=" + quoteExec(exePath) +
		"\nX-GNOME-Autostart-enabled=true\nTerminal=false\n"
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	if err := os.WriteFile(p, []byte(entry), 0o644); err != nil {
		return fmt.Errorf("autostart: write: %w", err)
	}
	return nil
}

// unquoteExec reverses quoteExec for the first argument of an Exec value.
func unquoteExec(v string) string {
	if !strings.HasPrefix(v, `"`) {
		arg, _, _ := strings.Cut(v, " ")
		return strings.ReplaceAll(arg, "%%", "%")
	}
	var b strings.Builder
	for i := 1; i < len(v); i++ {
		switch c := v[i]; {
		case c == '"':
			return b.String()
		case c == '\\' && i+1 < len(v):
			i++
			b.WriteByte(v[i])
		case c == '%' && i+1 < len(v) && v[i+1] == '%':
			i++
			b.WriteByte('%')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// Target returns the executable path the desktop entry's Exec key starts;
// "" if there is no entry.
func Target() (string, error) {
	b, err := os.ReadFile(entryPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("autostart: read: %w", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Exec="); ok {
			return unquoteExec(v), nil
		}
	}
	return "", nil
}

func Enabled() (bool, error) {
	_, err := os.Stat(entryPath())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
