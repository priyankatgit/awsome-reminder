package atlas

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"awesome-reminder/internal/cloudsync"
	"awesome-reminder/internal/model"
	"awesome-reminder/internal/settings"
)

// Documents are written with $set of the known fields only (so fields added
// by anything else survive) and read leniently from bson.Raw: a field of the
// wrong type reads as its zero value, which core validation then rejects.

func dt(t time.Time) bson.DateTime { return bson.NewDateTimeFromTime(t) }

func dtPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return dt(*t)
}

func leadsMs(l []time.Duration) any {
	if l == nil {
		return nil
	}
	out := make([]int64, len(l))
	for i, d := range l {
		out[i] = d.Milliseconds()
	}
	return out
}

func tagsOut(t []string) any {
	if t == nil {
		return nil
	}
	return append([]string{}, t...)
}

// itemSet is the $set document for an item.
func itemSet(it model.Item) bson.D {
	return bson.D{
		{Key: "kind", Value: string(it.Kind)}, {Key: "text", Value: it.Text}, {Key: "tags", Value: tagsOut(it.Tags)},
		{Key: "due_at", Value: dtPtr(it.DueAt)}, {Key: "tz", Value: it.TZ}, {Key: "rrule", Value: it.RRule},
		{Key: "lead_times", Value: leadsMs(it.LeadTimes)}, {Key: "status", Value: string(it.Status)},
		{Key: "done_at", Value: dtPtr(it.DoneAt)}, {Key: "snooze_until", Value: dtPtr(it.SnoozeUntil)},
		{Key: "created_at", Value: dt(it.CreatedAt)}, {Key: "updated_at", Value: dt(it.UpdatedAt)},
		{Key: "updated_by", Value: it.UpdatedBy}, {Key: "priority", Value: it.Priority},
		{Key: "description", Value: it.Description}, {Key: "reply", Value: it.Reply}, {Key: "owed_to", Value: it.OwedTo},
		{Key: "source", Value: it.Source}, {Key: "reply_outcome", Value: it.ReplyOutcome},
	}
}

// attachmentSet is the $set document for an attachment; nil data is a
// tombstone and the caller adds $unset for data.
func attachmentSet(a model.Attachment, data []byte) bson.D {
	d := bson.D{
		{Key: "item_id", Value: a.ItemID}, {Key: "mime", Value: a.Mime}, {Key: "width", Value: int64(a.Width)},
		{Key: "height", Value: int64(a.Height)}, {Key: "bytes", Value: int64(a.Bytes)}, {Key: "sha256", Value: a.SHA256},
		{Key: "created_at", Value: dt(a.CreatedAt)}, {Key: "updated_at", Value: dt(a.UpdatedAt)},
		{Key: "updated_by", Value: a.UpdatedBy}, {Key: "deleted", Value: a.Deleted},
	}
	if data != nil {
		d = append(d, bson.E{Key: "data", Value: bson.Binary{Subtype: bson.TypeBinaryGeneric, Data: data}})
	}
	return d
}

// sharedSet is the $set document for the shared settings.
func sharedSet(d cloudsync.SharedDoc) bson.D {
	return bson.D{
		{Key: "lead_times", Value: leadsMs(d.Shared.LeadTimes)}, {Key: "default_time", Value: d.Shared.DefaultTime},
		{Key: "next_week_day", Value: d.Shared.NextWeekDay}, {Key: "next_week_time", Value: d.Shared.NextWeekTime},
		{Key: "updated_at", Value: dt(d.UpdatedAt)}, {Key: "updated_by", Value: d.UpdatedBy},
	}
}

// Lenient readers.

func rStr(r bson.Raw, k string) string {
	s, _ := r.Lookup(k).StringValueOK()
	return s
}

func rTime(r bson.Raw, k string) (time.Time, bool) {
	ms, ok := r.Lookup(k).DateTimeOK()
	if !ok {
		return time.Time{}, false
	}
	return time.UnixMilli(ms).UTC(), true
}

