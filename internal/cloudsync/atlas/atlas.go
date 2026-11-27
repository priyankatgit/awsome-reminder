// Package atlas is the MongoDB Atlas cloudsync.Syncer, over the user's own
// cluster. It touches only the awesome_reminder database (collections items,
// attachments and settings) and never logs or returns the connection string.
package atlas

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"awesome-reminder/internal/cloudsync"
	"awesome-reminder/internal/model"
)

// Database is the only database the app uses.
const Database = "awesome_reminder"

// Options override the database and a collection-name prefix. The zero value
// is production; only tests set them.
type Options struct{ Database, Prefix string }

// Syncer implements cloudsync.Syncer over MongoDB.
type Syncer struct {
	client            *mongo.Client
	db                *mongo.Database
	dbName            string
	host              string
	items, atts, sets *mongo.Collection
}

var _ cloudsync.Syncer = (*Syncer)(nil)

// Open connects to the cluster, checks it with a ping and creates the pull
// indexes. A URI that is not mongodb:// or mongodb+srv:// gives ErrBadURI
// without touching the network. Errors never contain the password.
func Open(ctx context.Context, uri string, o Options) (*Syncer, error) {
	if _, ok := cutScheme(uri); !ok {
		return nil, ErrBadURI
	}
	if o.Database == "" {
		o.Database = Database
	}
	opts := options.Client().ApplyURI(uri).
		SetMaxPoolSize(2).SetMinPoolSize(0).
		SetMaxConnIdleTime(90 * time.Second).
		SetHeartbeatInterval(60 * time.Second).
		SetServerSelectionTimeout(10 * time.Second).
		SetConnectTimeout(10 * time.Second).
		SetAppName("awesome-reminder")
	if err := opts.Validate(); err != nil {
		return nil, redactedError{fmt.Errorf("atlas: %w", err)}
	}
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, redactedError{fmt.Errorf("atlas: %w", err)}
	}
	db := client.Database(o.Database)
	s := &Syncer{
		client: client, db: db, dbName: o.Database, host: Host(uri),
		items: db.Collection(o.Prefix + "items"), atts: db.Collection(o.Prefix + "attachments"),
		sets: db.Collection(o.Prefix + "settings"),
	}
	if err := s.init(ctx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, redactedError{err}
	}
	return s, nil
}

func (s *Syncer) init(ctx context.Context) error {
	if err := s.client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("atlas ping: %w", err)
	}
	for _, c := range []*mongo.Collection{s.items, s.atts} {
		_, err := c.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "_srv", Value: 1}, {Key: "_id", Value: 1}}})
		if err != nil {
			return fmt.Errorf("atlas index: %w", err)
		}
	}
	return nil
}

// push is the last-writer-wins conditional upsert: it matches only a missing
// document or an older stamp. Otherwise the upsert's insert collides on _id,
// which means the remote is equal or newer and nothing changes.
func push(ctx context.Context, c *mongo.Collection, id string, at time.Time, by string, set bson.D, unset ...string) error {
	t := dt(at)
	filter := bson.D{{Key: "_id", Value: id}, {Key: "$or", Value: bson.A{
		bson.D{{Key: "updated_at", Value: bson.D{{Key: "$lt", Value: t}}}},
		bson.D{{Key: "updated_at", Value: t}, {Key: "updated_by", Value: bson.D{{Key: "$lt", Value: by}}}},
	}}}
	update := bson.D{
		{Key: "$set", Value: set},
		{Key: "$currentDate", Value: bson.D{{Key: "_srv", Value: bson.D{{Key: "$type", Value: "date"}}}}},
	}
	if len(unset) > 0 {
		u := bson.D{}
		for _, k := range unset {
			u = append(u, bson.E{Key: k, Value: ""})
		}
		update = append(update, bson.E{Key: "$unset", Value: u})
	}
	_, err := c.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return err
	}
	return nil
}

// PushItem implements cloudsync.Syncer.
func (s *Syncer) PushItem(ctx context.Context, it model.Item) error {
	return push(ctx, s.items, it.ID, it.UpdatedAt, it.UpdatedBy, itemSet(it))
}

// PushAttachment implements cloudsync.Syncer.
func (s *Syncer) PushAttachment(ctx context.Context, a model.Attachment, data []byte) error {
	if data == nil {
		return push(ctx, s.atts, a.ID, a.UpdatedAt, a.UpdatedBy, attachmentSet(a, nil), "data")
	}
	return push(ctx, s.atts, a.ID, a.UpdatedAt, a.UpdatedBy, attachmentSet(a, data))
}

// PushShared implements cloudsync.Syncer.
func (s *Syncer) PushShared(ctx context.Context, d cloudsync.SharedDoc) error {
	return push(ctx, s.sets, "settings", d.UpdatedAt, d.UpdatedBy, sharedSet(d))
}

