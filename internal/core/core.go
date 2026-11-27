// Package core holds the domain operations (capture, done, snooze, view)
// shared by the scheduler and the UI service. It is plain Go, no Wails.
package core

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/parser"
	"awesome-reminder/internal/settings"
	"awesome-reminder/internal/store"
)

// ErrEmpty is returned when the input has no text left after parsing.
var ErrEmpty = errors.New("empty input")

// Core implements the domain operations on top of a store.
type Core struct {
	st  *store.Store
	now func() time.Time
	loc *time.Location

	// opMu serializes every mutation, including its read-modify-write, since
	// the UI service and the scheduler call Core concurrently. OnChange
	// listeners run after it is released.
	opMu sync.Mutex

	mu       sync.Mutex
	onChange func()
}

// New creates a Core. loc is the user's local zone, used to parse input and
// as the zone recorded on newly captured items.
func New(st *store.Store, now func() time.Time, loc *time.Location) *Core {
	if loc == nil {
		loc = time.Local
	}
	return &Core{st: st, now: now, loc: loc}
}

// OnChange registers fn, called after every successful mutation. The last
// registration wins.
func (c *Core) OnChange(fn func()) {
	c.mu.Lock()
	c.onChange = fn
	c.mu.Unlock()
}

func (c *Core) changed() {
	c.mu.Lock()
	fn := c.onChange
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// settings loads current settings, falling back to defaults if the stored
// ones are invalid so parsing always has a usable configuration.
func (c *Core) settings() (settings.Settings, error) {
	s, err := c.st.LoadSettings(runtime.GOOS)
	if err != nil {
		return s, err
	}
	if s.Validate() != nil {
		return settings.Defaults(runtime.GOOS), nil
	}
	return s, nil
}

func (c *Core) parse(input string) (parser.Result, settings.Settings, error) {
	s, err := c.settings()
	if err != nil {
		return parser.Result{}, s, err
	}
	return parser.Parse(input, c.now().In(c.loc), s.ParserConfig(c.loc)), s, nil
}

// itemLoc is the zone of an item, falling back to the core's zone.
func (c *Core) itemLoc(it model.Item) *time.Location {
	if it.TZ != "" {
		if l, err := time.LoadLocation(it.TZ); err == nil {
			return l
		}
	}
	return c.loc
}

func withInbox(tags []string) []string {
	if len(tags) == 0 {
		return []string{inboxTag}
	}
	return tags
}

func kindFor(due *time.Time) model.Kind {
	if due != nil {
		return model.KindReminder
	}
	return model.KindTodo
}

// Preview parses input without saving anything.
func (c *Core) Preview(input string) Preview {
	r, s, err := c.parse(input)
	if err != nil {
		return Preview{Kind: model.KindTodo, Text: input, Tags: []string{inboxTag}, Alerts: []string{}, Warnings: []string{err.Error()}}
	}
	p := Preview{
		Kind: kindFor(r.DueAt), Text: r.Text, Tags: withInbox(r.Tags), DueAt: r.DueAt, RRule: r.RRule,
		Alerts: []string{}, Warnings: r.Warnings,
	}
	if p.Warnings == nil {
		p.Warnings = []string{}
	}
	if r.DueAt != nil {
		p.Human = humanDue(*r.DueAt, r.RRule, c.loc)
		p.Alerts = alertLabels(*r.DueAt, c.now(), s.LeadTimes)
	}
	return p
}

// WithDue re-renders input with its time replaced by due (tags, text and
// recurrence are kept). A to-do becomes a reminder at due.
func (c *Core) WithDue(input string, due time.Time) string {
	r, _, err := c.parse(input)
	if err != nil {
		return input
	}
	d := due.UTC()
	return parser.Render(r.Text, r.Tags, &d, r.RRule, c.loc)
}

// Capture parses input and stores a new open item.
func (c *Core) Capture(input string) (model.Item, error) {
	r, _, err := c.parse(input)
	if err != nil {
		return model.Item{}, err
	}
	if r.Text == "" {
		return model.Item{}, ErrEmpty
	}
	now := c.now().UTC()
	it := model.Item{
		ID: ulid.Make().String(), Kind: kindFor(r.DueAt), Text: r.Text, Tags: withInbox(r.Tags),
		DueAt: r.DueAt, TZ: c.loc.String(), RRule: r.RRule, Status: model.StatusOpen,
		CreatedAt: now,
	}
	if err := c.write(func() error { return c.save(&it, now) }); err != nil {
		return model.Item{}, err
	}
	return it, nil
}

// write runs fn under the operation lock and, if it succeeded, notifies
// listeners after the lock is released.
func (c *Core) write(fn func() error) error {
	c.opMu.Lock()
	err := fn()
	c.opMu.Unlock()
	if err != nil {
		return err
	}
	c.changed()
	return nil
}

// save stamps the update metadata and stores the item. Callers hold opMu.
func (c *Core) save(it *model.Item, now time.Time) error {
	it.UpdatedAt = now.UTC()
	it.UpdatedBy = c.st.DeviceID()
	return c.st.Put(*it)
}

// mutate loads an item, applies fn, and saves it, all under the operation
// lock.
func (c *Core) mutate(id string, fn func(it *model.Item, now time.Time) error) (model.Item, error) {
	var it model.Item
	err := c.write(func() error {
		var err error
		if it, err = c.st.Get(id); err != nil {
			return fmt.Errorf("item %s: %w", id, err)
		}
		now := c.now().UTC()
		if err := fn(&it, now); err != nil {
			return err
		}
		return c.save(&it, now)
	})
	if err != nil {
		return model.Item{}, err
	}
	return it, nil
}

// EditText returns the editable one-line form of an item. The implicit
// inbox tag is left out when it is the only tag; Update adds it back.
func (c *Core) EditText(id string) (string, error) {
	it, err := c.st.Get(id)
	if err != nil {
		return "", fmt.Errorf("item %s: %w", id, err)
	}
	tags := it.Tags
	if len(tags) == 1 && tags[0] == inboxTag {
		tags = nil
	}
	return parser.Render(it.Text, tags, it.DueAt, it.RRule, c.itemLoc(it)), nil
}

// Update re-parses input for an existing item. ID, CreatedAt, LeadTimes, TZ
// and Status are kept; any snooze is cleared.
func (c *Core) Update(id, input string) (model.Item, error) {
	r, _, err := c.parse(input)
	if err != nil {
		return model.Item{}, err
	}
	if r.Text == "" {
		return model.Item{}, ErrEmpty
	}
	return c.mutate(id, func(it *model.Item, _ time.Time) error {
		it.Kind, it.Text, it.Tags = kindFor(r.DueAt), r.Text, withInbox(r.Tags)
		it.DueAt, it.RRule, it.SnoozeUntil = r.DueAt, r.RRule, nil
		return nil
	})
}

// Done completes an item. A recurring item instead advances DueAt to its next
// occurrence after now and stays open.
func (c *Core) Done(id string) (model.Item, error) {
	return c.mutate(id, func(it *model.Item, now time.Time) error {
		done := now
		it.DoneAt, it.SnoozeUntil = &done, nil
		if it.RRule != "" && it.DueAt != nil {
			next, err := nextOccurrence(it.RRule, *it.DueAt, now, c.itemLoc(*it))
			if err != nil {
				return err
			}
			it.DueAt = &next
			return nil
		}
		it.Status = model.StatusDone
		return nil
	})
}

// Reopen marks a done item open again.
func (c *Core) Reopen(id string) error {
	_, err := c.mutate(id, func(it *model.Item, _ time.Time) error {
		it.Status, it.DoneAt, it.SnoozeUntil = model.StatusOpen, nil, nil
		return nil
	})
	return err
}

// StopRepeating clears the recurrence; the item keeps its current due time.
func (c *Core) StopRepeating(id string) error {
	_, err := c.mutate(id, func(it *model.Item, _ time.Time) error {
		it.RRule = ""
		return nil
	})
	return err
}

// Snooze postpones a reminder: "10m", "1h" or "tomorrow" (tomorrow at the
// default time, in the item's zone).
func (c *Core) Snooze(id, preset string) error {
	s, err := c.settings()
	if err != nil {
		return err
	}
	_, err = c.mutate(id, func(it *model.Item, now time.Time) error {
		if it.Kind != model.KindReminder {
			return errors.New("only reminders can be snoozed")
		}
		var until time.Time
		switch strings.TrimSpace(preset) {
		case "10m":
			until = now.Add(10 * time.Minute)
		case "1h":
			until = now.Add(time.Hour)
		case "tomorrow":
			cfg := s.ParserConfig(c.loc)
			l := c.itemLoc(*it)
			t := now.In(l).AddDate(0, 0, 1)
			until = time.Date(t.Year(), t.Month(), t.Day(), cfg.DefaultTime.Hour, cfg.DefaultTime.Minute, 0, 0, l)
		default:
			return fmt.Errorf("unknown snooze preset %q", preset)
		}
		until = until.UTC()
		it.SnoozeUntil = &until
		return nil
	})
	return err
}

// Delete tombstones an item (status=deleted); the row is kept.
func (c *Core) Delete(id string) error {
	_, err := c.mutate(id, func(it *model.Item, _ time.Time) error {
		it.Status = model.StatusDeleted
		return nil
	})
	return err
}

// View builds the main-window data.
func (c *Core) View() (View, error) {
	open, err := c.st.ListOpen()
	if err != nil {
		return View{}, err
	}
	done, err := c.st.ListDone(doneLimit)
	if err != nil {
		return View{}, err
	}
	return buildView(open, done, c.now()), nil
}

// Item returns the stored item with the given ID.
func (c *Core) Item(id string) (model.Item, error) { return c.st.Get(id) }

// Settings returns the saved settings (defaults if none saved).
func (c *Core) Settings() (settings.Settings, error) {
	return c.st.LoadSettings(runtime.GOOS)
}

// SaveSettings validates and saves the settings.
func (c *Core) SaveSettings(s settings.Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	return c.write(func() error { return c.st.SaveSettings(s) })
}
