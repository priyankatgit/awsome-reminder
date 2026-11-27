package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/settings"
	"awesome-reminder/internal/store"
)

// ErrAttachmentFileMissing is returned by AttachmentForSync for a live row
// whose image file is not on disk.
var ErrAttachmentFileMissing = errors.New("attachment file missing")

// ErrInvalidRemote is wrapped by every validation failure of a remote
// document, so the sync engine can skip that document and move on instead of
// retrying it forever. Other errors are local failures (disk, database).
var ErrInvalidRemote = errors.New("invalid remote document")

// staleAlertAge is how old an alert must be before restoring an item marks it
// as fired. It matches the scheduler's missed threshold; younger alerts still
// show, so a reminder that came due during the pull lag is not lost.
const staleAlertAge = 2 * time.Minute

// Applied reports one item written by ApplyRemoteItems. Before is nil when the
// item was inserted on this device.
type Applied struct {
	Before *model.Item
	After  model.Item
}

// validRemoteItem checks a remote item before it is stored.
func validRemoteItem(it model.Item) error {
	if !attachmentIDRe.MatchString(it.ID) {
		return errors.New("bad id")
	}
	if it.Kind != model.KindTodo && it.Kind != model.KindReminder {
		return fmt.Errorf("bad kind %q", it.Kind)
	}
	switch it.Status {
	case model.StatusOpen, model.StatusDone, model.StatusDeleted:
	default:
		return fmt.Errorf("bad status %q", it.Status)
	}
	if !model.ValidPriority(it.Priority) {
		return fmt.Errorf("bad priority %q", it.Priority)
	}
	if len(it.Tags) == 0 {
		return errors.New("no tags")
	}
	if (it.Kind == model.KindReminder) != (it.DueAt != nil) {
		return errors.New("due time does not match kind")
	}
	if it.TZ != "" {
		if _, err := time.LoadLocation(it.TZ); err != nil {
			return fmt.Errorf("bad tz %q", it.TZ)
		}
	}
	return nil
}

// ApplyRemoteItems stores the remote items that win last-writer-wins. Remote
// stamps are kept as they are and nothing is queued for push. Invalid items
// are logged and skipped. Alerts already due for an item inserted here are
// marked as fired, so restoring a device does not replay missed alerts.
func (c *Core) ApplyRemoteItems(items []model.Item) ([]Applied, error) {
	c.opMu.Lock()
	applied, err := c.applyRemoteItemsLocked(items)
	c.opMu.Unlock()
	if len(applied) > 0 {
		c.changed()
	}
	return applied, err
}

func (c *Core) applyRemoteItemsLocked(items []model.Item) ([]Applied, error) {
	var applied []Applied
	var rules scheduler.Rules
	if len(items) > 0 {
		st, err := c.settings()
		if err != nil {
			return nil, err
		}
		rules = SchedulerRules(st, c.loc)
	}
	now := c.now()
	never := func(string) bool { return false }
	for _, it := range items {
		if err := validRemoteItem(it); err != nil {
			log.Printf("sync: skipping remote item %q: %v", it.ID, err)
			continue
		}
		local, err := c.st.Get(it.ID)
		var before *model.Item
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return applied, err
		default:
			if !model.Newer(it.UpdatedAt, it.UpdatedBy, local.UpdatedAt, local.UpdatedBy) {
				continue
			}
			l := local
			before = &l
		}
		// Mark the already-old alerts of an inserted item first: if we stop
		// between the two writes, a retry still sees no local copy and
		// marks again, and the marks are harmless without the row. An owed
		// reply's old nudges are covered the same way, due time or not. Only
		// the item's own keys: the end-of-day sweep is about every reply.
		if before == nil && it.Status == model.StatusOpen {
			_, covered, _ := scheduler.Plan([]model.Item{it}, rules, now.Add(-staleAlertAge), never)
			for _, key := range covered {
				if !strings.HasPrefix(key, it.ID+"|") {
					continue
				}
				if err := c.st.MarkNotified(key, now); err != nil {
					return applied, err
				}
			}
		}
		if err := c.st.PutRemote(it); err != nil {
			return applied, err
		}
		applied = append(applied, Applied{Before: before, After: it})
	}
	return applied, nil
}

// SchedulerRules turns settings into the scheduler's rules, with nudge
// times in loc.
func SchedulerRules(s settings.Settings, loc *time.Location) scheduler.Rules {
	return scheduler.Rules{Leads: s.LeadTimes, WorkDays: s.IsWorkDay, DefaultTime: s.DefaultTime,
		EndOfDay: s.EndOfDay, Sweep: s.EndOfDaySweep, Loc: loc}
}

