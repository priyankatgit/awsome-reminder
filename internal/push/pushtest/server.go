// Package pushtest is a fake ntfy server for tests. It models the behaviour
// verified against ntfy.sh (2026-11-28):
//
//   - POST /<topic>/<seq> (path form) with X-Delay stores a pending message;
//     DELETE /<topic>/<seq> removes every pending message with that seq.
//   - Publishing the same seq twice keeps both (no replace).
//   - A sequence ID sent in the X-Sequence-ID header can't be cancelled.
//   - A publish without X-Delay is delivered at once.
//
// Delivered messages are cached per topic and served by
// GET /<topic>/json?poll=1&since=<id|all> and streamed by GET /<topic>/json
// (with since=<id|all>, the cached messages after it are sent first).
//
// Headers are decoded with mime.WordDecoder (RFC 2047) and X-Actions as
// ntfy's JSON array form, so tests can round-trip exact text.
package pushtest

import (
	"bufio"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Request is one recorded request.
type Request struct {
	Method string
	Path   string
	Query  string // raw query
	Header http.Header
	Body   []byte
}

// Action is a decoded X-Actions entry. Keep in sync with push.actionJSON.
type Action struct {
	Action  string            `json:"action"`
	Label   string            `json:"label"`
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	Clear   bool              `json:"clear,omitempty"`
}

// Message is a published message as the fake decoded it.
type Message struct {
	ID        string
	Topic     string
	Seq       string    // from the path; empty for header-form or no seq
	HeaderSeq string    // from X-Sequence-ID (not cancellable)
	DeliverAt time.Time // zero when published without X-Delay
	Title     string
	Body      string
	Priority  int
	Tags      []string
	Actions   []Action
	Header    http.Header
}

type failRule struct {
	method, path string
	status       int
	left         int // <= 0: forever
}

// Server is a fake ntfy server. Create it with New and Close it when done.
type Server struct {
	*httptest.Server

	// Now resolves relative X-Delay values such as "1h" (default time.Now).
	// Set it before use.
	Now func() time.Time

	mu        sync.Mutex
	token     string
	fails     []*failRule
	requests  []Request
	pending   []Message
	delivered []Message
	cache     map[string][]Message // topic → delivered, in order
	subs      map[string]map[chan Message]struct{}
	nextID    int
	kill      chan struct{} // closed by CloseStreams / Close
	closed    bool
}

// New starts a fake ntfy server.
func New() *Server {
	s := &Server{
		Now:   time.Now,
		cache: map[string][]Message{},
		subs:  map[string]map[chan Message]struct{}{},
		kill:  make(chan struct{}),
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// Close ends open streams and shuts the server down. Safe to call twice.
func (s *Server) Close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.kill)
	}
	s.mu.Unlock()
	s.Server.Close()
}

// CloseStreams ends every open stream (the client sees the body end).
func (s *Server) CloseStreams() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	close(s.kill)
	s.kill = make(chan struct{})
}

// RequireToken makes every request need "Authorization: Bearer <tok>"
// (401 otherwise). Empty turns the check off.
func (s *Server) RequireToken(tok string) {
	s.mu.Lock()
	s.token = tok
	s.mu.Unlock()
}

// Fail answers the next n requests matching method and path with status
// (n <= 0: every matching request). Empty method or path matches any.
// Rules are checked before auth, in the order added.
func (s *Server) Fail(method, path string, status, n int) {
	s.mu.Lock()
	s.fails = append(s.fails, &failRule{method: method, path: path, status: status, left: n})
	s.mu.Unlock()
}

// ClearFails removes every Fail rule.
func (s *Server) ClearFails() {
	s.mu.Lock()
	s.fails = nil
	s.mu.Unlock()
}

// Requests returns every request received, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Pending returns the scheduled messages not yet delivered, in publish order.
func (s *Server) Pending() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.pending...)
}

// Delivered returns every delivered message, in delivery order.
func (s *Server) Delivered() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.delivered...)
}

// Deliver moves pending messages due at or before now to delivered and
// returns them ordered by delivery time.
func (s *Server) Deliver(now time.Time) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due, keep []Message
	for _, m := range s.pending {
		if !m.DeliverAt.After(now) {
			due = append(due, m)
		} else {
			keep = append(keep, m)
		}
	}
	s.pending = keep
	sort.SliceStable(due, func(i, j int) bool { return due[i].DeliverAt.Before(due[j].DeliverAt) })
	for _, m := range due {
		s.deliverLocked(m)
	}
	return due
}

// Subscribers returns how many streams are open on topic.
func (s *Server) Subscribers(topic string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs[topic])
}

