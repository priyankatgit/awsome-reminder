package push

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/store"
)

const (
	reconcileEvery = 15 * time.Minute // periodic run (also the 429 back-off)
	wakeCheckEvery = time.Minute      // wall-clock jump check (wake from sleep)
	jumpTolerance  = 5 * time.Second
	retryMin       = 30 * time.Second
	retryMax       = 15 * time.Minute

	quotaLimit   = 200           // requests per local day before the guard
	quotaHorizon = 6 * time.Hour // above the limit, only pushes due this soon

	// keepAfter: log entries outlive delivery this long, so handling the item
	// can still cancel the phone-snoozed "<seq>s".
	keepAfter = time.Hour
	// windowFloor matches Desired's lower bound: a push due sooner is never
	// desired but must not be cancelled for that.
	windowFloor = 15 * time.Second
	// dismissKeep is how long a dismissal of a push not in the desired set is
	// remembered (the end of the publish window).
	dismissKeep = 71 * time.Hour
)

// Status messages (user-facing).
const (
	msgOffline = "Offline · will retry"
	msgQuota   = "Daily limit nearly reached · only pushes due in the next 6 hours are sent"
	msgAuth    = "Error: %d — this computer needs an access token"
)

// Log is the push log; *store.Store satisfies it.
type Log interface {
	PushLog() ([]store.PushEntry, error)
	PutPush(store.PushEntry) error
	DeletePush(string) error
	PrunePush(time.Time) error
	ClearPush() error
}

// Deps is what the Reconciler reads. Config returns false when push is off;
// the Config it returns then may still carry the token, used to delete what
// is pending.
type Deps struct {
	Items    func() ([]model.Item, error) // open items
	Rules    func() scheduler.Rules
	Config   func() (Config, bool)
	Log      Log
	Now      func() time.Time
	HTTP     *http.Client
	OnStatus func(Status) // called on its goroutines when the status changes
}

// Status is shown in Settings › Phone push.
type Status struct {
	State     string    `json:"state"`     // "off" | "ok" | "offline" | "auth" | "quota" | "error"
	Scheduled int       `json:"scheduled"` // entries pending in the log
	LastSent  time.Time `json:"lastSent"`
	Message   string    `json:"message"` // user-facing line; "" when ok or off
	SentToday int       `json:"sentToday"`
}

// Reconciler keeps the ntfy server's pending messages equal to the desired
// set. All its network I/O runs on its own goroutine (SendTest on the
// caller's).
type Reconciler struct {
	d       Deps
	kick    chan struct{}
	dismiss chan struct{}
	wg      sync.WaitGroup

	// Intervals; tests shorten them before Start.
	every, check, backoffMin, backoffMax time.Duration

	mu       sync.Mutex
	status   Status
	queue    []string // seqs to dismiss
	quotaDay string   // local date the count belongs to
	sent     int      // requests today
	lastSent time.Time

	// Loop goroutine (or the tests' synchronous calls) only.
	backoff     time.Duration
	authStopped bool   // 401/403: wait for a Kick
	authErr     error  // the error that stopped it
	idleOff     bool   // off and cleaned up: nothing to do until a Kick
	last        result // outcome behind the current status
}

// NewReconciler creates a Reconciler; Start runs it.
func NewReconciler(d Deps) *Reconciler {
	return &Reconciler{
		d: d, kick: make(chan struct{}, 1), dismiss: make(chan struct{}, 1),
		every: reconcileEvery, check: wakeCheckEvery, backoffMin: retryMin, backoffMax: retryMax,
		status: Status{State: "off"},
	}
}

// Kick requests a run. It never blocks; concurrent kicks coalesce. A Kick
// also lifts an access-token stop.
func (r *Reconciler) Kick() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Dismiss cancels the pushes for seqs (and their phone-snoozed "<seq>s")
// and records them so they are not published again. Asynchronous; it never
// blocks. Works for seqs another computer scheduled too.
func (r *Reconciler) Dismiss(seqs []string) {
	if len(seqs) == 0 {
		return
	}
	r.mu.Lock()
	r.queue = append(r.queue, seqs...)
	r.mu.Unlock()
	select {
	case r.dismiss <- struct{}{}:
	default:
	}
}

