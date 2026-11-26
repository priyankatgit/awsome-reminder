package core

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"awesome-reminder/internal/model"
)

// A mutation waits for the operation lock, and OnChange runs after the lock
// has been released.
func TestMutationsSerializedAndNotifyOutsideLock(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "tomorrow 5pm deploy")
	var lockFreeInCallback atomic.Bool
	e.c.OnChange(func() {
		if e.c.opMu.TryLock() {
			lockFreeInCallback.Store(true)
			e.c.opMu.Unlock()
		}
	})

	e.c.opMu.Lock()
	done := make(chan error, 1)
	go func() { _, err := e.c.Done(it.ID); done <- err }()
	select {
	case <-done:
		t.Fatal("Done ran while the operation lock was held")
	case <-time.After(50 * time.Millisecond):
	}
	e.c.opMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not finish after unlock")
	}
	if !lockFreeInCallback.Load() {
		t.Fatal("OnChange ran while the operation lock was held")
	}
}

func TestConcurrentDoneAndSnooze(t *testing.T) {
	e := newEnv(t)
	it := mustCapture(t, e, "tomorrow 5pm deploy")
	var changes atomic.Int32
	e.c.OnChange(func() { changes.Add(1) })

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				_, err := e.c.Done(it.ID)
				errs <- err
			} else {
				errs <- e.c.Snooze(it.ID, "10m")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := changes.Load(); got != n {
		t.Fatalf("OnChange called %d times, want %d", got, n)
	}
	got, err := e.st.Get(it.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Every Done ran at least once, so the item is done, and the fields
	// written together are consistent.
	if got.Status != model.StatusDone || got.DoneAt == nil || got.Kind != model.KindReminder || got.DueAt == nil {
		t.Fatalf("inconsistent final state: %+v", got)
	}
}
