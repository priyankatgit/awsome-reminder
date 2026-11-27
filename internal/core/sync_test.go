package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/store"
)

func remoteItem(id string, by string, at time.Time) model.Item {
	return model.Item{
		ID: id, Kind: model.KindTodo, Text: "remote", Tags: []string{"inbox"}, Status: model.StatusOpen,
		CreatedAt: at.Add(-time.Hour), UpdatedAt: at, UpdatedBy: by,
	}
}

const (
	idA = "01JD0000000000000000000001"
	idB = "01JD0000000000000000000002"
	idC = "01JD0000000000000000000003"
	idD = "01JD0000000000000000000004"
)

func TestApplyRemoteItemsLWW(t *testing.T) {
	e := newEnv(t)
	base := e.now.UTC()
	if err := e.st.Put(remoteItem(idA, "M", base)); err != nil {
		t.Fatal(err)
	}
	try := func(at time.Time, by string) bool {
		it := remoteItem(idA, by, at)
		res, err := e.c.ApplyRemoteItems([]model.Item{it})
		if err != nil {
			t.Fatal(err)
		}
		return len(res) == 1
	}
	if !try(base.Add(time.Second), "A") {
		t.Error("newer should apply")
	}
	if try(base, "Z") {
		t.Error("older should be skipped")
	}
	cur := base.Add(time.Second)
	if try(cur, "A") {
		t.Error("identical stamp should be skipped")
	}
	if !try(cur, "B") {
		t.Error("equal time, greater device should apply")
	}
	if try(cur, "AA") {
		t.Error("equal time, smaller device should be skipped")
	}
	got, _ := e.st.Get(idA)
	if got.UpdatedBy != "B" || !got.UpdatedAt.Equal(cur) {
		t.Errorf("stamp not kept verbatim: %+v", got)
	}
}

func TestApplyRemoteClearsPendingOnlyWhenRemoteWins(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "local thing")
	pending := func() int { n, _ := e.c.OutboxCount(); return n }
	if pending() != 1 {
		t.Fatalf("want 1 pending, got %d", pending())
	}
	old := remoteItem(it.ID, "Z", it.UpdatedAt.Add(-time.Minute))
	e.c.ApplyRemoteItems([]model.Item{old})
	if pending() != 1 {
		t.Error("older remote must leave outbox row")
	}
	win := remoteItem(it.ID, "Z", it.UpdatedAt.Add(time.Minute))
	e.c.ApplyRemoteItems([]model.Item{win})
	if pending() != 0 {
		t.Error("winning remote must clear outbox row")
	}
}

func TestApplyRemoteSkipsInvalid(t *testing.T) {
	e := newEnv(t)
	at := e.now.UTC()
	due := at.Add(time.Hour)
	bad1 := remoteItem("not-a-ulid", "A", at)
	bad2 := remoteItem(idA, "A", at)
	bad2.Kind = "x"
	bad3 := remoteItem(idB, "A", at)
	bad3.Priority = "urgent"
	bad4 := remoteItem(idC, "A", at)
	bad4.Kind = model.KindReminder
	bad5 := remoteItem("01JD0000000000000000000005", "A", at)
	bad5.Tags = nil
	bad6 := remoteItem("01JD0000000000000000000006", "A", at)
	bad6.DueAt = &due // to-do with a due time
	bad7 := remoteItem("01JD0000000000000000000007", "A", at)
	bad7.TZ = "Nowhere/Land"
	bad8 := remoteItem("01JD0000000000000000000008", "A", at)
	bad8.Status = "weird"
	good := remoteItem(idD, "A", at)
	res, err := e.c.ApplyRemoteItems([]model.Item{bad1, bad2, bad3, bad4, bad5, bad6, bad7, bad8, good})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].After.ID != idD || res[0].Before != nil {
		t.Fatalf("want only the valid item inserted, got %+v", res)
	}
	all, _ := e.st.ListOpen()
	if len(all) != 1 {
		t.Errorf("want 1 stored, got %d", len(all))
	}
}

