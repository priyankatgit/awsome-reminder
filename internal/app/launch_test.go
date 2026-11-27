package app

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

// M3: a second-instance request that arrives before the windows exist is
// held and run once they are ready, in order; later ones run at once.
func TestLaunchQueueHoldsUntilReady(t *testing.T) {
	var q launchQueue
	var mu sync.Mutex
	var got []string
	done := make(chan struct{}, 10)
	add := func(s string) func() {
		return func() {
			mu.Lock()
			got = append(got, s)
			mu.Unlock()
			done <- struct{}{}
		}
	}
	q.Do(add("capture"))
	q.Do(add("list"))
	select {
	case <-done:
		t.Fatal("ran before Ready")
	case <-time.After(30 * time.Millisecond):
	}
	q.Ready()
	for range 2 {
		<-done
	}
	q.Do(add("settings"))
	<-done
	q.Ready() // idempotent
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(got, []string{"capture", "list", "settings"}) {
		t.Fatalf("ran %v", got)
	}
}
