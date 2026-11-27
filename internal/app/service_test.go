package app

import (
	"errors"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"awesome-reminder/internal/core"
	"awesome-reminder/internal/hotkey"
	"awesome-reminder/internal/model"
	"awesome-reminder/internal/reply"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/settings"
	"awesome-reminder/internal/store"
)

type fakeReg struct {
	mu         sync.Mutex
	registered []string
	fail       map[string]error
}

func (f *fakeReg) reg(c hotkey.Combo, _ func()) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[c.String()]; err != nil {
		return nil, err
	}
	f.registered = append(f.registered, c.String())
	return func() {}, nil
}

type fakeWindows struct {
	mu            sync.Mutex
	shown, hidden int
	opened        []string
	pending       string
	alertsShown   int // ShowAlerts calls
	alertsHidden  int // DismissAlerts calls
	alertHeights  []int
	settings      int
}

func (w *fakeWindows) ShowCapture()       { w.shown++ }
func (w *fakeWindows) HideCapture()       { w.hidden++ }
func (w *fakeWindows) ResizeCapture(int)  {}
func (w *fakeWindows) OpenList(id string) { w.opened = append(w.opened, id) }
func (w *fakeWindows) TakePendingFocus() string {
	id := w.pending
	w.pending = ""
	return id
}
func (w *fakeWindows) OpenSettings() { w.settings++ }
func (w *fakeWindows) ShowAlerts() {
	w.mu.Lock()
	w.alertsShown++
	w.mu.Unlock()
}
func (w *fakeWindows) ResizeAlerts(h int) { w.alertHeights = append(w.alertHeights, h) }
func (w *fakeWindows) WidenList(int)      {}
func (w *fakeWindows) RestoreListWidth()  {}
func (w *fakeWindows) DismissAlerts() {
	w.mu.Lock()
	w.alertsHidden++
	w.mu.Unlock()
}

type harness struct {
	svc       *Service
	st        *store.Store
	reg       *fakeReg
	win       *fakeWindows
	autostart []bool
	exports   int
	statuses  int
	emitted   []emitted
}

type emitted struct {
	name string
	data any
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessAt(t, time.Now)
}

// newHarnessAt is newHarness with the core's clock set to now.
func newHarnessAt(t *testing.T, now func() time.Time) *harness {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := &harness{st: st, reg: &fakeReg{fail: map[string]error{}}, win: &fakeWindows{}}
	c := core.New(st, now, time.UTC)
	hk := hotkey.NewManager(h.reg.reg, func() {})
	if err := hk.Set(settings.Defaults(runtime.GOOS).Hotkey); err != nil {
		t.Fatal(err)
	}
	h.svc = NewService(Deps{
		Core:          c,
		Windows:       h.win,
		Hotkey:        hk,
		Autostart:     func(on bool) error { h.autostart = append(h.autostart, on); return nil },
		Export:        func() { h.exports++ },
		StatusChanged: func() { h.statuses++ },
		Emit:          func(name string, data any) { h.emitted = append(h.emitted, emitted{name, data}) },
	})
	return h
}

func TestServiceSaveSettingsReRegistersHotkey(t *testing.T) {
	h := newHarness(t)
	s := settings.Defaults(runtime.GOOS)
	s.Hotkey = "Alt+K"
	s.Autostart = false
	if err := h.svc.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if last := h.reg.registered[len(h.reg.registered)-1]; last != "Alt+K" {
		t.Fatalf("registrar calls %v, want last Alt+K", h.reg.registered)
	}
	got, err := h.svc.GetSettings()
	if err != nil || got.Hotkey != "Alt+K" {
		t.Fatalf("stored %+v, %v", got, err)
	}
	if !reflect.DeepEqual(h.autostart, []bool{false}) {
		t.Fatalf("autostart calls %v", h.autostart)
	}
	if h.exports != 1 {
		t.Fatalf("exports %d, want 1", h.exports)
	}
	if st := h.svc.Status(); st.HotkeyErr != "" {
		t.Fatalf("HotkeyErr %q", st.HotkeyErr)
	}
}

func TestServiceSaveSettingsInvalid(t *testing.T) {
	h := newHarness(t)
	before, err := h.svc.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	calls := len(h.reg.registered)
	s := settings.Defaults(runtime.GOOS)
	s.Hotkey = "Alt+K"
	s.DefaultTime = "25:00"
	if err := h.svc.SaveSettings(s); err == nil {
		t.Fatal("invalid settings saved without error")
	}
	after, err := h.svc.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("settings changed: %+v -> %+v", before, after)
	}
	if len(h.reg.registered) != calls || len(h.autostart) != 0 || h.exports != 0 {
		t.Fatalf("side effects on invalid save: reg %v autostart %v exports %d", h.reg.registered, h.autostart, h.exports)
	}
}

func TestServiceSaveSettingsHotkeyTakenStillSaves(t *testing.T) {
	h := newHarness(t)
	h.reg.fail["Alt+K"] = errors.New("Alt+K is already used by another app")
	s := settings.Defaults(runtime.GOOS)
	s.Hotkey = "Alt+K"
	if err := h.svc.SaveSettings(s); err == nil {
		t.Fatal("want hotkey error")
	}
	got, _ := h.svc.GetSettings()
	if got.Hotkey != "Alt+K" {
		t.Fatalf("settings not saved: %+v", got)
	}
	if st := h.svc.Status(); st.HotkeyErr == "" {
		t.Fatal("Status().HotkeyErr empty after failed registration")
	}
	if h.statuses == 0 {
		t.Fatal("StatusChanged not called")
	}
	if len(h.autostart) != 1 || h.exports != 1 {
		t.Fatalf("autostart %v exports %d: later steps skipped", h.autostart, h.exports)
	}
}

