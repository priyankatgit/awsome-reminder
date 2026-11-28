package app

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"awesome-reminder/internal/core"
	"awesome-reminder/internal/grab"
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
	active     map[string]int // registrations not yet released, by combo
}

func (f *fakeReg) reg(c hotkey.Combo, _ func()) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[c.String()]; err != nil {
		return nil, err
	}
	f.registered = append(f.registered, c.String())
	if f.active == nil {
		f.active = map[string]int{}
	}
	f.active[c.String()]++
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.active[c.String()]--; f.active[c.String()] == 0 {
				delete(f.active, c.String())
			}
		})
	}, nil
}

// activeCombos lists the registered, unreleased combos, sorted.
func (f *fakeReg) activeCombos() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for c := range f.active {
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}

// exclusiveReg is a registrar shared by several managers that, like the OS,
// refuses a combo that is already registered.
type exclusiveReg struct {
	mu   sync.Mutex
	held map[string]bool
}

func (e *exclusiveReg) reg(c hotkey.Combo, _ func()) (func(), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.held[c.String()] {
		return nil, errors.New(c.String() + " is already used by another app")
	}
	e.held[c.String()] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			delete(e.held, c.String())
		})
	}, nil
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
	replyReg  *fakeReg // the reply hotkey's registrar
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
	h := &harness{st: st, reg: &fakeReg{fail: map[string]error{}}, replyReg: &fakeReg{fail: map[string]error{}}, win: &fakeWindows{}}
	c := core.New(st, now, time.UTC)
	hk := hotkey.NewManager(h.reg.reg, func() {})
	if err := hk.Set(settings.Defaults(runtime.GOOS).Hotkey); err != nil {
		t.Fatal(err)
	}
	rhk := hotkey.NewManager(h.replyReg.reg, func() {})
	if err := rhk.Set(settings.Defaults(runtime.GOOS).ReplyHotkey); err != nil {
		t.Fatal(err)
	}
	h.svc = NewService(Deps{
		Core:          c,
		Windows:       h.win,
		Hotkey:        hk,
		ReplyHotkey:   rhk,
		Autostart:     func(on bool) error { h.autostart = append(h.autostart, on); return nil },
		Export:        func() { h.exports++ },
		StatusChanged: func() { h.statuses++ },
		Emit:          func(name string, data any) { h.emitted = append(h.emitted, emitted{name, data}) },
	})
	return h
}

// A Settings draft loaded before another computer's shared-settings change
// landed saves its local fields and returns the note for the window.
func TestServiceSaveSettingsStaleDraftNote(t *testing.T) {
	h := newHarness(t)
	draft, _ := h.svc.GetSettings()
	p := draft.Shared()
	p.DefaultTime = "07:00"
	if ok, err := h.svc.d.Core.ApplyRemoteShared(p, time.Now().Add(time.Minute), "ZZZZ"); err != nil || !ok {
		t.Fatalf("remote: %v %v", ok, err)
	}
	draft.Hotkey = "Alt+K"
	note, err := h.svc.SaveSettings(draft)
	if err != nil || note != core.SettingsChangedElsewhere {
		t.Fatalf("note %q, err %v", note, err)
	}
	got, _ := h.svc.GetSettings()
	if got.DefaultTime != "07:00" || got.Hotkey != "Alt+K" {
		t.Errorf("stored %+v", got)
	}
	if last := h.reg.registered[len(h.reg.registered)-1]; last != "Alt+K" {
		t.Errorf("registrar calls %v, want last Alt+K", h.reg.registered)
	}
	if fresh, _ := h.svc.SaveSettings(got); fresh != "" {
		t.Errorf("a draft with the stored stamp got a note: %q", fresh)
	}
}

