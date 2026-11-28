package push

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/push/pushtest"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/store"
)

var _ Log = (*store.Store)(nil)

// memLog is an in-memory Log.
type memLog struct {
	mu sync.Mutex
	m  map[string]store.PushEntry
}

func newMemLog() *memLog { return &memLog{m: map[string]store.PushEntry{}} }

func (l *memLog) PushLog() ([]store.PushEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []store.PushEntry{}
	for _, e := range l.m {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].DeliverAt.Equal(out[j].DeliverAt) {
			return out[i].DeliverAt.Before(out[j].DeliverAt)
		}
		return out[i].Seq < out[j].Seq
	})
	return out, nil
}

func (l *memLog) PutPush(e store.PushEntry) error {
	l.mu.Lock()
	l.m[e.Seq] = e
	l.mu.Unlock()
	return nil
}

func (l *memLog) DeletePush(seq string) error {
	l.mu.Lock()
	delete(l.m, seq)
	l.mu.Unlock()
	return nil
}

func (l *memLog) PrunePush(before time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, e := range l.m {
		if e.DeliverAt.Before(before) {
			delete(l.m, k)
		}
	}
	return nil
}

func (l *memLog) ClearPush() error {
	l.mu.Lock()
	l.m = map[string]store.PushEntry{}
	l.mu.Unlock()
	return nil
}

func (l *memLog) get(seq string) (store.PushEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.m[seq]
	return e, ok
}

func (l *memLog) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.m)
}

// rig is one simulated computer.
type rig struct {
	t   *testing.T
	srv *pushtest.Server
	log *memLog
	r   *Reconciler

	mu       sync.Mutex
	now      time.Time
	items    []model.Item
	cfg      Config
	on       bool
	cfgErr   error  // Config's error (settings unreadable)
	token    string // Deps.Token's value (the saved token)
	tokReads int    // Deps.Token calls
	statuses []Status
}

func newRig(t *testing.T, srv *pushtest.Server, items ...model.Item) *rig {
	g := &rig{t: t, srv: srv, log: newMemLog(), now: tNow, items: items, on: true}
	g.cfg = Config{Server: srv.URL, Topic: "ar-topic", Grace: 2 * time.Minute, Loc: utc}
	// The server resolves X-Delay durations on the simulated clock (the
	// latest rig's; rigs sharing a server keep their clocks together).
	srv.Now = func() time.Time {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.now
	}
	g.r = NewReconciler(Deps{
		Items: func() ([]model.Item, error) {
			g.mu.Lock()
			defer g.mu.Unlock()
			return append([]model.Item(nil), g.items...), nil
		},
		Rules: func() scheduler.Rules { return tRule },
		Config: func() (Config, bool, error) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.cfgErr != nil {
				return Config{}, false, g.cfgErr
			}
			return g.cfg, g.on, nil
		},
		Token: func() string {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.tokReads++
			return g.token
		},
		Log: g.log,
		Now: func() time.Time {
			g.mu.Lock()
			defer g.mu.Unlock()
			return g.now
		},
		HTTP: srv.Client(),
		OnStatus: func(s Status) {
			g.mu.Lock()
			g.statuses = append(g.statuses, s)
			g.mu.Unlock()
		},
	})
	return g
}

func (g *rig) set(fn func(g *rig)) {
	g.mu.Lock()
	fn(g)
	g.mu.Unlock()
}

func (g *rig) run() time.Duration {
	g.t.Helper()
	return g.r.runOnce(context.Background())
}

func (g *rig) sawState(state string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.statuses {
		if s.State == state {
			return true
		}
	}
	return false
}

func newServer(t *testing.T) *pushtest.Server {
	srv := pushtest.New()
	t.Cleanup(srv.Close)
	return srv
}

type req struct{ Method, Path string }

func reqs(srv *pushtest.Server, from int) []req {
	var out []req
	for _, r := range srv.Requests()[from:] {
		out = append(out, req{r.Method, r.Path})
	}
	return out
}

func wantReqs(t *testing.T, got []req, want ...req) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("requests = %v, want %v", got, want)
		}
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var tDue = tNow.Add(5 * time.Hour)

func seqOf(id string, due time.Time) string { return SeqFor(id, scheduler.OccReminder, due) }

