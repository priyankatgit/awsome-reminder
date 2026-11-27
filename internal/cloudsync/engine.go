package cloudsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"awesome-reminder/internal/core"
	"awesome-reminder/internal/model"
	"awesome-reminder/internal/store"
)

// Meta keys the engine keeps in the local store.
const (
	metaWMAttachments = "sync_wm_attachments"
	metaWMItems       = "sync_wm_items"
	metaLastAt        = "sync_last_at"
)

const (
	pushBatch      = 100
	maxPushBatches = 10
	itemPage       = 200
	attachmentPage = 8 // attachments carry their bytes
	pullOverlap    = 2 * time.Minute
	maxSkew        = 2 * time.Minute
	offlineCapX    = 10 // the offline backoff caps at 10 polls (300 s)
	closeTimeout   = 5 * time.Second
)

// ErrStopped is returned by RunOnce after Stop.
var ErrStopped = errors.New("sync engine stopped")

// Engine pushes the local outbox and pulls remote changes. It runs one
// goroutine with one timer; RunOnce cycles never overlap. Core methods take
// their own locks, so no core lock is held across a network call.
type Engine struct {
	l Local
	s Syncer
	o Options

	kickCh chan struct{}
	nowCh  chan struct{}

	// cycleMu serializes cycles and Close. Fields below it are only touched
	// with it held.
	cycleMu    sync.Mutex
	needChecks bool // the clock and scope checks are still to run
	closed     bool

	mu       sync.Mutex // guards st, failures, started, stopped, cancel, done
	st       Status
	failures int
	started  bool
	stopped  bool
	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once
}

// NewEngine creates an engine. It does nothing until Start or RunOnce.
func NewEngine(l Local, s Syncer, o Options) *Engine {
	e := &Engine{
		l: l, s: s, o: o.withDefaults(),
		kickCh: make(chan struct{}, 1), nowCh: make(chan struct{}, 1),
		needChecks: true,
		st:         Status{State: StateOff, Warnings: []string{}, Host: o.Host},
	}
	if v, ok, err := l.SyncMeta(metaLastAt); err != nil {
		log.Printf("sync: read last sync time: %v", err)
	} else if ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			e.st.LastSyncAt = &t
		}
	}
	if n, err := l.OutboxCount(); err == nil {
		e.st.Pending = n
	}
	return e
}

// Start runs a manual-style first cycle (it shows syncing), then polls. It
// returns at once. Later calls, and calls after Stop, do nothing.
func (e *Engine) Start(ctx context.Context) {
	e.mu.Lock()
	if e.started || e.stopped {
		e.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	e.started, e.cancel, e.done = true, cancel, make(chan struct{})
	e.st.State = StateSyncing // so a Status() right after Start does not read off
	e.mu.Unlock()
	go e.loop(ctx)
}

// Stop cancels the loop, waits for the cycle in flight and closes the Syncer.
// It is safe to call more than once.
func (e *Engine) Stop() {
	e.stopOnce.Do(func() {
		e.mu.Lock()
		e.stopped = true
		cancel, done := e.cancel, e.done
		e.mu.Unlock()
		if cancel != nil {
			cancel()
			<-done
		}
		e.cycleMu.Lock()
		defer e.cycleMu.Unlock()
		e.closed = true
		ctx, cancelClose := context.WithTimeout(context.Background(), closeTimeout)
		defer cancelClose()
		if err := e.s.Close(ctx); err != nil {
			log.Printf("sync: close: %v", err)
		}
	})
}

// Kick asks for a cycle after the debounce, run only if something is waiting
// to be pushed by then.
func (e *Engine) Kick() {
	select {
	case e.kickCh <- struct{}{}:
	default:
	}
}

// SyncNow runs a cycle at once (it shows syncing) and resets the backoff.
func (e *Engine) SyncNow() {
	select {
	case e.nowCh <- struct{}{}:
	default:
	}
}

// Status returns the current status.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.statusLocked()
}

func (e *Engine) statusLocked() Status {
	s := e.st
	if s.LastSyncAt != nil {
		t := *s.LastSyncAt
		s.LastSyncAt = &t
	}
	s.Warnings = append([]string{}, s.Warnings...)
	return s
}

// nextDelay is how long to wait before the next cycle (Global Constraints
// cadence): poll after success, back off while offline, retry errors slowly.
func nextDelay(st State, failures int, o Options) time.Duration {
	o = o.withDefaults()
	switch st {
	case StateOffline:
		if failures < 1 {
			failures = 1
		}
		limit := offlineCapX * o.Poll
		d := o.Poll
		for i := 1; i < failures && d < limit; i++ {
			d *= 2
		}
		return min(d, limit)
	case StateError:
		return o.ErrorRetry
	}
	return o.Poll
}

func (e *Engine) delay() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return nextDelay(e.st.State, e.failures, e.o)
}

