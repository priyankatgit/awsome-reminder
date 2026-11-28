// Package cloudsynctest holds the conformance tests every cloudsync.Syncer
// backend must pass, so memsync and Atlas are held to the same rules.
package cloudsynctest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"awesome-reminder/internal/cloudsync"
	"awesome-reminder/internal/model"
	"awesome-reminder/internal/settings"
)

// RawInsert writes doc as is into a collection ("items" or "attachments") of
// the backend behind s, the way another writer would: field names as in
// Atlas, values unchecked, and stamped with a fresh server stamp (_srv) so it
// sorts after every earlier write. doc has an "_id".
type RawInsert func(t *testing.T, s cloudsync.Syncer, collection string, doc map[string]any)

// Option configures Run.
type Option func(*config)

type config struct {
	rawInsert RawInsert
}

// WithRawInsert enables the malformed-document cases. A backend should always
// pass it; without it those cases are skipped.
func WithRawInsert(fn RawInsert) Option {
	return func(c *config) { c.rawInsert = fn }
}

// Run runs the conformance tests. open must return a Syncer over an empty
// backend each time it is called; it registers its own cleanup.
func Run(t *testing.T, open func(t *testing.T) cloudsync.Syncer, opts ...Option) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	tests := []struct {
		name string
		fn   func(t *testing.T, s cloudsync.Syncer)
	}{
		{"PushNewerOverwrites", testPushNewerOverwrites},
		{"PushOlderIgnored", testPushOlderIgnored},
		{"PushEqualIsNoop", testPushEqualIsNoop},
		{"TombstoneClearsData", testTombstoneClearsData},
		{"PullOrderAndPaging", testPullOrderAndPaging},
		{"SharedLWW", testSharedLWW},
		{"TestSpecialTextRoundTrips", testSpecialTextRoundTrips},
		{"ReplyFieldsRoundTrip", testReplyFieldsRoundTrip},
		{"AttachmentBytesSumsLive", testAttachmentBytesSumsLive},
		{"AttachmentPageIsByteBounded", testAttachmentPageIsByteBounded},
		{"MalformedDocDoesNotFailPull", func(t *testing.T, s cloudsync.Syncer) {
			if cfg.rawInsert == nil {
				t.Skip("backend gives no RawInsert hook (WithRawInsert)")
			}
			testMalformedDocDoesNotFailPull(t, s, cfg.rawInsert)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, open(t))
		})
	}
}

var base = time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

const (
	devA = "01JDDEV000000000000000000A"
	devB = "01JDDEV000000000000000000B"
)

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func itemID(n int) string {
	const digits = "0123456789"
	return "01JD0000000000000000000" + string(digits[n/100%10]) + string(digits[n/10%10]) + string(digits[n%10])
}

func item(n int, text string, at time.Time, by string) model.Item {
	return model.Item{
		ID: itemID(n), Kind: model.KindTodo, Text: text, Tags: []string{"inbox"}, Status: model.StatusOpen,
		CreatedAt: base.Add(-time.Hour), UpdatedAt: at, UpdatedBy: by,
	}
}

// pullAllItems reads every item from the start.
func pullAllItems(t *testing.T, s cloudsync.Syncer) []model.Item {
	t.Helper()
	var out []model.Item
	var wm cloudsync.Watermark
	for range 100 {
		page, next, err := s.PullItems(ctx(t), wm, 50)
		if err != nil {
			t.Fatalf("pull items: %v", err)
		}
		out = append(out, page...)
		if len(page) < 50 {
			return out
		}
		wm = next
	}
	t.Fatal("pull items did not end")
	return nil
}

// pullAllAttachments reads every attachment from the start. A page may be
// cut short by its byte budget, so only an empty page ends the pull.
func pullAllAttachments(t *testing.T, s cloudsync.Syncer) []cloudsync.RemoteAttachment {
	t.Helper()
	var out []cloudsync.RemoteAttachment
	var wm cloudsync.Watermark
	for range 100 {
		page, next, err := s.PullAttachments(ctx(t), wm, 8)
		if err != nil {
			t.Fatalf("pull attachments: %v", err)
		}
		if len(page) == 0 {
			return out
		}
		out = append(out, page...)
		wm = next
	}
	t.Fatal("pull attachments did not end")
	return nil
}

