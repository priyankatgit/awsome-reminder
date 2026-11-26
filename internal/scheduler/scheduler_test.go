package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"awesome-reminder/internal/model"
)

type fakeSrc struct {
	mu       sync.Mutex
	items    []model.Item
	notified map[string]bool
}

func (f *fakeSrc) ListOpen() ([]model.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func TestSchedulerKickFires(t *testing.T) {
	due := t0
	src := &fakeSrc{items: []model.Item{rem("a", due, due.Add(-24*time.Hour))}, notified: map[string]bool{}}
	var mu sync.Mutex
	now := due
	ch := make(chan []Alert, 10)
	s := New(src, func() []time.Duration { return nil }, func(a []Alert) { ch <- a },
		func() time.Time { mu.Lock(); defer mu.Unlock(); return now })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	select {
	case a := <-ch:
		if len(a) != 1 {
			t.Fatalf("got %v", a)
		}
	case <-time.After(time.Second):
		t.Fatal("fire not called")
	}
	s.Kick()
	select {
	case a := <-ch:
		t.Fatalf("unexpected second fire %v", a)
	case <-time.After(200 * time.Millisecond):
	}
}