// page reads up to limit documents after the watermark in (_srv, _id) order.
// Documents are returned as raw BSON so a malformed one never fails the page;
// the watermark moves past every document read.
func page(ctx context.Context, c *mongo.Collection, after cloudsync.Watermark, limit int, each func(bson.Raw)) (cloudsync.Watermark, error) {
	filter := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "_srv", Value: bson.D{{Key: "$gt", Value: dt(after.T)}}}},
		bson.D{{Key: "_srv", Value: dt(after.T)}, {Key: "_id", Value: bson.D{{Key: "$gt", Value: after.ID}}}},
	}}}
	cur, err := c.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "_srv", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return after, err
	}
	defer cur.Close(context.Background())
	wm := after
	for cur.Next(ctx) {
		raw := cur.Current
		each(raw)
		if t, ok := rTime(raw, "_srv"); ok {
			wm = cloudsync.Watermark{T: t, ID: rID(raw)}
		}
	}
	if err := cur.Err(); err != nil {
		return after, err
	}
	return wm, nil
}

// PullItems implements cloudsync.Syncer.
func (s *Syncer) PullItems(ctx context.Context, after cloudsync.Watermark, limit int) ([]model.Item, cloudsync.Watermark, error) {
	var out []model.Item
	wm, err := page(ctx, s.items, after, limit, func(r bson.Raw) { out = append(out, decodeItem(r)) })
	if err != nil {
		return nil, after, err
	}
	return out, wm, nil
}

// PullAttachments implements cloudsync.Syncer.
func (s *Syncer) PullAttachments(ctx context.Context, after cloudsync.Watermark, limit int) ([]cloudsync.RemoteAttachment, cloudsync.Watermark, error) {
	var out []cloudsync.RemoteAttachment
	wm, err := page(ctx, s.atts, after, limit, func(r bson.Raw) { out = append(out, decodeAttachment(r)) })
	if err != nil {
		return nil, after, err
	}
	return out, wm, nil
}

// PullShared implements cloudsync.Syncer.
func (s *Syncer) PullShared(ctx context.Context) (*cloudsync.SharedDoc, error) {
	raw, err := s.sets.FindOne(ctx, bson.D{{Key: "_id", Value: "settings"}}).Raw()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeShared(raw), nil
}

// ServerTime implements cloudsync.Syncer (hello.localTime).
func (s *Syncer) ServerTime(ctx context.Context) (time.Time, error) {
	raw, err := s.db.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Raw()
	if err != nil {
		return time.Time{}, err
	}
	t, ok := rTime(raw, "localTime")
	if !ok {
		return time.Time{}, errors.New("atlas: hello has no localTime")
	}
	return t, nil
}

// AttachmentBytes implements cloudsync.Syncer.
func (s *Syncer) AttachmentBytes(ctx context.Context) (int64, error) {
	cur, err := s.atts.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.D{{Key: "deleted", Value: false}}}},
		{{Key: "$group", Value: bson.D{{Key: "_id", Value: nil}, {Key: "total", Value: bson.D{{Key: "$sum", Value: "$bytes"}}}}}},
	})
	if err != nil {
		return 0, err
	}
	defer cur.Close(context.Background())
	var total int64
	if cur.Next(ctx) {
		total, _ = cur.Current.Lookup("total").AsInt64OK()
	}
	return total, cur.Err()
}

// resource is a privilege resource: a database (and collection) or the
// cluster.
type resource struct {
	DB         string
	Collection *string // nil when the resource has no collection key
	Cluster    bool
}

// scopeTooBroad reports whether the privileges reach a database other than
// db (admin, local and config do not count). A resource with an empty db and
// a collection key means every database.
func scopeTooBroad(resources []resource, db string) bool {
	for _, r := range resources {
		if r.Cluster || (r.DB == "" && r.Collection == nil) {
			continue
		}
		switch r.DB {
		case db, "admin", "local", "config":
		default:
			return true // includes "" with a collection: any database
		}
	}
	return false
}

func parseResources(raw bson.Raw) []resource {
	arr, ok := raw.Lookup("authInfo", "authenticatedUserPrivileges").ArrayOK()
	if !ok {
		return nil
	}
	vals, _ := arr.Values()
	var out []resource
	for _, v := range vals {
		doc, ok := v.DocumentOK()
		if !ok {
			continue
		}
		res, ok := doc.Lookup("resource").DocumentOK()
		if !ok {
			continue
		}
		r := resource{DB: rStr(res, "db")}
		if c, ok := res.Lookup("collection").StringValueOK(); ok {
			r.Collection = &c
		}
		if cl, ok := res.Lookup("cluster").BooleanOK(); ok {
			r.Cluster = cl
		}
		out = append(out, r)
	}
	return out
}

// ScopeTooBroad implements cloudsync.Syncer.
func (s *Syncer) ScopeTooBroad(ctx context.Context) (bool, error) {
	raw, err := s.db.RunCommand(ctx, bson.D{{Key: "connectionStatus", Value: 1}, {Key: "showPrivileges", Value: true}}).Raw()
	if err != nil {
		return false, err
	}
	return scopeTooBroad(parseResources(raw), s.dbName), nil
}

// Classify implements cloudsync.Syncer.
func (s *Syncer) Classify(err error) (cloudsync.State, string) { return Classify(err, s.host) }

// Close implements cloudsync.Syncer.
func (s *Syncer) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }
