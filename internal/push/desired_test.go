package push

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/push/pushtest"
	"awesome-reminder/internal/scheduler"
)

var (
	utc   = time.UTC
	tNow  = time.Date(2026, 11, 28, 10, 0, 0, 0, utc)
	tRule = scheduler.Rules{Loc: utc, EndOfDay: "17:30", DefaultTime: "09:00"}
)

func cfg() Config {
	return Config{Server: "https://ntfy.example.com", Topic: "ar-topic", Grace: 2 * time.Minute, Loc: utc}
}

func reminder(id, text string, due time.Time) model.Item {
	return model.Item{ID: id, Kind: model.KindReminder, Text: text, DueAt: &due, TZ: "UTC",
		Status: model.StatusOpen, CreatedAt: tNow.Add(-24 * time.Hour)}
}

func only(t *testing.T, m map[string]Message) Message {
	t.Helper()
	if len(m) != 1 {
		t.Fatalf("got %d messages, want 1: %+v", len(m), m)
	}
	for _, v := range m {
		return v
	}
	return Message{}
}

func TestSeqForStable(t *testing.T) {
	a := SeqFor("item1", scheduler.OccReminder, tNow)
	if a != SeqFor("item1", scheduler.OccReminder, tNow.Add(500*time.Millisecond)) {
		t.Error("sub-second change changed the seq")
	}
	if len(a) != 22 || !strings.HasPrefix(a, "ar") || a != strings.ToLower(a) {
		t.Errorf("seq = %q", a)
	}
	if a == SeqFor("item1", scheduler.OccReminder, tNow.Add(time.Second)) || a == SeqFor("item2", scheduler.OccReminder, tNow) {
		t.Error("different input, same seq")
	}
}

func TestDesiredReminderGrace(t *testing.T) {
	due := time.Date(2026, 11, 28, 17, 0, 0, 0, utc)
	m := only(t, Desired([]model.Item{reminder("01A", "check deploy", due)}, tRule, cfg(), tNow))
	if !m.DeliverAt.Equal(due.Add(2 * time.Minute)) {
		t.Errorf("DeliverAt = %v", m.DeliverAt)
	}
	if m.Title != "⏰ 17:00 · check deploy" || m.Body != "check deploy" || m.Priority != 4 {
		t.Errorf("m = %+v", m)
	}
	if m.Seq != SeqFor("01A", scheduler.OccReminder, due) {
		t.Errorf("seq = %s", m.Seq)
	}
	if len(m.Actions) != 2 {
		t.Fatalf("actions = %+v", m.Actions)
	}
	d, s := m.Actions[0], m.Actions[1]
	if d.Label != "Done" || d.Method != "POST" || d.URL != "https://ntfy.example.com/ar-topic-ctl" || !d.Clear ||
		d.Body != `{"op":"done","id":"01A","occ":`+strconv.FormatInt(due.Unix(), 10)+`}` {
		t.Errorf("done = %+v", d)
	}
	if s.Label != "Snooze 1h" || s.URL != "https://ntfy.example.com/ar-topic/"+m.Seq+"s" || !s.Clear ||
		s.Headers["X-Delay"] != "1h" || !strings.HasPrefix(s.Headers["X-Title"], "=?UTF-8?") || s.Body != m.Body {
		t.Errorf("snooze = %+v", s)
	}
}

func TestDesiredOtherDayTitle(t *testing.T) {
	due := time.Date(2026, 11, 30, 9, 5, 0, 0, utc)
	m := only(t, Desired([]model.Item{reminder("a", "x", due)}, tRule, cfg(), tNow))
	if m.Title != "⏰ Mon 30 Nov 09:05 · x" {
		t.Errorf("title = %q", m.Title)
	}
}

