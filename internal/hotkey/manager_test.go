package hotkey

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// On Wayland the registration error names the flag the user should bind
// instead: --capture for the capture hotkey, --reply for the reply hotkey.
func TestWaylandErrorNamesFlag(t *testing.T) {
	c, _ := ParseCombo("Ctrl+Alt+R")
	for _, flag := range []string{"--capture", "--reply"} {
		_, err := platformFor("linux", "wayland", flag)(c, func() {})
		want := `bind "awesome-reminder ` + flag + `"`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("flag %s: err = %v, want it to contain %s", flag, err, want)
		}
	}
	// Elsewhere the real registrar is used.
	if r := platformFor("linux", "x11", "--reply"); r == nil {
		t.Fatal("no registrar for X11")
	}
}

type fakeReg struct {
	mu         sync.Mutex
	registered []string
	unregCount map[string]int
	fail       map[string]error
}

func newFakeReg() *fakeReg {
	return &fakeReg{unregCount: map[string]int{}, fail: map[string]error{}}
}

func (f *fakeReg) reg(c Combo, onPress func()) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := c.String()
	if err := f.fail[s]; err != nil {
		return nil, err
	}
	f.registered = append(f.registered, s)
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.unregCount[s]++
	}, nil
}

func TestHotkeyManagerSetAndReplace(t *testing.T) {
	f := newFakeReg()
	m := NewManager(f.reg, func() {})
	if err := m.Set("Ctrl+Shift+Space"); err != nil {
		t.Fatal(err)
	}
	if err := m.Set("ctrl+shift+space"); err != nil { // same combo: no-op
		t.Fatal(err)
	}
	if len(f.registered) != 1 {
		t.Fatalf("same combo re-registered: %v", f.registered)
	}
	if err := m.Set("Ctrl+Alt+K"); err != nil {
		t.Fatal(err)
	}
	if f.unregCount["Ctrl+Shift+Space"] != 1 {
		t.Fatalf("old not unregistered: %v", f.unregCount)
	}
	m.Close()
	if f.unregCount["Ctrl+Alt+K"] != 1 || m.Err() != nil {
		t.Fatalf("Close: %v err=%v", f.unregCount, m.Err())
	}
	m.Close() // idempotent
	if f.unregCount["Ctrl+Alt+K"] != 1 {
		t.Fatal("double unregister")
	}
}

func TestHotkeyManagerReportsRegisterError(t *testing.T) {
	f := newFakeReg()
	m := NewManager(f.reg, func() {})
	if err := m.Set("Ctrl+Shift+Space"); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("taken")
	f.fail["Ctrl+Alt+K"] = boom
	if err := m.Set("Ctrl+Alt+K"); !errors.Is(err, boom) {
		t.Fatalf("Set error = %v, want %v", err, boom)
	}
	if m.Err() == nil {
		t.Fatal("Err() nil after failed Set")
	}
	if f.unregCount["Ctrl+Shift+Space"] != 0 {
		t.Fatal("old hotkey dropped after failed Set")
	}
	// a later successful Set clears Err and releases the old hotkey
	if err := m.Set("Ctrl+Alt+J"); err != nil {
		t.Fatal(err)
	}
	if m.Err() != nil {
		t.Fatalf("Err() = %v after success", m.Err())
	}
	if f.unregCount["Ctrl+Shift+Space"] != 1 {
		t.Fatalf("old not unregistered: %v", f.unregCount)
	}
}

func TestHotkeyManagerParseErrorKeepsOld(t *testing.T) {
	f := newFakeReg()
	m := NewManager(f.reg, func() {})
	_ = m.Set("Ctrl+Shift+Space")
	if err := m.Set("Ctrl+Foo"); err == nil {
		t.Fatal("expected parse error")
	}
	if m.Err() == nil {
		t.Fatal("Err() nil")
	}
	if f.unregCount["Ctrl+Shift+Space"] != 0 {
		t.Fatal("old hotkey dropped on typo")
	}
	// setting back to the current combo clears the error without re-registering
	if err := m.Set("Ctrl+Shift+Space"); err != nil || m.Err() != nil {
		t.Fatalf("err=%v Err()=%v", err, m.Err())
	}
	if len(f.registered) != 1 {
		t.Fatalf("re-registered: %v", f.registered)
	}
}

func TestHotkeyManagerPassesOnPress(t *testing.T) {
	var got func()
	reg := func(c Combo, onPress func()) (func(), error) { got = onPress; return func() {}, nil }
	n := 0
	m := NewManager(reg, func() { n++ })
	_ = m.Set("Ctrl+Alt+K")
	got()
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
}

func TestValidateForOS(t *testing.T) {
	c, _ := ParseCombo("Cmd+K")
	if err := validateFor("windows", c); err == nil || err.Error() != "Cmd is only available on macOS" {
		t.Fatalf("got %v", err)
	}
	if err := validateFor("darwin", c); err != nil {
		t.Fatal(err)
	}
	c2, _ := ParseCombo("Ctrl+K")
	if err := validateFor("linux", c2); err != nil {
		t.Fatal(err)
	}
}

