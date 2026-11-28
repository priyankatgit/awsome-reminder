package app

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"awesome-reminder/internal/core"
	"awesome-reminder/internal/push"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/secret"
	"awesome-reminder/internal/settings"
	"awesome-reminder/internal/store"
)

// pushRequestTimeout bounds each reconciler request; the reconciler sets no
// deadline of its own.
const pushRequestTimeout = 15 * time.Second

// errEmptyPushToken is returned by PushSetToken for a blank token.
var errEmptyPushToken = errors.New("Paste an access token")

// pushReconciler is what the push controller uses of *push.Reconciler.
type pushReconciler interface {
	Start(context.Context)
	Wait()
	Kick()
	Dismiss(seqs []string)
	Status() push.Status
	SendTest(context.Context) error
}

// pushController owns phone push: the reconciler, the control listener on
// <topic>-ctl and the access token. Its methods are nil-safe, so a Service
// without one (tests) reports push as off.
type pushController struct {
	core  *core.Core
	rec   pushReconciler
	token secret.Store
	// control starts the control listener; nil in tests.
	control func(ctx context.Context, restart <-chan struct{}) (wait func())
	restart chan struct{} // buffered 1: reconnect (or stop) the listener

	mu      sync.Mutex
	ctlKey  string // the listener's config at the last check (pushListenKey)
	waitCtl func()
}

// newPushController wires push to the store and core. done is called after a
// phone Done completed an item (the app drops its alert cards); onStatus gets
// each status change.
func newPushController(c *core.Core, st *store.Store, dataDir string, done func(itemID string), onStatus func(push.Status)) *pushController {
	p := &pushController{core: c, token: secret.NewNamed(dataDir, st, secret.PushToken), restart: make(chan struct{}, 1)}
	p.rec = push.NewReconciler(push.Deps{
		Items:    st.ListOpen,
		Rules:    p.rules,
		Config:   p.config,
		Log:      st,
		Now:      time.Now,
		HTTP:     &http.Client{Timeout: pushRequestTimeout},
		OnStatus: onStatus,
	})
	p.control = func(ctx context.Context, restart <-chan struct{}) func() {
		return push.StartControl(ctx, push.ControlDeps{
			Config: p.config,
			Meta:   st,
			Done: func(id string, occ time.Time) error {
				// push.ApplyDone, keeping whether it applied: a stale tap
				// changes nothing, so the item's cards stay.
				applied, err := c.DoneOccurrence(id, occ)
				if err == nil && applied && done != nil {
					done(id)
				}
				return err
			},
			// No overall timeout: the stream stays open; the listener has
			// its own idle and header timers.
			HTTP: &http.Client{},
		}, restart)
	}
	return p
}

// start runs the reconciler and the control listener until ctx ends. The
// listener idles while push is off.
func (p *pushController) start(ctx context.Context) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.ctlKey = p.listenKey()
	p.mu.Unlock()
	p.rec.Start(ctx)
	if p.control != nil {
		w := p.control(ctx, p.restart)
		p.mu.Lock()
		p.waitCtl = w
		p.mu.Unlock()
	}
}

// wait blocks until the reconciler and the listener have exited (after the
// start context was cancelled).
func (p *pushController) wait() {
	if p == nil {
		return
	}
	p.rec.Wait()
	p.mu.Lock()
	w := p.waitCtl
	p.mu.Unlock()
	if w != nil {
		w()
	}
}

// kick requests a reconcile.
func (p *pushController) kick() {
	if p != nil {
		p.rec.Kick()
	}
}

// changed runs on every core change (items or settings, local or synced): it
// kicks the reconciler and, when push was switched or its server or topic
// changed, restarts the control listener.
func (p *pushController) changed() {
	if p == nil {
		return
	}
	p.rec.Kick()
	k := p.listenKey()
	p.mu.Lock()
	moved := k != p.ctlKey
	p.ctlKey = k
	p.mu.Unlock()
	if moved {
		p.restartListener()
	}
}

// tokenChanged kicks the reconciler and reconnects the listener with the
// new token.
func (p *pushController) tokenChanged() {
	p.rec.Kick()
	p.restartListener()
}

func (p *pushController) restartListener() {
	select {
	case p.restart <- struct{}{}:
	default:
	}
}

// dismiss cancels the phone pushes of dismissed cards (see pushSeq).
func (p *pushController) dismiss(cards []AlertCard) {
	if p == nil {
		return
	}
	var seqs []string
	for _, c := range cards {
		if seq, ok := pushSeq(c); ok {
			seqs = append(seqs, seq)
		}
	}
	p.rec.Dismiss(seqs)
}

// listenKey is what the control listener depends on besides the token.
func (p *pushController) listenKey() string {
	cfg, on := p.config()
	if !on {
		return ""
	}
	return cfg.Server + "\n" + cfg.Topic
}

