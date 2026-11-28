// Package push schedules phone notifications on an ntfy server.
//
// Facts the client relies on (verified against ntfy.sh, 2026-11-28):
// publishing with the sequence ID in the path (POST /<topic>/<seq>) lets
// DELETE /<topic>/<seq> cancel the pending scheduled message; a sequence ID
// sent in the X-Sequence-ID header can't be cancelled, so it is never used;
// publishing one seq twice delivers both, so Schedule deletes first.
//
// Nothing here logs. Errors never contain the token, the topic or bodies.
package push

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// requestTimeout bounds every request except the control stream.
const requestTimeout = 15 * time.Second

// streamIdle ends a control stream that has gone silent (ntfy sends a
// keepalive every 45 s), so a connection dead after sleep is noticed.
const streamIdle = 2 * time.Minute

var (
	ErrAuth        = errors.New("ntfy: not authorized") // 401/403
	ErrRateLimited = errors.New("ntfy: rate limited")   // 429
)

// TempError is a network error or a 5xx: worth retrying later.
type TempError struct{ Err error }

func (e *TempError) Error() string { return "ntfy: temporary failure: " + e.Err.Error() }
func (e *TempError) Unwrap() error { return e.Err }

// Message is one scheduled push.
type Message struct {
	Seq         string
	DeliverAt   time.Time
	Title, Body string
	Priority    int
	Tags        []string
	Actions     []Action
}

// Action is an ntfy "http" action button.
type Action struct {
	Label, URL, Method, Body string
	Headers                  map[string]string
	Clear                    bool
}

// CtlMessage is a message on the control topic (<topic>-ctl).
type CtlMessage struct{ ID, Message string }

// Client talks to one ntfy server and topic. HTTP nil uses a default client.
type Client struct {
	Server, Topic, Token string
	HTTP                 *http.Client
}

