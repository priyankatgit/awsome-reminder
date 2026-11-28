package app

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"awesome-reminder/internal/cloudsync"
	"awesome-reminder/internal/push"
)

// Events emitted to the frontend.
const (
	EventItemsChanged = "items:changed" // no payload
	EventAlertsShow   = "alerts:show"   // []AlertCard: the whole current queue
	EventListFocus    = "list:focus"    // item ID
	EventCaptureOpen  = "capture:open"  // no payload: reset and focus the input
	EventSyncStatus   = "sync:status"   // cloudsync.Status
	EventReplyOpen    = "reply:open"    // ReplyPrefill: open the capture box in reply mode
	EventPushStatus   = "push:status"   // push.Status
)

// ReplyPrefill is what the reply hotkey found, for the capture box in reply
// mode: the suggested one-line task, who is waiting, where the message came
// from and the message itself. Grabbed is false when Quote is the clipboard's
// own text (or empty) rather than the selection. NeedsAccess is true when
// macOS has not granted the Accessibility permission the grab needs.
//
// KeepText is true when the hotkey was pressed with the capture box already
// open in normal mode: nothing was grabbed, every other field is empty, and
// the box switches to reply mode keeping the text it holds.
type ReplyPrefill struct {
	Line        string `json:"line"`
	OwedTo      string `json:"owedTo"`
	Source      string `json:"source"`
	Quote       string `json:"quote"`
	Grabbed     bool   `json:"grabbed"`
	NeedsAccess bool   `json:"needsAccess"`
	KeepText    bool   `json:"keepText"`
}

func init() {
	application.RegisterEvent[application.Void]("items:changed")
	application.RegisterEvent[[]AlertCard]("alerts:show")
	application.RegisterEvent[string]("list:focus")
	application.RegisterEvent[application.Void]("capture:open")
	application.RegisterEvent[cloudsync.Status]("sync:status")
	application.RegisterEvent[ReplyPrefill]("reply:open")
	application.RegisterEvent[push.Status]("push:status")
}
