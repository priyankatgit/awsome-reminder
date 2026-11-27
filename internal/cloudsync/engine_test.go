package cloudsync_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"awesome-reminder/internal/cloudsync"
	"awesome-reminder/internal/cloudsync/memsync"
	"awesome-reminder/internal/core"
	"awesome-reminder/internal/model"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/settings"
	"awesome-reminder/internal/store"
)

const testHost = "cluster0.example.mongodb.net"

func tinyWebP(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// clock is a fake clock shared by the cores, the engines and memsync.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock(t time.Time) *clock { return &clock{t: t} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var start = time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

// setup returns a memsync backend and a clock it shares.
func setup() (*memsync.Syncer, *clock) {
	clk := newClock(start)
	ms := memsync.New()
	ms.SetNow(clk.Now)
	return ms, clk
}

// device is one machine: its own store and core, and an engine on a shared
// backend.
type device struct {
	t   *testing.T
	st  *store.Store
	c   *core.Core
	dir string
	clk *clock
	e   *cloudsync.Engine

	mu       sync.Mutex
	statuses []cloudsync.Status
	applied  [][]core.Applied
	changes  int
}

func newDevice(t *testing.T, s cloudsync.Syncer, clk *clock) *device {
	return newDeviceOpts(t, s, clk, cloudsync.Options{})
}

func newDeviceOpts(t *testing.T, s cloudsync.Syncer, clk *clock, o cloudsync.Options) *device {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d := &device{t: t, st: st, clk: clk, dir: filepath.Join(t.TempDir(), "attachments")}
	d.c = core.New(st, clk.Now, time.UTC)
	d.c.SetAttachmentDir(d.dir)
	d.c.OnChange(func() {
		d.mu.Lock()
		d.changes++
		d.mu.Unlock()
	})
	o.Host, o.Now = testHost, clk.Now
	o.OnStatus = func(s cloudsync.Status) {
		d.mu.Lock()
		d.statuses = append(d.statuses, s)
		d.mu.Unlock()
	}
	o.OnApplied = func(a []core.Applied) {
		d.mu.Lock()
		d.applied = append(d.applied, a)
		d.mu.Unlock()
	}
	d.e = cloudsync.NewEngine(d.c, s, o)
	return d
}

func (d *device) sync() {
	d.t.Helper()
	if err := d.e.RunOnce(context.Background(), false); err != nil {
		d.t.Fatalf("sync: %v", err)
	}
}

func (d *device) capture(in string) model.Item {
	d.t.Helper()
	it, err := d.c.Capture(in)
	if err != nil {
		d.t.Fatalf("capture %q: %v", in, err)
	}
	return it
}

func (d *device) item(id string) model.Item {
	d.t.Helper()
	it, err := d.c.Item(id)
	if err != nil {
		d.t.Fatalf("item %s: %v", id, err)
	}
	return it
}

func (d *device) pending() int {
	d.t.Helper()
	n, err := d.c.OutboxCount()
	if err != nil {
		d.t.Fatal(err)
	}
	return n
}

func (d *device) open() map[string]model.Item {
	d.t.Helper()
	items, err := d.st.ListOpen()
	if err != nil {
		d.t.Fatal(err)
	}
	m := map[string]model.Item{}
	for _, it := range items {
		m[it.ID] = it
	}
	return m
}

// recorded returns and clears what the callbacks saw.
func (d *device) recorded() (statuses []cloudsync.Status, applied [][]core.Applied, changes int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	statuses, applied, changes = d.statuses, d.applied, d.changes
	d.statuses, d.applied, d.changes = nil, nil, 0
	return
}

func (d *device) statusCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.statuses)
}

// lastIs reports whether the last emitted status has state st and nothing
// pending.
func (d *device) lastIs(st cloudsync.State) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.statuses) == 0 {
		return false
	}
	last := d.statuses[len(d.statuses)-1]
	return last.State == st && (st != cloudsync.StateSynced || last.Pending == 0)
}

// fireNow is what the scheduler would show on this device right now.
func (d *device) fireNow() []scheduler.Alert {
	d.t.Helper()
	items, err := d.st.ListOpen()
	if err != nil {
		d.t.Fatal(err)
	}
	s, err := d.c.Settings()
	if err != nil {
		d.t.Fatal(err)
	}
	fire, _, _ := scheduler.Plan(items, s.LeadTimes, d.clk.Now(), func(k string) bool {
		ok, _ := d.st.WasNotified(k)
		return ok
	})
	return fire
}

