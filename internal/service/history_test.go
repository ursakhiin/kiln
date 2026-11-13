package service_test

import (
	"slices"
	"testing"

	"connectrpc.com/connect"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/service"
)

// renameTask changes the title of item n based on revision base.
func renameTask(t *testing.T, svc *service.Service, n int, title string, base int64) {
	t.Helper()
	_, err := svc.UpdateItem(user, &underdrawv1.UpdateItemRequest{
		Item: &underdrawv1.Item{Id: id(n), Title: title}, BaseRevision: base, UpdateMask: mask("title"),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func entryIDs(es []*underdrawv1.HistoryEntry) []string {
	var ids []string
	for _, e := range es {
		ids = append(ids, e.Id)
	}
	return ids
}

func TestGetItemChanges(t *testing.T) {
	svc, _, _ := newService(t, proj(1), feat(2, 1, "a"), task(3, 2, "a", underdrawv1.Status_STATUS_READY))
	renameTask(t, svc, 3, "Second", 1)
	renameTask(t, svc, 3, "Third", 2)

	resp, err := svc.GetItemChanges(user, &underdrawv1.GetItemChangesRequest{Id: id(3), SinceRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Changes) != 2 {
		t.Fatalf("got %d changes, want 2", len(resp.Changes))
	}
	for i, want := range []int64{2, 3} {
		if got := resp.Changes[i].GetChange().GetRevision(); got != want {
			t.Errorf("change %d: revision %d, want %d", i, got, want)
		}
	}
	if !slices.IsSorted(entryIDs(resp.Changes)) {
		t.Error("changes not ordered by entry ID")
	}
	if got := resp.Changes[1].GetChange().GetSnapshot().GetTitle(); got != "Third" {
		t.Errorf("last snapshot title %q, want Third", got)
	}

	resp, err = svc.GetItemChanges(user, &underdrawv1.GetItemChangesRequest{Id: id(3), SinceRevision: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Changes) != 0 {
		t.Errorf("got %d changes since 3, want 0", len(resp.Changes))
	}
}

func TestGetItemChangesDeleted(t *testing.T) {
	svc, _, _ := newService(t, proj(1), feat(2, 1, "a"), task(3, 2, "a", underdrawv1.Status_STATUS_READY))
	if _, err := svc.DeleteItem(user, &underdrawv1.DeleteItemRequest{Id: id(3), BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	resp, err := svc.GetItemChanges(user, &underdrawv1.GetItemChangesRequest{Id: id(3)})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Changes) == 0 {
		t.Fatal("no changes")
	}
	last := resp.Changes[len(resp.Changes)-1]
	if got := last.GetChange().GetType(); got != underdrawv1.ChangeType_CHANGE_TYPE_DELETED {
		t.Errorf("last change type %v, want DELETED", got)
	}
}

func TestGetItemChangesUnknown(t *testing.T) {
	svc, _, _ := newService(t, proj(1))
	_, err := svc.GetItemChanges(user, &underdrawv1.GetItemChangesRequest{Id: id(99)})
	wantCode(t, err, connect.CodeNotFound)
	_, err = svc.GetItemChanges(user, &underdrawv1.GetItemChangesRequest{Id: "bad"})
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestListHistoryChildren(t *testing.T) {
	svc, _, _ := newService(t, proj(1))
	f, err := svc.CreateItem(user, &underdrawv1.CreateItemRequest{Item: newFeature("F", id(1))})
	if err != nil {
		t.Fatal(err)
	}
	fid := f.Item.Item.Id
	want := []string{fid}
	for _, title := range []string{"T1", "T2"} {
		r, err := svc.CreateItem(user, &underdrawv1.CreateItemRequest{Item: newTask(title, fid)})
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, r.Item.Item.Id)
	}

	resp, err := svc.ListHistory(user, &underdrawv1.ListHistoryRequest{Id: fid, IncludeChildren: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(resp.Entries))
	}
	if !slices.IsSorted(entryIDs(resp.Entries)) {
		t.Error("entries not in ID order")
	}
	var got []string
	for _, e := range resp.Entries {
		if e.GetChange().GetType() != underdrawv1.ChangeType_CHANGE_TYPE_CREATED {
			t.Errorf("entry %s: type %v, want CREATED", e.Id, e.GetChange().GetType())
		}
		got = append(got, e.ItemId)
	}
	if !slices.Equal(got, want) {
		t.Errorf("item IDs %v, want %v (creation order)", got, want)
	}

	resp, err = svc.ListHistory(user, &underdrawv1.ListHistoryRequest{Id: fid})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].ItemId != fid {
		t.Errorf("without children: got %v, want only the feature's entry", resp.Entries)
	}

	_, err = svc.ListHistory(user, &underdrawv1.ListHistoryRequest{Id: id(99)})
	wantCode(t, err, connect.CodeNotFound)
}

func TestDecisionNotes(t *testing.T) {
	svc, _, repo := newService(t, proj(1), feat(2, 1, "a"), task(3, 2, "a", underdrawv1.Status_STATUS_READY))
	resp, err := svc.AddDecisionNote(agent("a1"), &underdrawv1.AddDecisionNoteRequest{ItemId: id(3), Body: "chose X over Y"})
	if err != nil {
		t.Fatal(err)
	}
	e := resp.Entry
	if e.GetNote().GetBody() != "chose X over Y" || e.GetNote().GetItemRevision() != 1 || e.ItemId != id(3) {
		t.Errorf("unexpected entry %v", e)
	}
	if e.Actor.GetType() != underdrawv1.ActorType_ACTOR_TYPE_AGENT || e.Actor.GetAgentId() != "a1" {
		t.Errorf("actor %v, want agent a1", e.Actor)
	}
	if e.CreateTime == nil {
		t.Error("no create_time")
	}
	if got, err := svc.GetItem(user, &underdrawv1.GetItemRequest{Id: id(3)}); err != nil || got.Item.Item.Revision != 1 {
		t.Errorf("item changed: %v %v", got, err)
	}
	if h := history(t, repo, id(3)); len(h) != 1 || h[0].Id != e.Id {
		t.Errorf("note not stored: %v", h)
	}

	renameTask(t, svc, 3, "New", 1)
	list, err := svc.ListHistory(user, &underdrawv1.ListHistoryRequest{Id: id(3)})
	if err != nil || len(list.Entries) != 2 {
		t.Fatalf("got %v %v, want 2 entries", list, err)
	}
	list, err = svc.ListHistory(user, &underdrawv1.ListHistoryRequest{Id: id(3), NotesOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 1 || list.Entries[0].GetNote() == nil {
		t.Errorf("notes_only: got %v", list.Entries)
	}

	for _, body := range []string{"", "  \n\t"} {
		_, err = svc.AddDecisionNote(user, &underdrawv1.AddDecisionNoteRequest{ItemId: id(3), Body: body})
		wantCode(t, err, connect.CodeInvalidArgument)
	}
	_, err = svc.AddDecisionNote(user, &underdrawv1.AddDecisionNoteRequest{ItemId: id(99), Body: "x"})
	wantCode(t, err, connect.CodeNotFound)
}
