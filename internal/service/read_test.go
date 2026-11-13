package service_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"connectrpc.com/connect"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
	"github.com/ursakhiin/kiln/internal/service"
	"github.com/ursakhiin/kiln/internal/store"
)

func getInfo(t *testing.T, svc *service.Service, n int) *kilnv1.ItemInfo {
	t.Helper()
	resp, err := svc.GetItem(user, &kilnv1.GetItemRequest{Id: id(n)})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Item
}

func TestCallerRequired(t *testing.T) {
	svc, _, _ := newService(t, proj(1))
	_, err := svc.GetItem(context.Background(), &kilnv1.GetItemRequest{Id: id(1)})
	wantCode(t, err, connect.CodePermissionDenied)
}

func TestGetItem(t *testing.T) {
	svc, _, _ := newService(t, proj(1), feat(2, 1, "i"), task(3, 2, "i", kilnv1.Status_STATUS_READY))

	info := getInfo(t, svc, 3)
	if info.Item.Revision != 1 || info.Status != kilnv1.Status_STATUS_READY {
		t.Errorf("got revision %d status %v, want 1 READY", info.Item.Revision, info.Status)
	}

	_, err := svc.GetItem(user, &kilnv1.GetItemRequest{Id: id(42)})
	wantCode(t, err, connect.CodeNotFound)

	_, err = svc.GetItem(user, &kilnv1.GetItemRequest{Id: "../x"})
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestNewReportsBadFiles(t *testing.T) {
	repo := t.TempDir()
	st, err := store.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutItem(proj(1)); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(repo, ".kiln", "items", id(99)+".json")
	if err := os.WriteFile(bad, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, fileErrs, err := service.New(st, service.Options{Now: newClock().Now})
	if err != nil {
		t.Fatal(err)
	}
	if len(fileErrs) != 1 {
		t.Fatalf("got %d file errors, want 1: %v", len(fileErrs), fileErrs)
	}
	if got := getInfo(t, svc, 1); got.Item.Id != id(1) {
		t.Errorf("got item %s, want %s", got.Item.Id, id(1))
	}
}

func TestResponsesAreCopies(t *testing.T) {
	svc, _, _ := newService(t, proj(1))
	getInfo(t, svc, 1).Item.Title = "changed"
	if got := getInfo(t, svc, 1).Item.Title; got != "Project 1" {
		t.Errorf("title is %q after changing a response, want %q", got, "Project 1")
	}
}

func TestItemInfoDerived(t *testing.T) {
	const (
		p          = 1
		f1, f2     = 10, 20
		t1, t2, t3 = 11, 12, 21
	)
	svc, _, _ := newService(t,
		proj(p),
		feat(f1, p, "c"),
		task(t1, f1, "c", kilnv1.Status_STATUS_IN_PROGRESS),
		task(t2, f1, "m", kilnv1.Status_STATUS_READY),
		feat(f2, p, "m", dep(t2, kilnv1.BlockUntil_BLOCK_UNTIL_DONE)),
		task(t3, f2, "c", kilnv1.Status_STATUS_READY),
	)

	if info := getInfo(t, svc, f1); info.Status != kilnv1.Status_STATUS_IN_PROGRESS || info.Blocked {
		t.Errorf("F1: got %v blocked=%v, want IN_PROGRESS unblocked", info.Status, info.Blocked)
	}
	if info := getInfo(t, svc, f2); info.Status != kilnv1.Status_STATUS_READY || !info.Blocked {
		t.Errorf("F2: got %v blocked=%v, want READY blocked", info.Status, info.Blocked)
	}
	if info := getInfo(t, svc, t3); !info.Blocked {
		t.Error("T3 should be blocked through F2")
	}
	if info := getInfo(t, svc, t1); info.Blocked {
		t.Error("T1 should not be blocked")
	}
	if info := getInfo(t, svc, p); info.Status != kilnv1.Status_STATUS_UNSPECIFIED || info.Blocked || info.Claim != nil {
		t.Errorf("project: got %v blocked=%v claim=%v, want UNSPECIFIED, unblocked, no claim", info.Status, info.Blocked, info.Claim)
	}
}

func TestTaskWithMissingFeatureIsBlocked(t *testing.T) {
	svc, _, _ := newService(t, proj(1), task(3, 2, "m", kilnv1.Status_STATUS_READY))
	if info := getInfo(t, svc, 3); !info.Blocked {
		t.Error("a task whose feature is missing should be blocked")
	}
}

func TestListItemsOrderAndFilters(t *testing.T) {
	const (
		p1, p2     = 1, 2
		fA, fB, fC = 10, 11, 12
		tA1, tA2   = 20, 21
		tB1        = 22
	)
	names := map[string]string{
		id(p1): "P1", id(p2): "P2", id(fA): "A", id(fB): "B", id(fC): "C",
		id(tA1): "A1", id(tA2): "A2", id(tB1): "B1",
	}
	svc, _, _ := newService(t,
		proj(p2), proj(p1),
		feat(fA, p1, "m"), feat(fB, p2, "c"), feat(fC, p1, "x"),
		task(tA2, fA, "t", kilnv1.Status_STATUS_READY),
		task(tA1, fA, "g", kilnv1.Status_STATUS_READY),
		task(tB1, fB, "k", kilnv1.Status_STATUS_IN_PROGRESS),
	)
	list := func(req *kilnv1.ListItemsRequest) []string {
		t.Helper()
		resp, err := svc.ListItems(user, req)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, info := range resp.Items {
			got = append(got, names[info.Item.Id])
		}
		return got
	}
	check := func(label string, got []string, want ...string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", label, got, want)
		}
	}

	check("no filter", list(&kilnv1.ListItemsRequest{}), "P1", "P2", "B", "A", "C", "B1", "A1", "A2")
	check("project P1", list(&kilnv1.ListItemsRequest{ProjectId: id(p1)}), "A", "C", "A1", "A2")
	check("ready tasks", list(&kilnv1.ListItemsRequest{
		Kind:     kilnv1.ItemKind_ITEM_KIND_TASK,
		Statuses: []kilnv1.Status{kilnv1.Status_STATUS_READY},
	}), "A1", "A2")
	check("feature A", list(&kilnv1.ListItemsRequest{FeatureId: id(fA)}), "A1", "A2")
	check("in-progress features", list(&kilnv1.ListItemsRequest{
		Kind:     kilnv1.ItemKind_ITEM_KIND_FEATURE,
		Statuses: []kilnv1.Status{kilnv1.Status_STATUS_IN_PROGRESS},
	}), "B")

	_, err := svc.ListItems(user, &kilnv1.ListItemsRequest{FeatureId: "bad"})
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = svc.ListItems(user, &kilnv1.ListItemsRequest{ProjectId: "bad"})
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestListItemsOrphanTasksLast(t *testing.T) {
	svc, _, _ := newService(t,
		proj(1), feat(10, 1, "m"),
		task(31, 99, "a", kilnv1.Status_STATUS_READY),
		task(30, 98, "z", kilnv1.Status_STATUS_READY),
		task(20, 10, "m", kilnv1.Status_STATUS_READY),
	)
	resp, err := svc.ListItems(user, &kilnv1.ListItemsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, info := range resp.Items {
		got = append(got, info.Item.Id)
	}
	want := []string{id(1), id(10), id(20), id(30), id(31)}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestErrorCode(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code connect.Code
	}{
		{core.ErrNotFound, connect.CodeNotFound},
		{core.ErrInvalidArgument, connect.CodeInvalidArgument},
		{core.ErrPermissionDenied, connect.CodePermissionDenied},
		{core.ErrFailedPrecondition, connect.CodeFailedPrecondition},
		{core.ErrRevisionConflict, connect.CodeAborted},
		{&service.ConflictError{}, connect.CodeAborted},
		{errors.New("disk on fire"), connect.CodeInternal},
	} {
		if got := service.ErrorCode(fmt.Errorf("ctx: %w", tc.err)); got != tc.code {
			t.Errorf("ErrorCode(%v) = %v, want %v", tc.err, got, tc.code)
		}
	}
}
