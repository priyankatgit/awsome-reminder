package push

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"awesome-reminder/internal/push/pushtest"
)

const testTopic = "ar-testtopic"

func newClient(s *pushtest.Server) Client {
	return Client{Server: s.URL, Topic: testTopic, HTTP: s.Client()}
}

func TestScheduleDeleteThenPost(t *testing.T) {
	s := pushtest.New()
	defer s.Close()
	c := newClient(s)
	at := time.Now().Add(time.Hour).Truncate(time.Second)

	m := Message{Seq: "arseq1", DeliverAt: at, Title: "first", Body: "one", Priority: 4, Tags: []string{"alarm_clock"}}
	if err := c.Schedule(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	m.Body = "two"
	if err := c.Schedule(context.Background(), m); err != nil {
		t.Fatal(err)
	}

	p := s.Pending()
	if len(p) != 1 || p[0].Body != "two" || p[0].Seq != "arseq1" || p[0].Topic != testTopic {
		t.Fatalf("pending = %+v, want one with body two", p)
	}
	if !p[0].DeliverAt.Equal(at) || p[0].Priority != 4 || strings.Join(p[0].Tags, ",") != "alarm_clock" || p[0].Title != "first" {
		t.Fatalf("pending fields = %+v", p[0])
	}

	reqs := s.Requests()
	var methods []string
	for _, r := range reqs {
		methods = append(methods, r.Method)
	}
	if got := strings.Join(methods, ","); got != "DELETE,POST,DELETE,POST" {
		t.Fatalf("methods = %s", got)
	}
	for _, r := range reqs {
		if r.Path != "/"+testTopic+"/arseq1" {
			t.Errorf("%s path = %s", r.Method, r.Path)
		}
		if r.Header.Get("X-Sequence-ID") != "" {
			t.Errorf("%s carries X-Sequence-ID", r.Method)
		}
	}
	if got, want := reqs[1].Header.Get("X-Delay"), strconv.FormatInt(at.Unix(), 10); got != want {
		t.Fatalf("X-Delay = %q, want %q", got, want)
	}

	// Delivered only once due.
	if d := s.Deliver(at.Add(-time.Second)); len(d) != 0 {
		t.Fatalf("delivered early: %+v", d)
	}
	if d := s.Deliver(at); len(d) != 1 || d[0].Body != "two" {
		t.Fatalf("delivered = %+v", d)
	}
}

func TestFakeKeepsBothWithoutDelete(t *testing.T) {
	// Models ntfy.sh: two path-form publishes of one seq both stay pending.
	s := pushtest.New()
	defer s.Close()
	at := time.Now().Add(time.Hour).Unix()
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest("POST", s.URL+"/"+testTopic+"/dup", strings.NewReader("x"))
		req.Header.Set("X-Delay", strconv.FormatInt(at, 10))
		resp, err := s.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if n := len(s.Pending()); n != 2 {
		t.Fatalf("pending = %d, want 2", n)
	}
	// Header-form sequence IDs can't be cancelled.
	req, _ := http.NewRequest("POST", s.URL+"/"+testTopic, strings.NewReader("h"))
	req.Header.Set("X-Delay", strconv.FormatInt(at, 10))
	req.Header.Set("X-Sequence-ID", "hdr")
	resp, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	c := newClient(s)
	if err := c.Delete(context.Background(), "hdr"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), "dup"); err != nil {
		t.Fatal(err)
	}
	if p := s.Pending(); len(p) != 1 || p[0].Body != "h" {
		t.Fatalf("pending = %+v, want only the header-form message", p)
	}
}

