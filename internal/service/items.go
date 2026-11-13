package service

import (
	"context"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
)

// graph implements core.Graph over the service's items. If override is set,
// it stands in for the cached item with the same ID, so a change can be
// checked before it is written.
type graph struct {
	s        *Service
	override *kilnv1.Item
}

// Item returns the item with this ID, or nil.
func (g graph) Item(id string) *kilnv1.Item {
	if g.override != nil && g.override.GetId() == id {
		return g.override
	}
	return g.s.items[id]
}

// Tasks returns the IDs of a feature's tasks in sortedTasks order. An
// override task is placed last if it belongs to the feature.
func (g graph) Tasks(featureID string) []string {
	var ids []string
	for _, t := range g.s.sortedTasks(featureID) {
		if g.override == nil || t.GetId() != g.override.GetId() {
			ids = append(ids, t.GetId())
		}
	}
	if g.override.GetTask().GetFeatureId() == featureID {
		ids = append(ids, g.override.GetId())
	}
	return ids
}

// CreateItem adds a project, feature or task.
func (s *Service) CreateItem(ctx context.Context, req *kilnv1.CreateItemRequest) (*kilnv1.CreateItemResponse, error) {
	caller, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	item := proto.Clone(req.GetItem()).(*kilnv1.Item)
	if err := core.NormalizeNewItem(caller.Actor, item); err != nil {
		return nil, err
	}
	switch k := item.GetKind().(type) {
	case *kilnv1.Item_Feature:
		if err := s.checkParent(k.Feature.GetProjectId(), kilnv1.ItemKind_ITEM_KIND_PROJECT); err != nil {
			return nil, err
		}
		rank, err := s.bottomRank(ranksOf(s.sortedFeatures()))
		if err != nil {
			return nil, err
		}
		k.Feature.Rank = rank
	case *kilnv1.Item_Task:
		if err := s.checkParent(k.Task.GetFeatureId(), kilnv1.ItemKind_ITEM_KIND_FEATURE); err != nil {
			return nil, err
		}
		rank, err := s.bottomRank(ranksOf(s.sortedTasks(k.Task.GetFeatureId())))
		if err != nil {
			return nil, err
		}
		k.Task.Rank = rank
	}
	item.Id = core.NewID()
	if err := s.commit(caller, item, kilnv1.ChangeType_CHANGE_TYPE_CREATED, nil); err != nil {
		return nil, err
	}
	return &kilnv1.CreateItemResponse{Item: s.info(item)}, nil
}

// UpdateItem changes the fields named in the update mask.
func (s *Service) UpdateItem(ctx context.Context, req *kilnv1.UpdateItemRequest) (*kilnv1.UpdateItemResponse, error) {
	caller, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	current, err := s.lookup(req.GetItem().GetId())
	if err != nil {
		return nil, err
	}
	if err := s.checkBase(current, req.GetBaseRevision()); err != nil {
		return nil, err
	}
	paths := req.GetUpdateMask().GetPaths()
	if err := core.CheckUpdate(caller.Actor, current, req.GetItem(), paths); err != nil {
		return nil, err
	}

	updated := proto.Clone(current).(*kilnv1.Item)
	fields := slices.Clone(paths)
	for _, p := range paths {
		switch p {
		case "title":
			updated.Title = req.GetItem().GetTitle()
		case "description":
			updated.Description = req.GetItem().GetDescription()
		case "feature.project_id":
			if err := s.checkParent(req.GetItem().GetFeature().GetProjectId(), kilnv1.ItemKind_ITEM_KIND_PROJECT); err != nil {
				return nil, err
			}
			updated.GetFeature().ProjectId = req.GetItem().GetFeature().GetProjectId()
		case "feature.draft":
			updated.GetFeature().Draft = req.GetItem().GetFeature().GetDraft()
		case "task.feature_id":
			newFeature := req.GetItem().GetTask().GetFeatureId()
			if err := s.checkParent(newFeature, kilnv1.ItemKind_ITEM_KIND_FEATURE); err != nil {
				return nil, err
			}
			if newFeature != current.GetTask().GetFeatureId() {
				rank, err := s.bottomRank(ranksOf(s.sortedTasks(newFeature)))
				if err != nil {
					return nil, err
				}
				updated.GetTask().FeatureId = newFeature
				updated.GetTask().Rank = rank
				fields = append(fields, "task.rank")
			}
		}
	}

	if updated.GetTask().GetFeatureId() != current.GetTask().GetFeatureId() {
		// A new cycle can pass through a dependency held by any item.
		ids := make([]string, 0, len(s.items))
		for id := range s.items {
			ids = append(ids, id)
		}
		if core.HasCycle(ids, graph{s: s, override: updated}) {
			return nil, fmt.Errorf("moving task %s to feature %s would create a dependency cycle: %w",
				updated.GetId(), updated.GetTask().GetFeatureId(), core.ErrFailedPrecondition)
		}
	}

	if err := s.commit(caller, updated, kilnv1.ChangeType_CHANGE_TYPE_UPDATED, fields); err != nil {
		return nil, err
	}
	return &kilnv1.UpdateItemResponse{Item: s.info(updated)}, nil
}