func TestServiceCaptureAndDone(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Capture("   "); !errors.Is(err, core.ErrEmpty) {
		t.Fatalf("empty capture err = %v", err)
	}
	it, err := h.svc.Capture("deploy #ops")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Done(it.ID); err != nil {
		t.Fatal(err)
	}
	v, err := h.svc.View()
	if err != nil || len(v.Done) != 1 {
		t.Fatalf("view %+v %v", v, err)
	}
}

func TestServiceOpenListAndTakePendingFocus(t *testing.T) {
	w := &fakeWindows{pending: "01ABC"}
	svc := NewService(Deps{Windows: w})
	svc.OpenList("01ABC")
	if len(w.opened) != 1 || w.opened[0] != "01ABC" {
		t.Fatalf("opened %v", w.opened)
	}
	if got := svc.TakePendingFocus(); got != "01ABC" {
		t.Fatalf("take %q", got)
	}
	if got := svc.TakePendingFocus(); got != "" {
		t.Fatalf("second take %q", got)
	}
}

// SnoozeReplies (the sweep's Tomorrow) snoozes every open reply to the next
// working day at the default time, local time, and drops their cards and the
// sweep card; other items and their cards are left alone.
func TestServiceSnoozeReplies(t *testing.T) {
	wed := time.Date(2026, 12, 2, 17, 30, 0, 0, time.Local) // Wednesday
	h := newHarnessAt(t, func() time.Time { return wed })
	a, err := h.svc.CaptureReply("reply to Priya", reply.Meta{OwedTo: "Priya", Source: "Teams"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.svc.CaptureReply("answer the survey", reply.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	putReminder(t, h, "R", "call Sam", nil, "")
	h.svc.showAlerts([]scheduler.Alert{
		{Key: a.ID + "|nudge|1", ItemID: a.ID, Text: a.Text, DueAt: wed, Kind: scheduler.KindNudge},
		alert("R", "r1"),
		{Key: "sweep|2026-12-02", DueAt: wed, Kind: scheduler.KindSweep, People: []string{"Priya", "Someone"}},
	})
	if n := len(h.svc.PendingAlerts()); n != 3 {
		t.Fatalf("queue %v", keys(h.svc.PendingAlerts()))
	}
	if err := h.svc.SnoozeReplies(); err != nil {
		t.Fatal(err)
	}
	thu9 := time.Date(2026, 12, 3, 9, 0, 0, 0, time.Local)
	for _, id := range []string{a.ID, b.ID} {
		it, _ := h.st.Get(id)
		if it.SnoozeUntil == nil || !it.SnoozeUntil.Equal(thu9) {
			t.Fatalf("reply %s snoozed until %v, want %v", id, it.SnoozeUntil, thu9)
		}
	}
	if it, _ := h.st.Get("R"); it.SnoozeUntil != nil {
		t.Fatalf("reminder snoozed: %+v", it)
	}
	if got := keys(h.svc.PendingAlerts()); !reflect.DeepEqual(got, []string{"r1"}) {
		t.Fatalf("queue %v, want only the reminder's card", got)
	}

	// On a Friday the next working day is Monday.
	fri := time.Date(2026, 12, 4, 18, 0, 0, 0, time.Local)
	h2 := newHarnessAt(t, func() time.Time { return fri })
	c, err := h2.svc.CaptureReply("x", reply.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h2.svc.SnoozeReplies(); err != nil {
		t.Fatal(err)
	}
	mon9 := time.Date(2026, 12, 7, 9, 0, 0, 0, time.Local)
	if it, _ := h2.st.Get(c.ID); it.SnoozeUntil == nil || !it.SnoozeUntil.Equal(mon9) {
		t.Fatalf("friday: snoozed until %v, want %v", it.SnoozeUntil, mon9)
	}
}

func TestServiceCaptureAndDropReply(t *testing.T) {
	h := newHarness(t)
	it, err := h.svc.CaptureReply("deploy?", reply.Meta{OwedTo: "Priya", Source: "Teams", Quote: "can you check"})
	if err != nil {
		t.Fatal(err)
	}
	if !it.Reply || it.OwedTo != "Priya" || it.Source != "Teams" {
		t.Fatalf("captured %+v", it)
	}
	h.svc.showAlerts([]scheduler.Alert{{Key: it.ID + "|nudge|1", ItemID: it.ID, Text: it.Text, DueAt: time.Now(), Kind: scheduler.KindNudge}})
	if err := h.svc.DropReply(it.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := h.st.Get(it.ID)
	if got.Status != model.StatusDone || got.ReplyOutcome != model.ReplyDropped {
		t.Fatalf("dropped %+v", got)
	}
	if n := len(h.svc.PendingAlerts()); n != 0 || h.win.alertsHidden != 1 {
		t.Fatalf("queue %v hidden %d", keys(h.svc.PendingAlerts()), h.win.alertsHidden)
	}
	if err := h.svc.DropReply("missing"); err == nil {
		t.Fatal("DropReply of an unknown item succeeded")
	}
}