func (s *Server) deliverLocked(m Message) {
	s.delivered = append(s.delivered, m)
	s.cache[m.Topic] = append(s.cache[m.Topic], m)
	for ch := range s.subs[m.Topic] {
		select {
		case ch <- m:
		default: // slow test subscriber; drop rather than block
		}
	}
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: body})
	for _, f := range s.fails {
		if f.status == 0 || (f.method != "" && f.method != r.Method) || (f.path != "" && f.path != r.URL.Path) {
			continue
		}
		status := f.status
		if f.left > 0 {
			f.left--
			if f.left == 0 {
				f.status = 0 // spent
			}
		}
		s.mu.Unlock()
		httpError(w, status)
		return
	}
	if s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
		s.mu.Unlock()
		httpError(w, http.StatusUnauthorized)
		return
	}
	s.mu.Unlock()

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "json":
		if r.URL.Query().Get("poll") == "1" {
			s.poll(w, parts[0], r.URL.Query().Get("since"))
		} else {
			s.stream(w, r, parts[0], r.URL.Query().Get("since"))
		}
	case (r.Method == http.MethodPost || r.Method == http.MethodPut) && (len(parts) == 1 || len(parts) == 2) && parts[0] != "":
		seq := ""
		if len(parts) == 2 {
			seq = parts[1]
		}
		s.publish(w, r, parts[0], seq, body)
	case r.Method == http.MethodDelete && len(parts) == 2:
		s.mu.Lock()
		var keep []Message
		for _, m := range s.pending {
			if !(m.Topic == parts[0] && m.Seq == parts[1]) {
				keep = append(keep, m)
			}
		}
		s.pending = keep
		s.mu.Unlock()
		writeJSON(w, map[string]string{"event": "message_delete"})
	default:
		httpError(w, http.StatusNotFound)
	}
}

func (s *Server) publish(w http.ResponseWriter, r *http.Request, topic, seq string, body []byte) {
	dec := new(mime.WordDecoder)
	header := func(name string) string {
		v := r.Header.Get(name)
		if d, err := dec.DecodeHeader(v); err == nil {
			return d
		}
		return v
	}
	m := Message{
		Topic:     topic,
		Seq:       seq,
		HeaderSeq: r.Header.Get("X-Sequence-ID"),
		Title:     header("X-Title"),
		Body:      string(body),
		Header:    r.Header.Clone(),
	}
	if p := header("X-Priority"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			httpError(w, http.StatusBadRequest)
			return
		}
		m.Priority = n
	}
	if t := header("X-Tags"); t != "" {
		for _, tag := range strings.Split(t, ",") {
			m.Tags = append(m.Tags, strings.TrimSpace(tag))
		}
	}
	if a := header("X-Actions"); a != "" {
		if err := json.Unmarshal([]byte(a), &m.Actions); err != nil {
			httpError(w, http.StatusBadRequest)
			return
		}
	}
	if d := r.Header.Get("X-Delay"); d != "" {
		if n, err := strconv.ParseInt(d, 10, 64); err == nil {
			m.DeliverAt = time.Unix(n, 0)
		} else if dur, err := time.ParseDuration(d); err == nil {
			m.DeliverAt = s.Now().Add(dur).Truncate(time.Second)
		} else {
			httpError(w, http.StatusBadRequest)
			return
		}
	}
	s.mu.Lock()
	s.nextID++
	m.ID = "m" + strconv.Itoa(s.nextID)
	if m.DeliverAt.IsZero() {
		s.deliverLocked(m)
	} else {
		s.pending = append(s.pending, m)
	}
	s.mu.Unlock()
	writeJSON(w, map[string]any{"id": m.ID, "event": "message", "topic": topic})
}

// after returns msgs after the message with ID since: all for "all" or an
// unknown ID, none for "".
func after(msgs []Message, since string) []Message {
	if since == "" {
		return nil
	}
	if since != "all" {
		for i, m := range msgs {
			if m.ID == since {
				return msgs[i+1:]
			}
		}
	}
	return msgs
}

func (s *Server) poll(w http.ResponseWriter, topic, since string) {
	if since == "" {
		since = "all"
	}
	s.mu.Lock()
	msgs := after(append([]Message(nil), s.cache[topic]...), since)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/x-ndjson")
	bw := bufio.NewWriter(w)
	for _, m := range msgs {
		writeLine(bw, m)
	}
	bw.Flush()
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request, topic, since string) {
	ch := make(chan Message, 64)
	s.mu.Lock()
	if s.subs[topic] == nil {
		s.subs[topic] = map[chan Message]struct{}{}
	}
	s.subs[topic][ch] = struct{}{}
	backlog := after(append([]Message(nil), s.cache[topic]...), since)
	kill := s.kill
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs[topic], ch)
		s.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "application/x-ndjson")
	fl, _ := w.(http.Flusher)
	json.NewEncoder(w).Encode(map[string]any{"id": "open", "event": "open", "topic": topic})
	for _, m := range backlog {
		writeLine(w, m)
	}
	if fl != nil {
		fl.Flush()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-kill:
			return
		case m := <-ch:
			writeLine(w, m)
			if fl != nil {
				fl.Flush()
			}
		}
	}
}

func writeLine(w io.Writer, m Message) {
	json.NewEncoder(w).Encode(map[string]any{
		"id":      m.ID,
		"event":   "message",
		"topic":   m.Topic,
		"title":   m.Title,
		"message": m.Body,
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"code": status * 100, "http": status, "error": http.StatusText(status)})
}
