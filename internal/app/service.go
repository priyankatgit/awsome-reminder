// Package app wires the domain packages into the Wails application: the
// frontend-bound Service, the windows, and the startup sequence.
package app

import (
	"errors"
	"fmt"
	"log"
	"strings"
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
	// WidenList widens the open list window to minWidth (capped to the
	// screen) while the detail panel is open; RestoreListWidth undoes it.
	WidenList(minWidth int)
	RestoreListWidth()
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
	// OpenURL opens a link in the system browser (OpenURL binding).
	OpenURL func(url string) error
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
	return s.dropIfMoved(id, func() (model.Item, error) { return s.d.Core.Update(id, input) })
}

// dropIfMoved runs edit on item id and, like Update, removes the item's
// alert cards when the edit moved its due time or changed its status.
func (s *Service) dropIfMoved(id string, edit func() (model.Item, error)) (model.Item, error) {
	before, err := s.d.Core.Item(id)
	if err != nil {
		return edit() // reports the missing item
	}
	after, err := edit()
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
// alert cards.
func (s *Service) Done(id string) error {
	if _, err := s.d.Core.Done(id); err != nil {
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

// SetText replaces an item's text verbatim (no re-parse).
func (s *Service) SetText(id, text string) (model.Item, error) { return s.d.Core.SetText(id, text) }

// SetPriority sets an item's priority: "", "low", "medium" or "high".
func (s *Service) SetPriority(id, p string) (model.Item, error) {
	return s.d.Core.SetPriority(id, p)
}

// SetDue sets or (nil) clears an item's due time. Like Update, moving the
// due time removes the item's alert cards.
func (s *Service) SetDue(id string, due *time.Time) (model.Item, error) {
	return s.dropIfMoved(id, func() (model.Item, error) { return s.d.Core.SetDue(id, due) })
}

// SetRepeat sets an item's recurrence: "none", "daily", "weekdays" or
// "weekly". It keeps the due time today; it shares Update's card rule so a
// future change that moves the due time drops stale cards.
func (s *Service) SetRepeat(id, repeat string) (model.Item, error) {
	return s.dropIfMoved(id, func() (model.Item, error) { return s.d.Core.SetRepeat(id, repeat) })
}

// SetTags replaces an item's tags.
func (s *Service) SetTags(id string, tags []string) (model.Item, error) {
	return s.d.Core.SetTags(id, tags)
}

// SetDescription saves an item's Markdown description.
func (s *Service) SetDescription(id, md string) (model.Item, error) {
	return s.d.Core.SetDescription(id, md)
}

// AddAttachment stores a WebP image for an item.
func (s *Service) AddAttachment(itemID string, data []byte) (model.Attachment, error) {
	return s.d.Core.AddAttachment(itemID, data)
}

// ItemDetail returns an item with its attachments.
func (s *Service) ItemDetail(id string) (core.Detail, error) { return s.d.Core.ItemDetail(id) }

// WidenList widens the list window for the detail panel.
func (s *Service) WidenList(minWidth int) { s.d.Windows.WidenList(minWidth) }

// RestoreListWidth undoes WidenList.
func (s *Service) RestoreListWidth() { s.d.Windows.RestoreListWidth() }

// OpenURL opens a link in the system browser. Only http, https and mailto
// links are allowed.
func (s *Service) OpenURL(url string) error {
	if !allowedLink(url) {
		return errors.New("unsupported link")
	}
	return s.d.OpenURL(url)
}

func allowedLink(url string) bool {
	l := strings.ToLower(url)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "mailto:")
}
