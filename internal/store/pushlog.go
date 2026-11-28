package store

import "time"

// Push log states.
const (
	PushScheduled = "scheduled" // published to the server, delivery pending
	PushDismissed = "dismissed" // handled on the computer; cancelled on the server
)

// PushEntry is one phone push the server holds (or held) for delivery.
type PushEntry struct {
	Seq, Server, Topic, Hash string
	DeliverAt, SentAt        time.Time
	State                    string // PushScheduled or PushDismissed
}

// PushLog returns every entry, earliest DeliverAt first (then by Seq).
func (s *Store) PushLog() ([]PushEntry, error) {
	rows, err := s.db.Query(`SELECT seq,server,topic,hash,deliver_at,sent_at,state FROM push_log
		ORDER BY deliver_at, seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PushEntry{}
	for rows.Next() {
		var e PushEntry
		var deliver, sent int64
		if err := rows.Scan(&e.Seq, &e.Server, &e.Topic, &e.Hash, &deliver, &sent, &e.State); err != nil {
			return nil, err
		}
		e.DeliverAt, e.SentAt = time.UnixMilli(deliver).UTC(), time.UnixMilli(sent).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// PutPush inserts or replaces the entry with e.Seq.
func (s *Store) PutPush(e PushEntry) error {
	_, err := s.db.Exec(`INSERT INTO push_log(seq,server,topic,deliver_at,hash,state,sent_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(seq) DO UPDATE SET server=excluded.server, topic=excluded.topic,
		deliver_at=excluded.deliver_at, hash=excluded.hash, state=excluded.state, sent_at=excluded.sent_at`,
		e.Seq, e.Server, e.Topic, e.DeliverAt.UnixMilli(), e.Hash, e.State, e.SentAt.UnixMilli())
	return err
}

// DeletePush removes the entry with seq (absent is not an error).
func (s *Store) DeletePush(seq string) error {
	_, err := s.db.Exec(`DELETE FROM push_log WHERE seq=?`, seq)
	return err
}

// PrunePush removes the entries delivering strictly before before.
func (s *Store) PrunePush(before time.Time) error {
	_, err := s.db.Exec(`DELETE FROM push_log WHERE deliver_at < ?`, before.UnixMilli())
	return err
}

// ClearPush removes every entry.
func (s *Store) ClearPush() error {
	_, err := s.db.Exec(`DELETE FROM push_log`)
	return err
}