func TestReconcileSchedulesOnce(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	g.run()
	g.run()
	seq := seqOf("01A", tDue)
	wantReqs(t, reqs(srv, 0), req{"DELETE", "/ar-topic/" + seq}, req{"POST", "/ar-topic/" + seq})
	e, ok := g.log.get(seq)
	if !ok || e.State != store.PushScheduled || !e.DeliverAt.Equal(tDue.Add(2*time.Minute)) || e.Topic != "ar-topic" || e.Server != srv.URL {
		t.Errorf("log entry = %+v, %v", e, ok)
	}
	st := g.r.Status()
	if st.State != "ok" || st.Scheduled != 1 || st.Message != "" || st.SentToday != 2 || !st.LastSent.Equal(tNow) {
		t.Errorf("status = %+v", st)
	}
}

func TestReconcileEditReplaces(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	g.run()
	g.set(func(g *rig) { g.items = []model.Item{reminder("01A", "check the deploy", tDue)} })
	g.run()
	seq := seqOf("01A", tDue)
	wantReqs(t, reqs(srv, 2), req{"DELETE", "/ar-topic/" + seq}, req{"POST", "/ar-topic/" + seq})
	p := srv.Pending()
	if len(p) != 1 || p[0].Body != "check the deploy" {
		t.Fatalf("pending = %+v", p)
	}
}

func TestReconcileDoneCancels(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	g.run()
	g.set(func(g *rig) { g.items = nil })
	g.run()
	seq := seqOf("01A", tDue)
	wantReqs(t, reqs(srv, 2), req{"DELETE", "/ar-topic/" + seq}, req{"DELETE", "/ar-topic/" + seq + "s"})
	if len(srv.Pending()) != 0 || g.log.len() != 0 {
		t.Errorf("pending = %d, log = %d", len(srv.Pending()), g.log.len())
	}
	if st := g.r.Status(); st.Scheduled != 0 {
		t.Errorf("status = %+v", st)
	}
}

func TestDeliveredKeptThenSnoozeTwinCleaned(t *testing.T) {
	srv := newServer(t)
	due := tNow.Add(5 * time.Minute)
	g := newRig(t, srv, reminder("01A", "check deploy", due))
	g.run()
	seq := seqOf("01A", due)
	deliver := due.Add(2 * time.Minute)

	// About to fire (inside the 15 s floor): kept, not deleted.
	g.set(func(g *rig) { g.now = deliver.Add(-10 * time.Second) })
	g.run()
	// Delivered 10 minutes ago, item still open: kept for the snooze twin.
	g.set(func(g *rig) { g.now = deliver.Add(10 * time.Minute) })
	g.run()
	wantReqs(t, reqs(srv, 2))
	if _, ok := g.log.get(seq); !ok {
		t.Fatal("delivered entry dropped too early")
	}
	// Done within the hour: the phone may have created <seq>s.
	g.set(func(g *rig) { g.items = nil })
	g.run()
	wantReqs(t, reqs(srv, 2), req{"DELETE", "/ar-topic/" + seq}, req{"DELETE", "/ar-topic/" + seq + "s"})
	if g.log.len() != 0 {
		t.Error("log not emptied")
	}
}

func TestDeliveredPrunedAfterAnHour(t *testing.T) {
	srv := newServer(t)
	due := tNow.Add(5 * time.Minute)
	g := newRig(t, srv, reminder("01A", "check deploy", due))
	g.run()
	g.set(func(g *rig) { g.now = due.Add(2 * time.Hour); g.items = nil })
	g.run()
	wantReqs(t, reqs(srv, 2))
	if g.log.len() != 0 {
		t.Error("old entry not pruned")
	}
}

func TestTwoComputersOneDelivery(t *testing.T) {
	srv := newServer(t)
	it := reminder("01A", "check deploy", tDue)
	a, b := newRig(t, srv, it), newRig(t, srv, it)
	a.run()
	b.run()
	a.run()
	b.run()
	got := srv.Deliver(tDue.Add(2 * time.Minute))
	if len(got) != 1 {
		t.Fatalf("delivered %d, want 1: %+v", len(got), got)
	}
}

