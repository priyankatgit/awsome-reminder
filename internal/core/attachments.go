package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"golang.org/x/image/webp"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/store"
)

// Errors returned by the attachment operations.
var (
	ErrNotWebP         = errors.New("image must be WebP")
	ErrImageTooLarge   = errors.New("image too large after compression (max 2 MB)")
	ErrBadAttachmentID = errors.New("bad attachment id")
)

const (
	maxAttachmentBytes = 2 << 20
	unreferencedTTL    = 7 * 24 * time.Hour
	deletedItemTTL     = 30 * 24 * time.Hour
)

var attachmentIDRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// Detail is an item with its live attachments, for the details panel.
type Detail struct {
	Item        model.Item         `json:"item"`
	Attachments []model.Attachment `json:"attachments"`
}

// SetAttachmentDir sets where image files live. It is created on first add.
func (c *Core) SetAttachmentDir(dir string) {
	c.mu.Lock()
	c.attDir = dir
	c.mu.Unlock()
}

func (c *Core) attachmentDir() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attDir
}

// AddAttachment stores a WebP image for an item. It starts out unreferenced;
// SetDescription clears that once the description mentions it.
func (c *Core) AddAttachment(itemID string, data []byte) (model.Attachment, error) {
	cfg, err := checkWebP(data)
	if err != nil {
		return model.Attachment{}, err
	}
	if _, err := c.st.Get(itemID); err != nil {
		return model.Attachment{}, fmt.Errorf("item %s: %w", itemID, err)
	}
	dir := c.attachmentDir()
	if dir == "" {
		return model.Attachment{}, errors.New("attachment directory not set")
	}
	id := ulid.Make().String()
	sum := sha256.Sum256(data)
	path := filepath.Join(dir, id+".webp")
	if err := writeFileAtomic(path, data); err != nil {
		return model.Attachment{}, err
	}
	var a model.Attachment
	err = c.write(func() error {
		if _, err := c.st.Get(itemID); err != nil {
			return fmt.Errorf("item %s: %w", itemID, err)
		}
		now := c.now().UTC()
		a = model.Attachment{
			ID: id, ItemID: itemID, Mime: "image/webp", SHA256: hex.EncodeToString(sum[:]),
			Width: cfg.Width, Height: cfg.Height, Bytes: len(data),
			CreatedAt: now, UpdatedAt: now, UpdatedBy: c.st.DeviceID(), UnreferencedSince: &now,
		}
		return c.st.PutAttachment(a)
	})
	if err != nil {
		os.Remove(path)
		return model.Attachment{}, err
	}
	return a, nil
}

// checkWebP validates image bytes: WebP, at most 2 MB.
func checkWebP(data []byte) (image.Config, error) {
	cfg, err := webp.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return cfg, ErrNotWebP
	}
	if len(data) > maxAttachmentBytes {
		return cfg, ErrImageTooLarge
	}
	return cfg, nil
}

// writeFileAtomic writes data to a temp file beside path, then renames it.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".att-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// SetDescription saves the Markdown description and updates which of the
// item's attachments are still referenced as att:<id>.
func (c *Core) SetDescription(id, md string) (model.Item, error) {
	return c.mutate(id, func(it *model.Item, now time.Time) error {
		it.Description = md
		atts, err := c.st.ListAttachments(id)
		if err != nil {
			return err
		}
		for _, a := range atts {
			referenced := strings.Contains(md, "att:"+a.ID)
			if referenced == (a.UnreferencedSince == nil) {
				continue
			}
			if referenced {
				a.UnreferencedSince = nil
			} else {
				t := now
				a.UnreferencedSince = &t
			}
			a.UpdatedAt, a.UpdatedBy = nextStamp(now, a.UpdatedAt), c.st.DeviceID()
			if err := c.st.PutAttachment(a); err != nil {
				return err
			}
		}
		return nil
	})
}

// ItemDetail returns an item with its attachments.
func (c *Core) ItemDetail(id string) (Detail, error) {
	it, err := c.st.Get(id)
	if err != nil {
		return Detail{}, fmt.Errorf("item %s: %w", id, err)
	}
	atts, err := c.st.ListAttachments(id)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Item: it, Attachments: atts}, nil
}

// AttachmentPath returns the file path of a live attachment.
func (c *Core) AttachmentPath(id string) (string, error) {
	if !attachmentIDRe.MatchString(id) {
		return "", ErrBadAttachmentID
	}
	a, err := c.st.GetAttachment(id)
	if err != nil {
		return "", err
	}
	if a.Deleted {
		return "", fmt.Errorf("attachment %s: %w", id, store.ErrNotFound)
	}
	path := filepath.Join(c.attachmentDir(), id+".webp")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("attachment %s: %w", id, store.ErrNotFound)
	}
	return path, nil
}

// SweepAttachments tombstones attachments unreferenced for over 7 days or
// whose item has been deleted for over 30 days, and removes their files. It
// returns how many it swept.
func (c *Core) SweepAttachments() (int, error) {
	n := 0
	c.opMu.Lock()
	err := func() error {
		all, err := c.st.AllAttachments()
		if err != nil {
			return err
		}
		now := c.now().UTC()
		dir := c.attachmentDir()
		var errs []error
		for _, a := range all {
			if a.Deleted || !c.sweepable(a, now) {
				continue
			}
			// Remove the file first; a failed removal leaves the row live so a
			// later sweep retries it.
			if dir != "" {
				if err := os.Remove(filepath.Join(dir, a.ID+".webp")); err != nil && !errors.Is(err, os.ErrNotExist) {
					errs = append(errs, err)
					continue
				}
			}
			a.Deleted, a.UpdatedAt, a.UpdatedBy = true, nextStamp(now, a.UpdatedAt), c.st.DeviceID()
			if err := c.st.PutAttachment(a); err != nil {
				errs = append(errs, err)
				continue
			}
			n++
		}
		return errors.Join(errs...)
	}()
	c.opMu.Unlock()
	if n > 0 {
		c.changed()
	}
	return n, err
}

func (c *Core) sweepable(a model.Attachment, now time.Time) bool {
	if a.UnreferencedSince != nil && now.Sub(*a.UnreferencedSince) > unreferencedTTL {
		return true
	}
	it, err := c.st.Get(a.ItemID)
	if err != nil {
		return false
	}
	// A deleted item's UpdatedAt is when it was deleted.
	return it.Status == model.StatusDeleted && now.Sub(it.UpdatedAt) > deletedItemTTL
}
