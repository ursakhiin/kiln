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

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/gen/go/underdraw/v1/underdrawv1connect"
	"github.com/ursakhiin/underdraw/internal/api"
	"github.com/ursakhiin/underdraw/internal/service"
	"github.com/ursakhiin/underdraw/internal/store"
)

func newServer(t *testing.T) (underdrawv1connect.UnderdrawServiceClient, *httptest.Server) {
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
	return underdrawv1connect.NewUnderdrawServiceClient(srv.Client(), srv.URL), srv
}

func create(t *testing.T, c underdrawv1connect.UnderdrawServiceClient, item *underdrawv1.Item) *underdrawv1.Item {
	t.Helper()
	resp, err := c.CreateItem(context.Background(), connect.NewRequest(&underdrawv1.CreateItemRequest{Item: item}))
	if err != nil {
		t.Fatal(err)
	}
	return resp.Msg.Item.Item
}

func TestUserCreatesAndCompletes(t *testing.T) {
	c, _ := newServer(t)
	ctx := context.Background()
	p := create(t, c, &underdrawv1.Item{Title: "P", Kind: &underdrawv1.Item_Project{Project: &underdrawv1.Project{}}})
	f := create(t, c, &underdrawv1.Item{Title: "F", Kind: &underdrawv1.Item_Feature{Feature: &underdrawv1.Feature{ProjectId: p.Id}}})
	if f.GetFeature().GetDraft() {
		t.Fatal("feature created by the user must not be a draft")
	}
	tk := create(t, c, &underdrawv1.Item{Title: "T", Kind: &underdrawv1.Item_Task{Task: &underdrawv1.Task{FeatureId: f.Id}}})
	resp, err := c.SetStatus(ctx, connect.NewRequest(&underdrawv1.SetStatusRequest{
		Id: tk.Id, BaseRevision: tk.Revision, Status: underdrawv1.Status_STATUS_DONE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Msg.Item.Item.GetTask().GetStatus(); got != underdrawv1.Status_STATUS_DONE {
		t.Fatalf("status = %v", got)
	}
}

func TestConflictDetail(t *testing.T) {
	c, _ := newServer(t)
	ctx := context.Background()
	p := create(t, c, &underdrawv1.Item{Title: "P", Kind: &underdrawv1.Item_Project{Project: &underdrawv1.Project{}}})
	upd := func(base int64) error {
		_, err := c.UpdateItem(ctx, connect.NewRequest(&underdrawv1.UpdateItemRequest{
			Item:         &underdrawv1.Item{Id: p.Id, Title: "P2"},
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
	rc, ok := msg.(*underdrawv1.RevisionConflict)
	if !ok {
		t.Fatalf("detail is %T", msg)
	}
	if got := rc.Current.Item.Revision; got != 2 {
		t.Fatalf("current revision = %d", got)
	}
}

func TestUnimplemented(t *testing.T) {
	c, _ := newServer(t)
	_, err := c.RestoreVersion(context.Background(), connect.NewRequest(&underdrawv1.RestoreVersionRequest{}))
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("code = %v", connect.CodeOf(err))
	}
}

func TestCrossOriginRejected(t *testing.T) {
	_, srv := newServer(t)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/underdraw.v1.UnderdrawService/GetItem", strings.NewReader("{}"))
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
