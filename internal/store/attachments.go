package store

import (
	"database/sql"
	"errors"
	"time"

	"awesome-reminder/internal/model"
)

const attCols = `id,item_id,mime,sha256,width,height,bytes,created_at,updated_at,updated_by,deleted,unreferenced_since`

// PutAttachment inserts or replaces an attachment record by ID and queues it for sync.
func (s *Store) PutAttachment(a model.Attachment) error {
	return s.inTx(func(tx *sql.Tx) error {
		if err := putAttachment(tx, a); err != nil {
			return err
		}
		return enqueue(tx, OutboxAttachment, a.ID)
	})
}

// SetAttachmentUnreferenced sets only when the attachment stopped being
// referenced (nil: it is referenced). The field is local bookkeeping that
// never syncs, so the row is not restamped or queued.
func (s *Store) SetAttachmentUnreferenced(id string, since *time.Time) error {
	res, err := s.db.Exec(`UPDATE attachments SET unreferenced_since=? WHERE id=?`, toMs(since), id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

func putAttachment(db execer, a model.Attachment) error {
	del := 0
	if a.Deleted {
		del = 1
	}
	_, err := db.Exec(`INSERT INTO attachments (`+attCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		item_id=excluded.item_id,mime=excluded.mime,sha256=excluded.sha256,width=excluded.width,
		height=excluded.height,bytes=excluded.bytes,created_at=excluded.created_at,
		updated_at=excluded.updated_at,updated_by=excluded.updated_by,deleted=excluded.deleted,
		unreferenced_since=excluded.unreferenced_since`,
		a.ID, a.ItemID, a.Mime, a.SHA256, a.Width, a.Height, a.Bytes,
		a.CreatedAt.UnixMilli(), a.UpdatedAt.UnixMilli(), a.UpdatedBy, del, toMs(a.UnreferencedSince))
	return err
}

func scanAttachment(r scanner) (model.Attachment, error) {
	var (
		a                model.Attachment
		created, updated int64
		deleted          int
		unref            sql.NullInt64
	)
	if err := r.Scan(&a.ID, &a.ItemID, &a.Mime, &a.SHA256, &a.Width, &a.Height, &a.Bytes,
		&created, &updated, &a.UpdatedBy, &deleted, &unref); err != nil {
		return model.Attachment{}, err
	}
	a.CreatedAt = time.UnixMilli(created).UTC()
	a.UpdatedAt = time.UnixMilli(updated).UTC()
	a.Deleted = deleted != 0
	a.UnreferencedSince = fromMs(unref)
	return a, nil
}

// GetAttachment returns the attachment with the given ID, or ErrNotFound.
func (s *Store) GetAttachment(id string) (model.Attachment, error) {
	a, err := scanAttachment(s.db.QueryRow(`SELECT `+attCols+` FROM attachments WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Attachment{}, ErrNotFound
	}
	return a, err
}

func (s *Store) listAttachments(query string, args ...any) ([]model.Attachment, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Attachment{}
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListAttachments returns an item's non-deleted attachments, oldest first.
func (s *Store) ListAttachments(itemID string) ([]model.Attachment, error) {
	return s.listAttachments(`SELECT `+attCols+` FROM attachments WHERE item_id=? AND deleted=0 ORDER BY created_at, id`, itemID)
}

// AllAttachments returns every attachment record, including deleted ones.
func (s *Store) AllAttachments() ([]model.Attachment, error) {
	return s.listAttachments(`SELECT ` + attCols + ` FROM attachments ORDER BY created_at, id`)
}