// Schedule publishes m for delivery at m.DeliverAt, replacing any pending
// message with the same seq: DELETE then POST, both path form.
func (c Client) Schedule(ctx context.Context, m Message) error {
	if m.Seq == "" {
		return errors.New("ntfy: schedule without a sequence ID")
	}
	if err := c.Delete(ctx, m.Seq); err != nil {
		return err
	}
	h := http.Header{}
	setText(h, "X-Title", m.Title)
	if m.Priority > 0 {
		h.Set("X-Priority", strconv.Itoa(m.Priority))
	}
	if len(m.Tags) > 0 {
		setText(h, "X-Tags", strings.Join(m.Tags, ","))
	}
	if !m.DeliverAt.IsZero() {
		h.Set("X-Delay", strconv.FormatInt(m.DeliverAt.Unix(), 10))
	}
	if len(m.Actions) > 0 {
		a, err := actionsJSON(m.Actions)
		if err != nil {
			return err
		}
		setText(h, "X-Actions", a)
	}
	resp, err := c.do(ctx, http.MethodPost, c.url(c.Topic, m.Seq), h, []byte(m.Body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Delete cancels pending messages with seq. Not found is success.
func (c Client) Delete(ctx context.Context, seq string) error {
	resp, err := c.do(ctx, http.MethodDelete, c.url(c.Topic, seq), nil, nil)
	if errors.Is(err, errNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// SendNow publishes a message for immediate delivery, without a seq.
func (c Client) SendNow(ctx context.Context, title, body string) error {
	h := http.Header{}
	setText(h, "X-Title", title)
	resp, err := c.do(ctx, http.MethodPost, c.url(c.Topic), h, []byte(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// PostControl publishes body to <topic>-ctl, as the phone's Done button does.
func (c Client) PostControl(ctx context.Context, body []byte) error {
	resp, err := c.do(ctx, http.MethodPost, c.url(c.Topic+"-ctl"), nil, body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// PollControl lists cached control messages after the message ID since
// (all when empty).
func (c Client) PollControl(ctx context.Context, since string) ([]CtlMessage, error) {
	if since == "" {
		since = "all"
	}
	u := c.url(c.Topic+"-ctl", "json") + "?poll=1&since=" + url.QueryEscape(since)
	resp, err := c.do(ctx, http.MethodGet, u, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out []CtlMessage
	err = readLines(resp.Body, func(m CtlMessage) { out = append(out, m) })
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &TempError{Err: fmt.Errorf("reading poll: %w", err)}
	}
	return out, nil
}

// StreamControl subscribes to <topic>-ctl and calls fn for each new message.
// It returns when ctx ends (ctx.Err()) or the stream fails or closes
// (*TempError, or the status errors).
func (c Client) StreamControl(ctx context.Context, fn func(CtlMessage)) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, err := c.doStream(sctx, c.url(c.Topic+"-ctl", "json"))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	idle := time.AfterFunc(streamIdle, cancel)
	defer idle.Stop()
	err = readLines(&resetReader{r: resp.Body, onRead: func() { idle.Reset(streamIdle) }}, fn)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	if sctx.Err() != nil {
		err = errors.New("control stream idle")
	}
	return &TempError{Err: fmt.Errorf("control stream: %w", err)}
}

// errNotFound is internal: Delete maps it to nil, others to a plain error.
var errNotFound = errors.New("ntfy: not found")

func (c Client) do(ctx context.Context, method, u string, h http.Header, body []byte) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	resp, err := c.send(ctx, method, u, h, body)
	if err != nil {
		cancel()
		return nil, err
	}
	// Buffer the (small) response so the timeout can be released here.
	b, rerr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	cancel()
	if rerr != nil {
		return nil, &TempError{Err: fmt.Errorf("%s %s: reading response: %w", method, c.redact(u), unwrapURL(rerr))}
	}
	resp.Body = io.NopCloser(bytes.NewReader(b))
	return resp, nil
}

func (c Client) doStream(ctx context.Context, u string) (*http.Response, error) {
	return c.send(ctx, http.MethodGet, u, nil, nil)
}

// send performs the request and maps failures; on success the caller owns
// resp.Body.
func (c Client) send(ctx context.Context, method, u string, h http.Header, body []byte) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, fmt.Errorf("ntfy: bad server URL: %w", unwrapURL(err))
	}
	if (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.Host == "" {
		return nil, errors.New("ntfy: bad server URL: want http(s)://host")
	}
	for k, v := range h {
		req.Header[k] = v
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &TempError{Err: fmt.Errorf("%s %s: %w", method, c.redact(u), unwrapURL(err))}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	msg := serverError(resp.Body)
	code := resp.StatusCode
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return nil, fmt.Errorf("%w (HTTP %d)", ErrAuth, code)
	case code == http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w (HTTP %d)", ErrRateLimited, code)
	case code >= 500:
		return nil, &TempError{Err: fmt.Errorf("%s %s: HTTP %d%s", method, c.redact(u), code, msg)}
	case code == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s %s%s", errNotFound, method, c.redact(u), msg)
	default:
		return nil, fmt.Errorf("ntfy: %s %s: HTTP %d%s", method, c.redact(u), code, msg)
	}
}

// serverError extracts ntfy's {"error": "..."} text (the server's own
// message, never ours), for diagnosis.
func serverError(r io.Reader) string {
	var e struct {
		Error string `json:"error"`
	}
	b, _ := io.ReadAll(io.LimitReader(r, 4096))
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		return ": " + e.Error
	}
	return ""
}

// unwrapURL drops *url.Error's wrapper, whose text holds the full URL
// (with the topic).
func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// url joins the server URL (any case of scheme, trailing slash, subpath)
// with path segments.
func (c Client) url(segs ...string) string {
	u, err := url.Parse(strings.TrimSpace(c.Server))
	if err != nil {
		return c.Server // NewRequest reports it
	}
	p := strings.TrimRight(u.Path, "/")
	for _, s := range segs {
		p += "/" + url.PathEscape(s)
	}
	u.Path, u.RawPath = p, ""
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

// redact replaces the topic in a URL for error text.
func (c Client) redact(u string) string {
	if c.Topic == "" {
		return u
	}
	return strings.ReplaceAll(u, url.PathEscape(c.Topic), "<topic>")
}

// setText sets a header that may carry arbitrary text: RFC 2047-encoded
// when it has non-ASCII or control characters (ntfy decodes it).
func setText(h http.Header, name, v string) {
	if v == "" {
		return
	}
	h.Set(name, mime.BEncoding.Encode("UTF-8", v))
}

type actionJSON struct {
	Action  string            `json:"action"`
	Label   string            `json:"label"`
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	Clear   bool              `json:"clear,omitempty"`
}

// actionsJSON renders ntfy's JSON array form of X-Actions on one line.
func actionsJSON(as []Action) (string, error) {
	out := make([]actionJSON, len(as))
	for i, a := range as {
		out[i] = actionJSON{Action: "http", Label: a.Label, URL: a.URL, Method: a.Method, Headers: a.Headers, Body: a.Body, Clear: a.Clear}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return "", fmt.Errorf("ntfy: encoding actions: %w", err)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// readLines parses ntfy's newline-delimited JSON and calls fn for each
// "message" event (open/keepalive/other events are skipped).
func readLines(r io.Reader, fn func(CtlMessage)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev struct {
			ID      string `json:"id"`
			Event   string `json:"event"`
			Message string `json:"message"`
		}
		if json.Unmarshal(line, &ev) != nil || ev.Event != "message" {
			continue
		}
		fn(CtlMessage{ID: ev.ID, Message: ev.Message})
	}
	return sc.Err()
}

// resetReader calls onRead after every successful read.
type resetReader struct {
	r      io.Reader
	onRead func()
}

func (r *resetReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.onRead()
	}
	return n, err
}
