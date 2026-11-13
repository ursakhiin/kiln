package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/gen/go/kiln/v1/kilnv1connect"
	"github.com/ursakhiin/kiln/internal/api"
	"github.com/ursakhiin/kiln/internal/service"
	"github.com/ursakhiin/kiln/internal/store"
)

func newServer(t *testing.T) (kilnv1connect.KilnServiceClient, *httptest.Server) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc, _, err := service.New(st, service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	path, h := api.NewHandler(svc)
	mux := http.NewServeMux()
	mux.Handle(path, h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return kilnv1connect.NewKilnServiceClient(srv.Client(), srv.URL), srv
}

func create(t *testing.T, c kilnv1connect.KilnServiceClient, item *kilnv1.Item) *kilnv1.Item {
	t.Helper()
	resp, err := c.CreateItem(context.Background(), connect.NewRequest(&kilnv1.CreateItemRequest{Item: item}))
	if err != nil {
		t.Fatal(err)
	}
	return resp.Msg.Item.Item
}

func TestUserCreatesAndCompletes(t *testing.T) {
	c, _ := newServer(t)
	ctx := context.Background()
	p := create(t, c, &kilnv1.Item{Title: "P", Kind: &kilnv1.Item_Project{Project: &kilnv1.Project{}}})
	f := create(t, c, &kilnv1.Item{Title: "F", Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{ProjectId: p.Id}}})
	if f.GetFeature().GetDraft() {
		t.Fatal("feature created by the user must not be a draft")
	}
	tk := create(t, c, &kilnv1.Item{Title: "T", Kind: &kilnv1.Item_Task{Task: &kilnv1.Task{FeatureId: f.Id}}})
	resp, err := c.SetStatus(ctx, connect.NewRequest(&kilnv1.SetStatusRequest{
		Id: tk.Id, BaseRevision: tk.Revision, Status: kilnv1.Status_STATUS_DONE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Msg.Item.Item.GetTask().GetStatus(); got != kilnv1.Status_STATUS_DONE {
		t.Fatalf("status = %v", got)
	}
}

func TestConflictDetail(t *testing.T) {
	c, _ := newServer(t)
	ctx := context.Background()
	p := create(t, c, &kilnv1.Item{Title: "P", Kind: &kilnv1.Item_Project{Project: &kilnv1.Project{}}})
	upd := func(base int64) error {
		_, err := c.UpdateItem(ctx, connect.NewRequest(&kilnv1.UpdateItemRequest{
			Item:         &kilnv1.Item{Id: p.Id, Title: "P2"},
			BaseRevision: base,
			UpdateMask:   &fieldmaskpb.FieldMask{Paths: []string{"title"}},
		}))
		return err
	}
	if err := upd(p.Revision); err != nil {
		t.Fatal(err)
	}
	err := upd(p.Revision) // stale
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("code = %v, err = %v", connect.CodeOf(err), err)
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) || len(cerr.Details()) != 1 {
		t.Fatalf("want one detail, got %v", err)
	}
	msg, err := cerr.Details()[0].Value()
	if err != nil {
		t.Fatal(err)
	}
	rc, ok := msg.(*kilnv1.RevisionConflict)
	if !ok {
		t.Fatalf("detail is %T", msg)
	}
	if got := rc.Current.Item.Revision; got != 2 {
		t.Fatalf("current revision = %d", got)
	}
}

func TestUnimplemented(t *testing.T) {
	c, _ := newServer(t)
	_, err := c.RestoreVersion(context.Background(), connect.NewRequest(&kilnv1.RestoreVersionRequest{}))
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("code = %v", connect.CodeOf(err))
	}
}

func TestCrossOriginRejected(t *testing.T) {
	_, srv := newServer(t)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/kiln.v1.KilnService/GetItem", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
