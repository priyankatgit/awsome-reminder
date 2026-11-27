package app

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"awesome-reminder/internal/grab"
	"awesome-reminder/internal/hotkey"
	"awesome-reminder/internal/reply"
)

// replyStopTimeout bounds how long shutdown waits for a reply press in
// flight (a grab takes well under a second; on macOS one could be stuck
// waiting for the main queue, which has stopped by then).
const replyStopTimeout = 2 * time.Second

// flagKeys are the keys a --reply launch waits to be let go before the copy
// is injected: whatever modifiers the desktop shortcut that ran it held.
var flagKeys = []grab.Key{grab.KeyControl, grab.KeyAlt, grab.KeyShift}

// replyOpener runs the reply hotkey: it grabs the selected text from the
// foreground app, guesses who is waiting on a reply and opens the capture box
// in reply mode with that pre-fill.
//
// Press runs on a hotkey goroutine, never the Wails main thread (grab.Grab
// waits for the main queue on macOS and would deadlock there). It does
// nothing before Start (ApplicationStarted), once the app context is
// cancelled, or after Stop.
type replyOpener struct {
	grab    func(ctx context.Context, keys []grab.Key) grab.Result
	trusted func() bool // grab.AccessibilityTrusted
	// isOpen reports whether the capture box is already showing in reply
	// mode; a press then does nothing, so a double press doesn't grab twice.
	isOpen func() bool
	show   func(ReplyPrefill) // wailsWindows.ShowReply

	mu      sync.Mutex
	ctx     context.Context // nil until Start
	stopped bool
	running sync.WaitGroup // presses in flight
	busy    atomic.Bool    // one press at a time (the hotkey and --reply)
}

// Start lets presses through; ctx is the app's context, and presses stop
// once it is cancelled.
func (o *replyOpener) Start(ctx context.Context) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ctx = ctx
}

// Stop ignores later presses and waits up to timeout for one in flight, so
// its clipboard restore has been handed to grab.Wait before shutdown calls
// it.
func (o *replyOpener) Stop(timeout time.Duration) {
	o.mu.Lock()
	o.stopped = true
	o.mu.Unlock()
	done := make(chan struct{})
	go func() { o.running.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		log.Printf("reply: a grab is still running at shutdown")
	}
}

// Press grabs with the hotkey's keys (released before the copy) and opens
// the capture box in reply mode.
func (o *replyOpener) Press(keys []grab.Key) {
	o.mu.Lock()
	ctx := o.ctx
	if ctx == nil || ctx.Err() != nil || o.stopped {
		o.mu.Unlock()
		return
	}
	o.running.Add(1)
	o.mu.Unlock()
	defer o.running.Done()
	if !o.busy.CompareAndSwap(false, true) {
		return
	}
	defer o.busy.Store(false)
	if o.isOpen() {
		log.Printf("reply: already open; not grabbing again")
		return
	}
	res := o.grab(ctx, keys)
	if ctx.Err() != nil {
		return // shutting down: the windows are being torn down
	}
	p := replyPrefill(res, !o.trusted())
	log.Printf("reply: grabbed=%v source=%q person=%v needsAccess=%v", p.Grabbed, p.Source, p.OwedTo != "", p.NeedsAccess)
	o.show(p)
}

// replyPrefill builds the capture box's reply pre-fill from a grab.
func replyPrefill(res grab.Result, needsAccess bool) ReplyPrefill {
	m := reply.Guess(res.Text, res.Process, res.Title)
	return ReplyPrefill{
		Line:        reply.Line(m),
		OwedTo:      m.OwedTo,
		Source:      m.Source,
		Quote:       m.Quote,
		Grabbed:     res.Grabbed,
		NeedsAccess: needsAccess,
	}
}

// replyKeys are all of the hotkey's keys, modifiers and main key, as the
// virtual-key codes grab.Grab waits to be let go.
func replyKeys(c hotkey.Combo) []grab.Key {
	var keys []grab.Key
	for _, k := range c.VirtualKeys() {
		keys = append(keys, grab.Key(k))
	}
	return keys
}

// replyRegistrar wraps reg so each registration's press calls press with
// that combo's keys (the combo actually registered, which a failed Set
// leaves as the previous one).
func replyRegistrar(reg hotkey.Registrar, press func(keys []grab.Key)) hotkey.Registrar {
	return func(c hotkey.Combo, _ func()) (func(), error) {
		keys := replyKeys(c)
		return reg(c, func() { press(keys) })
	}
}
