package store

import (
	"database/sql"
	"fmt"
	"strconv"
)

// migrations are applied in order; index i upgrades schema version i to i+1.
// Only append: never edit a shipped migration.
var migrations = []string{
	// v1
	`CREATE TABLE items (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		text TEXT NOT NULL,
		tags_json TEXT NOT NULL,
		due_at INTEGER,
		tz TEXT NOT NULL,
		rrule TEXT NOT NULL,
		lead_times_json TEXT,
		status TEXT NOT NULL,
		done_at INTEGER,
		snooze_until INTEGER,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		updated_by TEXT NOT NULL
	);
	CREATE INDEX items_status ON items(status);
	CREATE TABLE settings (id INTEGER PRIMARY KEY CHECK (id = 1), json TEXT NOT NULL);
	CREATE TABLE notify_log (key TEXT PRIMARY KEY, fired_at INTEGER NOT NULL);`,
	// v2: priority, description, attachments
	`ALTER TABLE items ADD COLUMN priority TEXT NOT NULL DEFAULT '';
	ALTER TABLE items ADD COLUMN description TEXT NOT NULL DEFAULT '';
	CREATE TABLE attachments (
		id TEXT PRIMARY KEY,
		item_id TEXT NOT NULL,
		mime TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		width INTEGER NOT NULL DEFAULT 0,
		height INTEGER NOT NULL DEFAULT 0,
		bytes INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		updated_by TEXT NOT NULL,
		deleted INTEGER NOT NULL DEFAULT 0,
		unreferenced_since INTEGER
	);
	CREATE INDEX attachments_item ON attachments(item_id);`,
	// v3: sync outbox, seeded with every existing record
	`CREATE TABLE outbox (kind TEXT NOT NULL, id TEXT NOT NULL, seq INTEGER NOT NULL, PRIMARY KEY(kind,id));
	INSERT INTO outbox(kind,id,seq) SELECT 'item', id, 1 FROM items;
	INSERT INTO outbox(kind,id,seq) SELECT 'attachment', id, 1 FROM attachments;
	INSERT INTO outbox(kind,id,seq) SELECT 'settings', 'settings', 1 FROM settings WHERE id=1;`,
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	cur := 0
	var v string
	switch err := db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v); err {
	case nil:
		n, perr := strconv.Atoi(v)
		if perr != nil {
			return fmt.Errorf("bad schema_version %q", v)
		}
		cur = n
	case sql.ErrNoRows:
	default:
		return err
	}
	if cur > len(migrations) {
		return fmt.Errorf("database schema v%d is newer than this app (v%d)", cur, len(migrations))
	}
	for i := cur; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration v%d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES('schema_version',?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
