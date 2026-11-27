package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"awesome-reminder/internal/cloudsync"
	"awesome-reminder/internal/cloudsync/atlas"
	"awesome-reminder/internal/core"
	"awesome-reminder/internal/secret"
)

const (
	// metaSyncTarget is the cluster and database the watermarks belong to
	// ("<host>/awesome_reminder").
	metaSyncTarget = "sync_target"
	// syncConnectTimeout bounds a Connect (validate, SRV lookup, ping and
	// indexes; the driver's own timeouts are 10 s each).
	syncConnectTimeout = 30 * time.Second
	// syncInfoTimeout bounds the attachment-size query behind Info.
	syncInfoTimeout  = 10 * time.Second
	syncCloseTimeout = 5 * time.Second
)

// errSyncStopped is returned by Connect after Stop (the app is quitting).
var errSyncStopped = errors.New("Sync is shutting down.")

// SyncInfo is the sync section of Settings.
type SyncInfo struct {
	Connected       bool   `json:"connected"`       // a connection string is saved
	Host            string `json:"host"`            // its host, "" when none
	SecretStore     string `json:"secretStore"`     // secret.Store.Kind()
	AttachmentBytes int64  `json:"attachmentBytes"` // -1 when unknown (offline / not connected)
}

// syncDeps are the controller's collaborators. Open, Host and Classify are
// the Atlas functions in the app and fakes in tests.
type syncDeps struct {
	Core   *core.Core
	Secret secret.Store
	// Open validates and pings uri and returns its backend.
	Open func(ctx context.Context, uri string) (cloudsync.Syncer, error)
	// Host is the host shown for uri (never the credentials).
	Host func(uri string) string
	// Classify maps an Open failure to a state and user-facing text that
	// never contains uri's password.
	Classify  func(err error, uri string) (cloudsync.State, string)
	OnStatus  func(cloudsync.Status)
	OnApplied func([]core.Applied)
	// Engine timing; zero takes the cloudsync defaults (tests shorten them).
	Poll, Debounce, ErrorRetry time.Duration
}

// atlasOpen is the production syncDeps.Open.
func atlasOpen(ctx context.Context, uri string) (cloudsync.Syncer, error) {
	s, err := atlas.Open(ctx, uri, atlas.Options{})
	if err != nil {
		return nil, err // not a typed nil in the interface
	}
	return s, nil
}

// atlasClassify is the production syncDeps.Classify.
func atlasClassify(err error, uri string) (cloudsync.State, string) {
	return atlas.Classify(err, atlas.Host(uri))
}

// syncController owns the sync engine: it opens the saved connection at
// startup (retrying on the engine's backoff while that fails), swaps the
// engine on Connect and stops it on Disconnect and at shutdown.
//
// Locks: opMu serializes Start, Connect, Disconnect and Stop and may be held
// across the network (Connect's Open). idleMu serializes changes to, and
// emits of, the status shown while no engine runs. mu guards the fields
// below it and is never held across the network, an engine stop or a
// callback.
type syncController struct {
	d          syncDeps
	root       context.Context // cancelled by Stop
	rootCancel context.CancelFunc
	retry      chan struct{} // SyncNow while a failed open waits to retry

	opMu   sync.Mutex
	idleMu sync.Mutex

	mu         sync.Mutex
	gen        int // bumped by halt; a stale opener or engine is ignored
	stopped    bool
	eng        *cloudsync.Engine
	syncer     cloudsync.Syncer // eng's backend
	idle       cloudsync.Status // the status while eng is nil
	connected  bool             // a connection string is saved
	host       string
	openCancel context.CancelFunc // the startup opener, nil when none runs
	openDone   chan struct{}
}

func offStatus(host string) cloudsync.Status {
	return cloudsync.Status{State: cloudsync.StateOff, Warnings: []string{}, Host: host}
}

func newSyncController(d syncDeps) *syncController {
	root, cancel := context.WithCancel(context.Background())
	return &syncController{d: d, root: root, rootCancel: cancel, retry: make(chan struct{}, 1), idle: offStatus("")}
}

func (c *syncController) opts() cloudsync.Options {
	return cloudsync.Options{Poll: c.d.Poll, Debounce: c.d.Debounce, ErrorRetry: c.d.ErrorRetry}
}

// Start opens the saved connection string, if any, in a goroutine and
// returns at once. Until the open succeeds the state is off; a failed open
// shows its classified state and is retried on the engine's backoff.
func (c *syncController) Start(ctx context.Context) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || c.eng != nil || c.openDone != nil {
		return
	}
	octx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	c.openCancel, c.openDone = cancel, done
	go c.opener(octx, c.gen, done)
}