// loop is the engine's one goroutine. pollAt and kickAt share one timer.
func (e *Engine) loop(ctx context.Context) {
	defer close(e.done)
	e.runLogged(ctx, true)
	pollAt := time.Now().Add(e.delay())
	var kickAt time.Time
	timer := time.NewTimer(time.Until(pollAt))
	defer timer.Stop()
	reset := func() {
		at := pollAt
		if !kickAt.IsZero() && kickAt.Before(at) {
			at = kickAt
		}
		timer.Reset(time.Until(at))
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.nowCh:
			e.mu.Lock()
			e.failures = 0
			e.mu.Unlock()
			kickAt = time.Time{}
			e.runLogged(ctx, true)
			pollAt = time.Now().Add(e.delay())
		case <-e.kickCh:
			if kickAt.IsZero() {
				kickAt = time.Now().Add(e.o.Debounce)
			}
		case <-timer.C:
			now := time.Now()
			run := !pollAt.After(now)
			if !kickAt.IsZero() && !kickAt.After(now) {
				kickAt = time.Time{}
				if n, err := e.l.OutboxCount(); err != nil || n > 0 {
					run = true
				}
			}
			if run {
				e.runLogged(ctx, false)
				pollAt = time.Now().Add(e.delay())
			}
		}
		reset()
	}
}

func (e *Engine) runLogged(ctx context.Context, manual bool) {
	if err := e.RunOnce(ctx, manual); err != nil && ctx.Err() == nil {
		log.Printf("sync: cycle failed: %v", err)
	}
}

// RunOnce runs one full cycle: push the outbox, pull attachments, settings
// and items, then report the status. A manual cycle, or one with changes to
// push, shows syncing first; a quiet poll keeps the shown state.
func (e *Engine) RunOnce(ctx context.Context, manual bool) error {
	e.cycleMu.Lock()
	defer e.cycleMu.Unlock()
	if e.closed {
		return ErrStopped
	}
	pending, err := e.l.OutboxCount()
	e.mu.Lock()
	prev := e.st.State
	if err == nil {
		e.st.Pending = pending
	}
	show := manual || pending > 0
	if show {
		e.st.State = StateSyncing
	}
	snap := e.statusLocked()
	e.mu.Unlock()
	if show {
		e.emit(snap)
	}
	if err == nil {
		err = e.cycle(ctx)
	}
	e.finish(ctx, prev, err)
	return err
}

func (e *Engine) emit(s Status) {
	if e.o.OnStatus != nil {
		e.o.OnStatus(s)
	}
}

// finish records the outcome of a cycle and emits the status.
func (e *Engine) finish(ctx context.Context, prev State, err error) {
	now := e.o.Now()
	if err == nil {
		if serr := e.l.SetSyncMeta(metaLastAt, now.UTC().Format(time.RFC3339Nano)); serr != nil {
			log.Printf("sync: save last sync time: %v", serr)
		}
	}
	pending, perr := e.l.OutboxCount()
	e.mu.Lock()
	if perr == nil {
		e.st.Pending = pending
	}
	switch {
	case err == nil:
		e.st.State, e.st.Error, e.failures = StateSynced, "", 0
		e.st.LastSyncAt = &now
	case ctx.Err() != nil:
		// Stopped mid-cycle: not a failure, show what was shown before.
		e.st.State = prev
	default:
		e.failures++
		e.st.State, e.st.Error = e.s.Classify(err)
	}
	snap := e.statusLocked()
	e.mu.Unlock()
	e.emit(snap)
}

func (e *Engine) cycle(ctx context.Context) error {
	if err := e.push(ctx); err != nil {
		return err
	}
	if err := e.pullAttachments(ctx); err != nil {
		return err
	}
	// Settings before items: an item inserted here has its already-due
	// alerts marked as fired using the lead times in force, and those must
	// be the restored ones, or a restore pops a card for every overdue
	// reminder whose lead time differs from this device's default.
	if err := e.pullShared(ctx); err != nil {
		return err
	}
	if err := e.pullItems(ctx); err != nil {
		return err
	}
	if e.needChecks {
		e.needChecks = false
		e.check(ctx)
	}
	return nil
}

// push sends up to maxPushBatches batches of the outbox. An entry is acked
// only if it did not change while it was pushed; a changed one stays queued.
func (e *Engine) push(ctx context.Context) error {
	for range maxPushBatches {
		entries, err := e.l.Outbox(pushBatch)
		if err != nil {
			return err
		}
		for _, en := range entries {
			if err := e.pushOne(ctx, en); err != nil {
				return err
			}
		}
		if len(entries) < pushBatch {
			return nil
		}
	}
	return nil
}

func (e *Engine) pushOne(ctx context.Context, en store.OutboxEntry) error {
	var err error
	switch en.Kind {
	case store.OutboxItem:
		var it model.Item
		if it, err = e.l.Item(en.ID); err == nil {
			err = e.s.PushItem(ctx, it)
		}
	case store.OutboxAttachment:
		var a model.Attachment
		var data []byte
		if a, data, err = e.l.AttachmentForSync(en.ID); err == nil {
			err = e.s.PushAttachment(ctx, a, data)
		}
	case store.OutboxSettings:
		d := SharedDoc{}
		if d.Shared, d.UpdatedAt, d.UpdatedBy, err = e.l.SharedForSync(); err == nil {
			err = e.s.PushShared(ctx, d)
		}
	default:
		log.Printf("sync: dropping outbox entry of unknown kind %q", en.Kind)
	}
	switch {
	case errors.Is(err, core.ErrAttachmentFileMissing), errors.Is(err, store.ErrNotFound):
		log.Printf("sync: dropping %s %s: %v", en.Kind, en.ID, err)
	case err != nil:
		return fmt.Errorf("push %s %s: %w", en.Kind, en.ID, err)
	}
	return e.l.Ack(en)
}