// pumpReg is a Registrar built on pumpPresses, like the real one.
func pumpReg(events chan struct{}) Registrar {
	return func(c Combo, onPress func()) (func(), error) {
		stop := make(chan struct{})
		go pumpPresses((<-chan struct{})(events), stop, onPress)
		return func() { close(stop) }, nil
	}
}

func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("%s blocked while handler running", what)
	}
}

func TestBlockedHandlerDoesNotBlockSetOrClose(t *testing.T) {
	events := make(chan struct{})
	started := make(chan struct{})
	release := make(chan struct{})
	var m *Manager
	m = NewManager(pumpReg(events), func() {
		_ = m.Err() // re-entrancy must not deadlock
		close(started)
		<-release
	})
	defer close(release)
	if err := m.Set("Ctrl+Alt+K"); err != nil {
		t.Fatal(err)
	}
	events <- struct{}{}
	<-started
	within(t, "Set", func() { _ = m.Set("Ctrl+Alt+J") })
	within(t, "Close", m.Close)
}

func TestPumpPressesCoalescesWhileBusy(t *testing.T) {
	events := make(chan struct{})
	stop := make(chan struct{})
	defer close(stop)
	var n atomic.Int32
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	go pumpPresses((<-chan struct{})(events), stop, func() { n.Add(1); started <- struct{}{}; <-release })
	events <- struct{}{}
	<-started
	events <- struct{}{} // dropped: handler still running
	events <- struct{}{}
	close(release)
	time.Sleep(50 * time.Millisecond)
	events <- struct{}{} // handler finished: accepted
	<-started
	if n.Load() != 2 {
		t.Fatalf("handler ran %d times, want 2", n.Load())
	}
}

func TestHotkeyManagerSuspendResume(t *testing.T) {
	f := newFakeReg()
	m := NewManager(f.reg, func() {})
	if err := m.Set("Ctrl+Shift+Space"); err != nil {
		t.Fatal(err)
	}
	m.Suspend()
	if f.unregCount["Ctrl+Shift+Space"] != 1 {
		t.Fatalf("Suspend did not release the hotkey: %v", f.unregCount)
	}
	m.Suspend() // idempotent
	if f.unregCount["Ctrl+Shift+Space"] != 1 {
		t.Fatalf("second Suspend released again: %v", f.unregCount)
	}
	if err := m.Resume(); err != nil {
		t.Fatal(err)
	}
	if len(f.registered) != 2 || f.registered[1] != "Ctrl+Shift+Space" {
		t.Fatalf("Resume did not re-register: %v", f.registered)
	}
	if err := m.Resume(); err != nil || len(f.registered) != 2 { // not suspended: no-op
		t.Fatalf("Resume when not suspended: err=%v registered=%v", err, f.registered)
	}
}

func TestHotkeyManagerSetWhileSuspended(t *testing.T) {
	f := newFakeReg()
	m := NewManager(f.reg, func() {})
	_ = m.Set("Ctrl+Shift+Space")
	m.Suspend()
	if err := m.Set("Ctrl+Alt+K"); err != nil {
		t.Fatal(err)
	}
	if err := m.Resume(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"Ctrl+Shift+Space", "Ctrl+Alt+K"}; len(f.registered) != 2 || f.registered[1] != want[1] {
		t.Fatalf("registered %v, want %v (Set ends the suspension)", f.registered, want)
	}
	if f.unregCount["Ctrl+Shift+Space"] != 1 {
		t.Fatalf("old released %d times, want 1", f.unregCount["Ctrl+Shift+Space"])
	}
}

func TestHotkeyManagerResumeFailureReported(t *testing.T) {
	f := newFakeReg()
	m := NewManager(f.reg, func() {})
	_ = m.Set("Ctrl+Shift+Space")
	m.Suspend()
	boom := errors.New("taken meanwhile")
	f.fail["Ctrl+Shift+Space"] = boom
	if err := m.Resume(); !errors.Is(err, boom) || !errors.Is(m.Err(), boom) {
		t.Fatalf("Resume err=%v Err()=%v", err, m.Err())
	}
	delete(f.fail, "Ctrl+Shift+Space")
	// still suspended after the failure: a retry registers it
	if err := m.Resume(); err != nil || m.Err() != nil {
		t.Fatalf("retry: err=%v Err()=%v", err, m.Err())
	}
}

// A failed Set's error must survive a recording session: Resume puts the
// old combo back but the saved one is still not registered.
func TestHotkeyManagerResumeKeepsSetError(t *testing.T) {
	f := newFakeReg()
	m := NewManager(f.reg, func() {})
	if err := m.Set("Ctrl+Alt+A"); err != nil {
		t.Fatal(err)
	}
	taken := errors.New("Ctrl+Alt+B is already used by another app")
	f.fail["Ctrl+Alt+B"] = taken
	if err := m.Set("Ctrl+Alt+B"); !errors.Is(err, taken) {
		t.Fatalf("Set B err = %v", err)
	}
	m.Suspend()
	if err := m.Resume(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(m.Err(), taken) {
		t.Fatalf("Err() after Resume = %v, want B's error", m.Err())
	}
	// A later successful Set clears it.
	delete(f.fail, "Ctrl+Alt+B")
	if err := m.Set("Ctrl+Alt+B"); err != nil || m.Err() != nil {
		t.Fatalf("Set B again: err=%v Err()=%v", err, m.Err())
	}
}