func TestDeleteCancels(t *testing.T) {
	s := pushtest.New()
	defer s.Close()
	c := newClient(s)
	at := time.Now().Add(time.Hour)
	if err := c.Schedule(context.Background(), Message{Seq: "a", DeliverAt: at, Body: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Schedule(context.Background(), Message{Seq: "b", DeliverAt: at, Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if p := s.Pending(); len(p) != 1 || p[0].Seq != "b" {
		t.Fatalf("pending = %+v, want only b", p)
	}
	if d := s.Deliver(at.Add(time.Minute)); len(d) != 1 || d[0].Seq != "b" {
		t.Fatalf("delivered = %+v", d)
	}
}

func TestDelete404IsNil(t *testing.T) {
	s := pushtest.New()
	defer s.Close()
	s.Fail("DELETE", "/"+testTopic+"/gone", http.StatusNotFound, 0)
	if err := newClient(s).Delete(context.Background(), "gone"); err != nil {
		t.Fatalf("Delete 404 = %v, want nil", err)
	}
}

func TestAuthAndErrors(t *testing.T) {
	s := pushtest.New()
	defer s.Close()
	s.RequireToken("tk_secret")
	c := newClient(s)
	ctx := context.Background()

	if err := c.SendNow(ctx, "t", "b"); !errors.Is(err, ErrAuth) {
		t.Fatalf("no token: err = %v, want ErrAuth", err)
	}
	c.Token = "tk_secret"
	if err := c.SendNow(ctx, "t", "b"); err != nil {
		t.Fatalf("with token: %v", err)
	}
	reqs := s.Requests()
	if got := reqs[len(reqs)-1].Header.Get("Authorization"); got != "Bearer tk_secret" {
		t.Fatalf("Authorization = %q", got)
	}

	path := "/" + testTopic + "/x"
	s.Fail("POST", path, http.StatusUnauthorized, 1)
	if err := c.Schedule(ctx, Message{Seq: "x", DeliverAt: time.Now().Add(time.Hour)}); !errors.Is(err, ErrAuth) {
		t.Fatalf("401: err = %v, want ErrAuth", err)
	}
	s.Fail("POST", path, http.StatusForbidden, 1)
	if err := c.Schedule(ctx, Message{Seq: "x", DeliverAt: time.Now().Add(time.Hour)}); !errors.Is(err, ErrAuth) {
		t.Fatalf("403: err = %v, want ErrAuth", err)
	}
	s.Fail("POST", path, http.StatusTooManyRequests, 1)
	if err := c.Schedule(ctx, Message{Seq: "x", DeliverAt: time.Now().Add(time.Hour)}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("429: err = %v, want ErrRateLimited", err)
	}
	s.Fail("DELETE", path, http.StatusInternalServerError, 1)
	var te *TempError
	if err := c.Schedule(ctx, Message{Seq: "x", DeliverAt: time.Now().Add(time.Hour)}); !errors.As(err, &te) {
		t.Fatalf("500: err = %v, want *TempError", err)
	}
	s.Fail("POST", path, http.StatusBadRequest, 1)
	if err := c.Schedule(ctx, Message{Seq: "x", DeliverAt: time.Now().Add(time.Hour)}); err == nil || errors.As(err, &te) || errors.Is(err, ErrAuth) {
		t.Fatalf("400: err = %v, want a plain error", err)
	}

	url := s.URL
	s.Close()
	closed := Client{Server: url, Topic: testTopic}
	err := closed.Delete(ctx, "x")
	if !errors.As(err, &te) {
		t.Fatalf("closed server: err = %v, want *TempError", err)
	}
	if strings.Contains(err.Error(), testTopic) {
		t.Fatalf("error leaks topic: %v", err)
	}
}

type recordRT struct {
	mu   sync.Mutex
	urls []string
}

func (r *recordRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.urls = append(r.urls, req.Method+" "+req.URL.String())
	r.mu.Unlock()
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}, Request: req}, nil
}

func TestURLs(t *testing.T) {
	rt := &recordRT{}
	c := Client{Server: "HTTPS://ntfy.example.com/ntfy/", Topic: "ar-t", HTTP: &http.Client{Transport: rt}}
	ctx := context.Background()
	if err := c.Schedule(ctx, Message{Seq: "s1", DeliverAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := c.SendNow(ctx, "t", "b"); err != nil {
		t.Fatal(err)
	}
	if err := c.PostControl(ctx, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PollControl(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PollControl(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"DELETE https://ntfy.example.com/ntfy/ar-t/s1",
		"POST https://ntfy.example.com/ntfy/ar-t/s1",
		"POST https://ntfy.example.com/ntfy/ar-t",
		"POST https://ntfy.example.com/ntfy/ar-t-ctl",
		"GET https://ntfy.example.com/ntfy/ar-t-ctl/json?poll=1&since=all",
		"GET https://ntfy.example.com/ntfy/ar-t-ctl/json?poll=1&since=abc",
	}
	if strings.Join(rt.urls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("urls:\n%s\nwant:\n%s", strings.Join(rt.urls, "\n"), strings.Join(want, "\n"))
	}
}

func TestHeadersRoundTrip(t *testing.T) {
	s := pushtest.New()
	defer s.Close()
	c := newClient(s)
	c.Token = ""
	title := `⏰ 17:00 · fix "a, b; c"` + "\nnow 🚀"
	body := "line one\nline two · 🚀"
	acts := []Action{
		{Label: "Done", URL: s.URL + "/" + testTopic + "-ctl", Method: "POST", Body: `{"op":"done","id":"x","occ":1}`, Clear: true},
		{Label: "Snooze · 1h", URL: s.URL + "/" + testTopic + "/sqs", Method: "POST", Headers: map[string]string{"X-Delay": "1h", "X-Title": "Snoozed · " + title}, Body: body, Clear: true},
	}
	m := Message{Seq: "sq", DeliverAt: time.Now().Add(time.Hour), Title: title, Body: body, Priority: 5, Tags: []string{"alarm_clock", "x"}, Actions: acts}
	if err := c.Schedule(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	reqs := s.Requests()
	for k, v := range reqs[len(reqs)-1].Header {
		for _, s := range v {
			for _, r := range s {
				if r > 0x7e || r < 0x20 {
					t.Fatalf("header %s has unsafe byte %q", k, s)
				}
			}
		}
	}
	p := s.Pending()
	if len(p) != 1 {
		t.Fatalf("pending = %+v", p)
	}
	got := p[0]
	if got.Title != title || got.Body != body || got.Priority != 5 || strings.Join(got.Tags, ",") != "alarm_clock,x" {
		t.Fatalf("got %+v", got)
	}
	if len(got.Actions) != 2 {
		t.Fatalf("actions = %+v", got.Actions)
	}
	a := got.Actions[1]
	if a.Action != "http" || a.Label != "Snooze · 1h" || a.URL != acts[1].URL || a.Method != "POST" || a.Body != body || !a.Clear || a.Headers["X-Title"] != "Snoozed · "+title || a.Headers["X-Delay"] != "1h" {
		t.Fatalf("action = %+v", a)
	}
	if d := got.Actions[0]; d.Label != "Done" || d.Body != acts[0].Body || len(d.Headers) != 0 {
		t.Fatalf("done action = %+v", d)
	}
}

func TestSendNowDeliversImmediately(t *testing.T) {
	s := pushtest.New()
	defer s.Close()
	if err := newClient(s).SendNow(context.Background(), "Test · ok", "hello"); err != nil {
		t.Fatal(err)
	}
	if p := s.Pending(); len(p) != 0 {
		t.Fatalf("pending = %+v", p)
	}
	d := s.Delivered()
	if len(d) != 1 || d[0].Title != "Test · ok" || d[0].Body != "hello" || d[0].Seq != "" || d[0].Topic != testTopic {
		t.Fatalf("delivered = %+v", d)
	}
	r := s.Requests()[0]
	if r.Header.Get("X-Delay") != "" || r.Path != "/"+testTopic {
		t.Fatalf("request = %+v", r)
	}
}

func TestControlPollAndStream(t *testing.T) {
	s := pushtest.New()
	defer s.Close()
	c := newClient(s)
	ctx := context.Background()

	if err := c.PostControl(ctx, []byte(`{"op":"done","id":"1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.PostControl(ctx, []byte(`{"op":"done","id":"2"}`)); err != nil {
		t.Fatal(err)
	}
	all, err := c.PollControl(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Message != `{"op":"done","id":"1"}` || all[1].Message != `{"op":"done","id":"2"}` || all[0].ID == "" || all[0].ID == all[1].ID {
		t.Fatalf("poll all = %+v", all)
	}
	if r := s.Requests(); r[len(r)-1].Query != "poll=1&since=all" {
		t.Fatalf("query = %q", r[len(r)-1].Query)
	}
	since, err := c.PollControl(ctx, all[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 1 || since[0].ID != all[1].ID {
		t.Fatalf("poll since = %+v", since)
	}

	sctx, cancel := context.WithCancel(ctx)
	got := make(chan CtlMessage, 4)
	done := make(chan error, 1)
	go func() { done <- c.StreamControl(sctx, func(m CtlMessage) { got <- m }) }()
	deadline := time.Now().Add(5 * time.Second)
	for s.Subscribers(testTopic+"-ctl") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("stream never subscribed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := c.PostControl(ctx, []byte(`{"op":"done","id":"3"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-got:
		if m.Message != `{"op":"done","id":"3"}` || m.ID == "" {
			t.Fatalf("stream got %+v", m)
		}
		polled, _ := c.PollControl(ctx, all[1].ID)
		if len(polled) != 1 || polled[0].ID != m.ID {
			t.Fatalf("stream ID %q not the poll ID %+v", m.ID, polled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream got nothing")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("StreamControl after cancel = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StreamControl did not return on cancel")
	}

	// A server-closed stream returns an error. Wait for the cancelled
	// stream to unsubscribe first so the count below is the new one.
	for s.Subscribers(testTopic+"-ctl") != 0 {
		time.Sleep(5 * time.Millisecond)
	}
	go func() { done <- c.StreamControl(ctx, func(CtlMessage) {}) }()
	for s.Subscribers(testTopic+"-ctl") == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	s.CloseStreams()
	select {
	case err := <-done:
		var te *TempError
		if !errors.As(err, &te) {
			t.Fatalf("closed stream = %v, want *TempError", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StreamControl did not return when the stream closed")
	}
}