func TestDismissNotRepublished(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	g.run()
	seq := seqOf("01A", tDue)
	g.r.Dismiss([]string{seq})
	g.r.dismissNow(context.Background())
	wantReqs(t, reqs(srv, 2), req{"DELETE", "/ar-topic/" + seq}, req{"DELETE", "/ar-topic/" + seq + "s"})
	if len(srv.Pending()) != 0 {
		t.Fatalf("pending = %+v", srv.Pending())
	}
	g.run()
	wantReqs(t, reqs(srv, 4))
	e, ok := g.log.get(seq)
	if !ok || e.State != store.PushDismissed || !e.DeliverAt.Equal(tDue.Add(2*time.Minute)) {
		t.Errorf("entry = %+v %v", e, ok)
	}
	if st := g.r.Status(); st.Scheduled != 0 || st.State != "ok" {
		t.Errorf("status = %+v", st)
	}
}

func TestDismissUnknownSeq(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv) // another computer scheduled it
	g.r.Dismiss([]string{"arforeign"})
	g.r.dismissNow(context.Background())
	wantReqs(t, reqs(srv, 0), req{"DELETE", "/ar-topic/arforeign"}, req{"DELETE", "/ar-topic/arforeigns"})
	e, ok := g.log.get("arforeign")
	if !ok || e.State != store.PushDismissed || !e.DeliverAt.Equal(tNow.Add(71*time.Hour)) {
		t.Errorf("entry = %+v %v", e, ok)
	}
}

func TestDismissOfflineRetriedByRun(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	g.run()
	seq := seqOf("01A", tDue)
	srv.Fail("DELETE", "", 503, 1)
	g.r.Dismiss([]string{seq})
	g.r.dismissNow(context.Background())
	if st := g.r.Status(); st.State != "offline" {
		t.Fatalf("status = %+v", st)
	}
	n := len(srv.Requests())
	g.run() // retries the deletes, never republishes
	wantReqs(t, reqs(srv, n), req{"DELETE", "/ar-topic/" + seq}, req{"DELETE", "/ar-topic/" + seq + "s"})
	if len(srv.Pending()) != 0 {
		t.Fatal("still pending")
	}
	g.run()
	if len(srv.Requests()) != n+2 {
		t.Errorf("confirmed dismissal retried: %v", reqs(srv, n))
	}
}

func TestTempErrorRetries(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	srv.Fail("POST", "", 500, 3)
	for i, want := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute} {
		if d := g.run(); d != want {
			t.Fatalf("run %d: retry after %v, want %v", i, d, want)
		}
	}
	if g.log.len() != 0 {
		t.Fatal("failed publish recorded")
	}
	if st := g.r.Status(); st.State != "offline" || st.Message != "Offline · will retry" {
		t.Fatalf("status = %+v", st)
	}
	g.r.backoff = 10 * time.Minute
	if d := g.run(); d != 0 {
		t.Fatalf("success scheduled a retry %v", d)
	}
	srv.Fail("POST", "", 500, 3)
	g.set(func(g *rig) { g.items[0].Text = "edited" })
	if d := g.run(); d != 30*time.Second {
		t.Fatalf("backoff not reset after success: %v", d)
	}
	g.r.backoff = 10 * time.Minute
	g.run()
	if d := g.run(); d != 15*time.Minute {
		t.Fatalf("backoff not capped: %v", d)
	}

	// The loop's retry timer publishes once the server is back.
	srv2 := newServer(t)
	g2 := newRig(t, srv2, reminder("01A", "check deploy", tDue))
	g2.r.backoffMin = 20 * time.Millisecond
	srv2.Fail("POST", "", 500, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); g2.r.Wait() }()
	g2.r.Start(ctx)
	eventually(t, "retry", func() bool { return len(srv2.Pending()) == 1 && g2.r.Status().State == "ok" })
	if !g2.sawState("offline") {
		t.Error("never reported offline")
	}
	if g2.log.len() != 1 {
		t.Error("retry not logged")
	}
}

func TestAuthStops(t *testing.T) {
	srv := newServer(t)
	srv.RequireToken("tk_secret")
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	g.r.every, g.r.check = 5*time.Millisecond, 5*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); g.r.Wait() }()
	g.r.Start(ctx)
	eventually(t, "auth status", func() bool { return g.r.Status().State == "auth" })
	if m := g.r.Status().Message; m != "Error: 401 — this computer needs an access token" {
		t.Errorf("message = %q", m)
	}
	n := len(srv.Requests())
	time.Sleep(100 * time.Millisecond)
	if len(srv.Requests()) != n {
		t.Fatalf("requests after auth failure: %v", reqs(srv, n))
	}
	g.set(func(g *rig) { g.cfg.Token = "tk_secret" })
	g.r.Kick()
	eventually(t, "publish after Kick", func() bool { return len(srv.Pending()) == 1 && g.r.Status().State == "ok" })
}