func TestDesiredWindow(t *testing.T) {
	c := cfg() // grace 2m
	inDue := tNow.Add(70*time.Hour + 58*time.Minute)
	in := reminder("in", "in", inDue)                                    // delivers at 71h exactly
	out := reminder("out", "out", tNow.Add(70*time.Hour+59*time.Minute)) // delivers 71h01m
	m := Desired([]model.Item{in, out}, tRule, c, tNow)
	if len(m) != 1 {
		t.Fatalf("got %d, want only the first", len(m))
	}
	if _, ok := m[SeqFor("in", scheduler.OccReminder, inDue)]; !ok {
		t.Error("delivery at exactly now+71h missing")
	}
}

func TestDesiredWindowEdges(t *testing.T) {
	c := cfg()
	c.Grace = 0
	soon := reminder("soon", "s", tNow.Add(10*time.Second))
	edge := reminder("edge", "e", tNow.Add(15*time.Second))
	ok := reminder("ok", "o", tNow.Add(16*time.Second))
	top := reminder("top", "t", tNow.Add(71*time.Hour))
	over := reminder("over", "v", tNow.Add(71*time.Hour+time.Second))
	m := Desired([]model.Item{soon, edge, ok, top, over}, tRule, c, tNow)
	if len(m) != 2 {
		t.Fatalf("got %+v", m)
	}
	for _, it := range []model.Item{ok, top} {
		if _, found := m[SeqFor(it.ID, scheduler.OccReminder, *it.DueAt)]; !found {
			t.Errorf("%s missing", it.ID)
		}
	}
}

func TestDesiredHidden(t *testing.T) {
	c := cfg()
	c.HideText, c.Replies, c.Token = true, true, "tk_secret"
	due := time.Date(2026, 11, 28, 17, 0, 0, 0, utc)
	it := reminder("01A", "SECRETWORD plan", due)
	it.Tags = []string{"hushtag"}
	rep := model.Item{ID: "01B", Kind: model.KindTodo, Text: "SECRETWORD reply", Reply: true, OwedTo: "SECRETPERSON",
		Status: model.StatusOpen, CreatedAt: tNow.Add(-5 * time.Hour), DueAt: &due, TZ: "UTC"}
	tr := tRule
	tr.Sweep = true
	msgs := Desired([]model.Item{it, rep}, tr, c, tNow)
	if len(msgs) < 3 {
		t.Fatalf("want reminder, nudge(s) and sweep, got %d", len(msgs))
	}
	for _, m := range msgs {
		all := m.Title + "|" + m.Body
		for _, a := range m.Actions {
			all += "|" + a.Body
			for k, v := range a.Headers {
				if k != "Authorization" {
					all += "|" + v
				}
			}
		}
		for _, bad := range []string{"SECRETWORD", "SECRETPERSON", "hushtag"} {
			if strings.Contains(all, bad) {
				t.Errorf("%q leaked in %q", bad, all)
			}
		}
		if len(m.Actions) > 0 && m.Body != "Open Awesome Reminder on a computer." {
			t.Errorf("body = %q", m.Body)
		}
	}
	if m := msgs[SeqFor("01A", scheduler.OccReminder, due)]; m.Title != "Reminder due · 17:00" {
		t.Errorf("title = %q", m.Title)
	}
}

func TestDesiredRepliesOptIn(t *testing.T) {
	rep := model.Item{ID: "01B", Kind: model.KindTodo, Text: "answer budget", Reply: true, OwedTo: "Priya", Tags: []string{"fin"},
		Status: model.StatusOpen, CreatedAt: tNow.Add(-30 * time.Hour), TZ: "UTC"}
	tr := tRule
	tr.Sweep = true
	c := cfg()
	for _, m := range Desired([]model.Item{rep}, tr, c, tNow) {
		if m.Tags[0] != "alarm_clock" {
			t.Errorf("nudge or sweep without opt-in: %+v", m)
		}
	}
	c.Replies = true
	var nudge, sweep int
	for _, m := range Desired([]model.Item{rep}, tr, c, tNow) {
		if m.Tags[0] != "speech_balloon" {
			continue
		}
		if m.Priority == 3 {
			sweep++
			if len(m.Actions) != 0 || m.Title != "1 person still waiting" || m.Body != "Priya" {
				t.Errorf("sweep = %+v", m)
			}
			continue
		}
		nudge++
		if len(m.Actions) != 2 || m.Title != "Reply owed · Priya" || !strings.HasPrefix(m.Body, "waiting ") ||
			!strings.HasSuffix(m.Body, " · answer budget #fin") {
			t.Errorf("nudge = %+v", m)
		}
	}
	if nudge == 0 || sweep == 0 {
		t.Errorf("nudges %d sweeps %d, want both with Replies on", nudge, sweep)
	}
}

