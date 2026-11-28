package push

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"awesome-reminder/internal/core"
	"awesome-reminder/internal/model"
	"awesome-reminder/internal/push/pushtest"
	"awesome-reminder/internal/reply"
	"awesome-reminder/internal/store"
)

var ctlNY, _ = time.LoadLocation("America/New_York")

type coreEnv struct {
	c   *core.Core
	st  *store.Store
	now time.Time
}

func newCoreEnv(t *testing.T) *coreEnv {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &coreEnv{st: st, now: time.Date(2026, 11, 25, 10, 0, 0, 0, ctlNY)}
	e.c = core.New(st, func() time.Time { return e.now }, ctlNY)
	return e
}

func (e *coreEnv) capture(t *testing.T, in string) model.Item {
	t.Helper()
	it, err := e.c.Capture(in)
	if err != nil {
		t.Fatalf("capture %q: %v", in, err)
	}
	return it
}

func (e *coreEnv) get(t *testing.T, id string) model.Item {
	t.Helper()
	it, err := e.c.Item(id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestApplyDoneCurrentOccurrence(t *testing.T) {
	e := newCoreEnv(t)
	it := e.capture(t, "today 5pm deploy")
	if it.DueAt == nil || it.DueAt.In(ctlNY).Hour() != 17 {
		t.Fatalf("due = %v", it.DueAt)
	}
	if err := ApplyDone(e.c, it.ID, *it.DueAt); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, it.ID); got.Status != model.StatusDone {
		t.Fatalf("status = %s, want done", got.Status)
	}
}

func TestApplyDoneStaleOccurrence(t *testing.T) {
	e := newCoreEnv(t)
	e.now = time.Date(2026, 11, 25, 8, 0, 0, 0, ctlNY)
	it := e.capture(t, "every day 9am standup")
	today9 := time.Date(2026, 11, 25, 9, 0, 0, 0, ctlNY)
	tomorrow9 := today9.AddDate(0, 0, 1)
	if it.DueAt == nil || !it.DueAt.Equal(today9) {
		t.Fatalf("due = %v, want %v", it.DueAt, today9)
	}
	// Done at the desk at 09:01: the computer moves it on to tomorrow.
	e.now = time.Date(2026, 11, 25, 9, 1, 0, 0, ctlNY)
	if _, err := e.c.DoneThrough(it.ID, today9); err != nil {
		t.Fatal(err)
	}
	before := e.get(t, it.ID)
	if !before.DueAt.Equal(tomorrow9) {
		t.Fatalf("setup: due = %v, want %v", before.DueAt, tomorrow9)
	}
	e.now = time.Date(2026, 11, 25, 9, 3, 0, 0, ctlNY) // then the phone tap for today
	if err := ApplyDone(e.c, it.ID, today9); err != nil {
		t.Fatal(err)
	}
	got := e.get(t, it.ID)
	if got.Status != model.StatusOpen || !got.DueAt.Equal(tomorrow9) || !got.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("got status %s due %v updated %v, want unchanged", got.Status, got.DueAt, got.UpdatedAt)
	}
}

// The computer slept through tomorrow's 09:00 and has not advanced the item
// (DueAt still today 09:00) when the phone tap for tomorrow arrives.
func TestApplyDoneLaterOccurrenceNotYetAdvanced(t *testing.T) {
	today9 := time.Date(2026, 11, 25, 9, 0, 0, 0, ctlNY)
	tomorrow9 := today9.AddDate(0, 0, 1)
	for _, tc := range []struct {
		name    string
		occ     time.Time
		applied bool
	}{
		{"real occurrence", tomorrow9, true},
		{"not on the rule", tomorrow9.Add(30 * time.Minute), false},
		{"in the future", tomorrow9.AddDate(0, 0, 1), false},
		{"before due", today9.AddDate(0, 0, -1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCoreEnv(t)
			e.now = time.Date(2026, 11, 25, 8, 0, 0, 0, ctlNY)
			it := e.capture(t, "every day 9am standup")
			if it.DueAt == nil || !it.DueAt.Equal(today9) {
				t.Fatalf("due = %v, want %v", it.DueAt, today9)
			}
			e.now = tomorrow9.Add(3 * time.Minute)
			if err := ApplyDone(e.c, it.ID, tc.occ); err != nil {
				t.Fatal(err)
			}
			got := e.get(t, it.ID)
			want := today9
			if tc.applied {
				want = tomorrow9.AddDate(0, 0, 1)
			}
			if got.Status != model.StatusOpen || !got.DueAt.Equal(want) {
				t.Fatalf("got status %s due %v, want open %v", got.Status, got.DueAt, want)
			}
			if !tc.applied && !got.UpdatedAt.Equal(it.UpdatedAt) {
				t.Fatalf("ignored tap changed the item: updated %v", got.UpdatedAt)
			}
		})
	}
}

