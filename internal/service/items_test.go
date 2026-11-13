package service_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
	"github.com/ursakhiin/kiln/internal/service"
	"github.com/ursakhiin/kiln/internal/store"
)

func openStore(t *testing.T, repo string) *store.Store {
	t.Helper()
	st, err := store.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func history(t *testing.T, repo, itemID string) []*kilnv1.HistoryEntry {
	t.Helper()
	h, fileErrs, err := openStore(t, repo).ListHistory(itemID)
	if err != nil || len(fileErrs) > 0 {
		t.Fatalf("ListHistory: %v %v", err, fileErrs)
	}
	return h
}

func newProject(title string) *kilnv1.Item {
	return &kilnv1.Item{Title: title, Kind: &kilnv1.Item_Project{Project: &kilnv1.Project{}}}
}

func newFeature(title, project string) *kilnv1.Item {
	return &kilnv1.Item{Title: title, Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{ProjectId: project}}}
}

func newTask(title, feature string) *kilnv1.Item {
	return &kilnv1.Item{Title: title, Kind: &kilnv1.Item_Task{Task: &kilnv1.Task{FeatureId: feature}}}
}

func mask(paths ...string) *fieldmaskpb.FieldMask { return &fieldmaskpb.FieldMask{Paths: paths} }

// reparent builds an UpdateItem request moving task n to feature f.
func reparent(n, f int, base int64) *kilnv1.UpdateItemRequest {
	return &kilnv1.UpdateItemRequest{
		Item:         &kilnv1.Item{Id: id(n), Kind: &kilnv1.Item_Task{Task: &kilnv1.Task{FeatureId: id(f)}}},
		BaseRevision: base, UpdateMask: mask("task.feature_id"),
	}
}

func before(n int) *kilnv1.MoveItemRequest_BeforeId {
	return &kilnv1.MoveItemRequest_BeforeId{BeforeId: id(n)}
}

func after(n int) *kilnv1.MoveItemRequest_AfterId {
	return &kilnv1.MoveItemRequest_AfterId{AfterId: id(n)}
}

func TestCreateItem(t *testing.T) {
	svc, clk, repo := newService(t)
	create := func(item *kilnv1.Item) *kilnv1.ItemInfo {
		t.Helper()
		resp, err := svc.CreateItem(user, &kilnv1.CreateItemRequest{Item: item})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Item.Item.Revision != 1 || !core.ValidID(resp.Item.Item.Id) {
			t.Fatalf("got revision %d id %q", resp.Item.Item.Revision, resp.Item.Item.Id)
		}
		return resp.Item
	}
	req := newProject("P")
	p := create(req)
	if req.Id != "" {
		t.Error("CreateItem modified the request")
	}
	f1 := create(newFeature("F1", p.Item.Id))
	f2 := create(newFeature("F2", p.Item.Id))
	clk.Advance(time.Minute)
	t1 := create(newTask("T1", f1.Item.Id))

	if f1.Item.GetFeature().Rank >= f2.Item.GetFeature().Rank {
		t.Errorf("ranks: F1 %q, F2 %q", f1.Item.GetFeature().Rank, f2.Item.GetFeature().Rank)
	}
	if t1.Status != kilnv1.Status_STATUS_READY {
		t.Errorf("T1 status %v", t1.Status)
	}

	// The full write sequence, as the user.
	want := timestamppb.New(time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))
	if !proto.Equal(f1.Item.UpdateTime, want) || !proto.Equal(f1.Item.CreateTime, want) {
		t.Errorf("F1 times: %v %v, want %v", f1.Item.CreateTime, f1.Item.UpdateTime, want)
	}
	file, err := openStore(t, repo).GetItem(f1.Item.Id)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(file, f1.Item) {
		t.Errorf("file %v differs from response %v", file, f1.Item)
	}
	h := history(t, repo, f1.Item.Id)
	if len(h) != 1 {
		t.Fatalf("got %d history entries, want 1", len(h))
	}
	ch := h[0].GetChange()
	if ch.Type != kilnv1.ChangeType_CHANGE_TYPE_CREATED || ch.Revision != 1 || len(ch.ChangedFields) != 0 {
		t.Errorf("history change %v", ch)
	}
	if !proto.Equal(ch.Snapshot, file) {
		t.Errorf("snapshot %v differs from file %v", ch.Snapshot, file)
	}
	if h[0].Actor.Type != kilnv1.ActorType_ACTOR_TYPE_USER || h[0].Actor.AgentId != "" || h[0].Actor.Session != nil {
		t.Errorf("actor %v", h[0].Actor)
	}
	if !proto.Equal(h[0].CreateTime, want) {
		t.Errorf("history time %v", h[0].CreateTime)
	}
}

