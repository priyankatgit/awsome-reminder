// Package memsync is an in-memory cloudsync.Syncer with the same
// last-writer-wins and server-stamp semantics as the Atlas backend, plus
// hooks for tests: injected failures, a hook before each push, raw
// (malformed) documents and the scope-check result.
//
// Documents are kept as field maps named as in Atlas, so a push only sets the
// known fields (others survive) and a pull decodes leniently: a field of the
// wrong type reads as its zero value, which the core then rejects.
package memsync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"awesome-reminder/internal/cloudsync"
	"awesome-reminder/internal/model"
	"awesome-reminder/internal/settings"
)

// ErrNetwork is the error to inject (FailNext) for a lost connection.
// Classify maps it to offline.
var ErrNetwork = errors.New("memsync: network unreachable")

// errClosed is returned by every call after Close.
var errClosed = errors.New("memsync: closed")

const offlineText = "No connection to Atlas."

type doc = map[string]any

// Syncer is the in-memory backend. It is safe for concurrent use.
type Syncer struct {
	mu         sync.Mutex
	now        func() time.Time
	lastSrv    time.Time
	items      map[string]doc
	atts       map[string]doc
	shared     doc
	failErr    error
	failN      int
	beforePush func()
	scopeBroad bool
	scopeErr   error
	closed     bool
}

var _ cloudsync.Syncer = (*Syncer)(nil)

// New returns an empty backend whose server clock is time.Now.
func New() *Syncer {
	return &Syncer{now: time.Now, items: map[string]doc{}, atts: map[string]doc{}}
}

// SetNow sets the server clock (ServerTime and the _srv stamps).
func (s *Syncer) SetNow(fn func() time.Time) {
	s.mu.Lock()
	s.now = fn
	s.mu.Unlock()
}

// FailNext makes the next n network calls return err.
func (s *Syncer) FailNext(err error, n int) {
	s.mu.Lock()
	s.failErr, s.failN = err, n
	s.mu.Unlock()
}

// BeforePush sets fn to run at the start of every push, outside the lock (nil
// clears it).
func (s *Syncer) BeforePush(fn func()) {
	s.mu.Lock()
	s.beforePush = fn
	s.mu.Unlock()
}

// SetScopeTooBroad sets what ScopeTooBroad returns.
func (s *Syncer) SetScopeTooBroad(v bool, err error) {
	s.mu.Lock()
	s.scopeBroad, s.scopeErr = v, err
	s.mu.Unlock()
}

// PutRaw stores doc as is in the items collection, stamped like a write. Its
// "_id" is read with fmt.Sprint.
func (s *Syncer) PutRaw(d map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make(doc, len(d)+1)
	for k, v := range d {
		cp[k] = v
	}
	id := fmt.Sprint(d["_id"])
	cp["_id"] = id
	cp["_srv"] = s.stampLocked()
	s.items[id] = cp
}

// Closed reports whether Close was called.
func (s *Syncer) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// stampLocked returns the next server stamp: the clock at ms precision, but
// always strictly after the previous stamp.
func (s *Syncer) stampLocked() time.Time {
	t := s.now().UTC().Truncate(time.Millisecond)
	if !t.After(s.lastSrv) {
		t = s.lastSrv.Add(time.Millisecond)
	}
	s.lastSrv = t
	return t
}

// enter checks the context, Close and injected failures. On success the lock
// is held and the caller must unlock it.
func (s *Syncer) enter(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errClosed
	}
	if s.failN > 0 {
		s.failN--
		err := s.failErr
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *Syncer) runBeforePush() {
	s.mu.Lock()
	fn := s.beforePush
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func ms(t time.Time) time.Time { return t.UTC().Truncate(time.Millisecond) }

func msPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ms(*t)
}

// newerLocked reports whether the stamp (at, by) beats the stored document.
func newerLocked(existing doc, at time.Time, by string) bool {
	if existing == nil {
		return true
	}
	t, _ := existing["updated_at"].(time.Time)
	b, _ := existing["updated_by"].(string)
	return model.Newer(at, by, t, b)
}

// setLocked applies fields to the document with this id ($set semantics) and
// stamps it.
func (s *Syncer) setLocked(coll map[string]doc, id string, fields doc, unset ...string) {
	d := coll[id]
	if d == nil {
		d = doc{"_id": id}
		coll[id] = d
	}
	for k, v := range fields {
		d[k] = v
	}
	for _, k := range unset {
		delete(d, k)
	}
	d["_srv"] = s.stampLocked()
}

func leadsMs(l []time.Duration) any {
	if l == nil {
		return nil
	}
	out := make([]int64, len(l))
	for i, d := range l {
		out[i] = d.Milliseconds()
	}
	return out
}

