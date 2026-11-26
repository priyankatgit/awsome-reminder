package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/settings"
)

func mem(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func ms(sec int64) time.Time     { return time.UnixMilli(sec * 1000).UTC() }
func ptr(t time.Time) *time.Time { return &t }

func TestPutGetRoundTrip(t *testing.T) {
	s := mem(t)
	full := model.Item{
		ID: "01A", Kind: model.KindReminder, Text: "call mom", Tags: []string{"family", "x"},
		DueAt: ptr(ms(1_800_000_000)), TZ: "Europe/Berlin", RRule: "FREQ=WEEKLY",
		LeadTimes: []time.Duration{time.Hour, 10 * time.Minute}, Status: model.StatusDone,
		DoneAt: ptr(ms(1_800_000_100)), SnoozeUntil: ptr(ms(1_800_000_200)),
		CreatedAt: ms(1_700_000_000), UpdatedAt: ms(1_700_000_001), UpdatedBy: "dev1",
	}
	minimal := model.Item{
		ID: "01B", Kind: model.KindTodo, Text: "todo", Tags: []string{}, TZ: "UTC",
		Status: model.StatusOpen, CreatedAt: ms(1), UpdatedAt: ms(2), UpdatedBy: "dev1",
	}
	empty := minimal
	empty.ID = "01C"
	empty.LeadTimes = []time.Duration{}
	for _, it := range []model.Item{full, minimal, empty} {
		if err := s.Put(it); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(it.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, it) {
			t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, it)
		}
	}
	// nil Tags read back as non-nil
	n := minimal
	n.ID, n.Tags = "01D", nil
	if err := s.Put(n); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get("01D")
	if got.Tags == nil || len(got.Tags) != 0 {
		t.Errorf("tags = %#v", got.Tags)
	}
	// upsert
	full.Text = "changed"
	if err := s.Put(full); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get("01A")
	if got.Text != "changed" {
		t.Error("upsert failed")
	}
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func item(id string, st model.Status, done int64) model.Item {
	it := model.Item{ID: id, Kind: model.KindTodo, Text: id, Tags: []string{}, TZ: "UTC",
		Status: st, CreatedAt: ms(1), UpdatedAt: ms(1)}
	if done != 0 {
		it.DoneAt = ptr(ms(done))
	}
	return it
}

func TestListOpenExcludesDoneAndDeleted(t *testing.T) {
	s := mem(t)
	for _, it := range []model.Item{item("a", model.StatusOpen, 0), item("b", model.StatusDone, 5), item("c", model.StatusDeleted, 0), item("d", model.StatusOpen, 0)} {
		if err := s.Put(it); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListOpen()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "d" {
		t.Fatalf("%+v", got)
	}
}

func TestListDoneOrderAndLimit(t *testing.T) {
	s := mem(t)
	for _, it := range []model.Item{item("a", model.StatusDone, 10), item("b", model.StatusDone, 30), item("c", model.StatusDone, 20), item("o", model.StatusOpen, 0)} {
		if err := s.Put(it); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListDone(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "c" {
		t.Fatalf("%+v", got)
	}
	all, _ := s.ListDone(0)
	if len(all) != 3 {
		t.Fatalf("limit 0 should mean no limit, got %d", len(all))
	}
}

func TestDeviceIDStableAcrossReopen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	id := s.DeviceID()
	if len(id) != 26 {
		t.Fatalf("id %q", id)
	}
	s.Close()
	s, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.DeviceID() != id {
		t.Fatal("device id changed")
	}
}

func TestSettingsDefaultsThenSave(t *testing.T) {
	s := mem(t)
	got, err := s.LoadSettings("windows")
	if err != nil || !reflect.DeepEqual(got, settings.Defaults("windows")) {
		t.Fatalf("%+v %v", got, err)
	}
	want := settings.Defaults("windows")
	want.Hotkey = "Alt+Space"
	want.LeadTimes = []time.Duration{30 * time.Minute}
	want.ExportPath = "C:/x"
	want.Autostart = false
	if err := s.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err = s.LoadSettings("windows")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v %v", got, err)
	}
	want.Hotkey = "Alt+X"
	if err := s.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, _ = s.LoadSettings("windows")
	if got.Hotkey != "Alt+X" {
		t.Fatal("overwrite failed")
	}
}

func TestNotifyLog(t *testing.T) {
	s := mem(t)
	ok, err := s.WasNotified("k")
	if err != nil || ok {
		t.Fatal(ok, err)
	}
	if err := s.MarkNotified("k", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotified("k", time.Now()); err != nil {
		t.Fatal("second mark should be idempotent:", err)
	}
	ok, _ = s.WasNotified("k")
	if !ok {
		t.Fatal("expected notified")
	}
}

func TestMigrateIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.db")
	for i := 0; i < 2; i++ {
		s, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		var v string
		if err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v); err != nil || v != "1" {
			t.Fatalf("v=%q err=%v", v, err)
		}
		s.Close()
	}
}