// Status returns the current status.
func (r *Reconciler) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// SendTest publishes an immediate test push with the current settings. It
// counts against the daily quota.
func (r *Reconciler) SendTest(ctx context.Context) error {
	cfg, on := r.d.Config()
	if !on {
		return errors.New("Phone push is off")
	}
	r.rollDay(r.d.Now(), cfg.Loc)
	err := r.client(cfg.Server, cfg.Topic, cfg.Token).SendNow(ctx, "Awesome Reminder", "Test push: phone notifications work.")
	r.count(err, 1)
	r.mu.Lock()
	r.status.SentToday = r.sent
	st := r.status
	r.mu.Unlock()
	r.notify(st)
	return err
}

// Start launches the goroutine; it runs at once, then on Kick, Dismiss,
// every 15 min, after a wall-clock jump (wake from sleep) and on retry
// timers, until ctx is done.
func (r *Reconciler) Start(ctx context.Context) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.loop(ctx)
	}()
}

// Wait blocks until the goroutine started by Start has exited. It returns at
// once if Start was never called.
func (r *Reconciler) Wait() { r.wg.Wait() }

func (r *Reconciler) loop(ctx context.Context) {
	tick := time.NewTicker(r.every)
	defer tick.Stop()
	check := time.NewTicker(r.check)
	defer check.Stop()
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer retry.Stop()
	lastWall := time.Now().Round(0) // monotonic reading stripped

	after := func(d time.Duration) {
		if d > 0 {
			retry.Stop()
			retry.Reset(d)
		}
	}
	after(r.cycle(ctx, true))
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
			after(r.cycle(ctx, true))
		case <-r.dismiss:
			after(r.dismissNow(ctx))
		case <-tick.C:
			after(r.cycle(ctx, false))
		case <-retry.C:
			after(r.cycle(ctx, false))
		case <-check.C:
			wall := time.Now().Round(0)
			jumped := wallJumped(lastWall, wall, r.check)
			lastWall = wall
			if jumped {
				after(r.cycle(ctx, false))
			}
		}
	}
}

// wallJumped reports whether the wall clock moved by something other than
// expected beyond tolerance (sleep/wake or a clock change). Both times must
// have their monotonic readings stripped.
func wallJumped(last, wall time.Time, expected time.Duration) bool {
	d := wall.Sub(last) - expected
	return d > jumpTolerance || d < -jumpTolerance
}

// cycle runs a reconcile unless stopped (an access-token error, or off and
// already cleaned up) and not kicked. It returns the retry delay (0: none).
func (r *Reconciler) cycle(ctx context.Context, kicked bool) time.Duration {
	if kicked {
		r.authStopped, r.authErr = false, nil
	} else if r.authStopped || r.idleOff {
		return 0
	}
	return r.runOnce(ctx)
}

// runOnce reconciles and settles the status; it returns the retry delay.
func (r *Reconciler) runOnce(ctx context.Context) time.Duration {
	res := r.reconcile(ctx)
	if ctx.Err() != nil {
		return 0
	}
	return r.settle(res)
}

// dismissNow processes queued dismissals; it returns the retry delay. A
// successful dismissal keeps the last run's outcome (status and back-off),
// so it never hides a failed reconcile.
func (r *Reconciler) dismissNow(ctx context.Context) time.Duration {
	res := r.processDismissals(ctx)
	if ctx.Err() != nil {
		return 0
	}
	if res.err == nil && !res.off {
		r.setStatus(r.last)
		return 0
	}
	return r.settle(res)
}

type result struct {
	off bool
	err error
}

