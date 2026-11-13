package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/core"
)

// GetItem returns one item.
func (s *Service) GetItem(ctx context.Context, req *underdrawv1.GetItemRequest) (*underdrawv1.GetItemResponse, error) {
	_, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	item, err := s.lookup(req.GetId())
	if err != nil {
		return nil, err
	}
	return &underdrawv1.GetItemResponse{Item: s.info(item)}, nil
}

// ListItems returns the items matching every filter in req: projects by ID,
// then features in feature-rank order, then tasks grouped by feature (in
// feature-rank order) and by task rank, then tasks whose feature is missing,
// by ID.
func (s *Service) ListItems(ctx context.Context, req *underdrawv1.ListItemsRequest) (*underdrawv1.ListItemsResponse, error) {
	_, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	for _, f := range []struct{ name, id string }{
		{"project_id", req.GetProjectId()},
		{"feature_id", req.GetFeatureId()},
	} {
		if f.id != "" && !core.ValidID(f.id) {
			return nil, fmt.Errorf("invalid %s %q: %w", f.name, f.id, core.ErrInvalidArgument)
		}
	}

	resp := &underdrawv1.ListItemsResponse{}
	for _, item := range s.listOrder() {
		if s.matches(item, req) {
			resp.Items = append(resp.Items, s.info(item))
		}
	}
	return resp, nil
}

// listOrder returns every item in ListItems order.
func (s *Service) listOrder() []*underdrawv1.Item {
	var projects, orphans []*underdrawv1.Item
	for _, it := range s.items {
		switch {
		case it.GetProject() != nil:
			projects = append(projects, it)
		case it.GetTask() != nil && s.items[it.GetTask().GetFeatureId()].GetFeature() == nil:
			orphans = append(orphans, it)
		}
	}
	byID := func(a, b *underdrawv1.Item) int { return strings.Compare(a.GetId(), b.GetId()) }
	slices.SortFunc(projects, byID)
	slices.SortFunc(orphans, byID)

	features := s.sortedFeatures()
	out := append(projects, features...)
	for _, f := range features {
		out = append(out, s.sortedTasks(f.GetId())...)
	}
	return append(out, orphans...)
}

// matches reports whether item passes every filter in req.
func (s *Service) matches(item *underdrawv1.Item, req *underdrawv1.ListItemsRequest) bool {
	if k := req.GetKind(); k != underdrawv1.ItemKind_ITEM_KIND_UNSPECIFIED && k != kindOf(item) {
		return false
	}
	if p := req.GetProjectId(); p != "" {
		f := item.GetFeature()
		if t := item.GetTask(); t != nil {
			f = s.items[t.GetFeatureId()].GetFeature()
		}
		if f == nil || f.GetProjectId() != p {
			return false
		}
	}
	if f := req.GetFeatureId(); f != "" && (item.GetTask() == nil || item.GetTask().GetFeatureId() != f) {
		return false
	}
	if st := req.GetStatuses(); len(st) > 0 && !slices.Contains(st, s.effectiveStatus(item)) {
		return false
	}
	return true
}

// kindOf returns item's ItemKind.
func kindOf(item *underdrawv1.Item) underdrawv1.ItemKind {
	switch item.GetKind().(type) {
	case *underdrawv1.Item_Project:
		return underdrawv1.ItemKind_ITEM_KIND_PROJECT
	case *underdrawv1.Item_Feature:
		return underdrawv1.ItemKind_ITEM_KIND_FEATURE
	case *underdrawv1.Item_Task:
		return underdrawv1.ItemKind_ITEM_KIND_TASK
	default:
		return underdrawv1.ItemKind_ITEM_KIND_UNSPECIFIED
	}
}