// PushItem implements cloudsync.Syncer.
func (s *Syncer) PushItem(ctx context.Context, it model.Item) error {
	s.runBeforePush()
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if !newerLocked(s.items[it.ID], it.UpdatedAt, it.UpdatedBy) {
		return nil
	}
	s.setLocked(s.items, it.ID, doc{
		"kind": string(it.Kind), "text": it.Text, "tags": slices.Clone(it.Tags),
		"due_at": msPtr(it.DueAt), "tz": it.TZ, "rrule": it.RRule, "lead_times": leadsMs(it.LeadTimes),
		"status": string(it.Status), "done_at": msPtr(it.DoneAt), "snooze_until": msPtr(it.SnoozeUntil),
		"created_at": ms(it.CreatedAt), "updated_at": ms(it.UpdatedAt), "updated_by": it.UpdatedBy,
		"priority": it.Priority, "description": it.Description,
		"reply": it.Reply, "owed_to": it.OwedTo, "source": it.Source, "reply_outcome": it.ReplyOutcome,
	})
	return nil
}

// PushAttachment implements cloudsync.Syncer.
func (s *Syncer) PushAttachment(ctx context.Context, a model.Attachment, data []byte) error {
	s.runBeforePush()
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if !newerLocked(s.atts[a.ID], a.UpdatedAt, a.UpdatedBy) {
		return nil
	}
	fields := doc{
		"item_id": a.ItemID, "mime": a.Mime, "width": a.Width, "height": a.Height, "bytes": a.Bytes,
		"sha256": a.SHA256, "created_at": ms(a.CreatedAt), "updated_at": ms(a.UpdatedAt),
		"updated_by": a.UpdatedBy, "deleted": a.Deleted,
	}
	if data == nil {
		s.setLocked(s.atts, a.ID, fields, "data")
		return nil
	}
	fields["data"] = slices.Clone(data)
	s.setLocked(s.atts, a.ID, fields)
	return nil
}

// PushShared implements cloudsync.Syncer.
func (s *Syncer) PushShared(ctx context.Context, d cloudsync.SharedDoc) error {
	s.runBeforePush()
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if !newerLocked(s.shared, d.UpdatedAt, d.UpdatedBy) {
		return nil
	}
	if s.shared == nil {
		s.shared = doc{"_id": "settings"}
	}
	for k, v := range (doc{
		"lead_times": leadsMs(d.Shared.LeadTimes), "default_time": d.Shared.DefaultTime,
		"next_week_day": d.Shared.NextWeekDay, "next_week_time": d.Shared.NextWeekTime,
		"work_days": slices.Clone(d.Shared.WorkDays), "end_of_day": d.Shared.EndOfDay, "end_of_day_sweep": d.Shared.EndOfDaySweep,
		"updated_at": ms(d.UpdatedAt), "updated_by": d.UpdatedBy,
	}) {
		s.shared[k] = v
	}
	s.shared["_srv"] = s.stampLocked()
	return nil
}

func key(d doc) cloudsync.Watermark {
	t, _ := d["_srv"].(time.Time)
	id, _ := d["_id"].(string)
	return cloudsync.Watermark{T: t, ID: id}
}

