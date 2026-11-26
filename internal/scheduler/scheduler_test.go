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
