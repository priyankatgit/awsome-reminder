package app

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Events emitted to the frontend.
const (
	EventItemsChanged = "items:changed" // no payload
	EventAlertsShow   = "alerts:show"   // []AlertCard: the whole current queue
	EventListFocus    = "list:focus"    // item ID
	EventCaptureOpen  = "capture:open"  // no payload: reset and focus the input
)

func init() {
	application.RegisterEvent[application.Void]("items:changed")
	application.RegisterEvent[[]AlertCard]("alerts:show")
	application.RegisterEvent[string]("list:focus")
	application.RegisterEvent[application.Void]("capture:open")
}