func TestAuthMessage403(t *testing.T) {
	srv := newServer(t)
	srv.Fail("", "", 403, 0)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	g.run()
	if st := g.r.Status(); st.State != "auth" || st.Message != "Error: 403 — this computer needs an access token" {
		t.Errorf("status = %+v", st)
	}
}

func TestGenericErrorMessage(t *testing.T) {
	srv := newServer(t)
	srv.Fail("", "", 400, 0)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	if d := g.run(); d != 0 {
		t.Errorf("retry %v for a client error", d)
	}
	st := g.r.Status()
	if st.State != "error" || !strings.HasPrefix(st.Message, "Error: ") {
		t.Fatalf("status = %+v", st)
	}
	for _, bad := range []string{"ar-topic", "check deploy"} {
		if strings.Contains(st.Message, bad) {
			t.Errorf("message %q leaks %q", st.Message, bad)
		}
	}
}

func TestTopicChangeMovesPushes(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	g.run()
	g.set(func(g *rig) { g.cfg.Topic = "ar-other" })
	g.run()
	seq := seqOf("01A", tDue)
	wantReqs(t, reqs(srv, 2),
		req{"DELETE", "/ar-topic/" + seq}, req{"DELETE", "/ar-topic/" + seq + "s"},
		req{"DELETE", "/ar-other/" + seq}, req{"POST", "/ar-other/" + seq})
	p := srv.Pending()
	if len(p) != 1 || p[0].Topic != "ar-other" {
		t.Fatalf("pending = %+v", p)
	}
	if e, _ := g.log.get(seq); e.Topic != "ar-other" {
		t.Errorf("entry = %+v", e)
	}
}

func TestTurnOffClears(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue), reminder("01B", "call bank", tDue.Add(time.Hour)))
	g.run()
	g.set(func(g *rig) { g.on = false })
	g.run()
	if len(srv.Pending()) != 0 || g.log.len() != 0 {
		t.Fatalf("pending = %d, log = %d", len(srv.Pending()), g.log.len())
	}
	if st := g.r.Status(); st.State != "off" || st.Scheduled != 0 || st.Message != "" {
		t.Errorf("status = %+v", st)
	}
	n := len(srv.Requests())
	g.run()
	if len(srv.Requests()) != n {
		t.Error("network touched while off")
	}
	if err := g.r.SendTest(context.Background()); err == nil || err.Error() != "Phone push is off" {
		t.Errorf("SendTest = %v", err)
	}
}

// Off, Config carries no token (the keychain isn't read while off); the
// deletes fetch it through Deps.Token, and only when there is something to
// cancel.
func TestTurnOffReadsTokenOnlyToDelete(t *testing.T) {
	srv := newServer(t)
	srv.RequireToken("tk_secret")
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	g.set(func(g *rig) { g.cfg.Token, g.token = "tk_secret", "tk_secret" })
	g.run()
	if len(srv.Pending()) != 1 {
		t.Fatalf("pending = %+v", srv.Pending())
	}
	g.set(func(g *rig) { g.on, g.cfg.Token, g.tokReads = false, "", 0 })
	g.run()
	if len(srv.Pending()) != 0 || g.log.len() != 0 || g.sawState("auth") {
		t.Fatalf("pending = %d, log = %d, statuses %+v", len(srv.Pending()), g.log.len(), g.statuses)
	}
	g.set(func(g *rig) { g.items = append(g.items, reminder("01B", "x", tDue)) })
	g.r.Kick()
	g.run()
	g.run()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.tokReads != 1 {
		t.Errorf("token read %d times, want once (for the deletes)", g.tokReads)
	}
}

