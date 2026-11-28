package push

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"awesome-reminder/internal/core"
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

// ctlConfigRetry is how soon the listener tries again after the settings
// could not be read (a variable for tests).
var ctlConfigRetry = ctlBackoffMin

// ControlDeps is what the control listener reads. Config returns false when
// push is off; an error (settings unreadable) is retried after
// ctlConfigRetry, not taken as off. Done applies a phone Done (the app uses ApplyDone).
type ControlDeps struct {
	Config func() (Config, bool, error)
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
	// since is read once and then kept here across reconnects. Empty (none
	// stored, or unreadable) polls everything cached, which is safe: applying
	// a Done twice changes nothing.
	since, _, err := d.Meta.Meta(ctlSinceKey)
	if err != nil {
		log.Printf("push: control: reading last message ID: %v; replaying the cache", err)
		since = ""
	}
	backoff := time.Duration(0)
	for ctx.Err() == nil {
		cfg, on, err := d.Config()
		if err != nil {
			log.Printf("push: control listener: reading settings: %v", err)
			t := time.NewTimer(ctlConfigRetry)
			select {
			case <-ctx.Done():
			case <-restart:
			case <-t.C:
			}
			t.Stop()
			continue
		}
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
		err = controlSession(sctx, d, cfg, &since)
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
		log.Printf("push: control listener: %v", err) // ntfy errors carry no topic or token
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
// *since is the ID of the last message handled; it advances with each one.
func controlSession(ctx context.Context, d ControlDeps, cfg Config, since *string) error {
	c := Client{Server: cfg.Server, Topic: cfg.Topic, Token: cfg.Token, HTTP: d.HTTP}
	handle := func(m CtlMessage) {
		if id, occ, ok := parseDone(m.Message); ok {
			// A failed Done is dropped like a stale one: retrying a phone
			// tap later could complete a newer occurrence.
			if err := d.Done(id, occ); err != nil {
				log.Printf("push: control: done for item %s: %v", id, err)
			}
		}
		if m.ID != "" {
			*since = m.ID
			if err := d.Meta.SetMeta(ctlSinceKey, m.ID); err != nil {
				log.Printf("push: control: saving last message ID: %v", err)
			}
		}
	}
	msgs, err := c.PollControl(ctx, *since)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		handle(m)
	}
	from := *since
	if from == "" {
		// Nothing cached: replay from the start of the cache, so a message
		// posted between the poll and the subscribe is not lost.
		from = "all"
	}
	return c.StreamControl(ctx, from, handle)
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
// seconds) through c.DoneOccurrence, which completes it only while occ is
// still live: the item is open and occ is its due time, or (recurring) a
// later occurrence of its rule not in the future, which this computer has
// not advanced to yet (it was asleep). Any other occ means a computer already
// moved the item on (advance, edit, snooze to a new due) and the phone tap is
// stale, so it must not complete a later occurrence. An item without a due
// time (a reply) accepts any occ. Stale, missing or closed: nil, no change.
func ApplyDone(c *core.Core, itemID string, occ time.Time) error {
	_, err := c.DoneOccurrence(itemID, occ)
	return err
}