// pageLocked returns up to limit documents after the watermark, ascending.
func pageLocked(coll map[string]doc, after cloudsync.Watermark, limit int) ([]doc, cloudsync.Watermark) {
	var out []doc
	for _, d := range coll {
		if after.Less(key(d)) {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b doc) int {
		ka, kb := key(a), key(b)
		switch {
		case ka.Less(kb):
			return -1
		case kb.Less(ka):
			return 1
		}
		return 0
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	if len(out) == 0 {
		return nil, after
	}
	return out, key(out[len(out)-1])
}

// Lenient field readers: a missing or mistyped field reads as zero.

func str(d doc, k string) string { v, _ := d[k].(string); return v }

func tm(d doc, k string) time.Time { v, _ := d[k].(time.Time); return v }

func tmPtr(d doc, k string) *time.Time {
	v, ok := d[k].(time.Time)
	if !ok {
		return nil
	}
	return &v
}

func num(d doc, k string) int { v, _ := d[k].(int); return v }

func strs(d doc, k string) []string {
	v, ok := d[k].([]string)
	if !ok {
		return nil
	}
	return slices.Clone(v)
}

func leads(d doc, k string) []time.Duration {
	v, ok := d[k].([]int64)
	if !ok {
		return nil
	}
	out := make([]time.Duration, len(v))
	for i, m := range v {
		out[i] = time.Duration(m) * time.Millisecond
	}
	return out
}

func boolean(d doc, k string) bool {
	b, _ := d[k].(bool)
	return b
}

func decodeItem(d doc) model.Item {
	return model.Item{
		ID: str(d, "_id"), Kind: model.Kind(str(d, "kind")), Text: str(d, "text"), Tags: strs(d, "tags"),
		DueAt: tmPtr(d, "due_at"), TZ: str(d, "tz"), RRule: str(d, "rrule"), LeadTimes: leads(d, "lead_times"),
		Status: model.Status(str(d, "status")), DoneAt: tmPtr(d, "done_at"), SnoozeUntil: tmPtr(d, "snooze_until"),
		CreatedAt: tm(d, "created_at"), UpdatedAt: tm(d, "updated_at"), UpdatedBy: str(d, "updated_by"),
		Priority: str(d, "priority"), Description: str(d, "description"),
		Reply: boolean(d, "reply"), OwedTo: str(d, "owed_to"), Source: str(d, "source"), ReplyOutcome: str(d, "reply_outcome"),
	}
}

func decodeAttachment(d doc) cloudsync.RemoteAttachment {
	deleted, _ := d["deleted"].(bool)
	var data []byte
	if b, ok := d["data"].([]byte); ok {
		data = slices.Clone(b)
	}
	return cloudsync.RemoteAttachment{
		Attachment: model.Attachment{
			ID: str(d, "_id"), ItemID: str(d, "item_id"), Mime: str(d, "mime"), SHA256: str(d, "sha256"),
			Width: num(d, "width"), Height: num(d, "height"), Bytes: num(d, "bytes"),
			CreatedAt: tm(d, "created_at"), UpdatedAt: tm(d, "updated_at"), UpdatedBy: str(d, "updated_by"),
			Deleted: deleted,
		},
		Data: data,
	}
}

// PullItems implements cloudsync.Syncer.
func (s *Syncer) PullItems(ctx context.Context, after cloudsync.Watermark, limit int) ([]model.Item, cloudsync.Watermark, error) {
	if err := s.enter(ctx); err != nil {
		return nil, after, err
	}
	defer s.mu.Unlock()
	page, wm := pageLocked(s.items, after, limit)
	out := make([]model.Item, len(page))
	for i, d := range page {
		out[i] = decodeItem(d)
	}
	return out, wm, nil
}

// PullAttachments implements cloudsync.Syncer.
func (s *Syncer) PullAttachments(ctx context.Context, after cloudsync.Watermark, limit int) ([]cloudsync.RemoteAttachment, cloudsync.Watermark, error) {
	if err := s.enter(ctx); err != nil {
		return nil, after, err
	}
	defer s.mu.Unlock()
	page, wm := pageLocked(s.atts, after, limit)
	out := make([]cloudsync.RemoteAttachment, len(page))
	for i, d := range page {
		out[i] = decodeAttachment(d)
	}
	return out, wm, nil
}

// PullShared implements cloudsync.Syncer.
func (s *Syncer) PullShared(ctx context.Context) (*cloudsync.SharedDoc, error) {
	if err := s.enter(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	if s.shared == nil {
		return nil, nil
	}
	d := s.shared
	return &cloudsync.SharedDoc{
		Shared: settings.Shared{
			LeadTimes: leads(d, "lead_times"), DefaultTime: str(d, "default_time"),
			NextWeekDay: str(d, "next_week_day"), NextWeekTime: str(d, "next_week_time"),
			WorkDays: strs(d, "work_days"), EndOfDay: str(d, "end_of_day"), EndOfDaySweep: boolean(d, "end_of_day_sweep"),
		},
		UpdatedAt: tm(d, "updated_at"), UpdatedBy: str(d, "updated_by"),
	}, nil
}

// ServerTime implements cloudsync.Syncer.
func (s *Syncer) ServerTime(ctx context.Context) (time.Time, error) {
	if err := s.enter(ctx); err != nil {
		return time.Time{}, err
	}
	defer s.mu.Unlock()
	return s.now(), nil
}

// AttachmentBytes implements cloudsync.Syncer.
func (s *Syncer) AttachmentBytes(ctx context.Context) (int64, error) {
	if err := s.enter(ctx); err != nil {
		return 0, err
	}
	defer s.mu.Unlock()
	var n int64
	for _, d := range s.atts {
		if deleted, _ := d["deleted"].(bool); !deleted {
			n += int64(num(d, "bytes"))
		}
	}
	return n, nil
}

// ScopeTooBroad implements cloudsync.Syncer.
func (s *Syncer) ScopeTooBroad(ctx context.Context) (bool, error) {
	if err := s.enter(ctx); err != nil {
		return false, err
	}
	defer s.mu.Unlock()
	return s.scopeBroad, s.scopeErr
}

// Classify implements cloudsync.Syncer: ErrNetwork is offline, anything else
// an error with its own text.
func (s *Syncer) Classify(err error) (cloudsync.State, string) {
	if errors.Is(err, ErrNetwork) {
		return cloudsync.StateOffline, offlineText
	}
	return cloudsync.StateError, err.Error()
}

// Close implements cloudsync.Syncer. Later calls fail.
func (s *Syncer) Close(context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}