// opener is Start's goroutine. It reads the secret here too, since even a
// keychain may block.
func (c *syncController) opener(ctx context.Context, gen int, done chan struct{}) {
	defer close(done)
	uri, err := c.d.Secret.Get()
	if err != nil {
		if !errors.Is(err, secret.ErrNotFound) {
			log.Printf("sync: read the connection string: %v", err)
		}
		return
	}
	host := c.d.Host(uri)
	if !c.setIdle(gen, offStatus(host), false) {
		return
	}
	failures := 0
	for {
		s, err := c.d.Open(ctx, uri)
		if err == nil {
			if ctx.Err() != nil {
				closeSyncer(s)
				return
			}
			if err = c.prepareTarget(host); err == nil {
				c.install(gen, s, host)
				return
			}
			closeSyncer(s)
		}
		if ctx.Err() != nil {
			return
		}
		failures++
		state, text := c.d.Classify(err, uri)
		log.Printf("sync: connect to %s: %s", host, text)
		st := cloudsync.Status{State: state, Error: text, Host: host, Pending: c.pending(), Warnings: []string{}}
		if !c.setIdle(gen, st, true) {
			return
		}
		t := time.NewTimer(cloudsync.RetryDelay(state, failures, c.opts()))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-c.retry: // Sync now: retry at once and reset the backoff
			failures = 0
		case <-t.C:
		}
		t.Stop()
	}
}

// setIdle records the status shown while no engine runs (and that a
// connection string is saved), unless gen is stale. It reports whether it did.
func (c *syncController) setIdle(gen int, st cloudsync.Status, emit bool) bool {
	c.idleMu.Lock()
	defer c.idleMu.Unlock()
	c.mu.Lock()
	if gen != c.gen || c.stopped {
		c.mu.Unlock()
		return false
	}
	c.idle, c.connected, c.host = st, true, st.Host
	c.mu.Unlock()
	if emit {
		c.emit(st)
	}
	return true
}

func (c *syncController) emit(st cloudsync.Status) {
	if c.d.OnStatus != nil {
		c.d.OnStatus(st)
	}
}

// install starts an engine on s unless gen is stale (then s is closed).
func (c *syncController) install(gen int, s cloudsync.Syncer, host string) {
	c.mu.Lock()
	if gen != c.gen || c.stopped {
		c.mu.Unlock()
		closeSyncer(s)
		return
	}
	var e *cloudsync.Engine
	o := c.opts()
	o.Host = host
	o.OnApplied = c.d.OnApplied
	o.OnStatus = func(st cloudsync.Status) {
		// A stopping engine's last status must not overwrite its
		// successor's (or Disconnect's off).
		c.mu.Lock()
		current := c.eng == e
		c.mu.Unlock()
		if current {
			c.emit(st)
		}
	}
	e = cloudsync.NewEngine(c.d.Core, s, o)
	c.eng, c.syncer, c.connected, c.host = e, s, true, host
	c.idle = offStatus(host)
	e.Start(c.root) // returns at once
	c.mu.Unlock()
}

// halt stops the opener and the engine and waits for both. opMu is held.
func (c *syncController) halt() {
	c.mu.Lock()
	c.gen++
	eng, cancel, done := c.eng, c.openCancel, c.openDone
	c.eng, c.syncer, c.openCancel, c.openDone = nil, nil, nil, nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	if eng != nil {
		eng.Stop() // joins the cycle in flight and closes the backend
	}
}

// prepareTarget starts the pull over, and queues everything local for push,
// when host is not the cluster the watermarks belong to.
func (c *syncController) prepareTarget(host string) error {
	target := host + "/" + atlas.Database
	cur, ok, err := c.d.Core.SyncMeta(metaSyncTarget)
	if err != nil {
		return err
	}
	if ok && cur == target {
		return nil
	}
	for _, k := range cloudsync.WatermarkKeys() {
		if err := c.d.Core.DeleteSyncMeta(k); err != nil {
			return err
		}
	}
	if err := c.d.Core.EnqueueAll(); err != nil {
		return err
	}
	return c.d.Core.SetSyncMeta(metaSyncTarget, target) // last, so a failure retries all of it
}

func (c *syncController) pending() int {
	n, err := c.d.Core.OutboxCount()
	if err != nil {
		log.Printf("sync: outbox count: %v", err)
	}
	return n
}

func closeSyncer(s cloudsync.Syncer) {
	ctx, cancel := context.WithTimeout(context.Background(), syncCloseTimeout)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		log.Printf("sync: close: %v", err)
	}
}