func TestApplyDoneRecurringCurrent(t *testing.T) {
	e := newCoreEnv(t)
	e.now = time.Date(2026, 11, 25, 8, 0, 0, 0, ctlNY)
	it := e.capture(t, "every day 9am standup")
	today9 := time.Date(2026, 11, 25, 9, 0, 0, 0, ctlNY)
	if it.DueAt == nil || !it.DueAt.Equal(today9) {
		t.Fatalf("due = %v, want %v", it.DueAt, today9)
	}
	e.now = time.Date(2026, 11, 25, 9, 3, 0, 0, ctlNY) // tapped after the buzz
	if err := ApplyDone(e.c, it.ID, today9); err != nil {
		t.Fatal(err)
	}
	got := e.get(t, it.ID)
	if got.Status != model.StatusOpen || got.DueAt == nil || !got.DueAt.Equal(today9.AddDate(0, 0, 1)) {
		t.Fatalf("got status %s due %v, want open tomorrow 09:00", got.Status, got.DueAt)
	}
}

func TestApplyDoneReplyNoDue(t *testing.T) {
	e := newCoreEnv(t)
	it, err := e.c.CaptureReply("Reply to Priya: deploy?", reply.Meta{OwedTo: "Priya", Source: "Teams"})
	if err != nil {
		t.Fatal(err)
	}
	if it.DueAt != nil {
		t.Fatalf("reply has due %v", it.DueAt)
	}
	if err := ApplyDone(e.c, it.ID, time.Unix(1764000000, 0)); err != nil {
		t.Fatal(err)
	}
	got := e.get(t, it.ID)
	if got.Status != model.StatusDone || got.ReplyOutcome != model.ReplyReplied {
		t.Fatalf("got status %s outcome %q, want done/replied", got.Status, got.ReplyOutcome)
	}
}

func TestApplyDoneDoneItem(t *testing.T) {
	e := newCoreEnv(t)
	it := e.capture(t, "today 5pm deploy")
	if _, err := e.c.Done(it.ID); err != nil {
		t.Fatal(err)
	}
	before := e.get(t, it.ID)
	if err := ApplyDone(e.c, it.ID, *it.DueAt); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, it.ID); got.Status != model.StatusDone || !got.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("got %s updated %v, want unchanged", got.Status, got.UpdatedAt)
	}
	if err := ApplyDone(e.c, "no-such-item", time.Now()); err != nil {
		t.Fatalf("missing item: %v", err)
	}
}

// --- listener ---

type memMeta struct {
	mu sync.Mutex
	m  map[string]string
}

func (m *memMeta) Meta(k string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.m[k]
	return v, ok, nil
}

func (m *memMeta) SetMeta(k, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		m.m = map[string]string{}
	}
	m.m[k] = v
	return nil
}

func (m *memMeta) get(k string) string { v, _, _ := m.Meta(k); return v }

type doneCall struct {
	id  string
	occ time.Time
}

type ctlEnv struct {
	srv  *pushtest.Server
	meta *memMeta

	mu     sync.Mutex
	cfg    Config
	on     bool
	cfgErr error
	calls  chan doneCall
}

func newCtlEnv(t *testing.T) *ctlEnv {
	srv := pushtest.New()
	t.Cleanup(srv.Close)
	return &ctlEnv{srv: srv, meta: &memMeta{}, on: true, calls: make(chan doneCall, 16),
		cfg: Config{Server: srv.URL, Topic: testTopic}}
}

func (e *ctlEnv) deps() ControlDeps {
	return ControlDeps{
		Config: func() (Config, bool, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.cfgErr != nil {
				return Config{}, false, e.cfgErr
			}
			return e.cfg, e.on, nil
		},
		Meta: e.meta,
		Done: func(id string, occ time.Time) error {
			e.calls <- doneCall{id, occ}
			return nil
		},
		HTTP: e.srv.Client(),
	}
}

func (e *ctlEnv) post(t *testing.T, topic, body string) string {
	t.Helper()
	c := Client{Server: e.srv.URL, Topic: topic, HTTP: e.srv.Client()}
	if err := c.PostControl(context.Background(), []byte(body)); err != nil {
		t.Fatal(err)
	}
	ms, err := c.PollControl(context.Background(), "")
	if err != nil || len(ms) == 0 {
		t.Fatalf("poll after post: %v %v", ms, err)
	}
	return ms[len(ms)-1].ID
}

func (e *ctlEnv) waitCall(t *testing.T) doneCall {
	t.Helper()
	select {
	case c := <-e.calls:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("Done not called")
		return doneCall{}
	}
}

