package service

import (
	"context"

	"google.golang.org/protobuf/proto"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
)

// SetStatus sets a task's status, or a feature's status override.
func (s *Service) SetStatus(ctx context.Context, req *kilnv1.SetStatusRequest) (*kilnv1.SetStatusResponse, error) {
	caller, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	item, err := s.lookup(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.checkBase(item, req.GetBaseRevision()); err != nil {
		return nil, err
	}
	if err := s.setStatusLocked(caller, item, req.GetStatus()); err != nil {
		return nil, err
	}
	return &kilnv1.SetStatusResponse{Item: s.info(s.items[item.GetId()])}, nil
}

// setStatusLocked checks and applies a status change to the cached item,
// with s.mu held. Setting the status an item already has writes nothing.
func (s *Service) setStatusLocked(caller Caller, item *kilnv1.Item, to kilnv1.Status) error {
	if err := core.CheckStatusChange(caller.Actor, item, to); err != nil {
		return err
	}
	updated := proto.Clone(item).(*kilnv1.Item)
	var field string
	switch k := updated.GetKind().(type) {
	case *kilnv1.Item_Feature:
		if k.Feature.GetStatusOverride() == to {
			return nil
		}
		k.Feature.StatusOverride, field = to, "feature.status_override"
	case *kilnv1.Item_Task:
		if k.Task.GetStatus() == to {
			return nil
		}
		k.Task.Status, field = to, "task.status"
	}
	return s.commit(caller, updated, kilnv1.ChangeType_CHANGE_TYPE_STATUS_CHANGED, []string{field})
}
