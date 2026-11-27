//go:build windows

package hotkey

import (
	"strings"
	"testing"
	"time"
)

// An unusual combo, so the test does not collide with the user's hotkeys.
const testCombo = "Ctrl+Alt+Shift+F11"

// press simulates the combo by posting WM_HOTKEY for the newest
// registration to the hotkey thread.
func press(t *testing.T) {
	t.Helper()
	th, err := thread()
	if err != nil {
		t.Fatal(err)
	}
	var id uintptr
	if err := th.do(func() { id = th.nextID }); err != nil {
		t.Fatal(err)
	}
	if r, _, err := procPostThreadMessage.Call(uintptr(th.tid), wmHotkey, id, 0); r == 0 {
		t.Fatal(err)
	}
}

func TestWindowsRegisterPressUnregister(t *testing.T) {
	c, err := ParseCombo(testCombo)
	if err != nil {
		t.Fatal(err)
	}
	pressed := make(chan struct{}, 4)
	unreg, err := register(c, func() { pressed <- struct{}{} })
	if err != nil {
		t.Skipf("cannot register %s here: %v", testCombo, err)
	}
	press(t)
	select {
	case <-pressed:
	case <-time.After(2 * time.Second):
		t.Fatal("press not delivered")
	}

	// The same combo can't be registered twice; the error names the combo.
	if _, err := register(c, func() {}); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("second register: %v", err)
	}

	unreg()
	unreg() // idempotent
	// Released: it can be registered again.
	unreg2, err := register(c, func() {})
	if err != nil {
		t.Fatalf("register after unregister: %v", err)
	}
	unreg2()
}

// pressID posts WM_HOTKEY for registration id to the hotkey thread.
func pressID(t *testing.T, id uintptr) {
	t.Helper()
	th, err := thread()
	if err != nil {
		t.Fatal(err)
	}
	if r, _, err := procPostThreadMessage.Call(uintptr(th.tid), wmHotkey, id, 0); r == 0 {
		t.Fatal(err)
	}
}

// newestID is the RegisterHotKey id of the newest registration.
func newestID(t *testing.T) uintptr {
	t.Helper()
	th, err := thread()
	if err != nil {
		t.Fatal(err)
	}
	var id uintptr
	if err := th.do(func() { id = th.nextID }); err != nil {
		t.Fatal(err)
	}
	return id
}

// Two managers (the capture and reply hotkeys) are registered at once: each
// registration has its own id on the hotkey thread, a press reaches only its
// own handler, and releasing one leaves the other working.
func TestWindowsTwoManagersAtOnce(t *testing.T) {
	a := make(chan struct{}, 4)
	b := make(chan struct{}, 4)
	ma := NewManager(Platform("--capture"), func() { a <- struct{}{} })
	mb := NewManager(Platform("--capture"), func() { b <- struct{}{} })
	defer ma.Close()
	defer mb.Close()
	if err := ma.Set("Ctrl+Alt+Shift+F11"); err != nil {
		t.Skipf("cannot register here: %v", err)
	}
	idA := newestID(t)
	if err := mb.Set("Ctrl+Alt+Shift+F10"); err != nil {
		t.Skipf("cannot register here: %v", err)
	}
	idB := newestID(t)
	if idA == idB {
		t.Fatalf("both registrations got id %d", idA)
	}
	expect := func(want, other chan struct{}, name string) {
		t.Helper()
		select {
		case <-want:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: press not delivered", name)
		}
		select {
		case <-other:
			t.Fatalf("%s: press also reached the other hotkey", name)
		case <-time.After(30 * time.Millisecond):
		}
	}
	pressID(t, idA)
	expect(a, b, "A")
	pressID(t, idB)
	expect(b, a, "B")

	ma.Close()
	pressID(t, idB)
	expect(b, a, "B after A closed")
}
