// Package store persists items, settings and the notification log in SQLite.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/settings"
)

// ErrNotFound is returned when an item does not exist.
var ErrNotFound = errors.New("not found")

// Store is a SQLite-backed store.
type Store struct {
	db       *sql.DB
	deviceID string
}

// Open opens (creating if needed) the database at path and runs migrations.
// ":memory:" is allowed.
func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = "file:" + strings.ReplaceAll(url.PathEscape(strings.ReplaceAll(path, "\\", "/")), "%2F", "/") +
			"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: keeps :memory: databases coherent and avoids writer contention.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.initDeviceID(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initDeviceID() error {
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key='device_id'`).Scan(&s.deviceID)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	id := ulid.Make().String()
	if _, err := s.db.Exec(`INSERT INTO meta(key,value) VALUES('device_id',?)`, id); err != nil {
		return err
	}
	s.deviceID = id
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DeviceID returns the stable ULID identifying this installation.
func (s *Store) DeviceID() string { return s.deviceID }

func toMs(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

func fromMs(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64).UTC()
	return &t
}

// Put inserts or replaces an item by ID and queues it for sync.
func (s *Store) Put(it model.Item) error {
	return s.inTx(func(tx *sql.Tx) error {
		if err := putItem(tx, it); err != nil {
			return err
		}
		return enqueue(tx, OutboxItem, it.ID)
	})
}

func putItem(db execer, it model.Item) error {
	tags := it.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	var leads any
	if it.LeadTimes != nil {
		ms := make([]int64, len(it.LeadTimes))
		for i, d := range it.LeadTimes {
			ms[i] = d.Milliseconds()
		}
		b, err := json.Marshal(ms)
		if err != nil {
			return err
		}
		leads = string(b)
	}
	created, updated := it.CreatedAt.UnixMilli(), it.UpdatedAt.UnixMilli()
	_, err = db.Exec(`INSERT INTO items
		(id,kind,text,tags_json,due_at,tz,rrule,lead_times_json,status,done_at,snooze_until,created_at,updated_at,updated_by,priority,description,reply,owed_to,source,reply_outcome)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		kind=excluded.kind,text=excluded.text,tags_json=excluded.tags_json,due_at=excluded.due_at,
		tz=excluded.tz,rrule=excluded.rrule,lead_times_json=excluded.lead_times_json,status=excluded.status,
		done_at=excluded.done_at,snooze_until=excluded.snooze_until,created_at=excluded.created_at,
		updated_at=excluded.updated_at,updated_by=excluded.updated_by,
		priority=excluded.priority,description=excluded.description,
		reply=excluded.reply,owed_to=excluded.owed_to,source=excluded.source,reply_outcome=excluded.reply_outcome`,
		it.ID, string(it.Kind), it.Text, string(tagsJSON), toMs(it.DueAt), it.TZ, it.RRule, leads,
		string(it.Status), toMs(it.DoneAt), toMs(it.SnoozeUntil), created, updated, it.UpdatedBy, it.Priority, it.Description,
		it.Reply, it.OwedTo, it.Source, it.ReplyOutcome)
	return err
}

const itemCols = `id,kind,text,tags_json,due_at,tz,rrule,lead_times_json,status,done_at,snooze_until,created_at,updated_at,updated_by,priority,description,reply,owed_to,source,reply_outcome`

type scanner interface{ Scan(dest ...any) error }

func scanItem(r scanner) (model.Item, error) {
	var (
		it                model.Item
		kind, status      string
		tagsJSON          string
		due, done, snooze sql.NullInt64
		leads             sql.NullString
		created, updated  int64
	)
	if err := r.Scan(&it.ID, &kind, &it.Text, &tagsJSON, &due, &it.TZ, &it.RRule, &leads,
		&status, &done, &snooze, &created, &updated, &it.UpdatedBy, &it.Priority, &it.Description,
		&it.Reply, &it.OwedTo, &it.Source, &it.ReplyOutcome); err != nil {
		return model.Item{}, err
	}
	it.Kind, it.Status = model.Kind(kind), model.Status(status)
	it.Tags = []string{}
	if err := json.Unmarshal([]byte(tagsJSON), &it.Tags); err != nil {
		return model.Item{}, err
	}
	if it.Tags == nil {
		it.Tags = []string{}
	}
	if leads.Valid {
		var ms []int64
		if err := json.Unmarshal([]byte(leads.String), &ms); err != nil {
			return model.Item{}, err
		}
		it.LeadTimes = make([]time.Duration, len(ms))
		for i, m := range ms {
			it.LeadTimes[i] = time.Duration(m) * time.Millisecond
		}
	}
	it.DueAt, it.DoneAt, it.SnoozeUntil = fromMs(due), fromMs(done), fromMs(snooze)
	it.CreatedAt = time.UnixMilli(created).UTC()
	it.UpdatedAt = time.UnixMilli(updated).UTC()
	return it, nil
}

// Get returns the item with the given ID, or ErrNotFound.
func (s *Store) Get(id string) (model.Item, error) {
	it, err := scanItem(s.db.QueryRow(`SELECT `+itemCols+` FROM items WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Item{}, ErrNotFound
	}
	return it, err
}

func (s *Store) list(query string, args ...any) ([]model.Item, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListOpen returns all open items, oldest first (by creation, then ID).
func (s *Store) ListOpen() ([]model.Item, error) {
	return s.list(`SELECT ` + itemCols + ` FROM items WHERE status='open' ORDER BY created_at, id`)
}

// ListDone returns done items, newest done_at first. limit <= 0 means no limit.
func (s *Store) ListDone(limit int) ([]model.Item, error) {
	if limit <= 0 {
		limit = -1 // SQLite: negative LIMIT = unlimited
	}
	return s.list(`SELECT `+itemCols+` FROM items WHERE status='done' ORDER BY done_at DESC, id LIMIT ?`, limit)
}

// LoadSettings returns the saved settings, or Defaults(goos) if none saved.
func (s *Store) LoadSettings(goos string) (settings.Settings, error) {
	var js string
	err := s.db.QueryRow(`SELECT json FROM settings WHERE id=1`).Scan(&js)
	if errors.Is(err, sql.ErrNoRows) {
		return settings.Defaults(goos), nil
	}
	if err != nil {
		return settings.Settings{}, err
	}
	var st settings.Settings
	if err := json.Unmarshal([]byte(js), &st); err != nil {
		return settings.Settings{}, err
	}
	return st, nil
}

// SaveSettings persists the settings (it does not validate them).
func (s *Store) SaveSettings(st settings.Settings) error {
	return saveSettings(s.db, st)
}

// SaveSettingsShared is SaveSettings plus queueing the settings for sync.
func (s *Store) SaveSettingsShared(st settings.Settings) error {
	return s.inTx(func(tx *sql.Tx) error {
		if err := saveSettings(tx, st); err != nil {
			return err
		}
		return enqueue(tx, OutboxSettings, settingsOutboxID)
	})
}

func saveSettings(db execer, st settings.Settings) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO settings(id,json) VALUES(1,?)
		ON CONFLICT(id) DO UPDATE SET json=excluded.json`, string(b))
	return err
}

// WasNotified reports whether a notification with this key was already fired.
func (s *Store) WasNotified(key string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM notify_log WHERE key=?`, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// MarkNotified records that a notification fired; repeated calls are no-ops.
func (s *Store) MarkNotified(key string, at time.Time) error {
	_, err := s.db.Exec(`INSERT INTO notify_log(key,fired_at) VALUES(?,?) ON CONFLICT(key) DO NOTHING`,
		key, at.UnixMilli())
	return err
}