func (e *ctlEnv) noCall(t *testing.T) {
	t.Helper()
	select {
	case c := <-e.calls:
		t.Fatalf("unexpected Done %+v", c)
	default:
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func start(t *testing.T, e *ctlEnv, restart chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	wait := StartControl(ctx, e.deps(), restart)
	t.Cleanup(func() {
		cancel()
		wait()
	})
}

func TestControlPollsThenStreams(t *testing.T) {
	e := newCtlEnv(t)
	old := e.post(t, testTopic, `{"op":"done","id":"old","occ":1}`)
	e.meta.SetMeta(ctlSinceKey, old) // applied in an earlier run
	e.post(t, testTopic, `{"op":"done","id":"a","occ":1764064800}`)
	idB := e.post(t, testTopic, `{"op":"done","id":"b","occ":0}`)

	start(t, e, make(chan struct{}))
	if c := e.waitCall(t); c.id != "a" || !c.occ.Equal(time.Unix(1764064800, 0)) {
		t.Fatalf("first = %+v", c)
	}
	if c := e.waitCall(t); c.id != "b" {
		t.Fatalf("second = %+v", c)
	}
	waitFor(t, "stream", func() bool { return e.srv.Subscribers(testTopic+"-ctl") == 1 })
	if got := e.meta.get(ctlSinceKey); got != idB {
		t.Fatalf("since after poll = %q, want %q", got, idB)
	}
	var streamQ string
	for _, r := range e.srv.Requests() {
		if r.Method == "GET" && !strings.Contains(r.Query, "poll=1") {
			streamQ = r.Query
		}
	}
	if streamQ != "since="+idB {
		t.Fatalf("stream query = %q, want since=%s", streamQ, idB)
	}

	idC := e.post(t, testTopic, `{"op":"done","id":"c","occ":5}`)
	if c := e.waitCall(t); c.id != "c" || c.occ.Unix() != 5 {
		t.Fatalf("streamed = %+v", c)
	}
	waitFor(t, "since to advance", func() bool { return e.meta.get(ctlSinceKey) == idC })
	e.noCall(t)
}

func TestControlIgnoresGarbage(t *testing.T) {
	e := newCtlEnv(t)
	big := `{"op":"done","id":"big","occ":1,"pad":"` + strings.Repeat("x", 1100) + `"}`
	e.post(t, testTopic, big)
	e.post(t, testTopic, `{"op":"done",`)
	e.post(t, testTopic, `{"op":"delete","id":"del","occ":1}`)
	e.post(t, testTopic, `{"op":"done","id":"","occ":1}`)
	last := e.post(t, testTopic, `{"op":"done","id":"ok","occ":1}`)

	start(t, e, make(chan struct{}))
	if c := e.waitCall(t); c.id != "ok" {
		t.Fatalf("Done called with %+v", c)
	}
	waitFor(t, "since", func() bool { return e.meta.get(ctlSinceKey) == last })
	e.noCall(t)
}

func TestControlRestartOnConfigChange(t *testing.T) {
	e := newCtlEnv(t)
	restart := make(chan struct{}, 1)
	start(t, e, restart)
	waitFor(t, "stream on A", func() bool { return e.srv.Subscribers(testTopic+"-ctl") == 1 })
	// Nothing cached yet: the stream replays the whole cache, so a message
	// posted between the poll and the subscribe is not lost.
	if r := e.srv.Requests(); r[len(r)-1].Query != "since=all" {
		t.Fatalf("first stream query = %q, want since=all", r[len(r)-1].Query)
	}

	const topicB = "ar-othertopic"
	e.mu.Lock()
	e.cfg.Topic = topicB
	e.mu.Unlock()
	restart <- struct{}{}
	waitFor(t, "stream on B", func() bool {
		return e.srv.Subscribers(topicB+"-ctl") == 1 && e.srv.Subscribers(testTopic+"-ctl") == 0
	})
	e.post(t, topicB, `{"op":"done","id":"onB","occ":1}`)
	if c := e.waitCall(t); c.id != "onB" {
		t.Fatalf("Done = %+v", c)
	}

	// Turned off: the stream closes and nothing reconnects.
	e.mu.Lock()
	e.on = false
	e.mu.Unlock()
	restart <- struct{}{}
	waitFor(t, "stream closed", func() bool { return e.srv.Subscribers(topicB+"-ctl") == 0 })
	e.post(t, topicB, `{"op":"done","id":"whileOff","occ":1}`)
	time.Sleep(50 * time.Millisecond)
	e.noCall(t)
}

// A settings read error is not "off": the listener retries on its own
// instead of idling until a restart.
func TestControlConfigErrorRetries(t *testing.T) {
	defer func(d time.Duration) { ctlConfigRetry = d }(ctlConfigRetry)
	ctlConfigRetry = 20 * time.Millisecond
	e := newCtlEnv(t)
	e.cfgErr = errors.New("database is locked")
	start(t, e, make(chan struct{}))
	time.Sleep(50 * time.Millisecond)
	e.mu.Lock()
	e.cfgErr = nil
	e.mu.Unlock()
	waitFor(t, "stream after the error cleared", func() bool { return e.srv.Subscribers(testTopic+"-ctl") == 1 })
}
