// Package model defines the Item type shared by all layers of the app.
package model

import "time"

// Kind distinguishes undated todos from reminders with a due time.
type Kind string

const (
	KindTodo     Kind = "todo"
	KindReminder Kind = "reminder"
)

// Status is the lifecycle state of an Item.
type Status string

const (
	StatusOpen    Status = "open"
	StatusDone    Status = "done"
	StatusDeleted Status = "deleted"
)

// Priority values. PriorityNone is the empty string.
const (
	PriorityNone   = ""
	PriorityLow    = "low"
	PriorityMedium = "medium"
	PriorityHigh   = "high"
)

// ValidPriority reports whether p is one of the allowed priority values.
func ValidPriority(p string) bool {
	switch p {
	case PriorityNone, PriorityLow, PriorityMedium, PriorityHigh:
		return true
	}
	return false
}

// Item is a captured todo or reminder.
type Item struct {
	ID          string          `json:"id"` // ULID
	Kind        Kind            `json:"kind"`
	Text        string          `json:"text"`
	Tags        []string        `json:"tags"`      // lowercase, no '#', deduped, in input order
	DueAt       *time.Time      `json:"dueAt"`     // UTC; nil for todos
	TZ          string          `json:"tz"`        // IANA name
	RRule       string          `json:"rrule"`     // "" = not recurring
	LeadTimes   []time.Duration `json:"leadTimes"` // nil = settings default
	Status      Status          `json:"status"`
	DoneAt      *time.Time      `json:"doneAt"`
	SnoozeUntil *time.Time      `json:"snoozeUntil"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
	UpdatedBy   string          `json:"updatedBy"`
	Priority    string          `json:"priority"`    // "", low, medium, high
	Description string          `json:"description"` // Markdown
}

// Attachment is an image attached to an item's description.
type Attachment struct {
	ID                string     `json:"id"`
	ItemID            string     `json:"itemId"`
	Mime              string     `json:"mime"`
	SHA256            string     `json:"sha256"`
	Width             int        `json:"width"`
	Height            int        `json:"height"`
	Bytes             int        `json:"bytes"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
	UpdatedBy         string     `json:"updatedBy"`
	Deleted           bool       `json:"deleted"`
	UnreferencedSince *time.Time `json:"unreferencedSince"`
}