// Connect opens uri (trimmed), saves it and replaces the running engine. A
// new cluster starts the pull over and pushes everything local. It returns
// without waiting for the first cycle. On failure nothing is saved and the
// error is user-facing text without the connection string.
func (c *syncController) Connect(ctx context.Context, uri string) (cloudsync.Status, error) {
	uri = strings.TrimSpace(uri)
	ctx, cancel := context.WithTimeout(ctx, syncConnectTimeout)
	defer cancel()
	defer context.AfterFunc(c.root, cancel)() // Stop aborts a Connect in flight
	c.opMu.Lock()
	defer c.opMu.Unlock()
	c.mu.Lock()
	stopped := c.stopped
	c.mu.Unlock()
	if stopped {
		return c.Status(), errSyncStopped
	}
	s, err := c.d.Open(ctx, uri)
	if err != nil {
		_, text := c.d.Classify(err, uri)
		return c.Status(), errors.New(text)
	}
	if err := c.d.Secret.Set(uri); err != nil {
		closeSyncer(s)
		return c.Status(), fmt.Errorf("Couldn't save the connection string: %v", err)
	}
	host := c.d.Host(uri)
	c.halt()
	c.mu.Lock()
	gen := c.gen
	c.mu.Unlock()
	if err := c.prepareTarget(host); err != nil {
		closeSyncer(s)
		st := cloudsync.Status{State: cloudsync.StateError, Error: "Couldn't prepare this computer's data for sync: " + err.Error(), Host: host, Pending: c.pending(), Warnings: []string{}}
		c.setIdle(gen, st, true)
		return st, errors.New(st.Error)
	}
	c.install(gen, s, host)
	return c.Status(), nil
}

// Disconnect stops sync and forgets the connection string. Local data, the
// outbox, the watermarks and the target stay, so reconnecting to the same
// cluster resumes.
func (c *syncController) Disconnect() error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	c.halt()
	err := c.d.Secret.Delete()
	if err != nil {
		log.Printf("sync: forget the connection string: %v", err)
	}
	c.idleMu.Lock()
	defer c.idleMu.Unlock()
	off := offStatus("")
	c.mu.Lock()
	c.idle, c.connected, c.host = off, false, ""
	c.mu.Unlock()
	c.emit(off)
	return err
}

// Stop ends sync for shutdown: it aborts a Connect or open in flight and
// joins the engine, so nothing touches the store afterwards.
func (c *syncController) Stop() {
	c.rootCancel()
	c.opMu.Lock()
	defer c.opMu.Unlock()
	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()
	c.halt()
}

// Kick asks the engine to push a local change. With no engine because the
// open failed, it refreshes the waiting count instead.
func (c *syncController) Kick() {
	c.mu.Lock()
	eng := c.eng
	c.mu.Unlock()
	if eng != nil {
		eng.Kick()
		return
	}
	c.idleMu.Lock()
	defer c.idleMu.Unlock()
	c.mu.Lock()
	state := c.idle.State
	c.mu.Unlock()
	if state != cloudsync.StateOffline && state != cloudsync.StateError {
		return
	}
	n := c.pending()
	c.mu.Lock()
	if c.eng != nil || c.idle.State != state || c.idle.Pending == n {
		c.mu.Unlock()
		return
	}
	c.idle.Pending = n
	st := cloneStatus(c.idle)
	c.mu.Unlock()
	c.emit(st)
}

// SyncNow runs a cycle at once, or retries a failed open at once.
func (c *syncController) SyncNow() {
	c.mu.Lock()
	eng, opening := c.eng, c.openDone != nil
	c.mu.Unlock()
	switch {
	case eng != nil:
		eng.SyncNow()
	case opening:
		select {
		case c.retry <- struct{}{}:
		default:
		}
	}
}

// Status is the current sync status: off when no connection string is
// saved (and while the saved one is still being opened).
func (c *syncController) Status() cloudsync.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.eng != nil {
		return c.eng.Status()
	}
	return cloneStatus(c.idle)
}

func cloneStatus(s cloudsync.Status) cloudsync.Status {
	if s.LastSyncAt != nil {
		t := *s.LastSyncAt
		s.LastSyncAt = &t
	}
	s.Warnings = append([]string{}, s.Warnings...)
	return s
}

// Info describes the connection for Settings. The attachment size is asked
// of the cluster, so it is -1 while offline or not connected.
func (c *syncController) Info(ctx context.Context) SyncInfo {
	c.mu.Lock()
	eng, s := c.eng, c.syncer
	info := SyncInfo{Connected: c.connected, Host: c.host, AttachmentBytes: -1}
	c.mu.Unlock()
	if c.d.Secret != nil {
		info.SecretStore = c.d.Secret.Kind()
	}
	if eng == nil || eng.Status().State == cloudsync.StateOffline {
		return info
	}
	ctx, cancel := context.WithTimeout(ctx, syncInfoTimeout)
	defer cancel()
	defer context.AfterFunc(c.root, cancel)()
	if n, err := s.AttachmentBytes(ctx); err != nil {
		_, text := s.Classify(err)
		log.Printf("sync: attachment size: %s", text)
	} else {
		info.AttachmentBytes = n
	}
	return info
}
