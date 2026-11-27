package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"awesome-reminder/internal/cloudsync"
	"awesome-reminder/internal/cloudsync/atlas"
	"awesome-reminder/internal/cloudsync/memsync"
	"awesome-reminder/internal/core"
	"awesome-reminder/internal/model"
	"awesome-reminder/internal/secret"
	"awesome-reminder/internal/store"
)

// memSecret is a map-backed secret.Store.
type memSecret struct {
	mu     sync.Mutex
	v      string
	set    bool
	setErr error
}

func (m *memSecret) Get() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.set {
		return "", secret.ErrNotFound
	}
	return m.v, nil
}

func (m *memSecret) Set(v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setErr != nil {
		return m.setErr
	}
	m.v, m.set = v, true
	return nil
}

func (m *memSecret) Delete() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.v, m.set = "", false
	return nil
}

func (m *memSecret) Kind() string { return "Test store" }

// keepOpen is a memsync backend whose Close only counts, so one cluster's
// data survives an engine stop and a reconnect.
type keepOpen struct {
	*memsync.Syncer
	f *syncFixture
}

func (k keepOpen) Close(context.Context) error {
	k.f.mu.Lock()
	k.f.closes++
	k.f.mu.Unlock()
	return nil
}

// syncFixture is a controller over an in-memory store, with one memsync
// cluster per host and a fake Open.
type syncFixture struct {
	t   *testing.T
	st  *store.Store
	c   *core.Core
	sec *memSecret
	ctl *syncController

	mu       sync.Mutex
	clusters map[string]*memsync.Syncer
	opens    int
	closes   int
	openFn   func(ctx context.Context, uri string, n int) error // nil: succeed
	statuses []cloudsync.Status
	applied  [][]core.Applied
}

const (
	hostA = "a.example.mongodb.net"
	hostB = "b.example.mongodb.net"
	uriA  = "mongodb+srv://alice:S3cretPa55@" + hostA + "/?retryWrites=true"
	uriB  = "mongodb+srv://alice:S3cretPa55@" + hostB + "/"
)

// testClassify maps memsync.ErrNetwork to offline and everything else the
// way the app does.
func testClassify(err error, uri string) (cloudsync.State, string) {
	if errors.Is(err, memsync.ErrNetwork) {
		return cloudsync.StateOffline, "No connection to Atlas."
	}
	return atlasClassify(err, uri)
}

func newSyncFixture(t *testing.T, tune func(*syncDeps)) *syncFixture {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	f := &syncFixture{t: t, st: st, c: core.New(st, time.Now, time.UTC), sec: &memSecret{}, clusters: map[string]*memsync.Syncer{}}
	d := syncDeps{
		Core:   f.c,
		Secret: f.sec,
		Open: func(ctx context.Context, uri string) (cloudsync.Syncer, error) {
			f.mu.Lock()
			f.opens++
			n, fn := f.opens, f.openFn
			f.mu.Unlock()
			if !strings.HasPrefix(uri, "mongodb") {
				return nil, atlas.ErrBadURI
			}
			if fn != nil {
				if err := fn(ctx, uri, n); err != nil {
					return nil, err
				}
			}
			return keepOpen{f.cluster(atlas.Host(uri)), f}, nil
		},
		Host:     atlas.Host,
		Classify: testClassify,
		OnStatus: func(s cloudsync.Status) {
			f.mu.Lock()
			f.statuses = append(f.statuses, s)
			f.mu.Unlock()
		},
		OnApplied: func(a []core.Applied) {
			f.mu.Lock()
			f.applied = append(f.applied, a)
			f.mu.Unlock()
		},
		Poll:     20 * time.Millisecond,
		Debounce: time.Hour, // tests push explicitly
	}
	if tune != nil {
		tune(&d)
	}
	f.ctl = newSyncController(d)
	t.Cleanup(func() {
		f.ctl.Stop()
		st.Close()
	})
	return f
}

