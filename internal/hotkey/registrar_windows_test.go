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