func planFor(t *testing.T, e *env) ([]scheduler.Alert, time.Time) {
	t.Helper()
	items, err := e.st.ListOpen()
	if err != nil {
		t.Fatal(err)
	}
	s, _ := e.c.Settings()
	fire, _, next := scheduler.Plan(items, s.LeadTimes, e.now, func(k string) bool { n, _ := e.st.WasNotified(k); return n })
	return fire, next
}

func TestApplyRemoteNewItemSuppressesPastAlerts(t *testing.T) {
	e := newEnv(t)
	s, _ := e.c.Settings()
	s.LeadTimes = []time.Duration{time.Hour, 10 * time.Minute}
	if err := e.c.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	now := e.now.UTC()
	past := remoteItem(idA, "R", now.Add(-48*time.Hour))
	past.Kind = model.KindReminder
	pd := now.Add(-time.Hour)
	past.DueAt = &pd
	past.CreatedAt = now.Add(-72 * time.Hour)
	if _, err := e.c.ApplyRemoteItems([]model.Item{past}); err != nil {
		t.Fatal(err)
	}
	if fire, _ := planFor(t, e); len(fire) != 0 {
		t.Fatalf("inserted overdue item must not fire: %+v", fire)
	}
	// The marks exist as keys (written before the insert, see sync.go).
	if ok, _ := e.st.WasNotified(idA + "|" + strconv.FormatInt(pd.Unix(), 10) + "|0"); !ok {
		t.Fatal("at-time key not marked")
	}
	if ok, _ := e.st.WasNotified(idA + "|" + strconv.FormatInt(pd.Unix(), 10) + "|3600"); !ok {
		t.Fatal("1h lead key not marked")
	}

	// A reminder whose at-time passed 30 s ago still fires.
	fresh := remoteItem(idD, "R", now.Add(-48*time.Hour))
	fresh.Kind = model.KindReminder
	frd := now.Add(-30 * time.Second)
	fresh.DueAt = &frd
	fresh.CreatedAt = now.Add(-72 * time.Hour)
	if _, err := e.c.ApplyRemoteItems([]model.Item{fresh}); err != nil {
		t.Fatal(err)
	}
	if fire, _ := planFor(t, e); len(fire) != 1 || fire[0].ItemID != idD {
		t.Fatalf("fresh alert must fire: %+v", fire)
	}
	e.st.MarkNotified(idD+"|"+strconv.FormatInt(frd.Unix(), 10)+"|0", now)

	fut := remoteItem(idB, "R", now.Add(-48*time.Hour))
	fut.Kind = model.KindReminder
	fd := now.Add(2 * time.Hour)
	fut.DueAt = &fd
	fut.CreatedAt = now.Add(-72 * time.Hour)
	if _, err := e.c.ApplyRemoteItems([]model.Item{fut}); err != nil {
		t.Fatal(err)
	}
	if fire, next := planFor(t, e); len(fire) != 0 || next.IsZero() {
		t.Fatalf("future item keeps future alerts: fire=%+v next=%v", fire, next)
	}

	// An existing item updated remotely to 5 min ago still fires its at-time alert.
	ex := remoteItem(idC, "R", now.Add(-48*time.Hour))
	ex.Kind = model.KindReminder
	xd := now.Add(5 * time.Hour)
	ex.DueAt = &xd
	ex.CreatedAt = now.Add(-72 * time.Hour)
	e.c.ApplyRemoteItems([]model.Item{ex})
	upd := ex
	ud := now.Add(-5 * time.Minute)
	upd.DueAt = &ud
	upd.UpdatedAt = now.Add(-time.Minute)
	if _, err := e.c.ApplyRemoteItems([]model.Item{upd}); err != nil {
		t.Fatal(err)
	}
	fire, _ := planFor(t, e)
	if len(fire) != 1 || fire[0].ItemID != idC || !fire[0].Missed {
		t.Fatalf("updated item should fire a missed alert: %+v", fire)
	}
}

