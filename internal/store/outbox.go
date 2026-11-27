package store

import (
	"database/sql"

	"awesome-reminder/internal/model"
)

// Outbox kinds. The settings entry always has the ID "settings".
const (
	OutboxItem       = "item"
	OutboxAttachment = "attachment"
	OutboxSettings   = "settings"

	settingsOutboxID = "settings"
)

// OutboxEntry is a record waiting to be pushed. Seq counts local writes to the
// record, so an Ack only succeeds if nothing changed since the entry was read.
type OutboxEntry struct {
	Kind, ID string
	Seq      int64
}

// execer is satisfied by *sql.DB and *sql.Tx.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// inTx runs fn in a transaction. The pool has one connection, so fn must use
// only the tx, never s.db.
func (s *Store) inTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func enqueue(db execer, kind, id string) error {
	_, err := db.Exec(`INSERT INTO outbox(kind,id,seq) VALUES(?,?,1)
		ON CONFLICT(kind,id) DO UPDATE SET seq = outbox.seq + 1`, kind, id)
	return err
}

// PutRemote stores an item that came from the remote and drops its outbox row
// (the remote version won).
func (s *Store) PutRemote(it model.Item) error {
	return s.inTx(func(tx *sql.Tx) error {
		if err := putItem(tx, it); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM outbox WHERE kind=? AND id=?`, OutboxItem, it.ID)
		return err
	})
}

// PutAttachmentRemote is PutRemote for attachments.
func (s *Store) PutAttachmentRemote(a model.Attachment) error {
	return s.inTx(func(tx *sql.Tx) error {
		if err := putAttachment(tx, a); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM outbox WHERE kind=? AND id=?`, OutboxAttachment, a.ID)
		return err
	})
}

// Outbox returns up to limit pending entries: attachments first, then items,
// then settings; oldest first within a kind.
func (s *Store) Outbox(limit int) ([]OutboxEntry, error) {
	rows, err := s.db.Query(`SELECT kind,id,seq FROM outbox
		ORDER BY CASE kind WHEN 'attachment' THEN 0 WHEN 'item' THEN 1 ELSE 2 END, rowid LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OutboxEntry{}
	for rows.Next() {
		var e OutboxEntry
		if err := rows.Scan(&e.Kind, &e.ID, &e.Seq); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Ack removes the entry only if its seq is unchanged, and reports whether it did.
func (s *Store) Ack(e OutboxEntry) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM outbox WHERE kind=? AND id=? AND seq=?`, e.Kind, e.ID, e.Seq)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// OutboxCount returns the number of pending entries.
func (s *Store) OutboxCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM outbox`).Scan(&n)
	return n, err
}

// PendingItemIDs returns the sorted IDs of items that are pending, or that own a
// pending attachment.
func (s *Store) PendingItemIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM outbox WHERE kind='item'
		UNION SELECT a.item_id FROM outbox o JOIN attachments a ON a.id=o.id WHERE o.kind='attachment'
		ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// EnqueueAll queues every item, every attachment, and the settings row if one exists.
func (s *Store) EnqueueAll() error {
	return s.inTx(func(tx *sql.Tx) error {
		for _, q := range []string{
			`INSERT INTO outbox(kind,id,seq) SELECT 'item', id, 1 FROM items WHERE true
				ON CONFLICT(kind,id) DO UPDATE SET seq = outbox.seq + 1`,
			`INSERT INTO outbox(kind,id,seq) SELECT 'attachment', id, 1 FROM attachments WHERE true
				ON CONFLICT(kind,id) DO UPDATE SET seq = outbox.seq + 1`,
			`INSERT INTO outbox(kind,id,seq) SELECT 'settings', 'settings', 1 FROM settings WHERE id=1
				ON CONFLICT(kind,id) DO UPDATE SET seq = outbox.seq + 1`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		return nil
	})
}

// Meta reads a meta value; ok is false when the key is absent.
func (s *Store) Meta(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetMeta upserts a meta value.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// DeleteMeta removes a meta key (absent is not an error).
func (s *Store) DeleteMeta(key string) error {
	_, err := s.db.Exec(`DELETE FROM meta WHERE key=?`, key)
	return err
}