func TestServiceSaveSettingsReRegistersHotkey(t *testing.T) {
	h := newHarness(t)
	s := settings.Defaults(runtime.GOOS)
	s.Hotkey = "Alt+K"
	s.Autostart = false
	if _, err := h.svc.SaveSettings(s); err != nil {
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
	if _, err := h.svc.SaveSettings(s); err == nil {
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
	if _, err := h.svc.SaveSettings(s); err == nil {
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

// Tomorrow pressed after midnight, before the default time, means that
// morning, not the day after.
func TestServiceSnoozeRepliesAfterMidnight(t *testing.T) {
	thu := time.Date(2026, 12, 3, 0, 30, 0, 0, time.Local) // Thursday
	h := newHarnessAt(t, func() time.Time { return thu })
	a, err := h.svc.CaptureReply("reply to Priya", reply.Meta{OwedTo: "Priya"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SnoozeReplies(); err != nil {
		t.Fatal(err)
	}
	thu9 := time.Date(2026, 12, 3, 9, 0, 0, 0, time.Local)
	if it, _ := h.st.Get(a.ID); it.SnoozeUntil == nil || !it.SnoozeUntil.Equal(thu9) {
		t.Fatalf("snoozed until %v, want %v", it.SnoozeUntil, thu9)
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

// The details panel's "For" field saves through SetOwedTo.
func TestServiceSetOwedTo(t *testing.T) {
	h := newHarness(t)
	it, err := h.svc.CaptureReply("deploy?", reply.Meta{OwedTo: "Priya", Source: "Teams"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.SetOwedTo(it.ID, "  Sam ")
	if err != nil || got.OwedTo != "Sam" || !got.Reply {
		t.Fatalf("SetOwedTo: %+v %v", got, err)
	}
	if stored, _ := h.st.Get(it.ID); stored.OwedTo != "Sam" {
		t.Fatalf("stored OwedTo %q", stored.OwedTo)
	}
	if _, err := h.svc.SetOwedTo("missing", "Sam"); err == nil {
		t.Fatal("SetOwedTo of an unknown item succeeded")
	}
}

// A reply hotkey that cannot be registered is reported on its own: the
// settings stay saved, SaveSettings returns the error, Status carries it in
// ReplyHotkeyErr, and the capture hotkey is unaffected.
func TestSaveSettingsReportsReplyHotkeyError(t *testing.T) {
	h := newHarness(t)
	h.replyReg.fail["Ctrl+Alt+S"] = errors.New("Ctrl+Alt+S is already used by another app")
	s := settings.Defaults(runtime.GOOS)
	s.Hotkey = "Alt+K"
	s.ReplyHotkey = "Ctrl+Alt+S"
	_, err := h.svc.SaveSettings(s)
	if err == nil || !strings.Contains(err.Error(), "reply hotkey") {
		t.Fatalf("SaveSettings err = %v, want a reply hotkey error", err)
	}
	if got, _ := h.svc.GetSettings(); got.ReplyHotkey != "Ctrl+Alt+S" {
		t.Fatalf("settings not saved: %+v", got)
	}
	st := h.svc.Status()
	if st.ReplyHotkeyErr != "Ctrl+Alt+S is already used by another app" {
		t.Fatalf("ReplyHotkeyErr = %q", st.ReplyHotkeyErr)
	}
	if st.HotkeyErr != "" {
		t.Fatalf("HotkeyErr = %q, want the capture hotkey unaffected", st.HotkeyErr)
	}
	if got := h.reg.activeCombos(); !reflect.DeepEqual(got, []string{"Alt+K"}) {
		t.Fatalf("capture registrations %v, want [Alt+K]", got)
	}
	// The reply hotkey that worked before stays registered.
	if got := h.replyReg.activeCombos(); !reflect.DeepEqual(got, []string{"Ctrl+Alt+R"}) {
		t.Fatalf("reply registrations %v, want the old [Ctrl+Alt+R]", got)
	}
	if h.statuses == 0 {
		t.Fatal("StatusChanged not called")
	}

	// A later save that works clears the error.
	s.ReplyHotkey = "Ctrl+Alt+T"
	if _, err := h.svc.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if st := h.svc.Status(); st.ReplyHotkeyErr != "" || st.HotkeyErr != "" {
		t.Fatalf("status after a good save %+v", st)
	}
}

// Swapping the two hotkeys must work although each new combo is held by the
// other manager when the save starts (the OS refuses a combo registered
// twice).
func TestSaveSettingsSwapsHotkeys(t *testing.T) {
	h := newHarness(t)
	ex := &exclusiveReg{held: map[string]bool{}}
	capHk := hotkey.NewManager(ex.reg, func() {})
	repHk := hotkey.NewManager(ex.reg, func() {})
	if err := capHk.Set("Ctrl+Shift+Space"); err != nil {
		t.Fatal(err)
	}
	if err := repHk.Set("Ctrl+Alt+R"); err != nil {
		t.Fatal(err)
	}
	h.svc.d.Hotkey, h.svc.d.ReplyHotkey = capHk, repHk
	s := settings.Defaults(runtime.GOOS)
	s.Hotkey, s.ReplyHotkey = "Ctrl+Alt+R", "Ctrl+Shift+Space"
	if _, err := h.svc.SaveSettings(s); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if st := h.svc.Status(); st.HotkeyErr != "" || st.ReplyHotkeyErr != "" {
		t.Fatalf("status after swap %+v", st)
	}
	if len(ex.held) != 2 {
		t.Fatalf("held %v, want both combos", ex.held)
	}
}

// While Settings records a combo, both hotkeys are released so either combo
// reaches the recorder; afterwards both are registered again.
func TestHotkeyRecordingSuspendsBoth(t *testing.T) {
	h := newHarness(t)
	capCombo := h.reg.activeCombos()
	repCombo := h.replyReg.activeCombos()
	if len(capCombo) != 1 || len(repCombo) != 1 {
		t.Fatalf("registered before: %v %v", capCombo, repCombo)
	}
	h.svc.SetHotkeyRecording(true)
	if got := h.reg.activeCombos(); len(got) != 0 {
		t.Fatalf("capture hotkey still registered while recording: %v", got)
	}
	if got := h.replyReg.activeCombos(); len(got) != 0 {
		t.Fatalf("reply hotkey still registered while recording: %v", got)
	}
	h.svc.SetHotkeyRecording(false)
	if got := h.reg.activeCombos(); !reflect.DeepEqual(got, capCombo) {
		t.Fatalf("capture after recording %v, want %v", got, capCombo)
	}
	if got := h.replyReg.activeCombos(); !reflect.DeepEqual(got, repCombo) {
		t.Fatalf("reply after recording %v, want %v", got, repCombo)
	}
}

// fakeReplyWindows records what the reply opener shows.
type fakeReplyWindows struct {
	mu    sync.Mutex
	mode  captureMode
	shown []ReplyPrefill
}

func (w *fakeReplyWindows) captureMode() captureMode {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.mode
}

func (w *fakeReplyWindows) show(p ReplyPrefill) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.shown = append(w.shown, p)
}

func (w *fakeReplyWindows) got() []ReplyPrefill {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.shown)
}

// teamsGrab is a grab of "deploy?" in a Teams chat with Priya Sharma.
func teamsGrab(calls *[][]grab.Key) func(context.Context, []grab.Key) grab.Result {
	return func(_ context.Context, keys []grab.Key) grab.Result {
		*calls = append(*calls, keys)
		return grab.Result{Text: "deploy?", Process: "ms-teams.exe", Title: "Chat | Priya Sharma | Microsoft Teams", Grabbed: true}
	}
}

func TestReplyPrefillFromGrab(t *testing.T) {
	var calls [][]grab.Key
	w := &fakeReplyWindows{}
	o := &replyOpener{grab: teamsGrab(&calls), trusted: func() bool { return true }, mode: w.captureMode, show: w.show}
	o.Start(context.Background())
	keys := []grab.Key{grab.KeyControl, grab.KeyAlt, 0x52}
	o.Press(keys)
	want := ReplyPrefill{
		Line:    `Reply to Priya Sharma: "deploy?"`,
		OwedTo:  "Priya Sharma",
		Source:  "Teams",
		Quote:   "deploy?",
		Grabbed: true,
	}
	if got := w.got(); !reflect.DeepEqual(got, []ReplyPrefill{want}) {
		t.Fatalf("shown %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(calls, [][]grab.Key{keys}) {
		t.Fatalf("grab called with %v, want the hotkey's keys once", calls)
	}
}

// A select-all in a huge document reaches the capture box capped.
func TestReplyPrefillCapsQuote(t *testing.T) {
	big := strings.Repeat("z", reply.MaxQuoteBytes*2)
	p := replyPrefill(grab.Result{Text: big, Process: "WINWORD.EXE", Grabbed: true}, false)
	if want := strings.Repeat("z", reply.MaxQuoteBytes) + "…"; p.Quote != want {
		t.Fatalf("quote is %d bytes, want %d", len(p.Quote), len(want))
	}
}

// Without the macOS Accessibility permission the box says so (NeedsAccess)
// and still opens with what the fallback found.
func TestReplyPrefillNeedsAccess(t *testing.T) {
	w := &fakeReplyWindows{}
	o := &replyOpener{
		grab: func(context.Context, []grab.Key) grab.Result {
			return grab.Result{Text: "clipboard text", Process: "Slack"}
		},
		trusted: func() bool { return false },
		mode:    w.captureMode,
		show:    w.show,
	}
	o.Start(context.Background())
	o.Press(nil)
	got := w.got()
	if len(got) != 1 || !got[0].NeedsAccess || got[0].Grabbed || got[0].Source != "Slack" || got[0].Line != `Reply: "clipboard text"` {
		t.Fatalf("shown %+v", got)
	}
}

// grab.Grab must never run before ApplicationStarted (Start) or once the app
// is shutting down: on macOS it waits for the main queue.
func TestReplyPressIgnoredBeforeStartAndAfterCancel(t *testing.T) {
	var calls [][]grab.Key
	w := &fakeReplyWindows{}
	o := &replyOpener{grab: teamsGrab(&calls), trusted: func() bool { return true }, mode: w.captureMode, show: w.show}
	o.Press(nil)
	if len(calls) != 0 || len(w.got()) != 0 {
		t.Fatalf("pressed before Start: grabs %d shown %d", len(calls), len(w.got()))
	}
	ctx, cancel := context.WithCancel(context.Background())
	o.Start(ctx)
	cancel()
	o.Press(nil)
	if len(calls) != 0 || len(w.got()) != 0 {
		t.Fatalf("pressed after cancel: grabs %d shown %d", len(calls), len(w.got()))
	}
}

// A second press while the box is already open in reply mode does not grab
// again (the copy would come from our own box).
func TestReplyPressSkippedWhileOpen(t *testing.T) {
	var calls [][]grab.Key
	w := &fakeReplyWindows{mode: captureReply}
	o := &replyOpener{grab: teamsGrab(&calls), trusted: func() bool { return true }, mode: w.captureMode, show: w.show}
	o.Start(context.Background())
	o.Press(nil)
	if len(calls) != 0 || len(w.got()) != 0 {
		t.Fatalf("pressed while open: grabs %d shown %d", len(calls), len(w.got()))
	}
}

// A press while the normal capture box is open never grabs (the copy would
// come from our own box: old clipboard text as the quote, our own window as
// the source). The box switches to reply mode keeping what was typed: an
// empty prefill with KeepText.
func TestReplyPressWithCaptureOpenKeepsText(t *testing.T) {
	var calls [][]grab.Key
	w := &fakeReplyWindows{mode: captureNormal}
	o := &replyOpener{grab: teamsGrab(&calls), trusted: func() bool { return false }, mode: w.captureMode, show: w.show}
	o.Start(context.Background())
	o.Press([]grab.Key{grab.KeyControl, grab.KeyAlt, 0x52})
	if len(calls) != 0 {
		t.Fatalf("grabbed %d times with the capture box open", len(calls))
	}
	if got := w.got(); !reflect.DeepEqual(got, []ReplyPrefill{{KeepText: true}}) {
		t.Fatalf("shown %+v, want only KeepText", got)
	}

	// Once the box is hidden the hotkey grabs as before.
	w.mu.Lock()
	w.mode = captureHidden
	w.mu.Unlock()
	o.Press(nil)
	if got := w.got(); len(calls) != 1 || len(got) != 2 || got[1].KeepText || got[1].Line != `Reply to Priya Sharma: "deploy?"` {
		t.Fatalf("hidden box: grabs %d shown %+v", len(calls), got)
	}
}

// Stop waits for a press in flight (so its clipboard restore is handed off
// before grab.Wait), then ignores later presses; a press stuck past the
// timeout does not hold shutdown.
func TestReplyOpenerStop(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	w := &fakeReplyWindows{}
	o := &replyOpener{
		grab: func(context.Context, []grab.Key) grab.Result {
			close(entered)
			<-release
			return grab.Result{}
		},
		trusted: func() bool { return true },
		mode:    w.captureMode,
		show:    w.show,
	}
	o.Start(context.Background())
	go o.Press(nil)
	<-entered
	stopped := make(chan struct{})
	go func() { o.Stop(time.Second); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a press was in flight")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the press ended")
	}
	o.Press(nil) // ignored: grab would close entered twice and panic

	// A stuck press: Stop gives up after the timeout.
	unstick := make(chan struct{})
	t.Cleanup(func() { close(unstick) })
	stuck := &replyOpener{
		grab:    func(context.Context, []grab.Key) grab.Result { <-unstick; return grab.Result{} },
		trusted: func() bool { return true },
		mode:    w.captureMode,
		show:    w.show,
	}
	stuck.Start(context.Background())
	go stuck.Press(nil)
	time.Sleep(10 * time.Millisecond)
	start := time.Now()
	stuck.Stop(50 * time.Millisecond)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Stop waited %v for a stuck press", d)
	}
}

// Each reply registration's press carries the keys of the combo actually
// registered.
func TestReplyRegistrarPassesComboKeys(t *testing.T) {
	var onPress func()
	reg := func(_ hotkey.Combo, f func()) (func(), error) { onPress = f; return func() {}, nil }
	var got []grab.Key
	m := hotkey.NewManager(replyRegistrar(reg, func(keys []grab.Key) { got = keys }), nil)
	if err := m.Set("Ctrl+Alt+R"); err != nil {
		t.Fatal(err)
	}
	onPress()
	c, _ := hotkey.ParseCombo("Ctrl+Alt+R")
	if !reflect.DeepEqual(got, replyKeys(c)) || len(got) == 0 {
		t.Fatalf("press keys %#v, want %#v", got, replyKeys(c))
	}
}

// replyKeys are all the hotkey's keys for grab.Grab: the modifiers and, on
// Windows, the main key, as virtual-key codes.
func TestReplyKeys(t *testing.T) {
	c, err := hotkey.ParseCombo("Ctrl+Alt+Shift+R")
	if err != nil {
		t.Fatal(err)
	}
	want := []grab.Key{grab.KeyControl, grab.KeyAlt, grab.KeyShift}
	if runtime.GOOS == "windows" {
		want = append(want, 0x52) // 'R'
	}
	if got := replyKeys(c); !reflect.DeepEqual(got, want) {
		t.Fatalf("replyKeys = %#v, want %#v", got, want)
	}
	if runtime.GOOS == "windows" {
		f, _ := hotkey.ParseCombo("Ctrl+F5")
		if got := replyKeys(f); !reflect.DeepEqual(got, []grab.Key{grab.KeyControl, 0x74}) {
			t.Fatalf("replyKeys(Ctrl+F5) = %#v", got)
		}
	}
}
