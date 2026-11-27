package atlas_test

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"awesome-reminder/internal/cloudsync"
	"awesome-reminder/internal/cloudsync/atlas"
	"awesome-reminder/internal/cloudsync/cloudsynctest"
	"awesome-reminder/internal/core"
	"awesome-reminder/internal/model"
	"awesome-reminder/internal/store"
)

// The integration tests run against a real MongoDB when AWESOME_TEST_MONGO_URI
// is set (database: AWESOME_TEST_MONGO_DB, default awesome_reminder). Each
// test uses its own collection prefix test_<ulid>_ and drops its collections
// afterwards.

func testEnv(t *testing.T) (uri, db string) {
	t.Helper()
	uri = os.Getenv("AWESOME_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("AWESOME_TEST_MONGO_URI not set")
	}
	db = os.Getenv("AWESOME_TEST_MONGO_DB")
	if db == "" {
		db = atlas.Database
	}
	return uri, db
}

func newPrefix() string { return "test_" + ulid.Make().String() + "_" }

// openAt opens a Syncer on the test database with the given prefix and
// registers its cleanup (Close, then drop the prefixed collections).
func openAt(t *testing.T, prefix string) *atlas.Syncer {
	t.Helper()
	uri, db := testEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := atlas.Open(ctx, uri, atlas.Options{Database: db, Prefix: prefix})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	dropAfter(t, uri, db, prefix)
	return s
}

func dropAfter(t *testing.T, uri, db, prefix string) {
	t.Helper()
	t.Cleanup(func() {
		c, err := mongo.Connect(options.Client().ApplyURI(uri))
		if err != nil {
			return
		}
		defer c.Disconnect(context.Background())
		for _, name := range []string{"items", "attachments", "settings"} {
			_ = c.Database(db).Collection(prefix + name).Drop(context.Background())
		}
	})
}

// rawClient returns a plain driver client for poking at documents directly.
func rawClient(t *testing.T) (*mongo.Client, string, string) {
	t.Helper()
	uri, db := testEnv(t)
	c, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Disconnect(context.Background()) })
	return c, db, newPrefix()
}

func TestConformance(t *testing.T) {
	uri, db := testEnv(t)
	raw, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Disconnect(context.Background()) })
	prefixes := map[cloudsync.Syncer]string{}

	cloudsynctest.Run(t, func(t *testing.T) cloudsync.Syncer {
		p := newPrefix()
		s := openAt(t, p)
		prefixes[s] = p
		return s
	}, cloudsynctest.WithRawInsert(func(t *testing.T, s cloudsync.Syncer, collection string, doc map[string]any) {
		coll := raw.Database(db).Collection(prefixes[s] + collection)
		_, err := coll.UpdateOne(context.Background(), bson.D{{Key: "_id", Value: doc["_id"]}}, bson.D{
			{Key: "$set", Value: doc},
			{Key: "$currentDate", Value: bson.D{{Key: "_srv", Value: bson.D{{Key: "$type", Value: "date"}}}}},
		}, options.UpdateOne().SetUpsert(true))
		if err != nil {
			t.Fatalf("raw insert: %v", err)
		}
	}))
}

func TestPushKeepsUnknownFields(t *testing.T) {
	c, db, prefix := rawClient(t)
	s := openAt(t, prefix)
	ctx := context.Background()
	base := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	it := model.Item{ID: "01JD00000000000000000000AA", Kind: model.KindTodo, Text: "one", Tags: []string{"inbox"},
		Status: model.StatusOpen, CreatedAt: base, UpdatedAt: base, UpdatedBy: "A"}
	if err := s.PushItem(ctx, it); err != nil {
		t.Fatal(err)
	}
	coll := c.Database(db).Collection(prefix + "items")
	if _, err := coll.UpdateOne(ctx, bson.D{{Key: "_id", Value: it.ID}}, bson.D{{Key: "$set", Value: bson.D{{Key: "extra", Value: "from a newer app"}}}}); err != nil {
		t.Fatal(err)
	}
	it.Text, it.UpdatedAt = "two", base.Add(time.Second)
	if err := s.PushItem(ctx, it); err != nil {
		t.Fatal(err)
	}
	var got bson.M
	if err := coll.FindOne(ctx, bson.D{{Key: "_id", Value: it.ID}}).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["extra"] != "from a newer app" || got["text"] != "two" {
		t.Errorf("document after push: %v", got)
	}
}

func TestOpenErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// Nothing listens on port 1; the context ends the wait.
	s, err := atlas.Open(ctx, "mongodb://u:secret@127.0.0.1:1/", atlas.Options{})
	if err == nil {
		s.Close(ctx)
		t.Fatal("expected an error")
	}
	if got := err.Error(); strings.Contains(got, "secret") {
		t.Errorf("error leaks the password: %v", got)
	}
}

type machine struct {
	c *core.Core
	e *cloudsync.Engine
	s *store.Store
}

func newMachine(t *testing.T, s cloudsync.Syncer) *machine {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := core.New(st, time.Now, time.UTC)
	return &machine{c: c, s: st, e: cloudsync.NewEngine(c, s, cloudsync.Options{Host: "localhost"})}
}

func (m *machine) sync(t *testing.T) {
	t.Helper()
	if err := m.e.RunOnce(context.Background(), true); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

func (m *machine) open(t *testing.T) map[string]model.Item {
	t.Helper()
	items, err := m.s.ListOpen()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]model.Item{}
	for _, it := range items {
		out[it.ID] = it
	}
	return out
}

func TestTwoDevicesAgainstMongo(t *testing.T) {
	prefix := newPrefix()
	sa, sb := openAt(t, prefix), openAt(t, prefix)
	a, b := newMachine(t, sa), newMachine(t, sb)
	for _, in := range []string{"from a 1", "from a 2", "from a 3"} {
		if _, err := a.c.Capture(in); err != nil {
			t.Fatal(err)
		}
	}
	for _, in := range []string{"from b 1", "from b 2"} {
		if _, err := b.c.Capture(in); err != nil {
			t.Fatal(err)
		}
	}
	a.sync(t)
	b.sync(t)
	a.sync(t)
	ga, gb := a.open(t), b.open(t)
	if len(ga) != 5 || !reflect.DeepEqual(ga, gb) {
		t.Fatalf("not converged:\nA %v\nB %v", ga, gb)
	}
}
