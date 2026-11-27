package app

import (
	"log"
	"slices"
	"sync"
	"time"

	"awesome-reminder/internal/scheduler"
)

// AlertCard is one alert as the alert window shows it: the scheduler's alert
// plus the item details the card displays.
type AlertCard struct {
	Key       string        `json:"key"` // scheduler.Alert.Key; unique in the queue
	ItemID    string        `json:"itemId"`
	Text      string        `json:"text"`
	DueAt     time.Time     `json:"dueAt"`
	Lead      time.Duration `json:"lead"`   // nanoseconds; 0 = at due time
	Missed    bool          `json:"missed"` // fired more than 2 min late
	Tags      []string      `json:"tags"`   // never nil
	Recurring bool          `json:"recurring"`
	Snoozed   bool          `json:"snoozed"` // fired at the end of a snooze
}

// alertQueue holds the cards the alert window shows, oldest first. Its mutex
// is also held across the window calls and the event that follow a change,
// so the window state always matches the queue (a concurrent dismiss can't
// hide the window after an alert was added). Nothing on the main thread takes
// it, so waiting there for the main thread cannot deadlock.
type alertQueue struct {
	mu    sync.Mutex
	cards []AlertCard
}

// card enriches a scheduler alert with the item's tags and recurrence. An
// item that can't be read still gets a card from the alert alone. stale
// reports an alert for an occurrence before the item's current DueAt: it was
// fired before the item moved on or was marked done, and reached here late.
func (s *Service) card(a scheduler.Alert) (c AlertCard, stale bool) {
	c = AlertCard{Key: a.Key, ItemID: a.ItemID, Text: a.Text, DueAt: a.DueAt, Lead: a.Lead, Missed: a.Missed, Snoozed: a.Snooze, Tags: []string{}}
	if s.d.Core == nil {
		return c, false
	}
	it, err := s.d.Core.Item(a.ItemID)
	if err != nil {
		log.Printf("alert %s: item: %v", a.Key, err)
		return c, false
	}
	c.Tags = append(c.Tags, it.Tags...)
	c.Recurring = it.RRule != ""
	return c, it.DueAt != nil && a.DueAt.Before(*it.DueAt)
}

// showAlerts adds alerts the queue doesn't hold yet and shows the alert
// window. It is called from the scheduler's fire callback (off its loop). A
// card for a later occurrence of a recurring item replaces the item's cards
// for earlier ones, and one for an earlier occurrence than a queued card is
// dropped, so an item never shows cards for two occurrences.
func (s *Service) showAlerts(alerts []scheduler.Alert) {
	cards := make([]AlertCard, 0, len(alerts))
	for _, a := range alerts {
		if c, stale := s.card(a); !stale {
			cards = append(cards, c)
		}
	}
	q := &s.alerts
	q.mu.Lock()
	defer q.mu.Unlock()
	added := 0
	for _, c := range cards {
		if slices.ContainsFunc(q.cards, func(o AlertCard) bool {
			return o.Key == c.Key || (o.ItemID == c.ItemID && o.DueAt.After(c.DueAt))
		}) {
			continue
		}
		q.cards = slices.DeleteFunc(q.cards, func(o AlertCard) bool { return o.ItemID == c.ItemID && o.DueAt.Before(c.DueAt) })
		q.cards = append(q.cards, c)
		added++
	}
	if added == 0 {
		return
	}
	s.d.Windows.ShowAlerts()
	s.emitAlertsLocked()
}

// removeAlerts drops the cards matching drop. When the queue becomes empty
// the alert window is hidden (unless hide is false: it is already closing).
func (s *Service) removeAlerts(drop func(AlertCard) bool, hide bool) {
	q := &s.alerts
	q.mu.Lock()
	defer q.mu.Unlock()
	n := len(q.cards)
	q.cards = slices.DeleteFunc(q.cards, drop)
	if len(q.cards) == n {
		return
	}
	if len(q.cards) == 0 && hide {
		s.d.Windows.DismissAlerts()
	}
	s.emitAlertsLocked()
}

// emitAlertsLocked sends the current queue to the alert window. q.mu is held.
func (s *Service) emitAlertsLocked() {
	if s.d.Emit != nil {
		s.d.Emit(EventAlertsShow, cloneCards(s.alerts.cards))
	}
}

// dropItemAlerts removes every card of an item (it was done, snoozed or
// deleted, from the alert window or anywhere else).
func (s *Service) dropItemAlerts(id string) {
	s.removeAlerts(func(c AlertCard) bool { return c.ItemID == id }, true)
}

// advanceRecurring is the scheduler's before-plan step: it moves recurring
// reminders not marked done to their latest occurrence (core.AdvanceRecurring,
// ruling R15) and drops the moved items' cards, which were for an earlier
// occurrence. It returns when the scheduler must run it again.
func (s *Service) advanceRecurring(now time.Time) (time.Time, error) {
	moved, wake, err := s.d.Core.AdvanceRecurring(now)
	if len(moved) > 0 {
		log.Printf("recurring: moved %d item(s) to their latest occurrence", len(moved))
	}
	for _, id := range moved {
		s.dropItemAlerts(id)
	}
	return wake, err
}

// latestCardDue is the latest occurrence an item has a card for (zero if
// none).
func (s *Service) latestCardDue(id string) time.Time {
	s.alerts.mu.Lock()
	defer s.alerts.mu.Unlock()
	var due time.Time
	for _, c := range s.alerts.cards {
		if c.ItemID == id && c.DueAt.After(due) {
			due = c.DueAt
		}
	}
	return due
}

// alertsClosed forgets the queue when the alert window was closed by the
// user (e.g. Alt+F4); the window is already going away.
func (s *Service) alertsClosed() {
	s.removeAlerts(func(AlertCard) bool { return true }, false)
}

// PendingAlerts returns the alert window's cards, oldest first. The alert
// page calls it when it loads, so alerts fired before it was ready show.
func (s *Service) PendingAlerts() []AlertCard {
	s.alerts.mu.Lock()
	defer s.alerts.mu.Unlock()
	return cloneCards(s.alerts.cards)
}

// cloneCards copies cards; never nil, so the frontend always gets an array.
func cloneCards(cards []AlertCard) []AlertCard {
	return append([]AlertCard{}, cards...)
}

// DismissAlert removes one card; the window hides when none are left.
func (s *Service) DismissAlert(key string) {
	s.removeAlerts(func(c AlertCard) bool { return c.Key == key }, true)
}

// DismissAlerts removes every card and hides the alert window.
func (s *Service) DismissAlerts() {
	q := &s.alerts
	q.mu.Lock()
	defer q.mu.Unlock()
	q.cards = nil
	s.d.Windows.DismissAlerts()
	s.emitAlertsLocked()
}

// ResizeAlerts sets the alert window's height (CSS pixels) to fit its cards.
func (s *Service) ResizeAlerts(height int) { s.d.Windows.ResizeAlerts(height) }