func TestCreateItemParents(t *testing.T) {
	svc, _, _ := newService(t, proj(1))
	for name, tc := range map[string]struct {
		item *kilnv1.Item
		code connect.Code
	}{
		"unknown project":       {newFeature("F", id(99)), connect.CodeNotFound},
		"task under project":    {newTask("T", id(1)), connect.CodeInvalidArgument},
		"task with bad ID":      {newTask("T", "x"), connect.CodeInvalidArgument},
		"feature with bad ID":   {newFeature("F", "x"), connect.CodeInvalidArgument},
		"no kind":               {&kilnv1.Item{Title: "x"}, connect.CodeInvalidArgument},
		"feature under feature": {newFeature("F", id(1)), 0},
	} {
		_, err := svc.CreateItem(user, &kilnv1.CreateItemRequest{Item: tc.item})
		if tc.code == 0 {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if err == nil || service.ErrorCode(err) != tc.code {
			t.Errorf("%s: got %v, want %v", name, err, tc.code)
		}
	}
}

func TestAgentCreatesDraftFeature(t *testing.T) {
	svc, _, repo := newService(t, proj(1))
	resp, err := svc.CreateItem(agent("a1"), &kilnv1.CreateItemRequest{Item: newFeature("F", id(1))})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Item.Item.GetFeature().Draft || resp.Item.Status != kilnv1.Status_STATUS_DRAFT {
		t.Errorf("draft %v status %v", resp.Item.Item.GetFeature().Draft, resp.Item.Status)
	}
	h := history(t, repo, resp.Item.Item.Id)
	if len(h) != 1 {
		t.Fatalf("got %d history entries", len(h))
	}
	wantActor := &kilnv1.Actor{
		Type: kilnv1.ActorType_ACTOR_TYPE_AGENT, AgentId: "a1",
		Session: &kilnv1.AgentSession{Host: "test-host", HostVersion: "1.0"},
	}
	if !proto.Equal(h[0].Actor, wantActor) {
		t.Errorf("actor %v, want %v", h[0].Actor, wantActor)
	}
	file, err := openStore(t, repo).GetItem(resp.Item.Item.Id)
	if err != nil || !proto.Equal(file, resp.Item.Item) || !proto.Equal(h[0].GetChange().Snapshot, file) {
		t.Errorf("file/snapshot mismatch: %v %v", err, file)
	}
}

func TestUpdateItem(t *testing.T) {
	svc, clk, repo := newService(t, proj(1), feat(2, 1, "m"))
	clk.Advance(time.Hour)
	resp, err := svc.UpdateItem(agent("a1"), &kilnv1.UpdateItemRequest{
		Item:         &kilnv1.Item{Id: id(2), Title: "Renamed"},
		BaseRevision: 1, UpdateMask: mask("title"),
	})
	if err != nil {
		t.Fatal(err)
	}
	it := resp.Item.Item
	if it.Revision != 2 || it.Title != "Renamed" || it.GetFeature().Rank != "m" {
		t.Errorf("got %v", it)
	}
	if !proto.Equal(it.UpdateTime, timestamppb.New(clk.Now())) || !proto.Equal(it.CreateTime, seedTime) {
		t.Errorf("times %v %v", it.CreateTime, it.UpdateTime)
	}
	file, err := openStore(t, repo).GetItem(id(2))
	if err != nil || !proto.Equal(file, it) {
		t.Errorf("file %v (%v) differs from response", file, err)
	}
	h := history(t, repo, id(2))
	if len(h) != 1 {
		t.Fatalf("got %d history entries, want 1", len(h))
	}
	ch := h[0].GetChange()
	if ch.Type != kilnv1.ChangeType_CHANGE_TYPE_UPDATED || ch.Revision != 2 ||
		!slices.Equal(ch.ChangedFields, []string{"title"}) || !proto.Equal(ch.Snapshot, file) {
		t.Errorf("change %v", ch)
	}
	if h[0].Actor.Type != kilnv1.ActorType_ACTOR_TYPE_AGENT || h[0].Actor.AgentId != "a1" || h[0].Actor.Session.Host != "test-host" {
		t.Errorf("actor %v", h[0].Actor)
	}
}

func TestUpdateItemStale(t *testing.T) {
	svc, _, repo := newService(t, proj(1))
	_, err := svc.UpdateItem(user, &kilnv1.UpdateItemRequest{
		Item: &kilnv1.Item{Id: id(1), Title: "x"}, BaseRevision: 0, UpdateMask: mask("title"),
	})
	wantCode(t, err, connect.CodeAborted)
	var ce *service.ConflictError
	if !errors.As(err, &ce) || ce.Current.Item.Revision != 1 {
		t.Fatalf("got %v", err)
	}
	if f, _ := openStore(t, repo).GetItem(id(1)); f.Revision != 1 || f.Title != "Project 1" {
		t.Errorf("file changed: %v", f)
	}
	if h := history(t, repo, id(1)); len(h) != 0 {
		t.Errorf("history %v", h)
	}
}

func TestAgentCannotPromoteDraft(t *testing.T) {
	d := feat(2, 1, "m")
	d.GetFeature().Draft = true
	svc, _, _ := newService(t, proj(1), d)
	req := &kilnv1.UpdateItemRequest{
		Item:         &kilnv1.Item{Id: id(2), Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{Draft: false}}},
		BaseRevision: 1, UpdateMask: mask("feature.draft"),
	}
	_, err := svc.UpdateItem(agent("a1"), req)
	wantCode(t, err, connect.CodePermissionDenied)
	if _, err := svc.UpdateItem(user, req); err != nil {
		t.Errorf("user promote: %v", err)
	}
}

