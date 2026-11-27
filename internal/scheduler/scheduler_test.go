package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"awesome-reminder/internal/model"
)

type fakeSrc struct {
	mu        sync.Mutex
	items     []model.Item
	notified  map[string]bool
	failFirst bool
	listed    chan struct{}
}

func newSrc(items []model.Item) *fakeSrc {
	return &fakeSrc{items: items, notified: map[string]bool{}, listed: make(chan struct{}, 100)}
}

func (f *fakeSrc) ListOpen() ([]model.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed <- struct{}{}
	if f.failFirst {
		f.failFirst = false
		return nil, errors.New("transient")
	}
	return f.items, nil
}
func (f *fakeSrc) WasNotified(k string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.notified[k], nil
}
func (f *fakeSrc) MarkNotified(k string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notified[k] = true
	return nil
}

func waitList(t *testing.T, src *fakeSrc) {
	t.Helper()
	select {
	case <-src.listed:
	case <-time.After(time.Second):
		t.Fatal("ListOpen not called")
	}
}

func TestSchedulerKickFires(t *testing.T) {
	due := t0
	src := newSrc([]model.Item{rem("a", due, due.Add(-24*time.Hour))})
	ch := make(chan []Alert, 10)
	s := New(src, func() []time.Duration { return nil }, func(a []Alert) { ch <- a },
		func() time.Time { return due })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	waitList(t, src) // initial plan
	select {
	case a := <-ch:
		if len(a) != 1 {
			t.Fatalf("got %v", a)
		}
	case <-time.After(time.Second):
		t.Fatal("fire not called")
	}
	s.Kick()
	waitList(t, src) // proves the Kick was processed
	select {
	case a := <-ch:
		t.Fatalf("unexpected second fire %v", a)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSchedulerRetriesAfterSourceError(t *testing.T) {
	due := t0
	src := newSrc([]model.Item{rem("a", due, due.Add(-24*time.Hour))})
	src.failFirst = true
	ch := make(chan []Alert, 10)
	s := New(src, func() []time.Duration { return nil }, func(a []Alert) { ch <- a },
		func() time.Time { return due })
	s.retryDelay = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("no fire after transient error")
	}
}

// The before-plan hook runs ahead of ListOpen on every plan (so an item it
// moves is planned at once), and the wake time it returns bounds the next
// plan; a hook error retries after retryDelay.
func TestSchedulerBeforePlan(t *testing.T) {
	src := newSrc(nil)
	var fired []Alert
	s := New(src, func() []time.Duration { return nil }, func(a []Alert) { fired = append(fired, a...) },
		func() time.Time { return t0 })
	calls := 0
	wake := t0.Add(time.Hour)
	var hookErr error
	s.SetBeforePlan(func(now time.Time) (time.Time, error) {
		calls++
		if !now.Equal(t0) {
			t.Errorf("hook now %v", now)
		}
		// Simulate moving a recurring item to an occurrence that is due now.
		src.mu.Lock()
		src.items = []model.Item{rem("r", t0, t0.Add(-24*time.Hour))}
		src.mu.Unlock()
		return wake, hookErr
	})
	if next := s.plan(); !next.Equal(wake) || calls != 1 {
		t.Fatalf("next=%v calls=%d", next, calls)
	}
	if len(fired) != 1 || fired[0].ItemID != "r" {
		t.Fatalf("fired %+v", fired)
	}

	src.mu.Lock()
	src.items = append(src.items, rem("b", t0.Add(30*time.Minute), t0.Add(-24*time.Hour)))
	src.mu.Unlock()
	s.SetBeforePlan(func(time.Time) (time.Time, error) { calls++; return wake, nil })
	if next := s.plan(); !next.Equal(t0.Add(30 * time.Minute)) {
		t.Fatalf("an earlier alert must win over the wake time: next=%v", next)
	}

	hookErr = errors.New("db locked")
	s.SetBeforePlan(func(time.Time) (time.Time, error) { return time.Time{}, hookErr })
	s.retryDelay = time.Minute
	src.mu.Lock()
	src.items = nil
	src.mu.Unlock()
	if next := s.plan(); !next.Equal(t0.Add(time.Minute)) {
		t.Fatalf("hook error: next=%v, want retry", next)
	}
}