func remoteAtt(data []byte, at time.Time, by string) model.Attachment {
	sum := sha256.Sum256(data)
	return model.Attachment{
		ID: "01JD00000000000000000000A1", ItemID: idA, Mime: "image/webp", SHA256: hex.EncodeToString(sum[:]),
		Width: 1, Height: 1, Bytes: len(data), CreatedAt: at, UpdatedAt: at, UpdatedBy: by,
	}
}

func TestApplyRemoteAttachment(t *testing.T) {
	e, dir := attEnv(t)
	data := tinyWebP(t)
	at := e.now.UTC()
	a := remoteAtt(data, at, "R")

	ok, err := e.c.ApplyRemoteAttachment(a, data)
	if err != nil || !ok {
		t.Fatalf("apply: %v %v", ok, err)
	}
	if !fileExists(dir, a.ID) {
		t.Fatal("file not written")
	}
	got, err := e.st.GetAttachment(a.ID)
	if err != nil || got.UnreferencedSince != nil {
		t.Fatalf("row: %+v %v", got, err)
	}

	// sha mismatch and PNG body rejected, nothing written
	bad := a
	bad.ID = "01JD00000000000000000000A2"
	bad.SHA256 = "00"
	if _, err := e.c.ApplyRemoteAttachment(bad, data); err == nil {
		t.Error("sha mismatch must error")
	}
	png := []byte("\x89PNG\r\n\x1a\n0000")
	bp := remoteAtt(png, at, "R")
	bp.ID = "01JD00000000000000000000A3"
	if _, err := e.c.ApplyRemoteAttachment(bp, png); err == nil {
		t.Error("png must error")
	}
	for _, id := range []string{bad.ID, bp.ID} {
		if fileExists(dir, id) {
			t.Errorf("%s file written", id)
		}
		if _, err := e.st.GetAttachment(id); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s row written: %v", id, err)
		}
	}

	// older remote ignored
	older := a
	older.UpdatedAt = at.Add(-time.Hour)
	older.Deleted = true
	if ok, err := e.c.ApplyRemoteAttachment(older, nil); err != nil || ok {
		t.Fatalf("older: %v %v", ok, err)
	}
	if !fileExists(dir, a.ID) {
		t.Error("older tombstone removed file")
	}

	// local UnreferencedSince kept on a newer update; then a tombstone removes the file
	ref := at.Add(-time.Hour)
	loc := got
	loc.UnreferencedSince = &ref
	if err := e.st.PutAttachment(loc); err != nil {
		t.Fatal(err)
	}
	newer := a
	newer.UpdatedAt = at.Add(time.Minute)
	if ok, err := e.c.ApplyRemoteAttachment(newer, data); err != nil || !ok {
		t.Fatalf("newer: %v %v", ok, err)
	}
	got, _ = e.st.GetAttachment(a.ID)
	if got.UnreferencedSince == nil || !got.UnreferencedSince.Equal(ref) {
		t.Errorf("UnreferencedSince lost: %+v", got.UnreferencedSince)
	}
	tomb := a
	tomb.UpdatedAt = at.Add(2 * time.Minute)
	tomb.Deleted = true
	if ok, err := e.c.ApplyRemoteAttachment(tomb, nil); err != nil || !ok {
		t.Fatalf("tomb: %v %v", ok, err)
	}
	if fileExists(dir, a.ID) {
		t.Error("tombstone left file")
	}
	got, _ = e.st.GetAttachment(a.ID)
	if !got.Deleted {
		t.Error("row not deleted")
	}
}