// settle updates back-off and status after a run; it returns the retry
// delay (TempError only).
func (r *Reconciler) settle(res result) time.Duration {
	r.last = res
	r.idleOff = res.off && res.err == nil
	var retry time.Duration
	var te *TempError
	switch {
	case errors.As(res.err, &te):
		if r.backoff == 0 {
			r.backoff = r.backoffMin
		}
		retry = r.backoff
		r.backoff = min(r.backoff*2, r.backoffMax)
	case errors.Is(res.err, ErrAuth):
		r.authStopped, r.authErr = true, res.err
		r.backoff = 0
	case res.err == nil:
		r.backoff = 0
	}
	r.setStatus(res)
	return retry
}

func (r *Reconciler) setStatus(res result) {
	now := r.d.Now()
	scheduled := 0
	if entries, err := r.d.Log.PushLog(); err == nil {
		for _, e := range entries {
			if e.State == store.PushScheduled && e.DeliverAt.After(now) {
				scheduled++
			}
		}
	}
	r.mu.Lock()
	st := Status{Scheduled: scheduled, LastSent: r.lastSent, SentToday: r.sent}
	var te *TempError
	switch {
	case res.off:
		st.State, st.Scheduled = "off", 0
	case res.err == nil && r.sent >= quotaLimit:
		st.State, st.Message = "quota", msgQuota
	case res.err == nil:
		st.State = "ok"
	case errors.As(res.err, &te):
		st.State, st.Message = "offline", msgOffline
	case errors.Is(res.err, ErrAuth):
		code := 401
		if strings.Contains(res.err.Error(), "HTTP 403") {
			code = 403
		}
		st.State, st.Message = "auth", fmt.Sprintf(msgAuth, code)
	case errors.Is(res.err, ErrRateLimited):
		st.State, st.Message = "error", "Error: rate limited by the server · will retry"
	default:
		st.State, st.Message = "error", "Error: "+shortReason(res.err)
	}
	changed := st != r.status
	r.status = st
	r.mu.Unlock()
	if changed {
		r.notify(st)
	}
}

func (r *Reconciler) notify(st Status) {
	if r.d.OnStatus != nil {
		r.d.OnStatus(st)
	}
}

// shortReason is an error's text without the package prefix, on one line and
// short. Client errors never hold the token, the topic or bodies.
func shortReason(err error) string {
	return clip(oneLine(strings.TrimPrefix(err.Error(), "ntfy: ")), 120)
}

