package push

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"mime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"awesome-reminder/internal/model"
	"awesome-reminder/internal/scheduler"
	"awesome-reminder/internal/settings"
)

const (
	// maxMessageBytes keeps title plus body well under ntfy's 4 KiB limit.
	maxMessageBytes = 3500
	// maxSnoozeBody bounds the body repeated inside the Snooze action, so the
	// X-Actions header stays reasonable.
	maxSnoozeBody = 1000

	hiddenBody = "Open Awesome Reminder on a computer."
)

// Config is what Desired needs besides the items.
type Config struct {
	Server, Topic, Token string
	Replies, HideText    bool
	Grace                time.Duration
	Loc                  *time.Location
}

// ConfigFrom builds a Config from the settings, the access token ("" when
// none) and the zone for items without a loadable TZ.
func ConfigFrom(s settings.Settings, token string, loc *time.Location) Config {
	if loc == nil {
		loc = time.Local
	}
	return Config{Server: s.PushServer, Topic: s.PushTopic, Token: token,
		Replies: s.PushReplies, HideText: s.PushHideText, Grace: s.PushGrace, Loc: loc}
}

var seqEnc = base32.StdEncoding.WithPadding(base32.NoPadding)

// SeqFor is the sequence ID of an occurrence: "ar" + 20 lowercase base32
// characters of sha256(itemID|kind|unix seconds). Sweep: SeqFor("sweep",
// scheduler.OccSweep, at).
func SeqFor(itemID, kind string, occ time.Time) string {
	sum := sha256.Sum256([]byte(itemID + "|" + kind + "|" + strconv.FormatInt(occ.Unix(), 10)))
	return "ar" + strings.ToLower(seqEnc.EncodeToString(sum[:]))[:20]
}

