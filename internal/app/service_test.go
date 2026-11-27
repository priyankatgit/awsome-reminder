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
	shown, hidden int
	opened        []string
	pending       string
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
func (w *fakeWindows) OpenSettings()  {}
func (w *fakeWindows) DismissAlerts() {}

type harness struct {
	svc       *Service
	st        *store.Store
	reg       *fakeReg
	autostart []bool
	exports   int
	statuses  int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := &harness{st: st, reg: &fakeReg{fail: map[string]error{}}}
	c := core.New(st, time.Now, time.UTC)
	hk := hotkey.NewManager(h.reg.reg, func() {})
	if err := hk.Set(settings.Defaults(runtime.GOOS).Hotkey); err != nil {
		t.Fatal(err)
	}
	h.svc = NewService(Deps{
		Core:          c,
		Windows:       &fakeWindows{},
		Hotkey:        hk,
		Autostart:     func(on bool) error { h.autostart = append(h.autostart, on); return nil },
		Export:        func() { h.exports++ },
		StatusChanged: func() { h.statuses++ },
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
