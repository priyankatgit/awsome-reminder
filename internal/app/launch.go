package app

import "sync"

// launchQueue runs second-instance requests (--capture, --list, ...) off the
// caller's goroutine. Wails may deliver one before Run has created the
// windows; until Ready those are held and then run in arrival order.
type launchQueue struct {
	mu      sync.Mutex
	ready   bool
	pending []func()
}

// Do runs f on its own goroutine now if the windows are ready, else when
// Ready is called.
func (q *launchQueue) Do(f func()) {
	q.mu.Lock()
	if !q.ready {
		q.pending = append(q.pending, f)
		q.mu.Unlock()
		return
	}
	q.mu.Unlock()
	go f()
}

// Ready marks the windows usable and runs the held requests, in order, on
// one goroutine. Later calls do nothing.
func (q *launchQueue) Ready() {
	q.mu.Lock()
	if q.ready {
		q.mu.Unlock()
		return
	}
	q.ready = true
	held := q.pending
	q.pending = nil
	q.mu.Unlock()
	if len(held) > 0 {
		go func() {
			for _, f := range held {
				f()
			}
		}()
	}
}