func TestReparentTask(t *testing.T) {
	svc, _, repo := newService(t, proj(1),
		feat(2, 1, "c"), feat(3, 1, "m"),
		task(4, 2, "m", kilnv1.Status_STATUS_READY),
		task(5, 3, "c", kilnv1.Status_STATUS_READY),
		task(6, 3, "p", kilnv1.Status_STATUS_READY))
	resp, err := svc.UpdateItem(user, reparent(4, 3, 1))
	if err != nil {
		t.Fatal(err)
	}
	it := resp.Item.Item
	if it.GetTask().FeatureId != id(3) || it.GetTask().Rank <= "p" || it.GetTask().Status != kilnv1.Status_STATUS_READY {
		t.Errorf("got %v", it)
	}
	h := history(t, repo, id(4))
	if len(h) != 1 || !slices.Equal(h[0].GetChange().ChangedFields, []string{"task.feature_id", "task.rank"}) {
		t.Errorf("history %v", h)
	}
	// A project is not a feature.
	_, err = svc.UpdateItem(user, reparent(4, 1, 2))
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestReparentTaskCycle(t *testing.T) {
	svc, _, repo := newService(t, proj(1),
		feat(2, 1, "c"),
		task(4, 2, "m", kilnv1.Status_STATUS_READY),
		feat(3, 1, "m", dep(4, kilnv1.BlockUntil_BLOCK_UNTIL_DONE)))
	_, err := svc.UpdateItem(user, reparent(4, 3, 1))
	wantCode(t, err, connect.CodeFailedPrecondition)
	f, _ := openStore(t, repo).GetItem(id(4))
	if f.GetTask().FeatureId != id(2) || f.Revision != 1 {
		t.Errorf("file changed: %v", f)
	}
	if got := getInfo(t, svc, 4).Item.GetTask().FeatureId; got != id(2) {
		t.Errorf("memory changed: %v", got)
	}
	if h := history(t, repo, id(4)); len(h) != 0 {
		t.Errorf("history %v", h)
	}
}

func TestDeleteItem(t *testing.T) {
	svc, _, repo := newService(t, proj(1),
		feat(2, 1, "c"), feat(3, 1, "m"),
		task(4, 2, "m", kilnv1.Status_STATUS_READY),
		task(5, 3, "m", kilnv1.Status_STATUS_READY, dep(4, kilnv1.BlockUntil_BLOCK_UNTIL_DONE)))

	_, err := svc.DeleteItem(agent("a1"), &kilnv1.DeleteItemRequest{Id: id(5), BaseRevision: 1})
	wantCode(t, err, connect.CodePermissionDenied)
	_, err = svc.DeleteItem(user, &kilnv1.DeleteItemRequest{Id: id(2), BaseRevision: 1})
	wantCode(t, err, connect.CodeFailedPrecondition) // has tasks
	_, err = svc.DeleteItem(user, &kilnv1.DeleteItemRequest{Id: id(4), BaseRevision: 1})
	wantCode(t, err, connect.CodeFailedPrecondition) // T5 depends on it
	_, err = svc.DeleteItem(user, &kilnv1.DeleteItemRequest{Id: id(5), BaseRevision: 0})
	wantCode(t, err, connect.CodeAborted)

	if _, err := svc.DeleteItem(user, &kilnv1.DeleteItemRequest{Id: id(5), BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := openStore(t, repo).GetItem(id(5)); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("file still there: %v", err)
	}
	_, err = svc.GetItem(user, &kilnv1.GetItemRequest{Id: id(5)})
	wantCode(t, err, connect.CodeNotFound)
	h := history(t, repo, id(5))
	if len(h) != 1 {
		t.Fatalf("got %d history entries", len(h))
	}
	ch := h[0].GetChange()
	if ch.Type != kilnv1.ChangeType_CHANGE_TYPE_DELETED || ch.Revision != 2 || ch.Snapshot != nil ||
		h[0].Actor.Type != kilnv1.ActorType_ACTOR_TYPE_USER ||
		!h[0].CreateTime.AsTime().Equal(time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("history %v", h[0])
	}
	// With T5 gone, T4 and then F2 can go.
	for _, n := range []int{4, 2} {
		if _, err := svc.DeleteItem(user, &kilnv1.DeleteItemRequest{Id: id(n), BaseRevision: 1}); err != nil {
			t.Errorf("delete %d: %v", n, err)
		}
	}
}

func featureOrder(t *testing.T, svc *service.Service) []string {
	t.Helper()
	resp, err := svc.ListItems(user, &kilnv1.ListItemsRequest{Kind: kilnv1.ItemKind_ITEM_KIND_FEATURE})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range resp.Items {
		out = append(out, it.Item.Id)
	}
	return out
}

func TestMoveItem(t *testing.T) {
	const a, b, c = 2, 3, 4
	svc, _, repo := newService(t, proj(1),
		feat(a, 1, "c"), feat(b, 1, "m"), feat(c, 1, "p"),
		task(5, a, "m", kilnv1.Status_STATUS_READY), task(6, b, "m", kilnv1.Status_STATUS_READY))

	resp, err := svc.MoveItem(user, &kilnv1.MoveItemRequest{Id: id(c), BaseRevision: 1, Position: before(a)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Item.Item.Revision != 2 {
		t.Errorf("revision %d", resp.Item.Item.Revision)
	}
	if got, want := featureOrder(t, svc), []string{id(c), id(a), id(b)}; !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	h := history(t, repo, id(c))
	if len(h) != 1 || h[0].GetChange().Type != kilnv1.ChangeType_CHANGE_TYPE_MOVED ||
		!slices.Equal(h[0].GetChange().ChangedFields, []string{"feature.rank"}) {
		t.Errorf("history %v", h)
	}

	if _, err := svc.MoveItem(user, &kilnv1.MoveItemRequest{Id: id(a), BaseRevision: 1, Position: after(b)}); err != nil {
		t.Fatal(err)
	}
	if got, want := featureOrder(t, svc), []string{id(c), id(b), id(a)}; !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	cases := map[string]struct {
		ctx  context.Context
		req  *kilnv1.MoveItemRequest
		code connect.Code
	}{
		"task after other feature's task": {user, &kilnv1.MoveItemRequest{Id: id(5), BaseRevision: 1, Position: after(6)}, connect.CodeInvalidArgument},
		"task after a feature":            {user, &kilnv1.MoveItemRequest{Id: id(5), BaseRevision: 1, Position: after(a)}, connect.CodeInvalidArgument},
		"project":                         {user, &kilnv1.MoveItemRequest{Id: id(1), BaseRevision: 1, Position: after(a)}, connect.CodeInvalidArgument},
		"no position":                     {user, &kilnv1.MoveItemRequest{Id: id(b), BaseRevision: 1}, connect.CodeInvalidArgument},
		"self":                            {user, &kilnv1.MoveItemRequest{Id: id(b), BaseRevision: 1, Position: after(b)}, connect.CodeInvalidArgument},
		"unknown sibling":                 {user, &kilnv1.MoveItemRequest{Id: id(b), BaseRevision: 1, Position: after(99)}, connect.CodeNotFound},
		"stale":                           {user, &kilnv1.MoveItemRequest{Id: id(b), BaseRevision: 7, Position: after(a)}, connect.CodeAborted},
		"agent":                           {agent("a1"), &kilnv1.MoveItemRequest{Id: id(b), BaseRevision: 1, Position: after(a)}, connect.CodePermissionDenied},
	}
	for name, tc := range cases {
		_, err := svc.MoveItem(tc.ctx, tc.req)
		if err == nil || service.ErrorCode(err) != tc.code {
			t.Errorf("%s: got %v, want %v", name, err, tc.code)
		}
	}
}

func TestMoveTask(t *testing.T) {
	svc, _, repo := newService(t, proj(1), feat(2, 1, "m"),
		task(3, 2, "c", kilnv1.Status_STATUS_READY), task(4, 2, "m", kilnv1.Status_STATUS_READY))
	resp, err := svc.MoveItem(user, &kilnv1.MoveItemRequest{Id: id(4), BaseRevision: 1, Position: before(3)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Item.Item.GetTask().Rank >= "c" {
		t.Errorf("rank %q", resp.Item.Item.GetTask().Rank)
	}
	h := history(t, repo, id(4))
	if len(h) != 1 || !slices.Equal(h[0].GetChange().ChangedFields, []string{"task.rank"}) {
		t.Errorf("history %v", h)
	}
}

func TestMoveItemTiedRanks(t *testing.T) {
	svc, _, _ := newService(t, proj(1), feat(2, 1, "m"), feat(3, 1, "m"), feat(4, 1, "z"))
	_, err := svc.MoveItem(user, &kilnv1.MoveItemRequest{Id: id(4), BaseRevision: 1, Position: before(3)})
	wantCode(t, err, connect.CodeFailedPrecondition)
	if !strings.Contains(err.Error(), "ranks tied; move a neighbor first") {
		t.Errorf("message %q", err)
	}
}

func TestCommitFailureLeavesMemory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	svc, _, repo := newService(t, proj(1))
	dir := filepath.Join(repo, ".kiln", "items")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	_, err := svc.UpdateItem(user, &kilnv1.UpdateItemRequest{
		Item: &kilnv1.Item{Id: id(1), Title: "x"}, BaseRevision: 1, UpdateMask: mask("title"),
	})
	wantCode(t, err, connect.CodeInternal)
	if got := getInfo(t, svc, 1).Item; got.Revision != 1 || got.Title != "Project 1" {
		t.Errorf("memory changed: %v", got)
	}
	if h := history(t, repo, id(1)); len(h) != 0 {
		t.Errorf("history %v", h)
	}
}

func TestHistoryFailureIsLoggedNotReturned(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	repo := t.TempDir()
	st := openStore(t, repo)
	if err := st.PutItem(proj(1)); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	svc, _, err := service.New(st, service.Options{Now: newClock().Now, Logger: slog.New(slog.NewTextHandler(&buf, nil))})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repo, ".kiln", "history")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	resp, err := svc.UpdateItem(user, &kilnv1.UpdateItemRequest{
		Item: &kilnv1.Item{Id: id(1), Title: "x"}, BaseRevision: 1, UpdateMask: mask("title"),
	})
	if err != nil {
		t.Fatalf("got %v, want success: the item file is saved", err)
	}
	if resp.Item.Item.Revision != 2 {
		t.Errorf("revision %d", resp.Item.Item.Revision)
	}
	if f, _ := st.GetItem(id(1)); f.Revision != 2 {
		t.Errorf("file revision %d", f.Revision)
	}
	log := buf.String()
	if !strings.Contains(log, "level=ERROR") || !strings.Contains(log, id(1)) || !strings.Contains(log, "revision=2") {
		t.Errorf("log %q", log)
	}
}