func (e *Engine) loadWatermark(key string) Watermark {
	v, ok, err := e.l.SyncMeta(key)
	if err != nil || !ok {
		if err != nil {
			log.Printf("sync: read %s: %v", key, err)
		}
		return Watermark{}
	}
	var wm Watermark
	if err := json.Unmarshal([]byte(v), &wm); err != nil {
		log.Printf("sync: bad %s, pulling from the start: %v", key, err)
		return Watermark{}
	}
	return wm
}

func (e *Engine) saveWatermark(key string, saved, last Watermark) error {
	if !saved.Less(last) {
		return nil
	}
	b, err := json.Marshal(last)
	if err != nil {
		return err
	}
	return e.l.SetSyncMeta(key, string(b))
}

// overlap is where a pull starts: 2 min before the saved watermark, so a
// write whose server stamp landed just before it is not missed.
func overlap(saved Watermark) Watermark {
	if saved.T.IsZero() {
		return Watermark{}
	}
	return Watermark{T: saved.T.Add(-pullOverlap)}
}

// errStuck guards against a backend whose watermark does not advance, which
// would otherwise page forever.
func errStuck(after, next Watermark) error {
	return fmt.Errorf("pull did not advance past %v/%s (got %v/%s)", after.T, after.ID, next.T, next.ID)
}

func (e *Engine) pullAttachments(ctx context.Context) error {
	saved := e.loadWatermark(metaWMAttachments)
	after, done := overlap(saved), saved
	for {
		page, next, err := e.s.PullAttachments(ctx, after, attachmentPage)
		if err != nil {
			return errors.Join(err, e.saveWatermark(metaWMAttachments, saved, done))
		}
		for _, d := range page {
			if _, err := e.l.ApplyRemoteAttachment(d.Attachment, d.Data); err != nil {
				if !errors.Is(err, core.ErrInvalidRemote) {
					// Keep the progress of earlier pages only, so this
					// document is pulled again.
					return errors.Join(err, e.saveWatermark(metaWMAttachments, saved, done))
				}
				log.Printf("sync: skipping remote attachment: %v", err)
			}
		}
		if len(page) > 0 {
			if !after.Less(next) {
				return errStuck(after, next)
			}
			after = next
			if done.Less(next) {
				done = next
			}
		}
		if len(page) < attachmentPage {
			return e.saveWatermark(metaWMAttachments, saved, done)
		}
	}
}

func (e *Engine) pullItems(ctx context.Context) error {
	saved := e.loadWatermark(metaWMItems)
	after, done := overlap(saved), saved
	var all []core.Applied
	defer func() {
		if len(all) > 0 && e.o.OnApplied != nil {
			e.o.OnApplied(all)
		}
	}()
	for {
		page, next, err := e.s.PullItems(ctx, after, itemPage)
		if err != nil {
			return errors.Join(err, e.saveWatermark(metaWMItems, saved, done))
		}
		// Invalid items are skipped inside; an error is a store failure.
		applied, err := e.l.ApplyRemoteItems(page)
		all = append(all, applied...)
		if err != nil {
			return errors.Join(err, e.saveWatermark(metaWMItems, saved, done))
		}
		if len(page) > 0 {
			if !after.Less(next) {
				return errStuck(after, next)
			}
			after = next
			if done.Less(next) {
				done = next
			}
		}
		if len(page) < itemPage {
			return e.saveWatermark(metaWMItems, saved, done)
		}
	}
}

func (e *Engine) pullShared(ctx context.Context) error {
	d, err := e.s.PullShared(ctx)
	if err != nil || d == nil {
		return err
	}
	_, err = e.l.ApplyRemoteShared(d.Shared, d.UpdatedAt, d.UpdatedBy)
	return err
}

// check runs the once-per-start clock and scope checks. Their failures are
// logged, never shown.
func (e *Engine) check(ctx context.Context) {
	warnings := []string{}
	if srv, err := e.s.ServerTime(ctx); err != nil {
		log.Printf("sync: server time: %v", err)
	} else if skew := srv.Sub(e.o.Now()).Abs(); skew > maxSkew {
		warnings = append(warnings, fmt.Sprintf("This computer's clock is %d min off. Sync keeps the most recent edit by clock time, so fix the clock to avoid losing edits.", int(math.Round(skew.Minutes()))))
	}
	if broad, err := e.s.ScopeTooBroad(ctx); err != nil {
		log.Printf("sync: scope check: %v", err)
	} else if broad {
		warnings = append(warnings, ScopeWarning)
	}
	e.mu.Lock()
	e.st.Warnings = warnings
	e.mu.Unlock()
}
