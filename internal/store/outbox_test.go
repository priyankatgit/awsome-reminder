package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/settings"
)

func att(id, itemID string) model.Attachment {
	return model.Attachment{ID: id, ItemID: itemID, Mime: "image/webp", SHA256: "h", CreatedAt: ms(1), UpdatedAt: ms(1), UpdatedBy: "d"}
}

func count(t *testing.T, s *Store) int {
	t.Helper()
	n, err := s.OutboxCount()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigrateV2ToV3SeedsOutbox(t *testing.T) {
	p := filepath.Join(t.TempDir(), "v2.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{migrations[0], migrations[1],
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta VALUES('schema_version','2')`,
		`INSERT INTO items VALUES('A','todo','one','["a"]',NULL,'UTC','',NULL,'open',NULL,NULL,1000,2000,'dev','','')`,
		`INSERT INTO items VALUES('B','todo','two','[]',NULL,'UTC','',NULL,'open',NULL,NULL,1000,2000,'dev','high','d')`,
		`INSERT INTO attachments(id,item_id,mime,sha256,created_at,updated_at,updated_by) VALUES('X','A','image/webp','h',1,2,'dev')`,
		`INSERT INTO settings VALUES(1,'{"hotkey":"Ctrl+Alt+Space"}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()

	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v); err != nil || v != "4" {
		t.Fatalf("v=%q err=%v", v, err)
	}
	if n := count(t, s); n != 4 {
		t.Fatalf("outbox count=%d", n)
	}
	es, _ := s.Outbox(10)
	for _, e := range es {
		if e.Seq != 1 {
			t.Fatalf("seed seq: %+v", e)
		}
	}
	b, err := s.Get("B")
	if err != nil || b.Text != "two" || b.Priority != "high" || b.Description != "d" {
		t.Fatalf("B=%+v err=%v", b, err)
	}
	if a, err := s.GetAttachment("X"); err != nil || a.ItemID != "A" {
		t.Fatalf("att=%+v err=%v", a, err)
	}
	var js string
	if err := s.db.QueryRow(`SELECT json FROM settings WHERE id=1`).Scan(&js); err != nil || js != `{"hotkey":"Ctrl+Alt+Space"}` {
		t.Fatalf("settings=%q err=%v", js, err)
	}
}

func TestPutEnqueuesAndAckBySeq(t *testing.T) {
	s := mem(t)
	it := item("a", model.StatusOpen, 0)
	if err := s.Put(it); err != nil {
		t.Fatal(err)
	}
	es, _ := s.Outbox(10)
	if !reflect.DeepEqual(es, []OutboxEntry{{Kind: OutboxItem, ID: "a", Seq: 1}}) {
		t.Fatalf("%+v", es)
	}
	if err := s.Put(it); err != nil {
		t.Fatal(err)
	}
	es, _ = s.Outbox(10)
	if len(es) != 1 || es[0].Seq != 2 {
		t.Fatalf("%+v", es)
	}
	ok, err := s.Ack(OutboxEntry{Kind: OutboxItem, ID: "a", Seq: 1})
	if err != nil || ok || count(t, s) != 1 {
		t.Fatalf("stale ack ok=%v err=%v", ok, err)
	}
	ok, err = s.Ack(OutboxEntry{Kind: OutboxItem, ID: "a", Seq: 2})
	if err != nil || !ok || count(t, s) != 0 {
		t.Fatalf("ack ok=%v err=%v", ok, err)
	}
}

func TestPutRemoteDoesNotEnqueueAndClearsPending(t *testing.T) {
	s := mem(t)
	if err := s.Put(item("a", model.StatusOpen, 0)); err != nil {
		t.Fatal(err)
	}
	if count(t, s) != 1 {
		t.Fatal("expected pending")
	}
	r := item("a", model.StatusOpen, 0)
	r.Text = "remote"
	if err := s.PutRemote(r); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("a"); got.Text != "remote" || count(t, s) != 0 {
		t.Fatalf("got=%+v count=%d", got, count(t, s))
	}
	if err := s.PutRemote(item("new", model.StatusOpen, 0)); err != nil {
		t.Fatal(err)
	}
	if count(t, s) != 0 {
		t.Fatal("PutRemote of a new id must not enqueue")
	}
}

func TestPutAttachmentRemote(t *testing.T) {
	s := mem(t)
	if err := s.PutAttachment(att("x", "i")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAttachmentRemote(att("x", "i")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAttachmentRemote(att("y", "i")); err != nil {
		t.Fatal(err)
	}
	if count(t, s) != 0 {
		t.Fatal("expected empty outbox")
	}
	if _, err := s.GetAttachment("y"); err != nil {
		t.Fatal(err)
	}
}

func TestOutboxOrder(t *testing.T) {
	s := mem(t)
	if err := s.Put(item("i1", model.StatusOpen, 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAttachment(att("a1", "i1")); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSettingsShared(settings.Defaults("windows")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(item("i0", model.StatusOpen, 0)); err != nil {
		t.Fatal(err)
	}
	es, err := s.Outbox(10)
	if err != nil {
		t.Fatal(err)
	}
	want := []OutboxEntry{
		{OutboxAttachment, "a1", 1}, {OutboxItem, "i1", 1}, {OutboxItem, "i0", 1}, {OutboxSettings, "settings", 1},
	}
	if !reflect.DeepEqual(es, want) {
		t.Fatalf("got %+v want %+v", es, want)
	}
	es, _ = s.Outbox(2)
	if len(es) != 2 || es[1].ID != "i1" {
		t.Fatalf("limit: %+v", es)
	}
	got, _ := s.LoadSettings("windows")
	if !reflect.DeepEqual(got, settings.Defaults("windows")) {
		t.Fatalf("settings not saved: %+v", got)
	}
}

func TestPendingItemIDsIncludesAttachmentOwners(t *testing.T) {
	s := mem(t)
	for _, id := range []string{"b", "a"} {
		if err := s.Put(item(id, model.StatusOpen, 0)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutAttachment(att("x", "c")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAttachment(att("y", "a")); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSettingsShared(settings.Defaults("windows")); err != nil {
		t.Fatal(err)
	}
	ids, err := s.PendingItemIDs()
	if err != nil || !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
}

func TestMetaRoundTrip(t *testing.T) {
	s := mem(t)
	if _, ok, err := s.Meta("k"); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if err := s.SetMeta("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta("k", "v2"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := s.Meta("k"); v != "v2" || !ok || err != nil {
		t.Fatalf("v=%q ok=%v err=%v", v, ok, err)
	}
	if err := s.DeleteMeta("k"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Meta("k"); ok {
		t.Fatal("still there")
	}
}

func TestEnqueueAll(t *testing.T) {
	s := mem(t)
	_ = s.Put(item("a", model.StatusOpen, 0))
	_ = s.Put(item("b", model.StatusDone, 5))
	_ = s.PutAttachment(att("x", "a"))
	_ = s.SaveSettingsShared(settings.Defaults("windows"))
	es, _ := s.Outbox(100)
	for _, e := range es {
		if ok, err := s.Ack(e); !ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	if count(t, s) != 0 {
		t.Fatal("not empty")
	}
	if err := s.EnqueueAll(); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s); n != 4 {
		t.Fatalf("count=%d", n)
	}
	if err := s.EnqueueAll(); err != nil { // idempotent: bumps seq, same rows
		t.Fatal(err)
	}
	if n := count(t, s); n != 4 {
		t.Fatalf("count=%d", n)
	}
}

func TestEnqueueAllWithoutSettingsRow(t *testing.T) {
	s := mem(t)
	_ = s.Put(item("a", model.StatusOpen, 0))
	if err := s.EnqueueAll(); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s); n != 1 {
		t.Fatalf("count=%d", n)
	}
}
