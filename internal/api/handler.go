package api

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/gen/go/kiln/v1/kilnv1connect"
	"github.com/ursakhiin/kiln/internal/service"
)

// NewHandler serves KilnService to the user. It returns the path to mount it
// at and the handler, which rejects cross-origin requests that change state.
func NewHandler(svc *service.Service) (string, http.Handler) {
	path, h := kilnv1connect.NewKilnServiceHandler(&handler{svc: svc})
	return path, http.NewCrossOriginProtection().Handler(h)
}

// handler embeds the unimplemented handler so RestoreVersion and WatchItems
// answer CodeUnimplemented until they exist.
type handler struct {
	kilnv1connect.UnimplementedKilnServiceHandler
	svc *service.Service
}

// call runs a service method as the user and maps its error to a Connect error.
func call[Req, Resp any](ctx context.Context, req *connect.Request[Req], f func(context.Context, *Req) (*Resp, error)) (*connect.Response[Resp], error) {
	ctx = service.WithCaller(ctx, service.Caller{Actor: kilnv1.ActorType_ACTOR_TYPE_USER})
	resp, err := f(ctx, req.Msg)
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(resp), nil
}

func toConnectError(err error) error {
	cerr := connect.NewError(service.ErrorCode(err), err)
	var conflict *service.ConflictError
	if errors.As(err, &conflict) {
		if d, derr := connect.NewErrorDetail(&kilnv1.RevisionConflict{Current: conflict.Current}); derr == nil {
			cerr.AddDetail(d)
		}
	}
	return cerr
}

func (h *handler) CreateItem(ctx context.Context, req *connect.Request[kilnv1.CreateItemRequest]) (*connect.Response[kilnv1.CreateItemResponse], error) {
	return call(ctx, req, h.svc.CreateItem)
}

func (h *handler) GetItem(ctx context.Context, req *connect.Request[kilnv1.GetItemRequest]) (*connect.Response[kilnv1.GetItemResponse], error) {
	return call(ctx, req, h.svc.GetItem)
}

func (h *handler) ListItems(ctx context.Context, req *connect.Request[kilnv1.ListItemsRequest]) (*connect.Response[kilnv1.ListItemsResponse], error) {
	return call(ctx, req, h.svc.ListItems)
}

func (h *handler) UpdateItem(ctx context.Context, req *connect.Request[kilnv1.UpdateItemRequest]) (*connect.Response[kilnv1.UpdateItemResponse], error) {
	return call(ctx, req, h.svc.UpdateItem)
}

func (h *handler) DeleteItem(ctx context.Context, req *connect.Request[kilnv1.DeleteItemRequest]) (*connect.Response[kilnv1.DeleteItemResponse], error) {
	return call(ctx, req, h.svc.DeleteItem)
}

func (h *handler) MoveItem(ctx context.Context, req *connect.Request[kilnv1.MoveItemRequest]) (*connect.Response[kilnv1.MoveItemResponse], error) {
	return call(ctx, req, h.svc.MoveItem)
}

func (h *handler) AddDependency(ctx context.Context, req *connect.Request[kilnv1.AddDependencyRequest]) (*connect.Response[kilnv1.AddDependencyResponse], error) {
	return call(ctx, req, h.svc.AddDependency)
}

func (h *handler) RemoveDependency(ctx context.Context, req *connect.Request[kilnv1.RemoveDependencyRequest]) (*connect.Response[kilnv1.RemoveDependencyResponse], error) {
	return call(ctx, req, h.svc.RemoveDependency)
}

func (h *handler) SetStatus(ctx context.Context, req *connect.Request[kilnv1.SetStatusRequest]) (*connect.Response[kilnv1.SetStatusResponse], error) {
	return call(ctx, req, h.svc.SetStatus)
}

func (h *handler) NextTask(ctx context.Context, req *connect.Request[kilnv1.NextTaskRequest]) (*connect.Response[kilnv1.NextTaskResponse], error) {
	return call(ctx, req, h.svc.NextTask)
}

func (h *handler) ClaimTask(ctx context.Context, req *connect.Request[kilnv1.ClaimTaskRequest]) (*connect.Response[kilnv1.ClaimTaskResponse], error) {
	return call(ctx, req, h.svc.ClaimTask)
}

func (h *handler) UpdateWorkState(ctx context.Context, req *connect.Request[kilnv1.UpdateWorkStateRequest]) (*connect.Response[kilnv1.UpdateWorkStateResponse], error) {
	return call(ctx, req, h.svc.UpdateWorkState)
}

func (h *handler) ReleaseTask(ctx context.Context, req *connect.Request[kilnv1.ReleaseTaskRequest]) (*connect.Response[kilnv1.ReleaseTaskResponse], error) {
	return call(ctx, req, h.svc.ReleaseTask)
}

func (h *handler) GetTaskContext(ctx context.Context, req *connect.Request[kilnv1.GetTaskContextRequest]) (*connect.Response[kilnv1.GetTaskContextResponse], error) {
	return call(ctx, req, h.svc.GetTaskContext)
}

func (h *handler) GetItemChanges(ctx context.Context, req *connect.Request[kilnv1.GetItemChangesRequest]) (*connect.Response[kilnv1.GetItemChangesResponse], error) {
	return call(ctx, req, h.svc.GetItemChanges)
}

func (h *handler) ListHistory(ctx context.Context, req *connect.Request[kilnv1.ListHistoryRequest]) (*connect.Response[kilnv1.ListHistoryResponse], error) {
	return call(ctx, req, h.svc.ListHistory)
}

func (h *handler) AddDecisionNote(ctx context.Context, req *connect.Request[kilnv1.AddDecisionNoteRequest]) (*connect.Response[kilnv1.AddDecisionNoteResponse], error) {
	return call(ctx, req, h.svc.AddDecisionNote)
}
