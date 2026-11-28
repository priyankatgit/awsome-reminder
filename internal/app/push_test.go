package app

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"awesome-reminder/internal/core"
	"awesome-reminder/internal/model"
	"awesome-reminder/internal/push"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/secret"
	"awesome-reminder/internal/settings"
)

// fakeRec records what the push controller asks of the reconciler.
type fakeRec struct {
	mu        sync.Mutex
	kicks     int
	dismissed [][]string
}

func (f *fakeRec) Start(context.Context) {}
func (f *fakeRec) Wait()                 {}
func (f *fakeRec) Kick()                 { f.mu.Lock(); f.kicks++; f.mu.Unlock() }
func (f *fakeRec) Dismiss(seqs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dismissed = append(f.dismissed, append([]string{}, seqs...))
}
func (f *fakeRec) Status() push.Status            { return push.Status{State: "ok", Scheduled: 3} }
func (f *fakeRec) SendTest(context.Context) error { return nil }
func (f *fakeRec) take() (got [][]string) {
	f.mu.Lock()
	got, f.dismissed = f.dismissed, nil
	f.mu.Unlock()
	return got
}
func (f *fakeRec) kicked() int { f.mu.Lock(); defer f.mu.Unlock(); return f.kicks }

// fakeToken is an in-memory secret.Store.
type fakeToken struct{ v string }

func (t *fakeToken) Get() (string, error) {
	if t.v == "" {
		return "", secret.ErrNotFound
	}
	return t.v, nil
}
func (t *fakeToken) Set(v string) error { t.v = v; return nil }
func (t *fakeToken) Delete() error      { t.v = ""; return nil }
func (t *fakeToken) Kind() string       { return "Test store" }

// withPush gives the harness's Service a push controller over fakes.
func withPush(h *harness) (*fakeRec, *fakeToken) {
	rec, tok := &fakeRec{}, &fakeToken{}
	h.svc.d.Push = &pushController{core: h.svc.d.Core, rec: rec, token: tok, restart: make(chan struct{}, 1)}
	return rec, tok
}

// Each card the dismiss hook cancels maps to the seq of the occurrence
// scheduler.Upcoming produced for it, so the dismissal hits the push the
// reconciler scheduled: at-time, snooze end, snoozed reply, nudge, sweep.
func TestDismissSeqMatchesUpcoming(t *testing.T) {
	base := time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC) // a Tuesday
	at := func(d time.Duration) *time.Time { v := base.Add(d); return &v }
	created := base.Add(-time.Hour)
	items := []model.Item{
		{ID: "A", Kind: model.KindReminder, Text: "at time", DueAt: at(2 * time.Hour), TZ: "UTC"},
		{ID: "B", Kind: model.KindReminder, Text: "snoozed", DueAt: at(time.Hour), SnoozeUntil: at(3 * time.Hour), TZ: "UTC"},
		{ID: "C", Kind: model.KindTodo, Text: "reply", Reply: true, OwedTo: "Priya"},
		{ID: "D", Kind: model.KindTodo, Text: "snoozed reply", Reply: true, SnoozeUntil: at(4 * time.Hour)},
	}
	h := newHarness(t)
	for i := range items {
		it := &items[i]
		it.Status, it.Tags, it.CreatedAt, it.UpdatedAt, it.UpdatedBy = model.StatusOpen, []string{}, created, created, "test"
		if err := h.st.Put(*it); err != nil {
			t.Fatal(err)
		}
	}
	rec, _ := withPush(h)
	r := core.SchedulerRules(settings.Defaults(runtime.GOOS), time.UTC)
	never := func(string) bool { return false }

	covered := map[string]bool{} // "<item or sweep>/<kind>/<snooze?>"
	for _, occ := range scheduler.Upcoming(items, r, base, base.Add(30*time.Hour)) {
		owner := occ.ItemID
		if occ.Kind == scheduler.OccSweep {
			owner = "sweep"
		}
		fired, _, _ := scheduler.Plan(items, r, occ.At, never)
		var a *scheduler.Alert
		for i := range fired {
			if (occ.Kind == scheduler.OccSweep && fired[i].Kind == scheduler.KindSweep) || (occ.ItemID != "" && fired[i].ItemID == occ.ItemID) {
				a = &fired[i]
			}
		}
		if a == nil {
			t.Fatalf("no alert fired for %+v", occ)
		}
		h.svc.showAlerts([]scheduler.Alert{*a})
		h.svc.DismissAlert(a.Key)
		want := push.SeqFor(owner, occ.Kind, occ.At)
		got := rec.take()
		if len(got) != 1 || len(got[0]) != 1 || got[0][0] != want {
			t.Fatalf("card %s (%s at %s): dismissed %v, want [[%s]]", a.Key, occ.Kind, occ.At, got, want)
		}
		covered[owner+"/"+occ.Kind+"/"+map[bool]string{true: "snooze"}[a.Snooze]] = true
	}
	for _, k := range []string{"A/reminder/", "B/reminder/snooze", "C/nudge/", "D/nudge/snooze", "sweep/sweep/"} {
		if !covered[k] {
			t.Errorf("case %s not covered (covered %v)", k, covered)
		}
	}
}