// reconcile is one run (see the package spec "Reconciler"). A failed
// request leaves the log as it was for that entry. TempError, 429, 401/403
// (and a store error) stop the run; any other failure skips just that entry
// and is reported once the rest is done.
func (r *Reconciler) reconcile(ctx context.Context) result {
	now := r.d.Now()
	cfg, on := r.d.Config()
	r.rollDay(now, cfg.Loc)
	entries, err := r.d.Log.PushLog()
	if err != nil {
		return result{off: !on, err: fmt.Errorf("reading the push log: %w", err)}
	}
	r.initLastSent(entries)
	if !on {
		return result{off: true, err: r.turnOff(ctx, cfg, entries, now)}
	}
	items, err := r.d.Items()
	if err != nil {
		return result{err: fmt.Errorf("reading items: %w", err)}
	}
	rules := r.d.Rules()
	desired := Desired(items, rules, cfg, now)
	// Occurrences still valid an hour back: a delivered (or about to be)
	// push for one of these is left alone.
	recent := Desired(items, rules, cfg, now.Add(-keepAfter))
	cl := r.client(cfg.Server, cfg.Topic, cfg.Token)
	var skipped error // first per-entry failure
	skip := func(err error) bool {
		if stopsRun(ctx, err) {
			return false
		}
		if skipped == nil {
			skipped = err
		}
		return true
	}

	logged := map[string]store.PushEntry{}
	for _, e := range entries {
		if e.Server == cfg.Server && e.Topic == cfg.Topic {
			logged[e.Seq] = e
			continue
		}
		// Server or topic changed: cancel on the old one (best effort).
		if r.pendingOnServer(e, now) {
			old := r.client(e.Server, e.Topic, cfg.Token)
			if err := r.deleteBoth(ctx, old, e.Seq); err != nil && retryable(ctx, err) {
				return result{err: err}
			}
		}
		if e.State == store.PushDismissed {
			// Still handled: keep it on the new server/topic, where the
			// deletes are (re)sent below, so it is never republished there.
			e.Server, e.Topic, e.SentAt = cfg.Server, cfg.Topic, time.Time{}
			if err := r.d.Log.PutPush(e); err != nil {
				return result{err: err}
			}
			logged[e.Seq] = e
			continue
		}
		if err := r.d.Log.DeletePush(e.Seq); err != nil {
			return result{err: err}
		}
	}

	for _, le := range entries {
		e, ok := logged[le.Seq]
		if !ok {
			continue
		}
		switch e.State {
		case store.PushDismissed:
			if !e.SentAt.IsZero() {
				continue
			}
			// A dismissal whose deletes haven't gone through yet.
			if err := r.deleteBoth(ctx, cl, e.Seq); err != nil {
				if skip(err) {
					continue
				}
				return result{err: err}
			}
			e.SentAt = now
			if err := r.d.Log.PutPush(e); err != nil {
				return result{err: err}
			}
		case store.PushScheduled:
			_, want := desired[e.Seq]
			if !want && !e.DeliverAt.After(now.Add(windowFloor)) {
				_, want = recent[e.Seq]
			}
			if want || !e.DeliverAt.After(now.Add(-keepAfter)) {
				continue // wanted, or old: PrunePush drops it
			}
			if err := r.deleteBoth(ctx, cl, e.Seq); err != nil {
				if skip(err) {
					continue
				}
				return result{err: err}
			}
			if err := r.d.Log.DeletePush(e.Seq); err != nil {
				return result{err: err}
			}
			delete(logged, e.Seq)
		}
	}

	msgs := make([]Message, 0, len(desired))
	for _, m := range desired {
		msgs = append(msgs, m)
	}
	sort.Slice(msgs, func(i, j int) bool {
		if !msgs[i].DeliverAt.Equal(msgs[j].DeliverAt) {
			return msgs[i].DeliverAt.Before(msgs[j].DeliverAt)
		}
		return msgs[i].Seq < msgs[j].Seq
	})
	for _, m := range msgs {
		e, ok := logged[m.Seq]
		h := Hash(m)
		if ok && (e.State == store.PushDismissed || e.Hash == h) {
			continue
		}
		if r.overQuota() && m.DeliverAt.After(now.Add(quotaHorizon)) {
			continue
		}
		err := cl.Schedule(ctx, m)
		r.count(err, 2)
		if err != nil {
			if skip(err) {
				continue
			}
			return result{err: err}
		}
		if err := r.d.Log.PutPush(store.PushEntry{Seq: m.Seq, Server: cfg.Server, Topic: cfg.Topic, Hash: h,
			DeliverAt: m.DeliverAt, SentAt: now, State: store.PushScheduled}); err != nil {
			return result{err: err}
		}
		r.mu.Lock()
		r.lastSent = now
		r.mu.Unlock()
	}
	if err := r.d.Log.PrunePush(now.Add(-keepAfter)); err != nil {
		return result{err: err}
	}
	return result{err: skipped}
}

// turnOff cancels everything pending and clears the log. A retryable failure
// keeps the entries not yet cancelled for the next run.
func (r *Reconciler) turnOff(ctx context.Context, cfg Config, entries []store.PushEntry, now time.Time) error {
	if len(entries) == 0 {
		return nil // nothing to do: no store write per Kick while off
	}
	for _, e := range entries {
		if r.pendingOnServer(e, now) {
			err := r.deleteBoth(ctx, r.client(e.Server, e.Topic, cfg.Token), e.Seq)
			if err != nil && retryable(ctx, err) {
				return err
			}
		}
		if err := r.d.Log.DeletePush(e.Seq); err != nil {
			return err
		}
	}
	return r.d.Log.ClearPush()
}

