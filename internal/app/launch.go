package app

import (
	"slices"
	"sync"
)

// launchAction is what a command-line flag asks for, on a first launch or
// from a second instance.
type launchAction int

const (
	launchNone     launchAction = iota // no flag
	launchCapture                      // --capture: the capture box
	launchReply                        // --reply: the capture box in reply mode
	launchSettings                     // --settings
	launchList                         // --list
)

func (a launchAction) String() string {
	switch a {
	case launchCapture:
		return captureFlag
	case launchReply:
		return replyFlag
	case launchSettings:
		return settingsFlag
	case launchList:
		return listFlag
	}
	return "none"
}

// parseLaunch picks the action from the arguments (without the program
// name). When several flags are given the first of --capture, --reply,
// --settings, --list wins.
func parseLaunch(args []string) launchAction {
	switch {
	case slices.Contains(args, captureFlag):
		return launchCapture
	case slices.Contains(args, replyFlag):
		return launchReply
	case slices.Contains(args, settingsFlag):
		return launchSettings
	case slices.Contains(args, listFlag):
		return launchList
	}
	return launchNone
}

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