func TestQuotaGuard(t *testing.T) {
	srv := newServer(t)
	far, near := tNow.Add(10*time.Hour), tNow.Add(2*time.Hour)
	g := newRig(t, srv, reminder("01F", "far", far), reminder("01N", "near", near))
	g.r.rollDay(tNow, utc)
	g.r.count(nil, 200)
	g.run()
	p := srv.Pending()
	if len(p) != 1 || p[0].Seq != seqOf("01N", near) {
		t.Fatalf("pending = %+v", p)
	}
	st := g.r.Status()
	if st.State != "quota" || st.Message != "Daily limit nearly reached · only pushes due in the next 6 hours are sent" || st.SentToday != 202 {
		t.Errorf("status = %+v", st)
	}
	// A new local day resets the count.
	g.set(func(g *rig) {
		g.now = time.Date(2026, 11, 29, 0, 1, 0, 0, utc)
		g.items = []model.Item{reminder("01F", "far", far.Add(24*time.Hour))}
	})
	g.run()
	if st := g.r.Status(); st.State != "ok" || st.SentToday > 10 {
		t.Errorf("after midnight: %+v", st)
	}
}

func TestSendTestCounts(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv)
	if err := g.r.SendTest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := srv.Delivered(); len(d) != 1 || d[0].Topic != "ar-topic" {
		t.Fatalf("delivered = %+v", d)
	}
	if st := g.r.Status(); st.SentToday != 1 {
		t.Errorf("status = %+v", st)
	}
}

func TestKickNeverBlocks(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			g.r.Kick()
			g.r.Dismiss([]string{"arx"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Kick or Dismiss blocked without a running loop")
	}
}

func TestWaitAfterCancel(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	g.r.Wait() // never started: returns at once
	ctx, cancel := context.WithCancel(context.Background())
	g.r.Start(ctx)
	eventually(t, "first run", func() bool { return len(srv.Pending()) == 1 })
	cancel()
	done := make(chan struct{})
	go func() { g.r.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after cancel")
	}
}

func TestDismissThroughLoop(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); g.r.Wait() }()
	g.r.Start(ctx)
	eventually(t, "first run", func() bool { return len(srv.Pending()) == 1 })
	g.r.Dismiss([]string{seqOf("01A", tDue)})
	eventually(t, "dismissal", func() bool { return len(srv.Pending()) == 0 })
}

func TestWallJumped(t *testing.T) {
	base := time.Date(2026, 11, 28, 10, 0, 0, 0, utc)
	for _, c := range []struct {
		elapsed time.Duration
		want    bool
	}{{time.Minute, false}, {time.Minute + 4*time.Second, false}, {time.Minute + 6*time.Second, true}, {2 * time.Hour, true}, {50 * time.Second, true}} {
		if got := wallJumped(base, base.Add(c.elapsed), time.Minute); got != c.want {
			t.Errorf("elapsed %v: %v", c.elapsed, got)
		}
	}
}

func TestNonRetryableFailureSkipsOnlyThatEntry(t *testing.T) {
	srv := newServer(t)
	bad, good := tDue, tDue.Add(time.Hour)
	g := newRig(t, srv, reminder("01B", "bad", bad), reminder("01G", "good", good))
	srv.Fail("POST", "/ar-topic/"+seqOf("01B", bad), 400, 0)
	for i := 0; i < 2; i++ {
		if d := g.run(); d != 0 {
			t.Fatalf("retry %v for a client error", d)
		}
	}
	p := srv.Pending()
	if len(p) != 1 || p[0].Seq != seqOf("01G", good) {
		t.Fatalf("pending = %+v", p)
	}
	if _, ok := g.log.get(seqOf("01B", bad)); ok {
		t.Error("failed publish recorded")
	}
	if st := g.r.Status(); st.State != "error" || st.Scheduled != 1 {
		t.Errorf("status = %+v", st)
	}

	// A delete failing with 400 keeps its entry; the others still go.
	g.set(func(g *rig) { g.items = nil })
	srv.Fail("DELETE", "/ar-topic/"+seqOf("01G", good), 400, 1)
	g.set(func(g *rig) { g.items = []model.Item{reminder("01N", "new", good.Add(time.Hour))} })
	g.run()
	if _, ok := g.log.get(seqOf("01G", good)); !ok {
		t.Error("entry dropped after its delete failed")
	}
	if _, ok := g.log.get(seqOf("01N", good.Add(time.Hour))); !ok {
		t.Error("later message not scheduled after a failed delete")
	}
}

