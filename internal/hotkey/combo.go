// Package hotkey parses hotkey combos like "Ctrl+Shift+Space" and manages a
// single global hotkey registration.
package hotkey

import (
	"fmt"
	"strings"
)

// Combo is a parsed hotkey in canonical form: Mods is a subset of
// Ctrl, Alt, Shift, Cmd (in that order) and Key is e.g. "Space", "K", "F12".
type Combo struct {
	Mods []string
	Key  string
}

var modOrder = []string{"Ctrl", "Alt", "Shift", "Cmd"}

var modAliases = map[string]string{
	"ctrl": "Ctrl", "control": "Ctrl",
	"alt": "Alt", "option": "Alt",
	"shift": "Shift",
	"cmd":   "Cmd", "command": "Cmd", "meta": "Cmd",
}

var keyAliases = map[string]string{
	"space": "Space", "enter": "Enter", "return": "Enter",
	"tab": "Tab", "escape": "Escape", "esc": "Escape",
}

// canonKey returns the canonical key name for s, or "" if unsupported.
// Supported: Space, A-Z, 0-9, F1-F12, Enter, Tab, Escape.
func canonKey(s string) string {
	l := strings.ToLower(s)
	if k, ok := keyAliases[l]; ok {
		return k
	}
	if len(l) == 1 {
		if c := l[0]; c >= 'a' && c <= 'z' {
			return strings.ToUpper(l)
		} else if c >= '0' && c <= '9' {
			return l
		}
	}
	if len(l) >= 2 && l[0] == 'f' {
		n := 0
		for _, c := range l[1:] {
			if c < '0' || c > '9' {
				return ""
			}
			n = n*10 + int(c-'0')
		}
		if n >= 1 && n <= 12 && l[1] != '0' {
			return fmt.Sprintf("F%d", n)
		}
	}
	return ""
}

// ParseCombo parses a case-insensitive, '+'-separated combo. It requires at
// least one modifier and exactly one key. It is OS-independent; whether a
// modifier is usable on the current OS is checked at registration time.
func ParseCombo(s string) (Combo, error) {
	if strings.TrimSpace(s) == "" {
		return Combo{}, fmt.Errorf("hotkey is empty")
	}
	have := map[string]bool{}
	var keys []string
	for _, part := range strings.Split(s, "+") {
		p := strings.TrimSpace(part)
		if p == "" {
			return Combo{}, fmt.Errorf("hotkey has an empty part")
		}
		if m, ok := modAliases[strings.ToLower(p)]; ok {
			have[m] = true
			continue
		}
		k := canonKey(p)
		if k == "" {
			return Combo{}, fmt.Errorf("unknown key %q", p)
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return Combo{}, fmt.Errorf("hotkey needs a key (for example Space or K)")
	}
	if len(keys) > 1 {
		return Combo{}, fmt.Errorf("hotkey can only have one key (got %s and %s)", keys[0], keys[1])
	}
	var mods []string
	for _, m := range modOrder {
		if have[m] {
			mods = append(mods, m)
		}
	}
	if len(mods) == 0 {
		return Combo{}, fmt.Errorf("hotkey needs at least one modifier (Ctrl, Alt, Shift)")
	}
	return Combo{Mods: mods, Key: keys[0]}, nil
}

// String returns the canonical form, e.g. "Ctrl+Shift+Space".
func (c Combo) String() string {
	return strings.Join(append(append([]string{}, c.Mods...), c.Key), "+")
}

// validateFor reports whether c is usable on the given GOOS.
func validateFor(goos string, c Combo) error {
	for _, m := range c.Mods {
		if m == "Cmd" && goos != "darwin" {
			return fmt.Errorf("Cmd is only available on macOS")
		}
	}
	return nil
}
