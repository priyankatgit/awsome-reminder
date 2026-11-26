package app

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"awesome-reminder/internal/scheduler"
)

// Events emitted to the frontend.
const (
	EventItemsChanged = "items:changed" // no payload
	EventAlertsShow   = "alerts:show"   // []scheduler.Alert
	EventListFocus    = "list:focus"    // item ID
	EventCaptureOpen  = "capture:open"  // no payload: reset and focus the input
)

func init() {
	application.RegisterEvent[application.Void]("items:changed")
	application.RegisterEvent[[]scheduler.Alert]("alerts:show")
	application.RegisterEvent[string]("list:focus")
	application.RegisterEvent[application.Void]("capture:open")
}
