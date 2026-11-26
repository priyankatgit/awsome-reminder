package hotkey

import (
	"errors"
	"sync"
	"testing"
)

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
