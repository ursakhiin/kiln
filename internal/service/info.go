package service

import (
	"cmp"
	"slices"

	"google.golang.org/protobuf/proto"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/core"
)

// info returns a clone of item with the state Underdraw computes: its effective
// status, whether it is blocked, and its claim.
func (s *Service) info(item *underdrawv1.Item) *underdrawv1.ItemInfo {
	return &underdrawv1.ItemInfo{
		Item:    proto.Clone(item).(*underdrawv1.Item),
		Status:  s.effectiveStatus(item),
		Blocked: s.isBlocked(item),
		Claim:   s.claimOf(item),
	}
}

// effectiveStatus is a task's status, a feature's status derived from its
// tasks, or STATUS_UNSPECIFIED for a project.
func (s *Service) effectiveStatus(item *underdrawv1.Item) underdrawv1.Status {
	switch k := item.GetKind().(type) {
	case *underdrawv1.Item_Task:
		return k.Task.GetStatus()
	case *underdrawv1.Item_Feature:
		var tasks []underdrawv1.Status
		for _, it := range s.items {
			if t := it.GetTask(); t != nil && t.GetFeatureId() == item.GetId() {
				tasks = append(tasks, t.GetStatus())
			}
		}
		return core.DeriveFeatureStatus(k.Feature, tasks)
	default:
		return underdrawv1.Status_STATUS_UNSPECIFIED
	}
}

// statusOf returns the effective status of the item with id, for
// core.IsBlocked.
func (s *Service) statusOf(id string) (underdrawv1.Status, bool) {
	item, ok := s.items[id]
	if !ok {
		return underdrawv1.Status_STATUS_UNSPECIFIED, false
	}
	return s.effectiveStatus(item), true
}

// isBlocked reports whether any dependency still blocks item. A task is also
// blocked while its feature is, or when its feature is missing: it waits for
// its feature to start, and a missing item blocks, as in core.IsBlocked.
// Projects are never blocked.
func (s *Service) isBlocked(item *underdrawv1.Item) bool {
	switch k := item.GetKind().(type) {
	case *underdrawv1.Item_Feature:
		return core.IsBlocked(k.Feature.GetDependsOn(), s.statusOf)
	case *underdrawv1.Item_Task:
		if core.IsBlocked(k.Task.GetDependsOn(), s.statusOf) {
			return true
		}
		f, ok := s.items[k.Task.GetFeatureId()]
		return !ok || f.GetFeature() == nil || s.isBlocked(f)
	default:
		return false
	}
}

// sortedFeatures returns every feature by rank, ties broken by ID.
func (s *Service) sortedFeatures() []*underdrawv1.Item {
	var fs []*underdrawv1.Item
	for _, it := range s.items {
		if it.GetFeature() != nil {
			fs = append(fs, it)
		}
	}
	slices.SortFunc(fs, func(a, b *underdrawv1.Item) int {
		return cmp.Or(cmp.Compare(a.GetFeature().GetRank(), b.GetFeature().GetRank()), cmp.Compare(a.GetId(), b.GetId()))
	})
	return fs
}

// sortedTasks returns the tasks of the feature featureID by rank, ties broken
// by ID.
func (s *Service) sortedTasks(featureID string) []*underdrawv1.Item {
	var ts []*underdrawv1.Item
	for _, it := range s.items {
		if it.GetTask() != nil && it.GetTask().GetFeatureId() == featureID {
			ts = append(ts, it)
		}
	}
	slices.SortFunc(ts, func(a, b *underdrawv1.Item) int {
		return cmp.Or(cmp.Compare(a.GetTask().GetRank(), b.GetTask().GetRank()), cmp.Compare(a.GetId(), b.GetId()))
	})
	return ts
}
