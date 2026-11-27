//go:build windows

package autostart

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const valueName = "AwesomeReminder"

// runKeyPath is a var so tests can point at a scratch key.
var runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

func Set(enabled bool, exePath string) error {
	if !enabled {
		k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("autostart: open run key: %w", err)
		}
		defer k.Close()
		if err := k.DeleteValue(valueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("autostart: remove: %w", err)
		}
		return nil
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("autostart: open run key: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue(valueName, `"`+exePath+`"`); err != nil {
		return fmt.Errorf("autostart: set: %w", err)
	}
	return nil
}

// Target returns the executable path the Run value starts, without the
// quotes Set adds; "" if there is no entry.
func Target() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("autostart: open run key: %w", err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue(valueName)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("autostart: read: %w", err)
	}
	if len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
		v = v[1 : len(v)-1]
	}
	return v, nil
}

func Enabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("autostart: open run key: %w", err)
	}
	defer k.Close()
	_, _, err = k.GetStringValue(valueName)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("autostart: read: %w", err)
	}
	return true, nil
}
