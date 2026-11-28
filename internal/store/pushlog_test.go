package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPushLogCRUD(t *testing.T) {
	s := mem(t)
	got, err := s.PushLog()
	if err != nil || len(got) != 0 {
		t.Fatalf("empty log: %v %v", got, err)
	}
	a := PushEntry{Seq: "a", Server: "https://ntfy.sh", Topic: "ar-x", Hash: "h1",
		DeliverAt: ms(100), SentAt: ms(50), State: "scheduled"}
	b := PushEntry{Seq: "b", Server: "https://ntfy.sh", Topic: "ar-x", Hash: "h2",
		DeliverAt: ms(200), SentAt: ms(60), State: "scheduled"}
	for _, e := range []PushEntry{b, a} {
		if err := s.PutPush(e); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = s.PushLog()
	if !reflect.DeepEqual(got, []PushEntry{a, b}) {
		t.Fatalf("list=%+v", got)
	}

	// Upsert by Seq replaces the row.
	a.Hash, a.State, a.SentAt = "h1b", "dismissed", ms(70)
	if err := s.PutPush(a); err != nil {
		t.Fatal(err)
	}
	got, _ = s.PushLog()
	if !reflect.DeepEqual(got, []PushEntry{a, b}) {
		t.Fatalf("after upsert=%+v", got)
	}

	if err := s.DeletePush("a"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePush("missing"); err != nil {
		t.Fatal("deleting a missing seq:", err)
	}
	got, _ = s.PushLog()
	if !reflect.DeepEqual(got, []PushEntry{b}) {
		t.Fatalf("after delete=%+v", got)
	}

	c := PushEntry{Seq: "c", Server: "s", Topic: "t", Hash: "h3", DeliverAt: ms(300), SentAt: ms(1), State: "scheduled"}
	if err := s.PutPush(a); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPush(c); err != nil {
		t.Fatal(err)
	}
	// Prune is strict: DeliverAt == before is kept.
	if err := s.PrunePush(ms(200)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.PushLog()
	if !reflect.DeepEqual(got, []PushEntry{b, c}) {
		t.Fatalf("after prune=%+v", got)
	}

	if err := s.ClearPush(); err != nil {
		t.Fatal(err)
	}
	got, _ = s.PushLog()
	if len(got) != 0 {
		t.Fatalf("after clear=%+v", got)
	}
}

func TestMigrateV4ToV5(t *testing.T) {
	p := filepath.Join(t.TempDir(), "v4.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{migrations[0], migrations[1], migrations[2], migrations[3],
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta VALUES('schema_version','4')`,
		`INSERT INTO items VALUES('A','todo','one','["a"]',NULL,'UTC','',NULL,'open',NULL,NULL,1000,2000,'dev','high','d',1,'Priya','','')`,
	}
	for _, q := range stmts {
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
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v); err != nil || v != "5" {
		t.Fatalf("v=%q err=%v", v, err)
	}
	a, err := s.Get("A")
	if err != nil || a.Text != "one" || !a.Reply || a.OwedTo != "Priya" {
		t.Fatalf("A=%+v err=%v", a, err)
	}
	e := PushEntry{Seq: "s", Server: "x", Topic: "y", Hash: "h", DeliverAt: ms(5), SentAt: ms(4), State: "scheduled"}
	if err := s.PutPush(e); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.PushLog(); !reflect.DeepEqual(got, []PushEntry{e}) {
		t.Fatalf("log=%+v", got)
	}
}
