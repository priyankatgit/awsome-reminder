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
	TakePendingFocus() string
	OpenSettings()
	// ShowAlerts creates or reuses the alert window and shows it without
	// taking keyboard focus.
	ShowAlerts()
	ResizeAlerts(height int)
	// DismissAlerts hides the alert window.
	DismissAlerts()
}

// Hotkey is the subset of *hotkey.Manager the Service uses.
type Hotkey interface {
	Set(combo string) error
	Err() error
	Suspend()
	Resume() error
}

// Deps are the Service's collaborators. Export, StatusChanged and Emit may
// be nil.
type Deps struct {
	Core      *core.Core
	Windows   Windows
	Hotkey    Hotkey
	Autostart func(enabled bool) error
	Export    func()
	// StatusChanged is called after Status() may have changed (e.g. the
	// hotkey registration failed or recovered).
	StatusChanged func()
	// Emit sends an event to the frontend (EventAlertsShow).
	Emit func(name string, data any)
}

// Status is app health shown in the tray tooltip and the settings window.
type Status struct {
	HotkeyErr string `json:"hotkeyErr"` // "" when the hotkey is registered
}

// Service is bound to the frontend; its exported methods become bindings.
type Service struct {
	d      Deps
	alerts alertQueue
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

// Update re-parses input for an existing item. If that moves its due time
// (or changes its status) the item's alert cards are removed, since a "now"
// or "missed" card would be stale.
func (s *Service) Update(id, input string) (model.Item, error) {
	before, err := s.d.Core.Item(id)
	if err != nil {
		return s.d.Core.Update(id, input) // reports the missing item
	}
	after, err := s.d.Core.Update(id, input)
	if err != nil {
		return after, err
	}
	if !sameTime(before.DueAt, after.DueAt) || before.Status != after.Status {
		s.dropItemAlerts(id)
	}
	return after, nil
}

// sameTime reports whether a and b are both nil or the same instant.
func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// Done completes an item (a recurring one advances instead) and removes its
// alert cards. If a card is up for a later occurrence than the item's DueAt
// (the scheduler alerts ahead for an item left un-done), that occurrence is
// completed too.
func (s *Service) Done(id string) error {
	if _, err := s.d.Core.DoneThrough(id, s.latestCardDue(id)); err != nil {
		return err
	}
	s.dropItemAlerts(id)
	return nil
}

// Reopen marks a done item open again.
func (s *Service) Reopen(id string) error { return s.d.Core.Reopen(id) }

// StopRepeating clears an item's recurrence.
func (s *Service) StopRepeating(id string) error { return s.d.Core.StopRepeating(id) }

// Snooze postpones a reminder: "10m", "1h" or "tomorrow". Its alert cards
// are removed.
func (s *Service) Snooze(id, preset string) error {
	if err := s.d.Core.Snooze(id, preset); err != nil {
		return err
	}
	s.dropItemAlerts(id)
	return nil
}

// Delete tombstones an item and removes its alert cards.
func (s *Service) Delete(id string) error {
	if err := s.d.Core.Delete(id); err != nil {
		return err
	}
	s.dropItemAlerts(id)
	return nil
}

// Undelete undoes Delete, restoring the item's previous status and snooze
// (the list's Undo toast).
func (s *Service) Undelete(id string) error { return s.d.Core.Undelete(id) }

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

// TakePendingFocus returns the item ID the list window should scroll to
// ("" if none) and clears it. The list page calls it when it has loaded and
// whenever it receives EventListFocus.
func (s *Service) TakePendingFocus() string { return s.d.Windows.TakePendingFocus() }

// OpenSettings shows the settings window.
func (s *Service) OpenSettings() { s.d.Windows.OpenSettings() }

// SetHotkeyRecording releases the global hotkey while the settings window
// records a new combo (so pressing the current combo reaches the recorder
// instead of opening the capture box) and registers it again afterwards.
func (s *Service) SetHotkeyRecording(on bool) {
	if on {
		s.d.Hotkey.Suspend()
		return
	}
	if err := s.d.Hotkey.Resume(); err != nil {
		log.Printf("hotkey resume: %v", err)
	}
	if s.d.StatusChanged != nil {
		s.d.StatusChanged()
	}
}
