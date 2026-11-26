// Package app wires the domain packages into the Wails application: the
// frontend-bound Service, the windows, and the startup sequence.
package app

import (
	"errors"
	"fmt"
	"log"
	"time"

	"awesome-reminder/internal/core"
	"awesome-reminder/internal/model"
	"awesome-reminder/internal/settings"
)

// Windows is what the Service needs from the window layer.
type Windows interface {
	ShowCapture()
	HideCapture()
	ResizeCapture(height int)
	OpenList(focusItemID string)
	OpenSettings()
	DismissAlerts()
}

// Hotkey is the subset of *hotkey.Manager the Service uses.
type Hotkey interface {
	Set(combo string) error
	Err() error
}

// Deps are the Service's collaborators. Export and StatusChanged may be nil.
type Deps struct {
	Core      *core.Core
	Windows   Windows
	Hotkey    Hotkey
	Autostart func(enabled bool) error
	Export    func()
	// StatusChanged is called after Status() may have changed (e.g. the
	// hotkey registration failed or recovered).
	StatusChanged func()
}

// Status is app health shown in the tray tooltip and the settings window.
type Status struct {
	HotkeyErr string `json:"hotkeyErr"` // "" when the hotkey is registered
}

// Service is bound to the frontend; its exported methods become bindings.
type Service struct {
	d Deps
}

// NewService creates a Service.
func NewService(d Deps) *Service {
	return &Service{d: d}
}

// Preview parses input without saving.
func (s *Service) Preview(input string) core.Preview { return s.d.Core.Preview(input) }

// WithDue returns input with its time replaced by due.
func (s *Service) WithDue(input string, due time.Time) string {
	return s.d.Core.WithDue(input, due)
}

// Capture stores a new item parsed from input.
func (s *Service) Capture(input string) (model.Item, error) { return s.d.Core.Capture(input) }

// EditText returns the editable one-line form of an item.
func (s *Service) EditText(id string) (string, error) { return s.d.Core.EditText(id) }

// Update re-parses input for an existing item.
func (s *Service) Update(id, input string) (model.Item, error) { return s.d.Core.Update(id, input) }

// Done completes an item (a recurring one advances instead).
func (s *Service) Done(id string) error {
	_, err := s.d.Core.Done(id)
	return err
}

// Reopen marks a done item open again.
func (s *Service) Reopen(id string) error { return s.d.Core.Reopen(id) }

// StopRepeating clears an item's recurrence.
func (s *Service) StopRepeating(id string) error { return s.d.Core.StopRepeating(id) }

// Snooze postpones a reminder: "10m", "1h" or "tomorrow".
func (s *Service) Snooze(id, preset string) error { return s.d.Core.Snooze(id, preset) }

// Delete tombstones an item.
func (s *Service) Delete(id string) error { return s.d.Core.Delete(id) }

// View returns the list-window data.
func (s *Service) View() (core.View, error) { return s.d.Core.View() }

// GetSettings returns the saved settings.
func (s *Service) GetSettings() (settings.Settings, error) { return s.d.Core.Settings() }

// SaveSettings validates and saves st, then applies it: re-registers the
// hotkey, sets autostart and triggers the export. Invalid settings return an
// error and change nothing. If the hotkey or autostart cannot be applied the
// settings stay saved, the remaining steps still run, and the error is
// returned (a hotkey error is also reported by Status).
func (s *Service) SaveSettings(st settings.Settings) error {
	if err := s.d.Core.SaveSettings(st); err != nil {
		return err
	}
	var errs []error
	if err := s.d.Hotkey.Set(st.Hotkey); err != nil {
		errs = append(errs, fmt.Errorf("hotkey: %w", err))
	}
	if s.d.StatusChanged != nil {
		s.d.StatusChanged()
	}
	if s.d.Autostart != nil {
		if err := s.d.Autostart(st.Autostart); err != nil {
			errs = append(errs, err)
		}
	}
	if s.d.Export != nil {
		s.d.Export()
	}
	err := errors.Join(errs...)
	if err != nil {
		log.Printf("settings: %v", err)
	}
	return err
}

// Status reports app health.
func (s *Service) Status() Status {
	var st Status
	if err := s.d.Hotkey.Err(); err != nil {
		st.HotkeyErr = err.Error()
	}
	return st
}

// HideCapture hides the capture window.
func (s *Service) HideCapture() { s.d.Windows.HideCapture() }

// ResizeCapture sets the capture window's height (CSS pixels) to fit its
// content.
func (s *Service) ResizeCapture(height int) { s.d.Windows.ResizeCapture(height) }

// OpenList shows the list window, focusing focusItemID if not "".
func (s *Service) OpenList(focusItemID string) { s.d.Windows.OpenList(focusItemID) }

// OpenSettings shows the settings window.
func (s *Service) OpenSettings() { s.d.Windows.OpenSettings() }

// DismissAlerts hides the alert window.
func (s *Service) DismissAlerts() { s.d.Windows.DismissAlerts() }