// pendingOnServer reports whether e may still have something on its server
// to cancel: a push not long delivered (its snooze twin), or a dismissal
// whose deletes haven't gone through.
func (r *Reconciler) pendingOnServer(e store.PushEntry, now time.Time) bool {
	if e.State == store.PushDismissed {
		return e.SentAt.IsZero()
	}
	return e.DeliverAt.After(now.Add(-keepAfter))
}

// stopsRun: a failure that ends the run (also ErrAuth: every other request
// would fail the same way).
func stopsRun(ctx context.Context, err error) bool {
	return retryable(ctx, err) || errors.Is(err, ErrAuth)
}

// retryable: worth stopping the run for (the entry is kept and retried).
// Other failures of a best-effort delete are dropped.
func retryable(ctx context.Context, err error) bool {
	var te *TempError
	return ctx.Err() != nil || errors.As(err, &te) || errors.Is(err, ErrRateLimited)
}

// processDismissals records queued dismissals, then cancels them on the
// server. The record comes first so the push is never republished; a failed
// cancel is retried by the next run (SentAt stays zero until it succeeds).
func (r *Reconciler) processDismissals(ctx context.Context) result {
	r.mu.Lock()
	seqs := r.queue
	r.queue = nil
	r.mu.Unlock()
	cfg, on := r.d.Config()
	if !on {
		return result{off: true}
	}
	now := r.d.Now()
	r.rollDay(now, cfg.Loc)
	var desired map[string]Message
	if items, err := r.d.Items(); err == nil {
		desired = Desired(items, r.d.Rules(), cfg, now)
	}
	var todo []store.PushEntry
	seen := map[string]bool{}
	for _, seq := range seqs {
		if seq == "" || seen[seq] {
			continue
		}
		seen[seq] = true
		deliver := now.Add(dismissKeep)
		if m, ok := desired[seq]; ok {
			deliver = m.DeliverAt
		}
		e := store.PushEntry{Seq: seq, Server: cfg.Server, Topic: cfg.Topic, DeliverAt: deliver, State: store.PushDismissed}
		if err := r.d.Log.PutPush(e); err != nil {
			return result{err: err}
		}
		todo = append(todo, e)
	}
	if r.authStopped {
		return result{err: r.authErr}
	}
	cl := r.client(cfg.Server, cfg.Topic, cfg.Token)
	for _, e := range todo {
		if err := r.deleteBoth(ctx, cl, e.Seq); err != nil {
			return result{err: err}
		}
		e.SentAt = now
		if err := r.d.Log.PutPush(e); err != nil {
			return result{err: err}
		}
	}
	return result{}
}

func (r *Reconciler) client(server, topic, token string) Client {
	return Client{Server: server, Topic: topic, Token: token, HTTP: r.d.HTTP}
}

// deleteBoth cancels seq and its phone-snoozed twin seq+"s".
func (r *Reconciler) deleteBoth(ctx context.Context, c Client, seq string) error {
	for _, s := range []string{seq, seq + "s"} {
		err := c.Delete(ctx, s)
		r.count(err, 1)
		if err != nil {
			return err
		}
	}
	return nil
}

// count adds n requests to today's quota unless err shows they never reached
// the server.
func (r *Reconciler) count(err error, n int) {
	var te *TempError
	if errors.As(err, &te) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	r.mu.Lock()
	r.sent += n
	r.mu.Unlock()
}

// rollDay resets the count at local midnight (loc nil: time.Local).
func (r *Reconciler) rollDay(now time.Time, loc *time.Location) {
	if loc == nil {
		loc = time.Local
	}
	day := now.In(loc).Format("2006-01-02")
	r.mu.Lock()
	if day != r.quotaDay {
		r.quotaDay, r.sent = day, 0
	}
	r.mu.Unlock()
}

func (r *Reconciler) overQuota() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent >= quotaLimit
}

// initLastSent seeds LastSent from the log after a restart.
func (r *Reconciler) initLastSent(entries []store.PushEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.lastSent.IsZero() {
		return
	}
	for _, e := range entries {
		if e.State == store.PushScheduled && e.SentAt.After(r.lastSent) {
			r.lastSent = e.SentAt
		}
	}
}
