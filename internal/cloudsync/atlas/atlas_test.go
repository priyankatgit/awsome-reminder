package atlas

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/topology"

	"awesome-reminder/internal/cloudsync"
	"awesome-reminder/internal/model"
)

const password = "p%40ss"

func TestHostAndRedact(t *testing.T) {
	uri := "mongodb+srv://u:" + password + "@cluster0.ab.mongodb.net/x?retryWrites=true"
	if got := Host(uri); got != "cluster0.ab.mongodb.net" {
		t.Errorf("Host = %q", got)
	}
	red := Redact(uri)
	if strings.Contains(red, password) || !strings.Contains(red, "••••") || !strings.Contains(red, "cluster0.ab.mongodb.net") {
		t.Errorf("Redact = %q", red)
	}
	for in, want := range map[string]string{
		"mongodb://localhost:27017":          "localhost",
		"mongodb://a:b@h1:1,h2:2/db?x=y":     "h1",
		"postgres://u:p@host/db":             "",
		"":                                   "",
		"mongodb+srv://":                     "",
		"mongodb+srv://u:p@@host.example/db": "host.example",
	} {
		if got := Host(in); got != want {
			t.Errorf("Host(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Redact("mongodb://localhost:27017"); got != "mongodb://localhost:27017" {
		t.Errorf("no-credential URI changed: %q", got)
	}
}

func TestOpenRejectsBadScheme(t *testing.T) {
	for _, uri := range []string{"postgres://u:p@h/db", "", "http://example.com"} {
		start := time.Now()
		s, err := Open(context.Background(), uri, Options{})
		if s != nil || !errors.Is(err, ErrBadURI) {
			t.Errorf("Open(%q) = %v, %v", uri, s, err)
		}
		if d := time.Since(start); d > 100*time.Millisecond {
			t.Errorf("Open(%q) took %v", uri, d)
		}
	}
}

func TestClassify(t *testing.T) {
	selTimeout := topology.ServerSelectionError{Wrapped: context.DeadlineExceeded}
	const unreachable = "Can't reach your Atlas cluster. It may be paused (resume it in Atlas), or this network's IP isn't on the Atlas access list."
	tests := []struct {
		name  string
		err   error
		host  string
		state cloudsync.State
		text  string
	}{
		{"bad uri", fmt.Errorf("open: %w", ErrBadURI), "", cloudsync.StateError, ErrBadURI.Error()},
		{"auth", mongo.CommandError{Code: 18, Message: "bad creds"}, "", cloudsync.StateError, "Atlas rejected the username or password."},
		{"unauthorized", mongo.CommandError{Code: 13}, "", cloudsync.StateError, "This database user can't read and write awesome_reminder."},
		{"atlas 8000 authorization text", mongo.CommandError{Code: 8000, Message: "user is not allowed to do action [find]"}, "", cloudsync.StateError, "This database user can't read and write awesome_reminder."},
		{"atlas 8000 quota", mongo.CommandError{Code: 8000, Message: "you are over your space quota"}, "", cloudsync.StateError, ""},
		{"dns", &net.DNSError{Err: "no such host"}, "", cloudsync.StateOffline, "No connection to Atlas."},
		{"deadline", fmt.Errorf("pull: %w", context.DeadlineExceeded), "", cloudsync.StateOffline, "No connection to Atlas."},
		{"op error", &net.OpError{Op: "dial", Err: errors.New("refused")}, "", cloudsync.StateOffline, "No connection to Atlas."},
		{"eof", io.EOF, "", cloudsync.StateOffline, "No connection to Atlas."},
		{"selection timeout, resolves", selTimeout, "localhost", cloudsync.StateError, unreachable},
		{"selection timeout, no host", selTimeout, "", cloudsync.StateOffline, "No connection to Atlas."},
		{"selection timeout, unresolvable", selTimeout, "no-such-host.invalid", cloudsync.StateOffline, "No connection to Atlas."},
		{"other, redacted", errors.New("boom mongodb+srv://u:" + password + "@c.mongodb.net/x failed"), "", cloudsync.StateError, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, text := Classify(tc.err, tc.host)
			if st != tc.state {
				t.Errorf("state = %v, want %v", st, tc.state)
			}
			if tc.text != "" && text != tc.text {
				t.Errorf("text = %q, want %q", text, tc.text)
			}
			if strings.Contains(text, password) {
				t.Errorf("text leaks the password: %q", text)
			}
		})
	}
	if _, text := Classify(mongo.CommandError{Code: 8000, Message: "you are over your space quota"}, ""); !strings.Contains(text, "over your space quota") {
		t.Errorf("8000 quota text lost: %q", text)
	}
	if _, text := Classify(errors.New("boom mongodb+srv://u:"+password+"@c.mongodb.net/x failed"), ""); !strings.Contains(text, "boom") {
		t.Errorf("other error lost its text: %q", text)
	}
	s := &Syncer{host: "localhost"}
	if st, _ := s.Classify(io.EOF); st != cloudsync.StateOffline {
		t.Errorf("method: %v", st)
	}
}

func TestScopeTooBroad(t *testing.T) {
	coll := func(s string) *string { return &s }
	const db = Database
	tests := []struct {
		name string
		res  []resource
		want bool
	}{
		{"only sync db", []resource{{DB: db, Collection: coll("")}, {DB: db, Collection: coll("items")}}, false},
		{"plus admin and local", []resource{{DB: db, Collection: coll("")}, {DB: "admin", Collection: coll("")}, {DB: "local", Collection: coll("")}, {DB: "config", Collection: coll("")}}, false},
		{"any database", []resource{{DB: "", Collection: coll("")}}, true},
		{"other db", []resource{{DB: db, Collection: coll("")}, {DB: "otherproj", Collection: coll("")}}, true},
		{"other db without collection key", []resource{{DB: "otherproj"}}, true},
		{"cluster only", []resource{{Cluster: true}}, false},
		{"cluster plus sync db", []resource{{Cluster: true}, {DB: db, Collection: coll("")}}, false},
		{"none", nil, false},
	}
	for _, tc := range tests {
		if got := scopeTooBroad(tc.res, db); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestParseResources(t *testing.T) {
	raw, err := bson.Marshal(bson.D{{Key: "authInfo", Value: bson.D{{Key: "authenticatedUserPrivileges", Value: bson.A{
		bson.D{{Key: "resource", Value: bson.D{{Key: "cluster", Value: true}}}, {Key: "actions", Value: bson.A{"x"}}},
		bson.D{{Key: "resource", Value: bson.D{{Key: "db", Value: "other"}, {Key: "collection", Value: ""}}}},
	}}}}})
	if err != nil {
		t.Fatal(err)
	}
	res := parseResources(raw)
	if len(res) != 2 || !res[0].Cluster || res[1].DB != "other" || res[1].Collection == nil {
		t.Errorf("parsed %+v", res)
	}
	if !scopeTooBroad(res, Database) {
		t.Error("other db should be too broad")
	}
}

func TestDocMapping(t *testing.T) {
	due := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	created := time.Date(2026, 11, 20, 9, 30, 15, 123_456_789, time.UTC) // sub-ms is dropped
	full := model.Item{
		ID: "01JD0000000000000000000001", Kind: model.KindReminder, Text: "a.b $HOME {\"$gt\":1}", Tags: []string{"inbox", "x"},
		DueAt: &due, TZ: "Europe/Berlin", RRule: "FREQ=DAILY", LeadTimes: []time.Duration{time.Hour, 15 * time.Minute},
		Status: model.StatusOpen, DoneAt: &due, SnoozeUntil: &due, CreatedAt: created, UpdatedAt: created, UpdatedBy: "01JDDEV",
		Priority: "high", Description: "![](att:1)",
	}
	nilDue := full
	nilDue.DueAt, nilDue.DoneAt, nilDue.SnoozeUntil, nilDue.LeadTimes = nil, nil, nil, nil
	emptyLeads := nilDue
	emptyLeads.LeadTimes = []time.Duration{}

	for name, it := range map[string]model.Item{"full": full, "nil due and leads": nilDue, "empty leads": emptyLeads} {
		raw, err := bson.Marshal(itemSet(it))
		if err != nil {
			t.Fatal(name, err)
		}
		got := decodeItem(withID(t, raw, it.ID))
		want := it
		want.CreatedAt = created.Truncate(time.Millisecond)
		want.UpdatedAt = created.Truncate(time.Millisecond)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", name, got, want)
		}
	}
	if emptyLeads.LeadTimes == nil || nilDue.LeadTimes != nil {
		t.Fatal("test setup")
	}
}

func TestAttachmentMapping(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 30, 15, 0, time.UTC)
	a := model.Attachment{ID: "01JA", ItemID: "01JI", Mime: "image/webp", SHA256: "ab", Width: 3, Height: 4, Bytes: 5,
		CreatedAt: at, UpdatedAt: at, UpdatedBy: "D"}
	raw, err := bson.Marshal(attachmentSet(a, []byte{1, 2, 3}))
	if err != nil {
		t.Fatal(err)
	}
	got := decodeAttachment(withID(t, raw, a.ID))
	if !reflect.DeepEqual(got.Attachment, a) || !reflect.DeepEqual(got.Data, []byte{1, 2, 3}) {
		t.Errorf("got %+v", got)
	}
	// A tombstone carries no data.
	a.Deleted = true
	raw, _ = bson.Marshal(attachmentSet(a, nil))
	got = decodeAttachment(withID(t, raw, a.ID))
	if got.Data != nil || !got.Deleted {
		t.Errorf("tombstone: %+v", got)
	}
}

func TestLenientDecode(t *testing.T) {
	raw, err := bson.Marshal(bson.D{{Key: "_id", Value: "x"}, {Key: "text", Value: 42}, {Key: "tags", Value: "no"},
		{Key: "updated_at", Value: "yesterday"}, {Key: "lead_times", Value: "?"}})
	if err != nil {
		t.Fatal(err)
	}
	it := decodeItem(bson.Raw(raw))
	if it.ID != "x" || it.Text != "" || it.Tags != nil || !it.UpdatedAt.IsZero() || it.LeadTimes != nil {
		t.Errorf("lenient: %+v", it)
	}
}

// withID adds _id to a $set-style document, as the server would on insert.
func withID(t *testing.T, set []byte, id string) bson.Raw {
	t.Helper()
	var d bson.D
	if err := bson.Unmarshal(set, &d); err != nil {
		t.Fatal(err)
	}
	d = append(bson.D{{Key: "_id", Value: id}}, d...)
	raw, err := bson.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestOpenSRVLookupFailureIsOffline(t *testing.T) {
	old := lookupSRV
	t.Cleanup(func() { lookupSRV = old })
	lookupSRV = func(context.Context, string) error {
		return &net.DNSError{Err: "no such host", Name: "_mongodb._tcp.c.invalid"}
	}
	uri := "mongodb+srv://u:" + password + "@c.invalid/x"
	s, err := Open(context.Background(), uri, Options{})
	if s != nil || err == nil {
		t.Fatalf("Open = %v, %v", s, err)
	}
	if strings.Contains(err.Error(), password) {
		t.Errorf("leaks password: %v", err)
	}
	if st, text := Classify(err, Host(uri)); st != cloudsync.StateOffline || text != "No connection to Atlas." {
		t.Errorf("classified %v %q (%v)", st, text, err)
	}
}

func TestClientOptions(t *testing.T) {
	o := clientOptions("mongodb+srv://user:" + password + "@cluster0.example.mongodb.net/")
	for name, got := range map[string]*time.Duration{
		"operation timeout": o.Timeout, "server selection": o.ServerSelectionTimeout,
		"connect": o.ConnectTimeout, "heartbeat": o.HeartbeatInterval, "idle": o.MaxConnIdleTime,
	} {
		if got == nil || *got <= 0 {
			t.Errorf("%s not set: %v", name, got)
		}
	}
	if o.Timeout != nil && *o.Timeout != 30*time.Second {
		t.Errorf("operation timeout %v, want 30s", *o.Timeout)
	}
	if o.MaxPoolSize == nil || *o.MaxPoolSize != 2 {
		t.Errorf("pool size %v", o.MaxPoolSize)
	}
}