func rTimeZero(r bson.Raw, k string) time.Time {
	t, _ := rTime(r, k)
	return t
}

func rTimePtr(r bson.Raw, k string) *time.Time {
	t, ok := rTime(r, k)
	if !ok {
		return nil
	}
	return &t
}

func rInt(r bson.Raw, k string) int {
	n, _ := r.Lookup(k).AsInt64OK()
	return int(n)
}

func rBool(r bson.Raw, k string) bool {
	b, _ := r.Lookup(k).BooleanOK()
	return b
}

func rID(r bson.Raw) string {
	v := r.Lookup("_id")
	if s, ok := v.StringValueOK(); ok {
		return s
	}
	if v.Type == 0 {
		return ""
	}
	return v.String()
}

func rTags(r bson.Raw) []string {
	arr, ok := r.Lookup("tags").ArrayOK()
	if !ok {
		return nil
	}
	vals, err := arr.Values()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if s, ok := v.StringValueOK(); ok {
			out = append(out, s)
		}
	}
	return out
}

func rLeads(r bson.Raw) []time.Duration {
	arr, ok := r.Lookup("lead_times").ArrayOK()
	if !ok {
		return nil
	}
	vals, err := arr.Values()
	if err != nil {
		return nil
	}
	out := make([]time.Duration, 0, len(vals))
	for _, v := range vals {
		if n, ok := v.AsInt64OK(); ok {
			out = append(out, time.Duration(n)*time.Millisecond)
		}
	}
	return out
}

func decodeItem(r bson.Raw) model.Item {
	return model.Item{
		ID: rID(r), Kind: model.Kind(rStr(r, "kind")), Text: rStr(r, "text"), Tags: rTags(r),
		DueAt: rTimePtr(r, "due_at"), TZ: rStr(r, "tz"), RRule: rStr(r, "rrule"), LeadTimes: rLeads(r),
		Status: model.Status(rStr(r, "status")), DoneAt: rTimePtr(r, "done_at"), SnoozeUntil: rTimePtr(r, "snooze_until"),
		CreatedAt: rTimeZero(r, "created_at"), UpdatedAt: rTimeZero(r, "updated_at"), UpdatedBy: rStr(r, "updated_by"),
		Priority: rStr(r, "priority"), Description: rStr(r, "description"),
		Reply: rBool(r, "reply"), OwedTo: rStr(r, "owed_to"), Source: rStr(r, "source"), ReplyOutcome: rStr(r, "reply_outcome"),
	}
}

func decodeAttachment(r bson.Raw) cloudsync.RemoteAttachment {
	var data []byte
	if _, b, ok := r.Lookup("data").BinaryOK(); ok {
		data = append([]byte{}, b...)
	}
	return cloudsync.RemoteAttachment{
		Attachment: model.Attachment{
			ID: rID(r), ItemID: rStr(r, "item_id"), Mime: rStr(r, "mime"), SHA256: rStr(r, "sha256"),
			Width: rInt(r, "width"), Height: rInt(r, "height"), Bytes: rInt(r, "bytes"),
			CreatedAt: rTimeZero(r, "created_at"), UpdatedAt: rTimeZero(r, "updated_at"), UpdatedBy: rStr(r, "updated_by"),
			Deleted: rBool(r, "deleted"),
		},
		Data: data,
	}
}

func decodeShared(r bson.Raw) *cloudsync.SharedDoc {
	return &cloudsync.SharedDoc{
		Shared: settings.Shared{
			LeadTimes: rLeads(r), DefaultTime: rStr(r, "default_time"),
			NextWeekDay: rStr(r, "next_week_day"), NextWeekTime: rStr(r, "next_week_time"),
		},
		UpdatedAt: rTimeZero(r, "updated_at"), UpdatedBy: rStr(r, "updated_by"),
	}
}