func (f *syncFixture) cluster(host string) *memsync.Syncer {
	f.mu.Lock()
	defer f.mu.Unlock()
	ms, ok := f.clusters[host]
	if !ok {
		ms = memsync.New()
		f.clusters[host] = ms
	}
	return ms
}

func (f *syncFixture) openCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens
}

func (f *syncFixture) seenStatuses() []cloudsync.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cloudsync.Status{}, f.statuses...)
}

// waitState waits until the controller reports want.
func (f *syncFixture) waitState(want cloudsync.State) cloudsync.Status {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := f.ctl.Status()
		if s.State == want {
			return s
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("state %q (%+v), want %q", s.State, s, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *syncFixture) meta(key string) (string, bool) {
	f.t.Helper()
	v, ok, err := f.st.Meta(key)
	if err != nil {
		f.t.Fatal(err)
	}
	return v, ok
}

func (f *syncFixture) outbox() int {
	f.t.Helper()
	n, err := f.st.OutboxCount()
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

// remoteItem stores an item as if a pull had written it: not in the outbox.
func (f *syncFixture) remoteItem(id string) {
	f.t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := f.st.PutRemote(model.Item{
		ID: id, Kind: model.KindTodo, Text: "item " + id, Tags: []string{}, Status: model.StatusOpen,
		CreatedAt: now, UpdatedAt: now, UpdatedBy: "01OTHERDEVICE0000000000000",
	}); err != nil {
		f.t.Fatal(err)
	}
}

// remoteIDs lists the item IDs a cluster holds.
func remoteIDs(t *testing.T, ms *memsync.Syncer) map[string]bool {
	t.Helper()
	items, _, err := ms.PullItems(context.Background(), cloudsync.Watermark{}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, it := range items {
		out[it.ID] = true
	}
	return out
}

// farWatermark is a watermark later than anything a test cluster stamps.
var farWatermark = func() string {
	b, _ := json.Marshal(cloudsync.Watermark{T: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), ID: "z"})
	return string(b)
}()

func TestSyncConnectBadURIStoresNothing(t *testing.T) {
	f := newSyncFixture(t, nil)
	f.remoteItem("01KEEP0000000000000000000A")
	for _, uri := range []string{"", "   ", "postgres://x@y/z"} {
		_, err := f.ctl.Connect(context.Background(), uri)
		if err == nil || err.Error() != atlas.ErrBadURI.Error() {
			t.Fatalf("Connect(%q) err = %v, want %q", uri, err, atlas.ErrBadURI.Error())
		}
	}
	if _, err := f.sec.Get(); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("secret saved after a bad URI: %v", err)
	}
	if _, ok := f.meta(metaSyncTarget); ok {
		t.Fatal("sync_target set after a bad URI")
	}
	if n := f.outbox(); n != 0 {
		t.Fatalf("outbox %d after a bad URI, want 0 (no EnqueueAll)", n)
	}
	if s := f.ctl.Status(); s.State != cloudsync.StateOff || s.Host != "" {
		t.Fatalf("status %+v, want off with no host", s)
	}
	if info := f.ctl.Info(context.Background()); info.Connected || info.AttachmentBytes != -1 {
		t.Fatalf("info %+v", info)
	}
}

func TestSyncConnectSavesSecretAndResetsOnNewTarget(t *testing.T) {
	f := newSyncFixture(t, nil)
	f.remoteItem("01FIRST0000000000000000000")
	if err := f.st.SetMeta("sync_wm_items", farWatermark); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetMeta("sync_wm_attachments", farWatermark); err != nil {
		t.Fatal(err)
	}

	// First connect: the URI is saved (trimmed), everything local is queued
	// and pushed, and the watermarks start over.
	s, err := f.ctl.Connect(context.Background(), "  "+uriA+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if s.Host != hostA || s.State == cloudsync.StateOff {
		t.Fatalf("Connect status %+v", s)
	}
	if v, err := f.sec.Get(); err != nil || v != uriA {
		t.Fatalf("secret %q, %v", v, err)
	}
	if v, _ := f.meta(metaSyncTarget); v != hostA+"/"+atlas.Database {
		t.Fatalf("sync_target %q", v)
	}
	f.waitState(cloudsync.StateSynced)
	if !remoteIDs(t, f.cluster(hostA))["01FIRST0000000000000000000"] {
		t.Fatal("first connect did not push the existing item (no EnqueueAll)")
	}
	for _, k := range []string{"sync_wm_items", "sync_wm_attachments"} {
		if v, _ := f.meta(k); v == farWatermark {
			t.Fatalf("%s not reset on first connect", k)
		}
	}
	if info := f.ctl.Info(context.Background()); !info.Connected || info.Host != hostA || info.SecretStore != "Test store" || info.AttachmentBytes != 0 {
		t.Fatalf("info %+v", info)
	}

	// Same cluster again: the watermark is kept and nothing is re-queued.
	f.remoteItem("01SECOND000000000000000000")
	if err := f.st.SetMeta("sync_wm_items", farWatermark); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ctl.Connect(context.Background(), uriA); err != nil {
		t.Fatal(err)
	}
	f.waitState(cloudsync.StateSynced)
	if v, _ := f.meta("sync_wm_items"); v != farWatermark {
		t.Fatalf("watermark %q changed on a reconnect to the same cluster", v)
	}
	if remoteIDs(t, f.cluster(hostA))["01SECOND000000000000000000"] {
		t.Fatal("reconnect to the same cluster re-queued everything")
	}

	// A different cluster: reset again and push everything there.
	if _, err := f.ctl.Connect(context.Background(), uriB); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.meta(metaSyncTarget); v != hostB+"/"+atlas.Database {
		t.Fatalf("sync_target %q", v)
	}
	f.waitState(cloudsync.StateSynced)
	if v, _ := f.meta("sync_wm_items"); v == farWatermark {
		t.Fatal("watermark not reset for a new cluster")
	}
	got := remoteIDs(t, f.cluster(hostB))
	if !got["01FIRST0000000000000000000"] || !got["01SECOND000000000000000000"] {
		t.Fatalf("new cluster holds %v, want both items", got)
	}
	if s := f.ctl.Status(); s.Host != hostB {
		t.Fatalf("host %q", s.Host)
	}
}

func TestSyncConnectSecretFailure(t *testing.T) {
	f := newSyncFixture(t, nil)
	f.sec.setErr = errors.New("keychain locked")
	_, err := f.ctl.Connect(context.Background(), uriA)
	if err == nil || err.Error() != "Couldn't save the connection string: keychain locked" {
		t.Fatalf("err = %v", err)
	}
	f.mu.Lock()
	closes := f.closes
	f.mu.Unlock()
	if closes != 1 {
		t.Fatalf("syncer closed %d times after a failed save, want 1", closes)
	}
	if s := f.ctl.Status(); s.State != cloudsync.StateOff {
		t.Fatalf("status %+v after a failed save", s)
	}
	if _, ok := f.meta(metaSyncTarget); ok {
		t.Fatal("sync_target set after a failed save")
	}
}

func TestSyncDisconnectKeepsLocalData(t *testing.T) {
	f := newSyncFixture(t, func(d *syncDeps) { d.Poll = time.Hour })
	if _, err := f.ctl.Connect(context.Background(), uriA); err != nil {
		t.Fatal(err)
	}
	f.waitState(cloudsync.StateSynced)
	// A change after the cycle stays queued (no kick, long debounce).
	it, err := f.c.Capture("water the plants")
	if err != nil {
		t.Fatal(err)
	}
	if f.outbox() == 0 {
		t.Fatal("capture not queued")
	}
	wm, _ := f.meta("sync_wm_items")
	before := f.outbox()

	if err := f.ctl.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if s := f.ctl.Status(); s.State != cloudsync.StateOff || s.Host != "" {
		t.Fatalf("status %+v, want off", s)
	}
	if all := f.seenStatuses(); all[len(all)-1].State != cloudsync.StateOff {
		t.Fatalf("last emitted %+v, want off", all[len(all)-1])
	}
	if _, err := f.sec.Get(); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("secret still saved: %v", err)
	}
	if got, err := f.c.Item(it.ID); err != nil || got.Text != "water the plants" {
		t.Fatalf("item %+v, %v", got, err)
	}
	if n := f.outbox(); n != before {
		t.Fatalf("outbox %d, want %d", n, before)
	}
	if v, _ := f.meta("sync_wm_items"); v != wm {
		t.Fatalf("watermark %q, want %q kept", v, wm)
	}
	if v, _ := f.meta(metaSyncTarget); v != hostA+"/"+atlas.Database {
		t.Fatalf("sync_target %q, want kept", v)
	}
	if info := f.ctl.Info(context.Background()); info.Connected || info.AttachmentBytes != -1 {
		t.Fatalf("info %+v", info)
	}
	f.ctl.Kick()
	f.ctl.SyncNow() // no engine: nothing happens, nothing panics
}

func TestStartDoesNotBlockOnNetwork(t *testing.T) {
	release := make(chan struct{})
	f := newSyncFixture(t, nil)
	f.openFn = func(ctx context.Context, _ string, _ int) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := f.sec.Set(uriA); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	f.ctl.Start(context.Background())
	if d := time.Since(t0); d > 50*time.Millisecond {
		t.Fatalf("Start took %v", d)
	}
	time.Sleep(30 * time.Millisecond)
	if s := f.ctl.Status(); s.State != cloudsync.StateOff {
		t.Fatalf("state %q while opening, want off", s.State)
	}
	close(release)
	if s := f.waitState(cloudsync.StateSynced); s.Host != hostA {
		t.Fatalf("host %q", s.Host)
	}
}

func TestStartWithoutURIDoesNothing(t *testing.T) {
	f := newSyncFixture(t, nil)
	f.ctl.Start(context.Background())
	time.Sleep(30 * time.Millisecond)
	if n := f.openCount(); n != 0 {
		t.Fatalf("Open called %d times with no URI", n)
	}
	if s := f.ctl.Status(); s.State != cloudsync.StateOff {
		t.Fatalf("status %+v", s)
	}
}

func TestStartRetriesAfterOpenFailure(t *testing.T) {
	f := newSyncFixture(t, nil)
	f.openFn = func(_ context.Context, _ string, n int) error {
		if n == 1 {
			return memsync.ErrNetwork
		}
		return nil
	}
	if err := f.sec.Set(uriA); err != nil {
		t.Fatal(err)
	}
	f.ctl.Start(context.Background())
	f.waitState(cloudsync.StateSynced)
	if n := f.openCount(); n != 2 {
		t.Fatalf("Open called %d times, want 2", n)
	}
	var offline *cloudsync.Status
	for _, s := range f.seenStatuses() {
		if s.State == cloudsync.StateOffline {
			offline = &s
			break
		}
	}
	if offline == nil || offline.Host != hostA || offline.Error != "No connection to Atlas." || offline.Warnings == nil {
		t.Fatalf("offline status %+v", offline)
	}
}

func TestStartOpenErrorRetriesSlowly(t *testing.T) {
	f := newSyncFixture(t, func(d *syncDeps) { d.ErrorRetry = time.Hour })
	f.openFn = func(context.Context, string, int) error { return errors.New("boom") }
	if err := f.sec.Set(uriA); err != nil {
		t.Fatal(err)
	}
	f.ctl.Start(context.Background())
	s := f.waitState(cloudsync.StateError)
	if s.Error != "boom" || s.Host != hostA {
		t.Fatalf("status %+v", s)
	}
	time.Sleep(100 * time.Millisecond) // five polls: an error waits for ErrorRetry
	if n := f.openCount(); n != 1 {
		t.Fatalf("Open called %d times, want 1 (error retry is an hour)", n)
	}
	// Sync now retries at once.
	f.mu.Lock()
	f.openFn = nil
	f.mu.Unlock()
	f.ctl.SyncNow()
	f.waitState(cloudsync.StateSynced)
}

func TestStatusNeverContainsURI(t *testing.T) {
	const password = "S3cretPa55"
	check := func(what, s string) {
		t.Helper()
		if strings.Contains(s, password) || strings.Contains(s, uriA) {
			t.Fatalf("%s leaks the connection string: %s", what, s)
		}
	}
	asJSON := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// An Open error that echoes the URI.
	f := newSyncFixture(t, nil)
	f.openFn = func(_ context.Context, uri string, _ int) error {
		return fmt.Errorf("connection() error occurred during connection handshake: %s", uri)
	}
	_, err := f.ctl.Connect(context.Background(), uriA)
	if err == nil {
		t.Fatal("want error")
	}
	check("Connect error", err.Error())

	// The same at startup: the status carries the classified text.
	if err := f.sec.Set(uriA); err != nil {
		t.Fatal(err)
	}
	f.ctl.Start(context.Background())
	s := f.waitState(cloudsync.StateError)
	check("status", asJSON(s))
	check("info", asJSON(f.ctl.Info(context.Background())))

	// Connected.
	g := newSyncFixture(t, nil)
	if s, err := g.ctl.Connect(context.Background(), uriA); err != nil {
		t.Fatal(err)
	} else {
		check("Connect status", asJSON(s))
	}
	g.waitState(cloudsync.StateSynced)
	check("status", asJSON(g.ctl.Status()))
	check("info", asJSON(g.ctl.Info(context.Background())))
	for _, s := range append(f.seenStatuses(), g.seenStatuses()...) {
		check("emitted status", asJSON(s))
	}
}

func TestServiceSyncBindingsWithoutController(t *testing.T) {
	svc := NewService(Deps{})
	if s := svc.SyncStatus(); s.State != cloudsync.StateOff || s.Warnings == nil {
		t.Fatalf("status %+v", s)
	}
	if info := svc.SyncInfo(); info.Connected || info.AttachmentBytes != -1 {
		t.Fatalf("info %+v", info)
	}
	if _, err := svc.SyncConnect(uriA); err == nil {
		t.Fatal("SyncConnect without a controller succeeded")
	}
	if err := svc.SyncDisconnect(); err != nil {
		t.Fatal(err)
	}
	svc.SyncNow()
}

func TestServiceSyncBindings(t *testing.T) {
	f := newSyncFixture(t, nil)
	svc := NewService(Deps{Core: f.c, Sync: f.ctl})
	if _, err := svc.SyncConnect(uriA); err != nil {
		t.Fatal(err)
	}
	f.waitState(cloudsync.StateSynced)
	if s := svc.SyncStatus(); s.State != cloudsync.StateSynced || s.Host != hostA {
		t.Fatalf("status %+v", s)
	}
	if info := svc.SyncInfo(); !info.Connected || info.Host != hostA {
		t.Fatalf("info %+v", info)
	}
	svc.SyncNow()
	if err := svc.SyncDisconnect(); err != nil {
		t.Fatal(err)
	}
	if s := svc.SyncStatus(); s.State != cloudsync.StateOff {
		t.Fatalf("status %+v", s)
	}
}

func TestKickRefreshesPendingWhileOpenFails(t *testing.T) {
	f := newSyncFixture(t, func(d *syncDeps) { d.Poll = time.Hour })
	f.openFn = func(context.Context, string, int) error { return memsync.ErrNetwork }
	if err := f.sec.Set(uriA); err != nil {
		t.Fatal(err)
	}
	f.ctl.Start(context.Background())
	if s := f.waitState(cloudsync.StateOffline); s.Pending != 0 {
		t.Fatalf("pending %d", s.Pending)
	}
	if _, err := f.c.Capture("buy milk"); err != nil {
		t.Fatal(err)
	}
	f.ctl.Kick()
	if s := f.ctl.Status(); s.State != cloudsync.StateOffline || s.Pending != 1 {
		t.Fatalf("status %+v, want offline with 1 waiting", s)
	}
	all := f.seenStatuses()
	if last := all[len(all)-1]; last.Pending != 1 {
		t.Fatalf("last emitted %+v, want 1 waiting", last)
	}
}