func TestApplyRemoteShared(t *testing.T) {
	e := newEnv(t)
	s, _ := e.c.Settings()
	s.Hotkey = "Ctrl+Alt+Q"
	if err := e.c.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	_, at, by, _ := e.c.SharedForSync()
	p := s.Shared()
	p.LeadTimes = []time.Duration{5 * time.Minute}
	ok, err := e.c.ApplyRemoteShared(p, at.Add(time.Minute), "R")
	if err != nil || !ok {
		t.Fatalf("newer: %v %v", ok, err)
	}
	got, _ := e.c.Settings()
	if len(got.LeadTimes) != 1 || got.LeadTimes[0] != 5*time.Minute || got.Hotkey != "Ctrl+Alt+Q" {
		t.Errorf("settings: %+v", got)
	}
	if got.SharedUpdatedBy != "R" || !got.SharedUpdatedAt.Equal(at.Add(time.Minute)) {
		t.Errorf("stamp: %v %v", got.SharedUpdatedAt, got.SharedUpdatedBy)
	}
	p2 := p
	p2.LeadTimes = []time.Duration{time.Hour}
	if ok, _ := e.c.ApplyRemoteShared(p2, at, by); ok {
		t.Error("older must be ignored")
	}
	p3 := p
	p3.DefaultTime = "25:00"
	if ok, _ := e.c.ApplyRemoteShared(p3, at.Add(time.Hour), "R"); ok {
		t.Error("invalid must be rejected")
	}
}

func TestApplyRemoteSharedClearsPending(t *testing.T) {
	e := newEnv(t)
	s, _ := e.c.Settings()
	s.LeadTimes = []time.Duration{time.Minute}
	if err := e.c.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.c.OutboxCount(); n != 1 {
		t.Fatalf("want 1, got %d", n)
	}
	_, at, _, _ := e.c.SharedForSync()
	e.c.ApplyRemoteShared(s.Shared(), at.Add(time.Minute), "R")
	if n, _ := e.c.OutboxCount(); n != 0 {
		t.Errorf("settings outbox not cleared: %d", n)
	}
}

func TestAttachmentForSync(t *testing.T) {
	e, dir := attEnv(t)
	it := mustCapture(t, e, "x")
	a, err := e.c.AddAttachment(it.ID, tinyWebP(t))
	if err != nil {
		t.Fatal(err)
	}
	got, data, err := e.c.AttachmentForSync(a.ID)
	if err != nil || got.ID != a.ID || len(data) != a.Bytes {
		t.Fatalf("live: %v %v %d", got, err, len(data))
	}
	os.Remove(filepath.Join(dir, a.ID+".webp"))
	if _, _, err := e.c.AttachmentForSync(a.ID); !errors.Is(err, ErrAttachmentFileMissing) {
		t.Errorf("want missing, got %v", err)
	}
	a.Deleted = true
	e.st.PutAttachment(a)
	got, data, err = e.c.AttachmentForSync(a.ID)
	if err != nil || data != nil || !got.Deleted {
		t.Errorf("tombstone: %v %v %v", got, data, err)
	}
}

func TestViewPending(t *testing.T) {
	e := newEnv(t)
	v, _ := e.c.View()
	if v.Pending == nil || len(v.Pending) != 0 {
		t.Fatalf("want empty non-nil, got %#v", v.Pending)
	}
	it := mustCapture(t, e, "a")
	v, _ = e.c.View()
	if len(v.Pending) != 1 || v.Pending[0] != it.ID {
		t.Errorf("pending: %v", v.Pending)
	}
}

func TestApplyBatchFiresOnChangeOnce(t *testing.T) {
	e := newEnv(t)
	n := 0
	e.c.OnChange(func() { n++ })
	at := e.now.UTC()
	e.c.ApplyRemoteItems([]model.Item{remoteItem(idA, "R", at), remoteItem(idB, "R", at), remoteItem(idC, "R", at)})
	if n != 1 {
		t.Errorf("want 1 notify, got %d", n)
	}
	e.c.ApplyRemoteItems([]model.Item{remoteItem(idA, "R", at)}) // nothing applied
	if n != 1 {
		t.Errorf("no-op notified: %d", n)
	}
}