func onlyItem(t *testing.T, s cloudsync.Syncer) model.Item {
	t.Helper()
	all := pullAllItems(t, s)
	if len(all) != 1 {
		t.Fatalf("want 1 remote item, got %d: %+v", len(all), all)
	}
	return all[0]
}

func push(t *testing.T, s cloudsync.Syncer, it model.Item) {
	t.Helper()
	if err := s.PushItem(ctx(t), it); err != nil {
		t.Fatalf("push %s: %v", it.ID, err)
	}
}

func testPushNewerOverwrites(t *testing.T, s cloudsync.Syncer) {
	push(t, s, item(1, "old", base, devA))
	push(t, s, item(1, "new", base.Add(time.Second), devA))
	got := onlyItem(t, s)
	if got.Text != "new" || !got.UpdatedAt.Equal(base.Add(time.Second)) {
		t.Errorf("newer did not win: %+v", got)
	}
	// Equal time, greater device wins.
	push(t, s, item(1, "by B", base.Add(time.Second), devB))
	if got := onlyItem(t, s); got.Text != "by B" || got.UpdatedBy != devB {
		t.Errorf("greater device did not win the tie: %+v", got)
	}
}

func testPushOlderIgnored(t *testing.T, s cloudsync.Syncer) {
	push(t, s, item(1, "new", base.Add(time.Second), devA))
	push(t, s, item(1, "old", base, devB))
	// Same time, smaller device: also older.
	push(t, s, item(1, "smaller device", base.Add(time.Second), "01JDDEV0000000000000000000"))
	got := onlyItem(t, s)
	if got.Text != "new" || got.UpdatedBy != devA {
		t.Errorf("older overwrote: %+v", got)
	}
}