// ApplyRemoteAttachment stores a remote attachment (and its image bytes, for a
// live row) when it wins last-writer-wins. It reports whether anything was
// applied. The local UnreferencedSince is kept. A document that fails
// validation gives an error wrapping ErrInvalidRemote.
func (c *Core) ApplyRemoteAttachment(a model.Attachment, data []byte) (bool, error) {
	if !attachmentIDRe.MatchString(a.ID) || !attachmentIDRe.MatchString(a.ItemID) {
		return false, fmt.Errorf("attachment %q (item %q): %w: %w", a.ID, a.ItemID, ErrBadAttachmentID, ErrInvalidRemote)
	}
	if a.Mime != "image/webp" {
		return false, fmt.Errorf("attachment %s: bad mime %q: %w", a.ID, a.Mime, ErrInvalidRemote)
	}
	// An equal or older remote changes nothing: skip it before decoding and
	// hashing its bytes (the pull overlap re-reads recent documents). The
	// locked section below checks again.
	if local, err := c.st.GetAttachment(a.ID); err == nil && !model.Newer(a.UpdatedAt, a.UpdatedBy, local.UpdatedAt, local.UpdatedBy) {
		return false, nil
	}
	if !a.Deleted {
		if _, err := checkWebP(data); err != nil {
			return false, fmt.Errorf("attachment %s: %w: %w", a.ID, err, ErrInvalidRemote)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != a.SHA256 {
			return false, fmt.Errorf("attachment %s: sha256 mismatch: %w", a.ID, ErrInvalidRemote)
		}
	}
	applied, err := func() (bool, error) {
		c.opMu.Lock()
		defer c.opMu.Unlock()
		local, err := c.st.GetAttachment(a.ID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			a.UnreferencedSince = nil
		case err != nil:
			return false, err
		default:
			if !model.Newer(a.UpdatedAt, a.UpdatedBy, local.UpdatedAt, local.UpdatedBy) {
				return false, nil
			}
			a.UnreferencedSince = local.UnreferencedSince
		}
		dir := c.attachmentDir()
		if dir == "" {
			return false, errors.New("attachment directory not set")
		}
		path := filepath.Join(dir, a.ID+".webp")
		if a.Deleted {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return false, err
			}
		} else if existing, err := os.ReadFile(path); err != nil || !bytes.Equal(existing, data) {
			if err := writeFileAtomic(path, data); err != nil {
				return false, err
			}
		}
		if err := c.st.PutAttachmentRemote(a); err != nil {
			return false, err
		}
		return true, nil
	}()
	if applied {
		c.changed()
	}
	return applied, err
}

// ApplyRemoteShared stores the shared settings when the remote stamp wins. An
// invalid remote value is logged and ignored.
func (c *Core) ApplyRemoteShared(p settings.Shared, at time.Time, by string) (bool, error) {
	applied, err := func() (bool, error) {
		c.opMu.Lock()
		defer c.opMu.Unlock()
		local, err := c.st.LoadSettings(runtime.GOOS)
		if err != nil {
			return false, err
		}
		if !model.Newer(at, by, local.SharedUpdatedAt, local.SharedUpdatedBy) {
			return false, nil
		}
		// A document written by an older device has no reply schedule: keep
		// the local one rather than reject the whole document.
		if p.EndOfDay == "" {
			p.EndOfDay, p.EndOfDaySweep = local.EndOfDay, local.EndOfDaySweep
		}
		if len(p.WorkDays) == 0 {
			p.WorkDays = local.WorkDays
		}
		next := local.WithShared(p)
		if err := next.Validate(); err != nil {
			log.Printf("sync: ignoring remote settings: %v", err)
			return false, nil
		}
		next.SharedUpdatedAt, next.SharedUpdatedBy = at, by
		if err := c.st.SaveSettings(next); err != nil {
			return false, err
		}
		// The remote version won: drop any pending local push of the old one.
		entries, err := c.st.Outbox(-1)
		if err != nil {
			return true, err
		}
		for _, e := range entries {
			if e.Kind == store.OutboxSettings {
				if _, err := c.st.Ack(e); err != nil {
					return true, err
				}
			}
		}
		return true, nil
	}()
	if applied {
		c.changed()
	}
	return applied, err
}

// SharedForSync returns the shared settings with their update stamp.
func (c *Core) SharedForSync() (settings.Shared, time.Time, string, error) {
	s, err := c.st.LoadSettings(runtime.GOOS)
	if err != nil {
		return settings.Shared{}, time.Time{}, "", err
	}
	return s.Shared(), s.SharedUpdatedAt, s.SharedUpdatedBy, nil
}

// AttachmentForSync returns an attachment row and its image bytes. The bytes
// are nil for a tombstone; a live row without a file gives
// ErrAttachmentFileMissing.
func (c *Core) AttachmentForSync(id string) (model.Attachment, []byte, error) {
	if !attachmentIDRe.MatchString(id) {
		return model.Attachment{}, nil, ErrBadAttachmentID
	}
	a, err := c.st.GetAttachment(id)
	if err != nil {
		return model.Attachment{}, nil, err
	}
	if a.Deleted {
		return a, nil, nil
	}
	data, err := os.ReadFile(filepath.Join(c.attachmentDir(), id+".webp"))
	if errors.Is(err, os.ErrNotExist) {
		return a, nil, ErrAttachmentFileMissing
	}
	if err != nil {
		return a, nil, err
	}
	return a, data, nil
}

// Outbox lists up to limit local changes waiting to be pushed.
func (c *Core) Outbox(limit int) ([]store.OutboxEntry, error) { return c.st.Outbox(limit) }

// Ack drops an outbox entry once pushed, unless it changed since it was read.
func (c *Core) Ack(e store.OutboxEntry) error {
	_, err := c.st.Ack(e)
	return err
}

// OutboxCount is the number of changes waiting to be pushed.
func (c *Core) OutboxCount() (int, error) { return c.st.OutboxCount() }

// EnqueueAll queues every local record for push (first connect).
func (c *Core) EnqueueAll() error { return c.st.EnqueueAll() }

// SyncMeta reads a sync bookkeeping value.
func (c *Core) SyncMeta(key string) (string, bool, error) { return c.st.Meta(key) }

// SetSyncMeta stores a sync bookkeeping value.
func (c *Core) SetSyncMeta(key, value string) error { return c.st.SetMeta(key, value) }

// DeleteSyncMeta removes a sync bookkeeping value.
func (c *Core) DeleteSyncMeta(key string) error { return c.st.DeleteMeta(key) }