func TestTopicChangeKeepsDismissal(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	g.run()
	seq := seqOf("01A", tDue)
	g.r.Dismiss([]string{seq})
	g.r.dismissNow(context.Background())
	g.set(func(g *rig) { g.cfg.Topic = "ar-other" })
	n := len(srv.Requests())
	g.run()
	// The dismissal moves with the topic: deleted there, never published.
	wantReqs(t, reqs(srv, n), req{"DELETE", "/ar-other/" + seq}, req{"DELETE", "/ar-other/" + seq + "s"})
	e, ok := g.log.get(seq)
	if !ok || e.State != store.PushDismissed || e.Topic != "ar-other" || e.SentAt.IsZero() {
		t.Fatalf("entry = %+v %v", e, ok)
	}
	g.run()
	wantReqs(t, reqs(srv, n+2))
}

func TestDismissSuccessKeepsFailedRunStatus(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	srv.Fail("POST", "", 500, 0)
	if d := g.run(); d != 30*time.Second {
		t.Fatalf("retry = %v", d)
	}
	g.r.Dismiss([]string{"arforeign"})
	if d := g.r.dismissNow(context.Background()); d != 0 {
		t.Errorf("dismissal scheduled a retry %v", d)
	}
	if st := g.r.Status(); st.State != "offline" {
		t.Errorf("status = %+v", st)
	}
	if d := g.run(); d != time.Minute {
		t.Errorf("back-off reset by a dismissal: %v", d)
	}
}

func TestOffIdleNoStoreWrites(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv)
	g.set(func(g *rig) { g.on = false })
	cl := &clearCounter{memLog: g.log}
	g.r.d.Log = cl
	g.run()
	g.run()
	if cl.clears != 0 {
		t.Errorf("ClearPush called %d times with an empty log", cl.clears)
	}
}

type clearCounter struct {
	*memLog
	clears int
}

func (c *clearCounter) ClearPush() error { c.clears++; return c.memLog.ClearPush() }

func TestFailedDeleteKeepsLog(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	g.run()
	seq := seqOf("01A", tDue)
	g.set(func(g *rig) { g.items = nil })
	srv.Fail("DELETE", "", 503, 1)
	g.run()
	if _, ok := g.log.get(seq); !ok {
		t.Fatal("entry dropped although its delete failed")
	}
	n := len(srv.Requests())
	g.run()
	wantReqs(t, reqs(srv, n), req{"DELETE", "/ar-topic/" + seq}, req{"DELETE", "/ar-topic/" + seq + "s"})
	if g.log.len() != 0 || len(srv.Pending()) != 0 {
		t.Errorf("log = %d, pending = %d", g.log.len(), len(srv.Pending()))
	}
}

// A settings read error is not "off": the run is skipped (nothing deleted,
// the log kept) and retried with back-off; queued dismissals wait for it.
func TestConfigErrorSkipsRun(t *testing.T) {
	srv := newServer(t)
	g := newRig(t, srv, reminder("01A", "check deploy", tDue))
	g.run()
	if len(srv.Pending()) != 1 || g.log.len() != 1 {
		t.Fatalf("pending = %d, log = %d", len(srv.Pending()), g.log.len())
	}
	n := len(srv.Requests())
	g.set(func(g *rig) { g.cfgErr = errors.New("database is locked") })
	if d := g.run(); d != 30*time.Second {
		t.Fatalf("retry after %v, want 30s", d)
	}
	g.r.Dismiss([]string{seqOf("01A", tDue)})
	g.r.dismissNow(context.Background())
	if len(srv.Requests()) != n || len(srv.Pending()) != 1 || g.log.len() != 1 {
		t.Fatalf("settings error touched the server or log: requests +%d, pending %d, log %d", len(srv.Requests())-n, len(srv.Pending()), g.log.len())
	}
	if st := g.r.Status(); st.State != "error" || !strings.Contains(st.Message, "reading settings") {
		t.Fatalf("status = %+v", st)
	}
	if err := g.r.SendTest(context.Background()); err == nil || !strings.Contains(err.Error(), "reading settings") {
		t.Fatalf("SendTest = %v", err)
	}
	g.set(func(g *rig) { g.cfgErr = nil })
	if d := g.run(); d != 0 || g.r.Status().State != "ok" {
		t.Fatalf("after recovery: retry %v, status %+v", d, g.r.Status())
	}
	// The held dismissal is signalled again and now applies.
	select {
	case <-g.r.dismiss:
	default:
		t.Fatal("held dismissal not signalled")
	}
	g.r.dismissNow(context.Background())
	if len(srv.Pending()) != 0 {
		t.Fatalf("dismissed push still pending: %d", len(srv.Pending()))
	}
}
