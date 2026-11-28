package push

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"awesome-reminder/internal/core"
	"awesome-reminder/internal/model"
	"awesome-reminder/internal/store"
)

const (
	// ctlSinceKey is the meta key holding the ID of the last control message
	// handled (applied or ignored).
	ctlSinceKey = "push_ctl_since"
	// ctlMaxBody: larger control messages are ignored.
	ctlMaxBody = 1 << 10

	ctlBackoffMin = 5 * time.Second
	ctlBackoffMax = 5 * time.Minute
	// ctlHealthy: a session that lasted this long resets the back-off.
	ctlHealthy = time.Minute
)

// ControlDeps is what the control listener reads. Config returns false when
// push is off. Done applies a phone Done (the app uses ApplyDone).
type ControlDeps struct {
	Config func() (Config, bool)
	Meta   interface {
		Meta(string) (string, bool, error)
		SetMeta(string, string) error
	}
	Done func(itemID string, occ time.Time) error
	HTTP *http.Client
}

// StartControl listens on <topic>-ctl while push is on: it polls the
// messages cached since the last one handled, applies them, then streams new
// ones, reconnecting with back-off (5 s doubling to 5 min) after a failure.
// A receive on restart (the config changed) reconnects at once, or stops
// listening when push is now off. wait blocks until the listener has exited
// after ctx ends.
func StartControl(ctx context.Context, d ControlDeps, restart <-chan struct{}) (wait func()) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		controlLoop(ctx, d, restart)
	}()
	return wg.Wait
}

func controlLoop(ctx context.Context, d ControlDeps, restart <-chan struct{}) {
	backoff := time.Duration(0)
	for ctx.Err() == nil {
		cfg, on := d.Config()
		if !on || cfg.Topic == "" {
			select {
			case <-ctx.Done():
			case <-restart:
			}
			backoff = 0
			continue
		}

		sctx, cancel := context.WithCancel(ctx)
		restarted := make(chan struct{})
		watched := make(chan struct{})
		go func() { // ends the session early when the config changes
			defer close(watched)
			select {
			case <-restart:
				close(restarted)
				cancel()
			case <-sctx.Done():
			}
		}()
		began := time.Now()
		controlSession(sctx, d, cfg)
		cancel()
		<-watched

		select {
		case <-restarted:
			backoff = 0
			continue
		default:
		}
		if ctx.Err() != nil {
			return
		}
		switch {
		case time.Since(began) >= ctlHealthy:
			backoff = ctlBackoffMin
		case backoff == 0:
			backoff = ctlBackoffMin
		default:
			backoff = min(backoff*2, ctlBackoffMax)
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
		case <-restart:
			backoff = 0
		case <-t.C:
		}
		t.Stop()
	}
}

// controlSession polls then streams until ctx ends or the connection fails.
func controlSession(ctx context.Context, d ControlDeps, cfg Config) error {
	c := Client{Server: cfg.Server, Topic: cfg.Topic, Token: cfg.Token, HTTP: d.HTTP}
	since, _, _ := d.Meta.Meta(ctlSinceKey)
	handle := func(m CtlMessage) {
		if id, occ, ok := parseDone(m.Message); ok {
			// A failed Done is dropped like a stale one: retrying a phone
			// tap later could complete a newer occurrence.
			_ = d.Done(id, occ)
		}
		if m.ID != "" {
			since = m.ID
			_ = d.Meta.SetMeta(ctlSinceKey, since) // kept in memory if it fails
		}
	}
	msgs, err := c.PollControl(ctx, since)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		handle(m)
	}
	return c.StreamControl(ctx, since, handle)
}

// parseDone validates a control message: at most 1 KiB of JSON with
// op "done" and a non-empty id. occ is unix seconds.
func parseDone(body string) (id string, occ time.Time, ok bool) {
	if len(body) > ctlMaxBody {
		return "", time.Time{}, false
	}
	var m struct {
		Op  string `json:"op"`
		ID  string `json:"id"`
		Occ int64  `json:"occ"`
	}
	if json.Unmarshal([]byte(body), &m) != nil || m.Op != "done" || m.ID == "" {
		return "", time.Time{}, false
	}
	return m.ID, time.Unix(m.Occ, 0), true
}

// ApplyDone applies a phone Done for the occurrence due at occ (whole
// seconds). A missing or no longer open item is ignored. An item with a due
// time is completed through c.DoneThrough only when occ is its current due
// time: a different occurrence means a computer already moved the item on
// (recurring advance, edit, snooze to a new due) and the phone tap is stale,
// so it must not complete a later occurrence. An item without a due time (a
// reply) accepts any occ.
func ApplyDone(c *core.Core, itemID string, occ time.Time) error {
	it, err := c.Item(itemID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if it.Status != model.StatusOpen {
		return nil
	}
	if it.DueAt != nil && occ.Unix() != it.DueAt.Unix() {
		return nil
	}
	_, err = c.DoneThrough(itemID, occ)
	return err
}