// Desired is the set of messages that should be pending on the server at
// now, keyed by Seq. Each also has a snooze twin "<seq>s" that is only ever
// published by the phone, so it is not part of the set.
func Desired(items []model.Item, r scheduler.Rules, c Config, now time.Time) map[string]Message {
	lo, hi := now.Add(15*time.Second), now.Add(71*time.Hour)
	byID := make(map[string]model.Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	out := map[string]Message{}
	for _, occ := range scheduler.Upcoming(items, r, lo.Add(-c.Grace), hi.Add(-c.Grace)) {
		// A snoozed reply with a due time is pushed like a reminder even
		// with replies off (its Kind stays OccNudge, so its card pairs).
		asReminder := occ.Kind == scheduler.OccReminder || (occ.Snooze && !occ.Due.IsZero() && !c.Replies)
		if !asReminder && !c.Replies {
			continue
		}
		deliver := occ.At.Add(c.Grace)
		if !deliver.After(lo) || deliver.After(hi) {
			continue
		}
		var m Message
		if occ.Kind == scheduler.OccSweep {
			m = buildSweep(occ, c)
			m.Seq = SeqFor("sweep", scheduler.OccSweep, occ.At)
		} else {
			it, ok := byID[occ.ItemID]
			if !ok {
				continue
			}
			m = buildItem(it, occ, c, deliver, asReminder)
			m.Seq = SeqFor(occ.ItemID, occ.Kind, occ.At)
			m.Actions = actions(c, occ, m)
		}
		m.DeliverAt = deliver
		out[m.Seq] = m
	}
	return out
}

func buildSweep(occ scheduler.Occurrence, c Config) Message {
	n := len(occ.People)
	title := fmt.Sprintf("%d people still waiting", n)
	if n == 1 {
		title = "1 person still waiting"
	}
	body := fmt.Sprintf("%d replies owed", n)
	if !c.HideText {
		body = clip(oneLine(strings.Join(occ.People, ", ")), maxMessageBytes-len(title))
	}
	return Message{Title: title, Body: body, Priority: 3, Tags: []string{"speech_balloon"}}
}

// buildItem renders an item's push; asReminder picks the reminder style.
func buildItem(it model.Item, occ scheduler.Occurrence, c Config, deliver time.Time, asReminder bool) Message {
	loc := c.Loc
	if l, err := time.LoadLocation(it.TZ); err == nil && it.TZ != "" {
		loc = l
	}
	at := occ.At.In(loc)
	clock := at.Format("15:04")
	m := Message{Priority: 4}
	if it.Priority == model.PriorityHigh {
		m.Priority = 5
	}
	text := it.Text
	for _, t := range it.Tags {
		text += " #" + t
	}
	text = strings.TrimSpace(text)
	var prefix string // before the text in the body
	if asReminder {
		m.Tags = []string{"alarm_clock"}
		// The phone shows the push at delivery, so the date is only needed
		// when delivery falls on another local day (never "now": the
		// message must not change at midnight).
		stamp := clock
		dy, dm, dd := deliver.In(loc).Date()
		if y, mo, d := at.Date(); y != dy || mo != dm || d != dd {
			stamp = at.Format("Mon 2 Jan ") + clock
		}
		if c.HideText {
			m.Title = "Reminder due · " + clock
		} else {
			m.Title = "⏰ " + stamp + " · " + oneLine(it.Text)
		}
	} else {
		m.Tags = []string{"speech_balloon"}
		who := it.OwedTo
		if strings.TrimSpace(who) == "" {
			who = "Someone"
		}
		m.Title = "Reply owed · " + oneLine(who)
		prefix = "waiting " + age(occ.At.Sub(it.CreatedAt)) + " · "
		if c.HideText {
			m.Title = "Reply owed"
		}
	}
	if c.HideText {
		m.Body = hiddenBody
		return m
	}
	m.Title = clip(m.Title, 500)
	m.Body = clip(prefix+text, maxMessageBytes-len(m.Title))
	return m
}

func actions(c Config, occ scheduler.Occurrence, m Message) []Action {
	base := Client{Server: c.Server, Topic: c.Topic}
	due := occ.Due
	if due.IsZero() {
		due = occ.At
	}
	itemID := occ.ItemID
	done := Action{
		Label: "Done", Method: "POST", URL: base.url(c.Topic + "-ctl"), Clear: true,
		Body: fmt.Sprintf(`{"op":"done","id":%s,"occ":%d}`, jsonString(itemID), due.Unix()),
	}
	if c.Token != "" {
		done.Headers = map[string]string{"Authorization": "Bearer " + c.Token}
	}
	// The snoozed re-buzz carries only Done (no second snooze).
	rebuzz, _ := actionsJSON([]Action{done}) // plain strings: can't fail
	snooze := Action{
		Label: "Snooze 1h", Method: "POST", URL: base.url(c.Topic, m.Seq+"s"), Clear: true,
		Body: clip(m.Body, maxSnoozeBody),
		Headers: map[string]string{"X-Delay": "1h", "X-Title": mime.BEncoding.Encode("UTF-8", oneLine("Snoozed · "+m.Title)),
			"X-Actions": rebuzz},
	}
	if c.Token != "" {
		snooze.Headers["Authorization"] = "Bearer " + c.Token
	}
	return []Action{done, snooze}
}

// Hash identifies a message's content, DeliverAt included.
func Hash(m Message) string {
	h := sha256.New()
	w := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	w(m.Seq)
	w(strconv.FormatInt(m.DeliverAt.UnixNano(), 10))
	w(m.Title)
	w(m.Body)
	w(strconv.Itoa(m.Priority))
	w(strings.Join(m.Tags, ","))
	w(strconv.Itoa(len(m.Actions)))
	for _, a := range m.Actions {
		w(a.Label)
		w(a.URL)
		w(a.Method)
		w(a.Body)
		w(strconv.FormatBool(a.Clear))
		keys := make([]string, 0, len(a.Headers))
		for k := range a.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			w(k)
			w(a.Headers[k])
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// clip cuts s to at most max bytes on a rune boundary, ending in "…" when cut.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	const ell = "…"
	cut := max - len(ell)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ell
}

func age(d time.Duration) string {
	switch {
	case d < time.Hour:
		if d < time.Minute {
			d = time.Minute
		}
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// oneLine replaces every control character (line breaks included, which
// headers can't hold; net/http rejects the rest) with a space.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}