// DeleteItem removes an item that has no children and that nothing depends
// on. Only the user may delete.
func (s *Service) DeleteItem(ctx context.Context, req *kilnv1.DeleteItemRequest) (*kilnv1.DeleteItemResponse, error) {
	caller, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := requireUser(caller, "delete items"); err != nil {
		return nil, err
	}
	item, err := s.lookup(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.checkBase(item, req.GetBaseRevision()); err != nil {
		return nil, err
	}
	if c, ok := s.claims[item.GetId()]; ok {
		return nil, fmt.Errorf("task %s is claimed by agent %s; release it first: %w", item.GetId(), c.agentID, core.ErrFailedPrecondition)
	}
	for _, other := range s.items {
		if parentOf(other) == item.GetId() {
			return nil, fmt.Errorf("item %s still has children (%s): %w", item.GetId(), other.GetId(), core.ErrFailedPrecondition)
		}
		if slices.ContainsFunc(dependenciesOf(other), func(d *kilnv1.Dependency) bool { return d.GetItemId() == item.GetId() }) {
			return nil, fmt.Errorf("item %s is a dependency of %s: %w", item.GetId(), other.GetId(), core.ErrFailedPrecondition)
		}
	}

	if err := s.store.DeleteItem(item.GetId()); err != nil {
		return nil, err
	}
	delete(s.items, item.GetId())
	delete(s.pending, item.GetId())
	s.appendHistory(caller.actor(), item.GetId(), timestamppb.New(s.now()), &kilnv1.ChangeEvent{
		Type:     kilnv1.ChangeType_CHANGE_TYPE_DELETED,
		Revision: item.GetRevision() + 1,
	})
	return &kilnv1.DeleteItemResponse{}, nil
}

// MoveItem places a feature among all features, or a task among the tasks
// of its feature, directly before or after a sibling. Only the user may move.
func (s *Service) MoveItem(ctx context.Context, req *kilnv1.MoveItemRequest) (*kilnv1.MoveItemResponse, error) {
	caller, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := requireUser(caller, "move items"); err != nil {
		return nil, err
	}
	item, err := s.lookup(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.checkBase(item, req.GetBaseRevision()); err != nil {
		return nil, err
	}

	var siblings []*kilnv1.Item
	var field string
	switch k := item.GetKind().(type) {
	case *kilnv1.Item_Feature:
		siblings, field = s.sortedFeatures(), "feature.rank"
	case *kilnv1.Item_Task:
		siblings, field = s.sortedTasks(k.Task.GetFeatureId()), "task.rank"
	default:
		return nil, fmt.Errorf("only features and tasks can move: %w", core.ErrInvalidArgument)
	}
	siblings = slices.DeleteFunc(slices.Clone(siblings), func(it *kilnv1.Item) bool { return it.GetId() == item.GetId() })

	var anchorID string
	var placeBefore bool
	switch p := req.GetPosition().(type) {
	case *kilnv1.MoveItemRequest_BeforeId:
		anchorID, placeBefore = p.BeforeId, true
	case *kilnv1.MoveItemRequest_AfterId:
		anchorID = p.AfterId
	default:
		return nil, fmt.Errorf("set exactly one of before_id or after_id: %w", core.ErrInvalidArgument)
	}
	if _, err := s.lookup(anchorID); err != nil {
		return nil, err
	}
	if anchorID == item.GetId() {
		return nil, fmt.Errorf("an item can't be moved next to itself: %w", core.ErrInvalidArgument)
	}
	at := slices.IndexFunc(siblings, func(it *kilnv1.Item) bool { return it.GetId() == anchorID })
	if at < 0 {
		return nil, fmt.Errorf("item %s is not a sibling of %s: %w", anchorID, item.GetId(), core.ErrInvalidArgument)
	}

	// The new rank goes between siblings[lo] and siblings[hi].
	lo, hi := at, at+1
	if placeBefore {
		lo, hi = at-1, at
	}
	var lowRank, highRank string
	if lo >= 0 {
		lowRank = rankOf(siblings[lo])
	}
	if hi < len(siblings) {
		highRank = rankOf(siblings[hi])
	}
	if lowRank != "" && lowRank == highRank {
		return nil, fmt.Errorf("ranks tied; move a neighbor first: %w", core.ErrFailedPrecondition)
	}
	rank, err := core.RankBetween(lowRank, highRank)
	if err != nil {
		return nil, err
	}

	moved := proto.Clone(item).(*kilnv1.Item)
	if f := moved.GetFeature(); f != nil {
		f.Rank = rank
	} else {
		moved.GetTask().Rank = rank
	}
	if err := s.commit(caller, moved, kilnv1.ChangeType_CHANGE_TYPE_MOVED, []string{field}); err != nil {
		return nil, err
	}
	return &kilnv1.MoveItemResponse{Item: s.info(moved)}, nil
}

// requireUser fails with PERMISSION_DENIED unless the caller is the user.
func requireUser(c Caller, what string) error {
	if c.Actor != kilnv1.ActorType_ACTOR_TYPE_USER {
		return fmt.Errorf("only the user can %s: %w", what, core.ErrPermissionDenied)
	}
	return nil
}

// checkParent checks that id names an existing item of kind want.
func (s *Service) checkParent(id string, want kilnv1.ItemKind) error {
	parent, err := s.lookup(id)
	if err != nil {
		return err
	}
	if kindOf(parent) != want {
		return fmt.Errorf("item %s is a %s, not a %s: %w", id, kindOf(parent), want, core.ErrInvalidArgument)
	}
	return nil
}

// bottomRank returns a rank after every rank in ranks, which are in order.
func (s *Service) bottomRank(ranks []string) (string, error) {
	last := ""
	if len(ranks) > 0 {
		last = ranks[len(ranks)-1]
	}
	return core.RankBetween(last, "")
}

// ranksOf returns the rank of each feature or task, in order.
func ranksOf(items []*kilnv1.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = rankOf(it)
	}
	return out
}

// rankOf returns the rank of a feature or task.
func rankOf(item *kilnv1.Item) string {
	if f := item.GetFeature(); f != nil {
		return f.GetRank()
	}
	return item.GetTask().GetRank()
}

// parentOf returns the ID of a feature's project or a task's feature.
func parentOf(item *kilnv1.Item) string {
	switch k := item.GetKind().(type) {
	case *kilnv1.Item_Feature:
		return k.Feature.GetProjectId()
	case *kilnv1.Item_Task:
		return k.Task.GetFeatureId()
	}
	return ""
}

// dependenciesOf returns what a feature or task depends on.
func dependenciesOf(item *kilnv1.Item) []*kilnv1.Dependency {
	switch k := item.GetKind().(type) {
	case *kilnv1.Item_Feature:
		return k.Feature.GetDependsOn()
	case *kilnv1.Item_Task:
		return k.Task.GetDependsOn()
	}
	return nil
}