func testPushEqualIsNoop(t *testing.T, s cloudsync.Syncer) {
	push(t, s, item(1, "first", base, devA))
	_, wm, err := s.PullItems(ctx(t), cloudsync.Watermark{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	push(t, s, item(1, "same stamp", base, devA))
	again, _, err := s.PullItems(ctx(t), wm, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("equal push restamped the document: %+v", again)
	}
	if got := onlyItem(t, s); got.Text != "first" {
		t.Errorf("equal push overwrote: %+v", got)
	}
}

func webpLike(n int) []byte {
	// Backends store bytes as given; validity is the core's concern.
	return bytes.Repeat([]byte{byte(n)}, 10*n)
}

func att(id string, n int, at time.Time, deleted bool) (model.Attachment, []byte) {
	data := webpLike(n)
	sum := sha256.Sum256(data)
	a := model.Attachment{
		ID: id, ItemID: itemID(1), Mime: "image/webp", SHA256: hex.EncodeToString(sum[:]),
		Width: 3, Height: 4, Bytes: len(data), CreatedAt: base, UpdatedAt: at, UpdatedBy: devA, Deleted: deleted,
	}
	if deleted {
		return a, nil
	}
	return a, data
}

func testTombstoneClearsData(t *testing.T, s cloudsync.Syncer) {
	const id = "01JD00000000000000000000A1"
	a, data := att(id, 2, base, false)
	if err := s.PushAttachment(ctx(t), a, data); err != nil {
		t.Fatal(err)
	}
	got := pullAllAttachments(t, s)
	if len(got) != 1 || !bytes.Equal(got[0].Data, data) || got[0].Deleted {
		t.Fatalf("live attachment: %+v", got)
	}
	want := a
	want.UnreferencedSince = nil
	if g := normAtt(got[0].Attachment); !reflect.DeepEqual(g, want) {
		t.Errorf("attachment fields:\n got %+v\nwant %+v", g, want)
	}

	tomb, _ := att(id, 2, base.Add(time.Minute), true)
	if err := s.PushAttachment(ctx(t), tomb, nil); err != nil {
		t.Fatal(err)
	}
	got = pullAllAttachments(t, s)
	if len(got) != 1 {
		t.Fatalf("want 1 attachment, got %d", len(got))
	}
	if !got[0].Deleted || got[0].Data != nil {
		t.Fatalf("tombstone: deleted=%v, %d data bytes", got[0].Deleted, len(got[0].Data))
	}
	if !got[0].UpdatedAt.Equal(tomb.UpdatedAt) {
		t.Errorf("tombstone stamp: %v", got[0].UpdatedAt)
	}
}

func testPullOrderAndPaging(t *testing.T, s cloudsync.Syncer) {
	// Pushed out of id order: the order is by server stamp, then id.
	for _, n := range []int{5, 3, 1, 4, 2} {
		push(t, s, item(n, "x", base, devA))
	}
	seen := map[string]bool{}
	var wm cloudsync.Watermark
	var sizes []int
	for page := 0; page < 10; page++ {
		got, next, err := s.PullItems(ctx(t), wm, 2)
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(got))
		if len(got) == 0 {
			if next != wm {
				t.Errorf("empty page moved the watermark: %+v → %+v", wm, next)
			}
			break
		}
		if !wm.Less(next) {
			t.Errorf("watermark not strictly ascending: %+v → %+v", wm, next)
		}
		for _, it := range got {
			if seen[it.ID] {
				t.Errorf("%s repeated", it.ID)
			}
			seen[it.ID] = true
		}
		wm = next
		if len(got) < 2 {
			break
		}
	}
	if !reflect.DeepEqual(sizes, []int{2, 2, 1}) {
		t.Errorf("page sizes %v, want [2 2 1]", sizes)
	}
	if len(seen) != 5 {
		t.Errorf("saw %d items, want 5", len(seen))
	}
	// Writes after the watermark are pulled next, and only they are.
	push(t, s, item(3, "edited", base.Add(time.Second), devA))
	got, _, err := s.PullItems(ctx(t), wm, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != itemID(3) || got[0].Text != "edited" {
		t.Errorf("after watermark: %+v", got)
	}
}

func testSharedLWW(t *testing.T, s cloudsync.Syncer) {
	got, err := s.PullShared(ctx(t))
	if err != nil || got != nil {
		t.Fatalf("empty cluster: %+v %v", got, err)
	}
	p := settings.Shared{LeadTimes: []time.Duration{time.Hour, 10 * time.Minute}, DefaultTime: "09:00", NextWeekDay: "monday", NextWeekTime: "08:30", WorkDays: []string{"monday", "saturday"}, EndOfDay: "16:45", EndOfDaySweep: true,
		PushEnabled: true, PushServer: "https://ntfy.example.com/ntfy", PushTopic: "ar-abcdefghijklmnopqrstuvwxyz", PushReplies: true, PushHideText: true, PushGrace: 5 * time.Minute}
	pushShared := func(d cloudsync.SharedDoc) {
		t.Helper()
		if err := s.PushShared(ctx(t), d); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want cloudsync.SharedDoc) {
		t.Helper()
		got, err := s.PullShared(ctx(t))
		if err != nil || got == nil {
			t.Fatalf("pull shared: %+v %v", got, err)
		}
		if !got.Shared.Equal(want.Shared) || !got.UpdatedAt.Equal(want.UpdatedAt) || got.UpdatedBy != want.UpdatedBy {
			t.Errorf("shared:\n got %+v\nwant %+v", *got, want)
		}
	}
	first := cloudsync.SharedDoc{Shared: p, UpdatedAt: base, UpdatedBy: devA}
	pushShared(first)
	check(first)

	older := first
	older.Shared.DefaultTime, older.UpdatedAt = "07:00", base.Add(-time.Minute)
	pushShared(older)
	check(first)

	equal := first
	equal.Shared.DefaultTime = "07:00"
	pushShared(equal)
	check(first)

	newer := first
	newer.Shared.LeadTimes = []time.Duration{5 * time.Minute}
	newer.UpdatedAt, newer.UpdatedBy = base.Add(time.Minute), devB
	pushShared(newer)
	check(newer)
}

func normItem(it model.Item) model.Item {
	utc := func(p *time.Time) *time.Time {
		if p == nil {
			return nil
		}
		u := p.UTC()
		return &u
	}
	it.DueAt, it.DoneAt, it.SnoozeUntil = utc(it.DueAt), utc(it.DoneAt), utc(it.SnoozeUntil)
	it.CreatedAt, it.UpdatedAt = it.CreatedAt.UTC(), it.UpdatedAt.UTC()
	return it
}

func normAtt(a model.Attachment) model.Attachment {
	a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	return a
}

func testSpecialTextRoundTrips(t *testing.T, s cloudsync.Syncer) {
	due := base.Add(26 * time.Hour)
	done := base.Add(-30 * time.Minute)
	snooze := base.Add(90 * time.Minute)
	desc := "# Notes $HOME a.b {\"$gt\":1}\n\n![x](att:01JD00000000000000000000A1)\n\n" +
		strings.Repeat("ünïcødé 日本語 🙂 $set.dot ", 400)
	if len(desc) < 10*1024 {
		t.Fatalf("description only %d bytes", len(desc))
	}
	full := model.Item{
		ID: itemID(1), Kind: model.KindReminder, Text: `$HOME a.b {"$gt":1} "quoted" \back ünï 🙂`,
		Tags: []string{"$home", "a.b", "日本"}, DueAt: &due, TZ: "America/New_York",
		RRule: "FREQ=WEEKLY;BYDAY=MO", LeadTimes: []time.Duration{time.Hour, 90 * time.Second},
		Status: model.StatusOpen, DoneAt: &done, SnoozeUntil: &snooze,
		CreatedAt: base.Add(-time.Hour), UpdatedAt: base.Add(123 * time.Millisecond), UpdatedBy: devA,
		Priority: model.PriorityHigh, Description: desc,
	}
	plain := item(2, "{\"$gt\":1}", base, devB) // LeadTimes nil: the settings default
	none := item(3, "no alerts", base, devB)
	none.LeadTimes = []time.Duration{} // explicitly none, distinct from nil
	for _, it := range []model.Item{full, plain, none} {
		push(t, s, it)
	}
	got := map[string]model.Item{}
	for _, it := range pullAllItems(t, s) {
		got[it.ID] = normItem(it)
	}
	for _, want := range []model.Item{full, plain, none} {
		g, ok := got[want.ID]
		if !ok {
			t.Errorf("%s missing", want.ID)
			continue
		}
		if !reflect.DeepEqual(g, want) {
			t.Errorf("%s did not round-trip:\n got %#v\nwant %#v", want.ID, g, want)
		}
		if (g.LeadTimes == nil) != (want.LeadTimes == nil) {
			t.Errorf("%s: nil lead times %v, want %v", want.ID, g.LeadTimes == nil, want.LeadTimes == nil)
		}
	}
}

func testReplyFieldsRoundTrip(t *testing.T, s cloudsync.Syncer) {
	reply := item(1, "reply to Priya", base, devA)
	reply.Reply, reply.OwedTo, reply.Source, reply.ReplyOutcome = true, "Priya", "Teams", "dropped"
	plain := item(2, "plain", base, devA)
	push(t, s, reply)
	push(t, s, plain)
	got := map[string]model.Item{}
	for _, it := range pullAllItems(t, s) {
		got[it.ID] = normItem(it)
	}
	if g := got[reply.ID]; !g.Reply || g.OwedTo != "Priya" || g.Source != "Teams" || g.ReplyOutcome != "dropped" {
		t.Errorf("reply fields lost: %+v", g)
	}
	if g := got[plain.ID]; g.Reply || g.OwedTo != "" || g.Source != "" || g.ReplyOutcome != "" {
		t.Errorf("plain item gained reply fields: %+v", g)
	}
}

func testAttachmentBytesSumsLive(t *testing.T, s cloudsync.Syncer) {
	n, err := s.AttachmentBytes(ctx(t))
	if err != nil || n != 0 {
		t.Fatalf("empty: %d %v", n, err)
	}
	a1, d1 := att("01JD00000000000000000000A1", 1, base, false) // 10 bytes
	a2, d2 := att("01JD00000000000000000000A2", 2, base, false) // 20 bytes
	a3, d3 := att("01JD00000000000000000000A3", 3, base, false) // 30 bytes, then deleted
	for _, x := range []struct {
		a model.Attachment
		d []byte
	}{{a1, d1}, {a2, d2}, {a3, d3}} {
		if err := s.PushAttachment(ctx(t), x.a, x.d); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.AttachmentBytes(ctx(t)); err != nil || n != 60 {
		t.Fatalf("three live: %d %v", n, err)
	}
	tomb, _ := att(a3.ID, 3, base.Add(time.Minute), true)
	if err := s.PushAttachment(ctx(t), tomb, nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.AttachmentBytes(ctx(t)); err != nil || n != 30 {
		t.Errorf("after tombstone: %d %v, want 30", n, err)
	}
}

func testMalformedDocDoesNotFailPull(t *testing.T, s cloudsync.Syncer, raw RawInsert) {
	push(t, s, item(1, "valid one", base, devA))
	raw(t, s, "items", map[string]any{
		"_id": itemID(2), "kind": 7, "text": map[string]any{"nested": true}, "tags": "not a list",
		"status": false, "due_at": "soon", "lead_times": "x", "created_at": 3.5,
		"updated_at": "yesterday", "updated_by": 9,
	})
	push(t, s, item(3, "valid three", base, devA))

	// One page holding all three: no error, the valid ones intact, the bad
	// one returned (leniently decoded) rather than dropped, so the page is
	// full and the watermark is past all three.
	page, wm, err := s.PullItems(ctx(t), cloudsync.Watermark{}, 3)
	if err != nil {
		t.Fatalf("pull failed on a malformed document: %v", err)
	}
	if len(page) != 3 {
		t.Fatalf("want 3 documents (the malformed one leniently decoded), got %d: %+v", len(page), page)
	}
	if page[0].ID != itemID(1) || page[0].Text != "valid one" || page[2].ID != itemID(3) || page[2].Text != "valid three" {
		t.Errorf("valid documents: %+v, %+v", page[0], page[2])
	}
	if page[1].ID != itemID(2) {
		t.Errorf("malformed document id %q, want %q", page[1].ID, itemID(2))
	}
	rest, next, err := s.PullItems(ctx(t), wm, 3)
	if err != nil || len(rest) != 0 || next != wm {
		t.Errorf("watermark not past all three: %d more docs, %+v → %+v, %v", len(rest), wm, next, err)
	}

	// One at a time: each page advances, the malformed one included.
	var cur cloudsync.Watermark
	var ids []string
	for range 5 {
		got, next, err := s.PullItems(ctx(t), cur, 1)
		if err != nil {
			t.Fatalf("paged pull: %v", err)
		}
		if len(got) == 0 {
			break
		}
		if !cur.Less(next) {
			t.Fatalf("watermark did not advance: %+v → %+v", cur, next)
		}
		ids = append(ids, got[0].ID)
		cur = next
	}
	if !reflect.DeepEqual(ids, []string{itemID(1), itemID(2), itemID(3)}) {
		t.Errorf("paged ids %v", ids)
	}
}

// testAttachmentPageIsByteBounded: an attachment page stops before its
// documents pass AttachmentPageBytes, so it fits the client timeout on a slow
// link, but always holds at least one document, so an oversized one still
// comes through. Paging still visits every document once, in order.
func testAttachmentPageIsByteBounded(t *testing.T, s cloudsync.Syncer) {
	const kib = 1024
	sizes := []int{900 * kib, 900 * kib, 900 * kib, cloudsync.AttachmentPageBytes + 512*kib, 10}
	var ids []string
	for i, n := range sizes {
		data := bytes.Repeat([]byte{byte(i + 1)}, n)
		sum := sha256.Sum256(data)
		a := model.Attachment{
			ID: fmt.Sprintf("01JD00000000000000000000P%d", i), ItemID: itemID(1), Mime: "image/webp",
			SHA256: hex.EncodeToString(sum[:]), Width: 1, Height: 1, Bytes: n,
			CreatedAt: base, UpdatedAt: base, UpdatedBy: devA,
		}
		if err := s.PushAttachment(ctx(t), a, data); err != nil {
			t.Fatalf("push %s: %v", a.ID, err)
		}
		ids = append(ids, a.ID)
	}
	var wm cloudsync.Watermark
	var pages [][]string
	for range 10 {
		page, next, err := s.PullAttachments(ctx(t), wm, 8)
		if err != nil {
			t.Fatalf("pull attachments: %v", err)
		}
		if len(page) == 0 {
			if next != wm {
				t.Errorf("empty page moved the watermark: %+v → %+v", wm, next)
			}
			break
		}
		if !wm.Less(next) {
			t.Errorf("watermark not strictly ascending: %+v → %+v", wm, next)
		}
		var got []string
		total := 0
		for _, a := range page {
			got = append(got, a.ID)
			total += len(a.Data)
			if len(a.Data) != a.Bytes {
				t.Errorf("%s: %d data bytes, want %d", a.ID, len(a.Data), a.Bytes)
			}
		}
		if len(page) > 1 && total > cloudsync.AttachmentPageBytes {
			t.Errorf("page %v holds %d bytes, over the %d budget", got, total, cloudsync.AttachmentPageBytes)
		}
		pages = append(pages, got)
		wm = next
	}
	want := [][]string{{ids[0], ids[1]}, {ids[2]}, {ids[3]}, {ids[4]}}
	if !reflect.DeepEqual(pages, want) {
		t.Errorf("pages %v, want %v", pages, want)
	}
}