func (d *device) watermark(key string) (cloudsync.Watermark, bool) {
	d.t.Helper()
	v, ok, err := d.c.SyncMeta(key)
	if err != nil {
		d.t.Fatal(err)
	}
	if !ok {
		return cloudsync.Watermark{}, false
	}
	var wm cloudsync.Watermark
	if err := json.Unmarshal([]byte(v), &wm); err != nil {
		d.t.Fatalf("%s: %v", key, err)
	}
	return wm, true
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// countingSyncer counts pulled pages and documents.
type countingSyncer struct {
	cloudsync.Syncer
	mu                  sync.Mutex
	itemPages, itemDocs int
	attDocs             int
}

func (c *countingSyncer) PullAttachments(ctx context.Context, after cloudsync.Watermark, limit int) ([]cloudsync.RemoteAttachment, cloudsync.Watermark, error) {
	atts, wm, err := c.Syncer.PullAttachments(ctx, after, limit)
	c.mu.Lock()
	c.attDocs += len(atts)
	c.mu.Unlock()
	return atts, wm, err
}

// docs returns and clears the pulled item and attachment document counts.
func (c *countingSyncer) docs() (items, atts int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	items, atts = c.itemDocs, c.attDocs
	c.itemDocs, c.attDocs = 0, 0
	return
}

func (c *countingSyncer) PullItems(ctx context.Context, after cloudsync.Watermark, limit int) ([]model.Item, cloudsync.Watermark, error) {
	items, wm, err := c.Syncer.PullItems(ctx, after, limit)
	c.mu.Lock()
	c.itemPages++
	c.itemDocs += len(items)
	c.mu.Unlock()
	return items, wm, err
}

func TestTwoDevicesConverge(t *testing.T) {
	ms, clk := setup()
	a, b := newDevice(t, ms, clk), newDevice(t, ms, clk)
	for i := range 3 {
		a.capture(fmt.Sprintf("from a %d", i))
		clk.Add(time.Second)
	}
	for i := range 2 {
		b.capture(fmt.Sprintf("from b %d", i))
		clk.Add(time.Second)
	}
	a.sync()
	b.sync()
	a.sync()
	ga, gb := a.open(), b.open()
	if len(ga) != 5 || !reflect.DeepEqual(ga, gb) {
		t.Fatalf("not converged:\nA %v\nB %v", ga, gb)
	}
	if a.pending() != 0 || b.pending() != 0 {
		t.Errorf("pending: a=%d b=%d", a.pending(), b.pending())
	}
}

func TestConcurrentEditLWW(t *testing.T) {
	ms, clk := setup()
	a, b := newDevice(t, ms, clk), newDevice(t, ms, clk)
	it := a.capture("original")
	a.sync()
	b.sync()

	// Both edit offline; the later edit wins on both.
	clk.Add(time.Minute)
	if _, err := a.c.Update(it.ID, "edit from a"); err != nil {
		t.Fatal(err)
	}
	clk.Add(time.Minute)
	if _, err := b.c.Update(it.ID, "edit from b"); err != nil {
		t.Fatal(err)
	}
	a.sync()
	b.sync()
	a.sync()
	if ga, gb := a.item(it.ID), b.item(it.ID); ga.Text != "edit from b" || !reflect.DeepEqual(ga, gb) {
		t.Fatalf("later edit lost:\nA %+v\nB %+v", ga, gb)
	}

	// The same millisecond: the greater device id wins on both, whichever
	// device syncs first.
	winner := a
	if b.st.DeviceID() > a.st.DeviceID() {
		winner = b
	}
	for _, order := range [][]*device{{a, b, a}, {b, a, b}} {
		clk.Add(time.Minute)
		if _, err := a.c.Update(it.ID, "tie edit a"); err != nil {
			t.Fatal(err)
		}
		if _, err := b.c.Update(it.ID, "tie edit b"); err != nil {
			t.Fatal(err)
		}
		want := winner.item(it.ID).Text
		for _, d := range order {
			d.sync()
		}
		ga, gb := a.item(it.ID), b.item(it.ID)
		if ga.Text != want || ga.UpdatedBy != winner.st.DeviceID() || !reflect.DeepEqual(ga, gb) {
			t.Fatalf("tie not resolved to %q:\nA %+v\nB %+v", want, ga, gb)
		}
		if a.pending() != 0 || b.pending() != 0 {
			t.Errorf("pending: a=%d b=%d", a.pending(), b.pending())
		}
	}
}

func TestDeleteTombstonePropagates(t *testing.T) {
	ms, clk := setup()
	a, b := newDevice(t, ms, clk), newDevice(t, ms, clk)
	it := a.capture("doomed")
	att, err := a.c.AddAttachment(it.ID, tinyWebP(t))
	if err != nil {
		t.Fatal(err)
	}
	a.sync()
	b.sync()
	if _, err := os.Stat(filepath.Join(b.dir, att.ID+".webp")); err != nil {
		t.Fatalf("attachment not restored on b: %v", err)
	}

	clk.Add(time.Minute)
	if err := a.c.Delete(it.ID); err != nil {
		t.Fatal(err)
	}
	clk.Add(8 * 24 * time.Hour) // the attachment was never referenced: swept
	if n, err := a.c.SweepAttachments(); err != nil || n != 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	a.sync()
	b.sync()
	if got := b.item(it.ID); got.Status != model.StatusDeleted {
		t.Errorf("delete not propagated: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(b.dir, att.ID+".webp")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("attachment file kept on b: %v", err)
	}
	if got, err := b.st.GetAttachment(att.ID); err != nil || !got.Deleted {
		t.Errorf("attachment row on b: %+v %v", got, err)
	}
}

func TestOfflineQueueReplay(t *testing.T) {
	ms, clk := setup()
	a, b := newDevice(t, ms, clk), newDevice(t, ms, clk)
	a.capture("one")
	a.capture("two")
	ms.FailNext(memsync.ErrNetwork, 3)
	for i := range 3 {
		if err := a.e.RunOnce(context.Background(), false); err == nil {
			t.Fatalf("cycle %d: want an error", i)
		}
		st := a.e.Status()
		if st.State != cloudsync.StateOffline || st.Pending != 2 || st.Error != "No connection to Atlas." {
			t.Fatalf("cycle %d: %+v", i, st)
		}
		if st.LastSyncAt != nil {
			t.Errorf("cycle %d: LastSyncAt set without a sync", i)
		}
	}
	if n := cloudsync.Failures(a.e); n != 3 {
		t.Errorf("failures %d, want 3", n)
	}
	clk.Add(time.Minute)
	a.sync()
	st := a.e.Status()
	if st.State != cloudsync.StateSynced || st.Pending != 0 || st.Error != "" || st.LastSyncAt == nil || !st.LastSyncAt.Equal(clk.Now()) {
		t.Fatalf("after recovery: %+v", st)
	}
	if v, ok, _ := a.c.SyncMeta("sync_last_at"); !ok || v == "" {
		t.Errorf("sync_last_at not saved")
	}
	if n := cloudsync.Failures(a.e); n != 0 {
		t.Errorf("failures %d after success", n)
	}
	b.sync()
	if len(b.open()) != 2 {
		t.Errorf("b has %d items", len(b.open()))
	}
}

func TestEditDuringPushStaysPending(t *testing.T) {
	ms, clk := setup()
	a, b := newDevice(t, ms, clk), newDevice(t, ms, clk)
	it := a.capture("first text")
	var once sync.Once
	ms.BeforePush(func() {
		once.Do(func() {
			clk.Add(time.Second)
			if _, err := a.c.Update(it.ID, "newer text"); err != nil {
				t.Error(err)
			}
		})
	})
	a.sync()
	if n := a.pending(); n != 1 {
		t.Fatalf("edit during push: pending %d, want 1", n)
	}
	if st := a.e.Status(); st.Pending != 1 {
		t.Errorf("status pending %d", st.Pending)
	}
	if got := a.item(it.ID); got.Text != "newer text" {
		t.Fatalf("local edit lost: %q", got.Text)
	}
	ms.BeforePush(nil)
	a.sync()
	if n := a.pending(); n != 0 {
		t.Fatalf("second cycle: pending %d", n)
	}
	b.sync()
	if ga, gb := a.item(it.ID), b.item(it.ID); gb.Text != "newer text" || !reflect.DeepEqual(ga, gb) {
		t.Errorf("not converged on the newer text:\nA %+v\nB %+v", ga, gb)
	}
}

// TestEditDuringPushSameMillisecond is TestEditDuringPushStaysPending with the
// edit landing in the same millisecond as the version being pushed.
func TestEditDuringPushSameMillisecond(t *testing.T) {
	ms, clk := setup() // the clock never moves
	a, b := newDevice(t, ms, clk), newDevice(t, ms, clk)
	it := a.capture("first text")
	var once sync.Once
	ms.BeforePush(func() {
		once.Do(func() {
			if _, err := a.c.Update(it.ID, "newer text"); err != nil {
				t.Error(err)
			}
		})
	})
	a.sync()
	if n := a.pending(); n != 1 {
		t.Fatalf("edit during push: pending %d, want 1", n)
	}
	ms.BeforePush(nil)
	a.sync()
	if n := a.pending(); n != 0 {
		t.Fatalf("second cycle: pending %d", n)
	}
	b.sync()
	if ga, gb := a.item(it.ID), b.item(it.ID); gb.Text != "newer text" || !reflect.DeepEqual(ga, gb) {
		t.Errorf("devices diverged:\nA %+v\nB %+v", ga, gb)
	}
}

func TestRestoreFreshDevice(t *testing.T) {
	ms, clk := setup()
	a := newDevice(t, ms, clk)
	clk.Add(-5 * 24 * time.Hour) // created days ago
	rem1 := a.capture("tomorrow 9am call bob")
	rem2 := a.capture("tomorrow 3pm pay rent #home")
	todo := a.capture("buy milk")
	if rem1.DueAt == nil || rem2.DueAt == nil {
		t.Fatal("fixture: reminders need a due time")
	}
	att, err := a.c.AddAttachment(todo.ID, tinyWebP(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.SetDescription(todo.ID, "see ![x](att:"+att.ID+")"); err != nil {
		t.Fatal(err)
	}
	s, _ := a.c.Settings()
	s.LeadTimes = []time.Duration{30 * time.Minute} // not one of the defaults
	if err := a.c.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	clk.Add(5 * 24 * time.Hour) // both reminders are now days overdue
	a.sync()
	if len(a.fireNow()) == 0 {
		t.Fatal("fixture: A's overdue reminders should be due to fire")
	}

	c := newDevice(t, ms, clk)
	cs, _ := c.c.Settings()
	cs.Hotkey = "Ctrl+Alt+K"
	if err := c.c.SaveSettings(cs); err != nil {
		t.Fatal(err)
	}
	if err := c.c.EnqueueAll(); err != nil { // first connect
		t.Fatal(err)
	}
	c.sync()

	for _, id := range []string{rem1.ID, rem2.ID, todo.ID} {
		if ga, gc := a.item(id), c.item(id); !reflect.DeepEqual(ga, gc) {
			t.Errorf("%s differs:\nA %+v\nC %+v", id, ga, gc)
		}
	}
	data, err := os.ReadFile(filepath.Join(c.dir, att.ID+".webp"))
	if err != nil {
		t.Fatalf("attachment file: %v", err)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != att.SHA256 {
		t.Error("attachment sha differs")
	}
	got, _ := c.c.Settings()
	if !reflect.DeepEqual(got.LeadTimes, []time.Duration{30 * time.Minute}) {
		t.Errorf("lead times %v, want A's", got.LeadTimes)
	}
	if got.Hotkey != "Ctrl+Alt+K" {
		t.Errorf("hotkey %q, want C's own", got.Hotkey)
	}
	if fire := c.fireNow(); len(fire) != 0 {
		t.Errorf("restore would pop %d cards: %+v", len(fire), fire)
	}
	if c.pending() != 0 {
		t.Errorf("c pending %d", c.pending())
	}
}

func TestPullOverlapIdempotent(t *testing.T) {
	ms, clk := setup()
	a := newDevice(t, ms, clk)
	it := a.capture("one")
	a.capture("two")
	if _, err := a.c.AddAttachment(it.ID, tinyWebP(t)); err != nil {
		t.Fatal(err)
	}
	s, _ := a.c.Settings()
	s.LeadTimes = []time.Duration{5 * time.Minute}
	if err := a.c.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	cs := &countingSyncer{Syncer: ms}
	b := newDevice(t, cs, clk)
	a.sync()
	b.sync()
	if _, applied, changes := b.recorded(); len(applied) != 1 || len(applied[0]) != 2 || changes == 0 {
		t.Fatalf("first pull: applied %v changes %d", applied, changes)
	}
	a.recorded()

	// 10 s later the previous pull started less than 2 min ago, so the
	// overlap re-reads what that pull read (a write it raced with may have
	// committed since). Applying the re-read documents is a no-op.
	clk.Add(10 * time.Second)
	cs.docs()
	b.sync()
	a.sync()
	if items, atts := cs.docs(); items != 2 || atts != 1 {
		t.Errorf("overlap re-read %d items, %d attachments; want 2, 1", items, atts)
	}
	for name, d := range map[string]*device{"a": a, "b": b} {
		if _, applied, changes := d.recorded(); len(applied) != 0 || changes != 0 {
			t.Errorf("%s: quiet cycle applied %v, %d changes", name, applied, changes)
		}
	}

	// Once the previous pull started more than 2 min after the newest
	// document, the overlap is past it: nothing is re-read.
	clk.Add(3 * time.Minute)
	b.sync() // the previous pull started 10 s after the newest document
	cs.docs()
	clk.Add(30 * time.Second)
	b.sync() // the previous pull started 3 min 10 s after it
	if items, atts := cs.docs(); items != 0 || atts != 0 {
		t.Errorf("quiet poll re-read %d items, %d attachments; want 0", items, atts)
	}
}

// TestQuietPollsDoNotRepull is the review probe: long after the last write,
// polls must not re-download the newest documents (an attachment's bytes
// included) on every cycle.
func TestQuietPollsDoNotRepull(t *testing.T) {
	ms, clk := setup()
	a := newDevice(t, ms, clk)
	it := a.capture("with a picture")
	a.capture("another")
	if _, err := a.c.AddAttachment(it.ID, tinyWebP(t)); err != nil {
		t.Fatal(err)
	}
	a.sync()
	cs := &countingSyncer{Syncer: ms}
	b := newDevice(t, cs, clk)
	b.sync()
	if items, atts := cs.docs(); items != 2 || atts != 1 {
		t.Fatalf("first pull: %d items, %d attachments", items, atts)
	}

	clk.Add(time.Hour)
	b.sync() // the previous pull is the restore itself: one overlap re-read
	cs.docs()
	for i := range 10 {
		clk.Add(30 * time.Second)
		b.sync()
		if items, atts := cs.docs(); items != 0 || atts != 0 {
			t.Errorf("quiet poll %d re-pulled %d items, %d attachments", i, items, atts)
		}
	}
}

// TestLateCommitInsideOverlapIsPulled: a write stamped before the saved
// watermark but committed after the pull that set it is still picked up,
// because it falls within 2 min of that pull's start.
func TestLateCommitInsideOverlapIsPulled(t *testing.T) {
	ms, clk := setup()
	ctx := context.Background()
	b := newDevice(t, ms, clk)
	early := remoteItem("01JD00000000000000000000C1", "early", clk.Now())
	if err := ms.PushItem(ctx, early); err != nil {
		t.Fatal(err)
	}
	b.sync()

	clk.Add(10 * time.Second)
	newer := remoteItem("01JD00000000000000000000C2", "newer", clk.Now())
	if err := ms.PushItem(ctx, newer); err != nil {
		t.Fatal(err)
	}
	clk.Add(10 * time.Second)
	pullStart := clk.Now()
	b.sync() // reads "newer": the watermark moves to its stamp
	wm, _ := b.watermark("sync_wm_items")

	// A write that started 15 s before that pull commits only now, with the
	// stamp it took when it started: before the watermark.
	late := remoteItem("01JD00000000000000000000C3", "late", clk.Now())
	if err := ms.PushItem(ctx, late); err != nil {
		t.Fatal(err)
	}
	stamp := pullStart.Add(-15 * time.Second)
	ms.SetItemSrv(late.ID, stamp)
	if !stamp.Before(wm.T) {
		t.Fatalf("fixture: late stamp %v should be before the watermark %v", stamp, wm.T)
	}

	clk.Add(30 * time.Second)
	b.sync()
	if got := b.item(late.ID); got.Text != "late" {
		t.Errorf("late commit not pulled: %+v", got)
	}
}

func TestPagingLargeBacklog(t *testing.T) {
	ms, clk := setup()
	a := newDevice(t, ms, clk)
	for i := range 450 {
		a.capture(fmt.Sprintf("item %d", i))
	}
	a.sync()
	if n := a.pending(); n != 0 {
		t.Fatalf("a pending %d", n)
	}
	cs := &countingSyncer{Syncer: ms}
	b := newDevice(t, cs, clk)
	b.sync()
	if n := len(b.open()); n != 450 {
		t.Fatalf("b has %d items", n)
	}
	_, applied, _ := b.recorded()
	total := 0
	for _, ap := range applied {
		total += len(ap)
	}
	if total != 450 {
		t.Errorf("OnApplied saw %d items", total)
	}
	if cs.itemPages != 3 {
		t.Errorf("%d item pages, want 3 (200+200+50)", cs.itemPages)
	}
}

const remoteDev = "01JDREM0TE0000000000000000"

func remoteItem(id, text string, at time.Time) model.Item {
	return model.Item{
		ID: id, Kind: model.KindTodo, Text: text, Tags: []string{"inbox"}, Status: model.StatusOpen,
		CreatedAt: at, UpdatedAt: at, UpdatedBy: remoteDev,
	}
}

func remoteAtt(id string, data []byte, at time.Time) model.Attachment {
	sum := sha256.Sum256(data)
	return model.Attachment{
		ID: id, ItemID: "01JD00000000000000000000C1", Mime: "image/webp", SHA256: hex.EncodeToString(sum[:]),
		Width: 1, Height: 1, Bytes: len(data), CreatedAt: at, UpdatedAt: at, UpdatedBy: remoteDev,
	}
}

func TestInvalidRemoteDocDoesNotStall(t *testing.T) {
	ms, clk := setup()
	ctx := context.Background()
	at := clk.Now()
	good1 := remoteItem("01JD00000000000000000000C1", "good one", at)
	good2 := remoteItem("01JD00000000000000000000C2", "good two", at)
	const badID = "01JD00000000000000000000B1"
	if err := ms.PushItem(ctx, good1); err != nil {
		t.Fatal(err)
	}
	ms.PutRaw(map[string]any{"_id": badID, "kind": "bogus", "text": 7, "tags": "x"})
	if err := ms.PushItem(ctx, good2); err != nil {
		t.Fatal(err)
	}
	data := tinyWebP(t)
	att1 := remoteAtt("01JD00000000000000000000A1", data, at)
	badAtt := remoteAtt("01JD00000000000000000000A2", data, at)
	badAtt.SHA256 = "00"
	att2 := remoteAtt("01JD00000000000000000000A3", data, at)
	for _, a := range []model.Attachment{att1, badAtt, att2} {
		if err := ms.PushAttachment(ctx, a, data); err != nil {
			t.Fatal(err)
		}
	}

	b := newDevice(t, ms, clk)
	b.sync()
	if b.item(good1.ID).Text != "good one" || b.item(good2.ID).Text != "good two" {
		t.Error("valid items not applied")
	}
	if _, err := b.c.Item(badID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("malformed item stored: %v", err)
	}
	for id, want := range map[string]bool{att1.ID: true, badAtt.ID: false, att2.ID: true} {
		_, err := os.Stat(filepath.Join(b.dir, id+".webp"))
		if (err == nil) != want {
			t.Errorf("attachment %s file present=%v, want %v", id, err == nil, want)
		}
	}
	_, lastItem, _ := ms.PullItems(ctx, cloudsync.Watermark{}, 100)
	_, lastAtt, _ := ms.PullAttachments(ctx, cloudsync.Watermark{}, 100)
	if wm, ok := b.watermark("sync_wm_items"); !ok || wm != lastItem {
		t.Errorf("items watermark %+v, want %+v (past the malformed doc)", wm, lastItem)
	}
	if wm, ok := b.watermark("sync_wm_attachments"); !ok || wm != lastAtt {
		t.Errorf("attachments watermark %+v, want %+v", wm, lastAtt)
	}

	b.recorded()
	clk.Add(10 * time.Second)
	b.sync()
	statuses, applied, changes := b.recorded()
	if len(applied) != 0 || changes != 0 {
		t.Errorf("next cycle not quiet: applied %v changes %d", applied, changes)
	}
	if len(statuses) != 1 || statuses[0].State != cloudsync.StateSynced {
		t.Errorf("next cycle statuses %+v", statuses)
	}
}

func TestApplyFailureKeepsWatermark(t *testing.T) {
	ms, clk := setup()
	ctx := context.Background()
	data := tinyWebP(t)
	att := remoteAtt("01JD00000000000000000000A1", data, clk.Now())
	if err := ms.PushAttachment(ctx, att, data); err != nil {
		t.Fatal(err)
	}
	b := newDevice(t, ms, clk)
	b.c.SetAttachmentDir("") // a local failure, not a bad document
	if err := b.e.RunOnce(ctx, false); err == nil {
		t.Fatal("want the cycle to fail")
	}
	if st := b.e.Status(); st.State != cloudsync.StateError || st.Error == "" {
		t.Errorf("status %+v", st)
	}
	if wm, ok := b.watermark("sync_wm_attachments"); ok {
		t.Errorf("watermark advanced past a failed document: %+v", wm)
	}
	b.c.SetAttachmentDir(b.dir)
	b.sync()
	if _, err := os.Stat(filepath.Join(b.dir, att.ID+".webp")); err != nil {
		t.Errorf("retry did not apply: %v", err)
	}
}

func TestQuietPollDoesNotShowSyncing(t *testing.T) {
	ms, clk := setup()
	a := newDevice(t, ms, clk)
	if st := a.e.Status(); st.Host != testHost || st.Warnings == nil {
		t.Errorf("initial status %+v", st)
	}
	if err := a.e.RunOnce(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	statuses, _, _ := a.recorded()
	if len(statuses) != 2 || statuses[0].State != cloudsync.StateSyncing || statuses[1].State != cloudsync.StateSynced {
		t.Fatalf("manual cycle statuses %+v", statuses)
	}

	clk.Add(30 * time.Second)
	a.sync()
	statuses, _, _ = a.recorded()
	if len(statuses) != 1 || statuses[0].State != cloudsync.StateSynced {
		t.Errorf("quiet poll statuses %+v", statuses)
	}

	a.capture("something to push")
	a.sync()
	statuses, _, _ = a.recorded()
	if len(statuses) != 2 || statuses[0].State != cloudsync.StateSyncing || statuses[0].Pending != 1 ||
		statuses[1].State != cloudsync.StateSynced || statuses[1].Pending != 0 {
		t.Errorf("poll with outbox statuses %+v", statuses)
	}
	for _, s := range statuses {
		if s.Warnings == nil || s.Host != testHost {
			t.Errorf("status %+v", s)
		}
	}
}

const skewText = "This computer's clock is 7 min off. Sync keeps the most recent edit by clock time, so fix the clock to avoid losing edits."

func TestClockSkewWarning(t *testing.T) {
	for _, skew := range []time.Duration{7 * time.Minute, -7 * time.Minute} {
		ms, clk := setup()
		ms.SetNow(func() time.Time { return clk.Now().Add(skew) })
		a := newDevice(t, ms, clk)
		a.sync()
		if st := a.e.Status(); !reflect.DeepEqual(st.Warnings, []string{skewText}) || st.State != cloudsync.StateSynced {
			t.Errorf("skew %v: %+v", skew, st)
		}
		a.sync() // the warning stays
		if st := a.e.Status(); !reflect.DeepEqual(st.Warnings, []string{skewText}) {
			t.Errorf("skew %v, second cycle: %+v", skew, st.Warnings)
		}
	}

	ms, clk := setup()
	ms.SetNow(func() time.Time { return clk.Now().Add(90 * time.Second) })
	a := newDevice(t, ms, clk)
	a.sync()
	if st := a.e.Status(); st.Warnings == nil || len(st.Warnings) != 0 {
		t.Errorf("90 s skew warned: %#v", st.Warnings)
	}
}

func TestScopeWarning(t *testing.T) {
	ms, clk := setup()
	ms.SetScopeTooBroad(true, nil)
	a := newDevice(t, ms, clk)
	a.sync()
	want := []string{"This Atlas user can reach other databases on your cluster. Limit it to readWrite on awesome_reminder (see the setup guide)."}
	if st := a.e.Status(); st.State != cloudsync.StateSynced || !reflect.DeepEqual(st.Warnings, want) {
		t.Errorf("too broad: %+v", st)
	}

	ms2, clk2 := setup()
	ms2.SetScopeTooBroad(true, errors.New("not authorized on admin"))
	b := newDevice(t, ms2, clk2)
	b.sync()
	if st := b.e.Status(); st.State != cloudsync.StateSynced || st.Warnings == nil || len(st.Warnings) != 0 {
		t.Errorf("check error: %+v", st)
	}
}

func TestNextDelay(t *testing.T) {
	o := cloudsync.Options{}
	for i, want := range []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second, 240 * time.Second, 300 * time.Second, 300 * time.Second} {
		if got := cloudsync.NextDelay(cloudsync.StateOffline, i+1, o); got != want {
			t.Errorf("offline, %d failures: %v, want %v", i+1, got, want)
		}
	}
	for _, n := range []int{1, 3, 10} {
		if got := cloudsync.NextDelay(cloudsync.StateError, n, o); got != 5*time.Minute {
			t.Errorf("error, %d failures: %v", n, got)
		}
	}
	if got := cloudsync.NextDelay(cloudsync.StateSynced, 0, o); got != 30*time.Second {
		t.Errorf("synced: %v", got)
	}
}

func TestStartKickStop(t *testing.T) {
	ms, clk := setup()
	a := newDeviceOpts(t, ms, clk, cloudsync.Options{Poll: time.Hour, Debounce: 10 * time.Millisecond})
	a.capture("before start")
	a.e.Start(context.Background())
	t.Cleanup(a.e.Stop)
	waitFor(t, "first cycle", func() bool { return a.lastIs(cloudsync.StateSynced) })
	statuses, _, _ := a.recorded()
	if len(statuses) == 0 || statuses[0].State != cloudsync.StateSyncing {
		t.Errorf("first cycle should show syncing: %+v", statuses)
	}

	// A kick with nothing to push runs no cycle.
	a.e.Kick()
	time.Sleep(60 * time.Millisecond)
	if n := a.statusCount(); n != 0 {
		t.Errorf("empty kick ran a cycle (%d statuses)", n)
	}

	// A kick after a change pushes it after the debounce, long before the poll.
	a.capture("after start")
	a.e.Kick()
	waitFor(t, "kicked push", func() bool { return a.pending() == 0 && a.lastIs(cloudsync.StateSynced) })
	remote, _, err := ms.PullItems(context.Background(), cloudsync.Watermark{}, 10)
	if err != nil || len(remote) != 2 {
		t.Fatalf("remote has %d items (%v)", len(remote), err)
	}

	// Stop joins the cycle in flight, then closes the backend.
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ms.BeforePush(func() {
		once.Do(func() { close(entered) })
		<-release
	})
	a.capture("in flight")
	a.e.SyncNow()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SyncNow did not start a cycle")
	}
	stopped := make(chan struct{})
	go func() {
		a.e.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a cycle was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	if ms.Closed() {
		t.Fatal("backend closed before the cycle ended")
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
	if !ms.Closed() {
		t.Error("Stop did not close the backend")
	}
	a.e.Stop()                      // idempotent
	a.e.Start(context.Background()) // no loop on a stopped engine
	if err := a.e.RunOnce(context.Background(), true); err == nil {
		t.Error("RunOnce after Stop must fail")
	}
}

func TestSyncNowResetsBackoff(t *testing.T) {
	ms, clk := setup()
	a := newDeviceOpts(t, ms, clk, cloudsync.Options{Poll: time.Hour})
	ms.FailNext(memsync.ErrNetwork, 2)
	a.capture("x")
	a.e.Start(context.Background())
	t.Cleanup(a.e.Stop)
	waitFor(t, "first failure", func() bool { return a.lastIs(cloudsync.StateOffline) })
	if n := cloudsync.Failures(a.e); n != 1 {
		t.Fatalf("failures %d", n)
	}
	a.recorded()
	a.e.SyncNow()
	waitFor(t, "second failure", func() bool { return a.statusCount() >= 2 && a.lastIs(cloudsync.StateOffline) })
	statuses, _, _ := a.recorded()
	if statuses[0].State != cloudsync.StateSyncing || statuses[len(statuses)-1].State != cloudsync.StateOffline {
		t.Errorf("SyncNow statuses %+v", statuses)
	}
	if n := cloudsync.Failures(a.e); n != 1 {
		t.Errorf("SyncNow did not reset the backoff: failures %d", n)
	}
	a.e.SyncNow()
	waitFor(t, "recovery", func() bool { return a.lastIs(cloudsync.StateSynced) })
	if n := cloudsync.Failures(a.e); n != 0 {
		t.Errorf("failures %d after success", n)
	}
}

// skippingSyncer drops items with an unknown kind from each page, keeping the
// page's watermark: a backend that skips undecodable documents.
type skippingSyncer struct{ cloudsync.Syncer }

func (s skippingSyncer) PullItems(ctx context.Context, after cloudsync.Watermark, limit int) ([]model.Item, cloudsync.Watermark, error) {
	items, wm, err := s.Syncer.PullItems(ctx, after, limit)
	var kept []model.Item
	for _, it := range items {
		if it.Kind == model.KindTodo || it.Kind == model.KindReminder {
			kept = append(kept, it)
		}
	}
	return kept, wm, err
}

func TestSkippedDocAdvancesWatermark(t *testing.T) {
	ms, clk := setup()
	ms.PutRaw(map[string]any{"_id": "01JD00000000000000000000B1", "kind": 7})
	_, want, _ := ms.PullItems(context.Background(), cloudsync.Watermark{}, 10)
	b := newDevice(t, skippingSyncer{ms}, clk)
	b.sync()
	if wm, ok := b.watermark("sync_wm_items"); !ok || wm != want {
		t.Errorf("watermark %+v (saved %v), want %+v past the skipped doc", wm, ok, want)
	}
}

// TestPushErrorStillPulls: a push refused for a reason other than the
// connection (an M0 over its space quota refuses every write) must not stop
// this machine from receiving changes. A lost connection still aborts.
func TestPushErrorStillPulls(t *testing.T) {
	ms, clk := setup()
	ctx := context.Background()
	b := newDevice(t, ms, clk)
	b.capture("local change the cluster refuses")
	remote := remoteItem("01JD00000000000000000000C1", "from elsewhere", clk.Now())
	if err := ms.PushItem(ctx, remote); err != nil {
		t.Fatal(err)
	}
	quota := errors.New("you are over your space quota, using 513 MB of 512 MB (8000)")
	ms.FailNext(quota, 1)
	if err := b.e.RunOnce(ctx, false); !errors.Is(err, quota) {
		t.Fatalf("cycle error %v, want the push error", err)
	}
	if got := b.item(remote.ID); got.Text != "from elsewhere" {
		t.Errorf("remote change not applied after a push error: %+v", got)
	}
	st := b.e.Status()
	if st.State != cloudsync.StateError || !strings.Contains(st.Error, quota.Error()) || st.Pending != 1 {
		t.Errorf("status %+v, want error with the push's text and 1 pending", st)
	}

	clk.Add(time.Minute)
	remote2 := remoteItem("01JD00000000000000000000C2", "while offline", clk.Now())
	if err := ms.PushItem(ctx, remote2); err != nil {
		t.Fatal(err)
	}
	ms.FailNext(memsync.ErrNetwork, 1)
	if err := b.e.RunOnce(ctx, false); !errors.Is(err, memsync.ErrNetwork) {
		t.Fatalf("cycle error %v, want offline", err)
	}
	if _, err := b.c.Item(remote2.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an offline push should abort the cycle before pulling: %v", err)
	}
	if st := b.e.Status(); st.State != cloudsync.StateOffline {
		t.Errorf("status %+v, want offline", st)
	}
}

// legacySettings saves custom reminder defaults the Phase 1.6 way (no shared
// stamp) with the migration's outbox seed, then runs the startup step that
// an upgraded app runs when it opens the store.
func (d *device) legacySettings(leads ...time.Duration) {
	d.t.Helper()
	s := settings.Defaults(runtime.GOOS)
	s.LeadTimes = leads
	if err := d.st.SaveSettingsShared(s); err != nil { // row + v3 seed
		d.t.Fatal(err)
	}
	if ok, err := d.st.StampLegacyShared(runtime.GOOS); err != nil || !ok {
		d.t.Fatalf("stamp legacy settings: %v %v", ok, err)
	}
}

func TestRestoreLegacySettings(t *testing.T) {
	ms, clk := setup()
	a := newDevice(t, ms, clk)
	a.legacySettings(30 * time.Minute)
	a.sync()

	c := newDevice(t, ms, clk)
	if err := c.c.EnqueueAll(); err != nil { // first connect
		t.Fatal(err)
	}
	c.sync()
	if got, _ := c.c.Settings(); !reflect.DeepEqual(got.LeadTimes, []time.Duration{30 * time.Minute}) {
		t.Errorf("fresh device lead times %v, want A's legacy ones", got.LeadTimes)
	}
	a.sync()
	if got, _ := a.c.Settings(); !reflect.DeepEqual(got.LeadTimes, []time.Duration{30 * time.Minute}) {
		t.Errorf("A's lead times %v after C connected", got.LeadTimes)
	}
}

func TestTwoLegacyDevicesConverge(t *testing.T) {
	ms, clk := setup()
	a, b := newDevice(t, ms, clk), newDevice(t, ms, clk)
	a.legacySettings(30 * time.Minute)
	b.legacySettings(15 * time.Minute)
	a.sync()
	b.sync()
	a.sync()
	ga, _ := a.c.Settings()
	gb, _ := b.c.Settings()
	if !ga.Shared().Equal(gb.Shared()) {
		t.Errorf("not converged: A %v, B %v", ga.LeadTimes, gb.LeadTimes)
	}
	if a.pending() != 0 || b.pending() != 0 {
		t.Errorf("pending: a=%d b=%d", a.pending(), b.pending())
	}
}
