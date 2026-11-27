package memsync_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"awesome-reminder/internal/cloudsync"
	"awesome-reminder/internal/cloudsync/cloudsynctest"
	"awesome-reminder/internal/cloudsync/memsync"
	"awesome-reminder/internal/model"
)

func TestConformance(t *testing.T) {
	cloudsynctest.Run(t, func(t *testing.T) cloudsync.Syncer {
		s := memsync.New()
		t.Cleanup(func() { s.Close(context.Background()) })
		return s
	})
}

func TestServerClockStrictlyIncreases(t *testing.T) {
	s := memsync.New()
	frozen := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time { return frozen })
	ctx := context.Background()
	for i := range 3 {
		it := model.Item{ID: fmt.Sprintf("01JD00000000000000000000%02d", i), Kind: model.KindTodo, Text: "x",
			Tags: []string{"inbox"}, Status: model.StatusOpen, UpdatedAt: frozen, UpdatedBy: "A"}
		if err := s.PushItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	// Each write gets its own stamp, so a page boundary never splits a tie.
	var marks []cloudsync.Watermark
	wm := cloudsync.Watermark{}
	for range 3 {
		_, next, err := s.PullItems(ctx, wm, 1)
		if err != nil {
			t.Fatal(err)
		}
		marks = append(marks, next)
		wm = next
	}
	for i := 1; i < len(marks); i++ {
		if !marks[i-1].T.Before(marks[i].T) {
			t.Errorf("stamps not strictly increasing: %v then %v", marks[i-1].T, marks[i].T)
		}
	}
}

func TestFailNextAndClassify(t *testing.T) {
	s := memsync.New()
	ctx := context.Background()
	s.FailNext(memsync.ErrNetwork, 2)
	for i := range 2 {
		if _, err := s.ServerTime(ctx); !errors.Is(err, memsync.ErrNetwork) {
			t.Fatalf("call %d: want ErrNetwork, got %v", i, err)
		}
	}
	if _, err := s.ServerTime(ctx); err != nil {
		t.Fatalf("third call: %v", err)
	}
	if st, text := s.Classify(fmt.Errorf("push: %w", memsync.ErrNetwork)); st != cloudsync.StateOffline || text != "No connection to Atlas." {
		t.Errorf("network: %v %q", st, text)
	}
	if st, text := s.Classify(errors.New("boom")); st != cloudsync.StateError || text != "boom" {
		t.Errorf("other: %v %q", st, text)
	}
}

func TestScopeAndClose(t *testing.T) {
	s := memsync.New()
	ctx := context.Background()
	if broad, err := s.ScopeTooBroad(ctx); broad || err != nil {
		t.Errorf("default: %v %v", broad, err)
	}
	boom := errors.New("boom")
	s.SetScopeTooBroad(true, boom)
	if broad, err := s.ScopeTooBroad(ctx); !broad || err != boom {
		t.Errorf("set: %v %v", broad, err)
	}
	if s.Closed() {
		t.Error("closed too early")
	}
	s.Close(ctx)
	if !s.Closed() {
		t.Error("not closed")
	}
	if _, err := s.ServerTime(ctx); err == nil {
		t.Error("use after close must fail")
	}
}

func TestPutRawIsPulledAsIs(t *testing.T) {
	s := memsync.New()
	ctx := context.Background()
	s.PutRaw(map[string]any{"_id": "garbage", "text": 42, "tags": "not a list", "updated_at": "yesterday"})
	got, wm, err := s.PullItems(ctx, cloudsync.Watermark{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "garbage" || got[0].Text != "" || got[0].Tags != nil || wm.ID != "garbage" {
		t.Errorf("raw doc: %+v %+v", got, wm)
	}
}