func TestDesiredHighPriority(t *testing.T) {
	it := reminder("a", "x", tNow.Add(time.Hour))
	it.Priority = model.PriorityHigh
	if m := only(t, Desired([]model.Item{it}, tRule, cfg(), tNow)); m.Priority != 5 {
		t.Errorf("priority = %d", m.Priority)
	}
}

func TestDesiredTruncates(t *testing.T) {
	it := reminder("a", strings.Repeat("é", 5000), tNow.Add(time.Hour))
	m := only(t, Desired([]model.Item{it}, tRule, cfg(), tNow))
	if len(m.Title)+len(m.Body) > 3500 || !strings.HasSuffix(m.Body, "…") {
		t.Errorf("sizes title %d body %d", len(m.Title), len(m.Body))
	}
	if len(m.Actions[1].Body) > 1000 {
		t.Errorf("snooze body %d bytes", len(m.Actions[1].Body))
	}
}

func TestMessageHeadersSafe(t *testing.T) {
	text := "fix \"a, b; c\"\nnow 🚀"
	it := reminder("01A", text, tNow.Add(time.Hour))
	srv := pushtest.New()
	defer srv.Close()
	c := cfg()
	c.Server = srv.URL
	m := only(t, Desired([]model.Item{it}, tRule, c, tNow))
	if strings.ContainsAny(m.Title, "\r\n") {
		t.Fatalf("newline in title %q", m.Title)
	}
	cl := Client{Server: srv.URL, Topic: c.Topic, HTTP: srv.Client()}
	if err := cl.Schedule(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	p := srv.Pending()
	if len(p) != 1 {
		t.Fatalf("pending = %+v", p)
	}
	if p[0].Title != m.Title || p[0].Body != m.Body || len(p[0].Actions) != 2 {
		t.Errorf("round trip: %+v", p[0])
	}
	if p[0].Body != text {
		t.Errorf("body = %q, want %q", p[0].Body, text)
	}
	sn := p[0].Actions[1]
	if sn.Body != m.Body || sn.Headers["X-Delay"] != "1h" || strings.ContainsAny(sn.Headers["X-Title"], "\r\n🚀") {
		t.Errorf("snooze = %+v", sn)
	}
}

func TestDesiredTokenInActions(t *testing.T) {
	it := reminder("a", "x", tNow.Add(time.Hour))
	c := cfg()
	for _, a := range only(t, Desired([]model.Item{it}, tRule, c, tNow)).Actions {
		if _, ok := a.Headers["Authorization"]; ok {
			t.Errorf("%s has Authorization without a token", a.Label)
		}
	}
	c.Token = "tk_x"
	for _, a := range only(t, Desired([]model.Item{it}, tRule, c, tNow)).Actions {
		if a.Headers["Authorization"] != "Bearer tk_x" {
			t.Errorf("%s headers = %v", a.Label, a.Headers)
		}
	}
}

func TestHashCoversDeliverAt(t *testing.T) {
	m := Message{Seq: "s", Title: "t", DeliverAt: tNow}
	n := m
	n.DeliverAt = tNow.Add(time.Minute)
	if Hash(m) == Hash(n) || Hash(m) != Hash(m) {
		t.Error("hash must change with DeliverAt and be stable")
	}
}
