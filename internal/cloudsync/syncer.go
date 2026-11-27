// Package cloudsync syncs the local store with a remote backend: it pushes the
// outbox, pulls remote changes by server stamp and applies them under
// last-writer-wins. The backend sits behind the Syncer interface (memsync for
// tests, Atlas in the app).
package cloudsync

import (
	"context"
	"log"
	"regexp"
	"time"

	"awesome-reminder/internal/core"
	"awesome-reminder/internal/model"
	"awesome-reminder/internal/settings"
	"awesome-reminder/internal/store"
)

// Watermark is the server stamp (_srv, _id) of the last pulled document.
type Watermark struct {
	T  time.Time `json:"t"`
	ID string    `json:"id"`
}

// Less reports whether w sorts before o by (T, ID).
func (w Watermark) Less(o Watermark) bool {
	if !w.T.Equal(o.T) {
		return w.T.Before(o.T)
	}
	return w.ID < o.ID
}

// RemoteAttachment is a pulled attachment with its image bytes (nil for a
// tombstone).
type RemoteAttachment struct {
	model.Attachment
	Data []byte
}

// SharedDoc is the synced settings document.
type SharedDoc struct {
	Shared    settings.Shared
	UpdatedAt time.Time
	UpdatedBy string
}

// Syncer is the backend (spec: "Sync is behind a Go Syncer interface").
type Syncer interface {
	// PushItem is a last-writer-wins conditional upsert. A remote copy that
	// is equal or newer is left alone and gives nil.
	PushItem(ctx context.Context, it model.Item) error
	// PushAttachment is PushItem for attachments. data nil means a tombstone:
	// the stored bytes are cleared.
	PushAttachment(ctx context.Context, a model.Attachment, data []byte) error
	// PushShared is PushItem for the shared settings.
	PushShared(ctx context.Context, d SharedDoc) error
	// PullItems returns up to limit items whose (_srv, _id) is after the
	// watermark, ascending, and the watermark of the last one returned
	// (after itself when none are).
	//
	// A pull never fails because of one bad document. A document that
	// cannot be decoded (a field of the wrong type, written by anything
	// else) is returned leniently decoded: its _id, and zero values for the
	// fields that do not decode, so core validation rejects and skips it.
	// It is returned rather than dropped, so a full page stays full (the
	// engine reads a short page as the end) and the watermark advances past
	// it. Only a failure to read at all (network, auth, server) is an error.
	PullItems(ctx context.Context, after Watermark, limit int) ([]model.Item, Watermark, error)
	// PullAttachments is PullItems for attachments, with their bytes, under
	// the same bad-document rule.
	PullAttachments(ctx context.Context, after Watermark, limit int) ([]RemoteAttachment, Watermark, error)
	// PullShared returns the shared settings, nil when the cluster has none.
	PullShared(ctx context.Context) (*SharedDoc, error)
	// ServerTime is the backend's clock, for the skew warning.
	ServerTime(ctx context.Context) (time.Time, error)
	// AttachmentBytes is the sum of bytes over live attachments.
	AttachmentBytes(ctx context.Context) (int64, error)
	// ScopeTooBroad reports whether the credentials reach databases other
	// than the sync database.
	ScopeTooBroad(ctx context.Context) (bool, error)
	// Classify maps an error to Offline or Error plus a user-facing reason.
	Classify(err error) (State, string)
	Close(ctx context.Context) error
}

// State is the machine sync state.
type State string

// Sync states.
const (
	StateOff     State = "off"
	StateSyncing State = "syncing"
	StateSynced  State = "synced"
	StateOffline State = "offline"
	StateError   State = "error"
)

// Status is what the tray and the UI show.
type Status struct {
	State      State      `json:"state"`
	LastSyncAt *time.Time `json:"lastSyncAt"`
	Pending    int        `json:"pending"`
	Error      string     `json:"error"`    // reason when State == error (or offline detail)
	Warnings   []string   `json:"warnings"` // clock-skew and/or scope notes; [] never null
	Host       string     `json:"host"`
}

// ScopeWarning is shown while the Atlas user can reach other databases.
const ScopeWarning = "This Atlas user can reach other databases on your cluster. Limit it to readWrite on awesome_reminder (see the setup guide)."

// Local is the part of *core.Core the engine uses.
type Local interface {
	Item(id string) (model.Item, error)
	AttachmentForSync(id string) (model.Attachment, []byte, error)
	SharedForSync() (settings.Shared, time.Time, string, error)
	Outbox(limit int) ([]store.OutboxEntry, error)
	Ack(e store.OutboxEntry) error
	OutboxCount() (int, error)
	ApplyRemoteItems(items []model.Item) ([]core.Applied, error)
	ApplyRemoteAttachment(a model.Attachment, data []byte) (bool, error)
	ApplyRemoteShared(p settings.Shared, at time.Time, by string) (bool, error)
	SyncMeta(key string) (string, bool, error)
	SetSyncMeta(key, value string) error
}

var _ Local = (*core.Core)(nil)

// Options configure an Engine. Zero durations take the defaults. OnStatus
// and OnApplied run on the cycle's goroutine, in order; they must not call
// RunOnce or Stop.
type Options struct {
	Host       string
	Now        func() time.Time // default time.Now
	Poll       time.Duration    // default 30 s
	Debounce   time.Duration    // default 2 s
	ErrorRetry time.Duration    // default 5 min
	OnStatus   func(Status)
	OnApplied  func([]core.Applied)
	// Logf writes a log line (default log.Printf). Lines never hold the
	// connection string: errors pass through Redact.
	Logf func(format string, args ...any)
}

// Defaults for Options.
const (
	DefaultPoll       = 30 * time.Second
	DefaultDebounce   = 2 * time.Second
	DefaultErrorRetry = 5 * time.Minute
)

func (o Options) withDefaults() Options {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Poll <= 0 {
		o.Poll = DefaultPoll
	}
	if o.Debounce <= 0 {
		o.Debounce = DefaultDebounce
	}
	if o.ErrorRetry <= 0 {
		o.ErrorRetry = DefaultErrorRetry
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	return o
}

// uriUserinfo matches a MongoDB connection string's scheme and user info (up
// to the last @ before the path, so an unescaped @ in a password is covered).
var uriUserinfo = regexp.MustCompile(`(mongodb(?:\+srv)?://)[^\s/?#]*@`)

// Redact replaces the user and password of any MongoDB connection string in s
// with ***, for log lines built from driver errors.
func Redact(s string) string {
	return uriUserinfo.ReplaceAllString(s, "${1}***@")
}