func TestPushSetTokenRejectsEmpty(t *testing.T) {
	h := newHarness(t)
	rec, tok := withPush(h)
	if err := h.svc.PushSetToken("  \t"); err == nil || err.Error() != "Paste an access token" {
		t.Fatalf("err = %v", err)
	}
	if tok.v != "" || rec.kicked() != 0 {
		t.Fatalf("token %q, kicks %d", tok.v, rec.kicked())
	}
	if err := h.svc.PushSetToken("  tk_abc \n"); err != nil {
		t.Fatal(err)
	}
	if tok.v != "tk_abc" || rec.kicked() != 1 || len(h.svc.d.Push.restart) != 1 {
		t.Fatalf("token %q, kicks %d, restart %d", tok.v, rec.kicked(), len(h.svc.d.Push.restart))
	}
	if !h.svc.PushInfo().HasToken {
		t.Fatal("HasToken false after set")
	}
	if err := h.svc.PushForgetToken(); err != nil {
		t.Fatal(err)
	}
	if h.svc.PushInfo().HasToken || rec.kicked() != 2 {
		t.Fatalf("after forget: HasToken true or kicks %d", rec.kicked())
	}
}

func TestPushInfoQR(t *testing.T) {
	h := newHarness(t)
	withPush(h)
	if info := h.svc.PushInfo(); info.SubscribeURL != "" || info.QR != "" || info.TokenStore != "Test store" || info.Status.State != "ok" {
		t.Fatalf("no topic: %+v", info)
	}
	st, err := h.svc.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	st.PushTopic = "ar-testtopic"
	if _, err := h.svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	info := h.svc.PushInfo()
	if info.SubscribeURL != "https://ntfy.sh/ar-testtopic" {
		t.Fatalf("SubscribeURL = %q", info.SubscribeURL)
	}
	if !strings.HasPrefix(info.QR, "data:image/png;base64,") || len(info.QR) < 200 {
		t.Fatalf("QR = %.60q", info.QR)
	}
}

func TestPushNewTopicChangesTopic(t *testing.T) {
	h := newHarness(t)
	withPush(h)
	st, err := h.svc.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	st.PushEnabled = true
	if _, err := h.svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	st, _ = h.svc.GetSettings()
	old := st.PushTopic
	got, err := h.svc.PushNewTopic()
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := h.svc.GetSettings()
	if got.PushTopic == "" || got.PushTopic == old || saved.PushTopic != got.PushTopic || !strings.HasPrefix(got.PushTopic, "ar-") {
		t.Fatalf("old %q, returned %q, saved %q", old, got.PushTopic, saved.PushTopic)
	}
	if !saved.PushEnabled {
		t.Fatal("PushNewTopic turned push off")
	}
}

// A settings change that moves the listener's topic restarts it; an item
// change only kicks the reconciler.
func TestPushChangedRestartsListenerOnTopicChange(t *testing.T) {
	h := newHarness(t)
	rec, _ := withPush(h)
	p := h.svc.d.Push
	p.changed()
	if rec.kicked() != 1 || len(p.restart) != 0 {
		t.Fatalf("item change: kicks %d, restart %d", rec.kicked(), len(p.restart))
	}
	st, _ := h.svc.GetSettings()
	st.PushEnabled = true
	if _, err := h.svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	p.changed()
	if len(p.restart) != 1 {
		t.Fatal("enabling push did not restart the listener")
	}
	<-p.restart
	p.changed()
	if len(p.restart) != 0 {
		t.Fatal("unchanged config restarted the listener")
	}
}
