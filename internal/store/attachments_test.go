package store

import (
	"errors"
	"reflect"
	"testing"

	"awesome-reminder/internal/model"
)

func TestAttachmentCRUD(t *testing.T) {
	s := mem(t)
	a1 := model.Attachment{ID: "a1", ItemID: "i1", Mime: "image/webp", SHA256: "h1", Width: 10, Height: 20,
		Bytes: 300, CreatedAt: ms(1), UpdatedAt: ms(2), UpdatedBy: "d", UnreferencedSince: ptr(ms(5))}
	a2 := model.Attachment{ID: "a2", ItemID: "i1", Mime: "image/webp", SHA256: "h2", Width: 1, Height: 2,
		Bytes: 3, CreatedAt: ms(3), UpdatedAt: ms(3), UpdatedBy: "d"}
	a3 := model.Attachment{ID: "a3", ItemID: "i1", Mime: "image/webp", SHA256: "h3", CreatedAt: ms(4),
		UpdatedAt: ms(4), UpdatedBy: "d", Deleted: true}
	other := model.Attachment{ID: "a4", ItemID: "i2", Mime: "image/webp", CreatedAt: ms(1), UpdatedAt: ms(1), UpdatedBy: "d"}
	for _, a := range []model.Attachment{a2, a1, a3, other} {
		if err := s.PutAttachment(a); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetAttachment("a1")
	if err != nil || !reflect.DeepEqual(got, a1) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	got, err = s.GetAttachment("a2")
	if err != nil || got.UnreferencedSince != nil {
		t.Fatalf("a2=%+v err=%v", got, err)
	}
	if _, err := s.GetAttachment("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	list, err := s.ListAttachments("i1")
	if err != nil || len(list) != 2 || list[0].ID != "a1" || list[1].ID != "a2" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	all, err := s.AllAttachments()
	if err != nil || len(all) != 4 {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	// upsert: mark a2 deleted
	a2.Deleted = true
	if err := s.PutAttachment(a2); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListAttachments("i1")
	if len(list) != 1 {
		t.Fatalf("list=%+v", list)
	}
}
