package app

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"awesome-reminder/internal/cloudsync"
)

// Events emitted to the frontend.
const (
	EventItemsChanged = "items:changed" // no payload
	EventAlertsShow   = "alerts:show"   // []AlertCard: the whole current queue
	EventListFocus    = "list:focus"    // item ID
	EventCaptureOpen  = "capture:open"  // no payload: reset and focus the input
	EventSyncStatus   = "sync:status"   // cloudsync.Status
)

func init() {
	application.RegisterEvent[application.Void]("items:changed")
	application.RegisterEvent[[]AlertCard]("alerts:show")
	application.RegisterEvent[string]("list:focus")
	application.RegisterEvent[application.Void]("capture:open")
	application.RegisterEvent[cloudsync.Status]("sync:status")
}