// config is the reconciler's and listener's view of the settings: on only
// with push enabled and a topic. The token is read on each call.
func (p *pushController) config() (push.Config, bool) {
	s, err := p.core.Settings()
	if err != nil {
		log.Printf("push: settings: %v", err)
		return push.Config{}, false
	}
	return push.ConfigFrom(s, p.tokenValue(), time.Local), s.PushEnabled && s.PushTopic != ""
}

// tokenValue is the saved access token, "" when none (or unreadable).
func (p *pushController) tokenValue() string {
	t, err := p.token.Get()
	if err != nil {
		if !errors.Is(err, secret.ErrNotFound) {
			log.Printf("push: access token unreadable: %v", err)
		}
		return ""
	}
	return t
}

// rules are the scheduler's rules from the current settings (as the
// scheduler reads them).
func (p *pushController) rules() scheduler.Rules {
	s, err := p.core.Settings()
	if err != nil {
		s = settings.Defaults(runtime.GOOS)
		s.LeadTimes = nil
	}
	return core.SchedulerRules(s, time.Local)
}

// pushSeq is the push sequence ID of the occurrence a card shows, and false
// for a card whose dismissal leaves the phone push alone: a heads-up (lead
// time > 0) is not handling the item. The occurrence is the one
// scheduler.Upcoming yields for the card: an at-time or missed card's DueAt,
// a snooze card's snooze end (from its "<id>|snooze|<unix>" key; a snoozed
// reply is a nudge), a nudge's or the sweep's own time.
func pushSeq(c AlertCard) (string, bool) {
	if c.Lead != 0 {
		return "", false
	}
	if c.Kind == scheduler.KindSweep {
		return push.SeqFor("sweep", scheduler.OccSweep, c.DueAt), true
	}
	if c.ItemID == "" {
		return "", false
	}
	kind := scheduler.OccReminder
	if c.Kind == scheduler.KindNudge {
		kind = scheduler.OccNudge
	}
	occ := c.DueAt
	if i := strings.LastIndex(c.Key, "|snooze|"); i >= 0 {
		unix, err := strconv.ParseInt(c.Key[i+len("|snooze|"):], 10, 64)
		if err != nil {
			return "", false
		}
		occ = time.Unix(unix, 0)
	}
	return push.SeqFor(c.ItemID, kind, occ), true
}

// PushInfo is Settings › Phone push's view of push.
type PushInfo struct {
	Status       push.Status `json:"status"`
	HasToken     bool        `json:"hasToken"`
	TokenStore   string      `json:"tokenStore"`   // where the token lives (secret.Store.Kind)
	SubscribeURL string      `json:"subscribeUrl"` // <server>/<topic>; "" when no topic
	QR           string      `json:"qr"`           // PNG data URL of SubscribeURL; "" when none
}

// PushInfo returns the push status, the token's presence and the subscribe
// link with its QR code.
func (s *Service) PushInfo() PushInfo {
	p := s.d.Push
	if p == nil {
		return PushInfo{Status: push.Status{State: "off"}}
	}
	info := PushInfo{Status: p.rec.Status(), HasToken: p.tokenValue() != "", TokenStore: p.token.Kind()}
	st, err := s.d.Core.Settings()
	if err != nil || st.PushTopic == "" {
		return info
	}
	info.SubscribeURL = strings.TrimRight(st.PushServer, "/") + "/" + st.PushTopic
	png, err := qrcode.Encode(info.SubscribeURL, qrcode.Medium, 256)
	if err != nil {
		log.Printf("push: QR code: %v", err)
		return info
	}
	info.QR = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	return info
}

// PushSetToken saves the ntfy access token (trimmed) and applies it.
func (s *Service) PushSetToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errEmptyPushToken
	}
	p := s.d.Push
	if p == nil {
		return errors.New("Phone push isn't available.")
	}
	if err := p.token.Set(token); err != nil {
		return err
	}
	p.tokenChanged()
	return nil
}

// PushForgetToken deletes the saved access token.
func (s *Service) PushForgetToken() error {
	p := s.d.Push
	if p == nil {
		return nil
	}
	if err := p.token.Delete(); err != nil && !errors.Is(err, secret.ErrNotFound) {
		return err
	}
	p.tokenChanged()
	return nil
}

// PushTest sends an immediate test push with the saved settings.
func (s *Service) PushTest() error {
	if s.d.Push == nil {
		return errors.New("Phone push is off")
	}
	return s.d.Push.rec.SendTest(context.Background())
}

// PushNewTopic saves a fresh secret topic and returns the saved settings.
// The reconciler moves pending pushes to it on the change.
func (s *Service) PushNewTopic() (settings.Settings, error) {
	st, err := s.d.Core.Settings()
	if err != nil {
		return settings.Settings{}, err
	}
	st.PushTopic = settings.NewPushTopic()
	if _, err := s.d.Core.SaveSettings(st); err != nil {
		return settings.Settings{}, err
	}
	return s.d.Core.Settings()
}
