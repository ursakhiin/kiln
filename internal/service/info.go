package service

import (
	"cmp"
	"slices"

	"google.golang.org/protobuf/proto"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
)

// info returns a clone of item with the state Kiln computes: its effective
// status, whether it is blocked, and its claim.
func (s *Service) info(item *kilnv1.Item) *kilnv1.ItemInfo {
	return &kilnv1.ItemInfo{
		Item:    proto.Clone(item).(*kilnv1.Item),
		Status:  s.effectiveStatus(item),
		Blocked: s.isBlocked(item),
		Claim:   s.claimOf(item),
	}
}

// claimOf returns the claim on item, if any. Claims don't exist yet.
func (s *Service) claimOf(*kilnv1.Item) *kilnv1.Claim {
	return nil
}

// effectiveStatus is a task's status, a feature's status derived from its
// tasks, or STATUS_UNSPECIFIED for a project.
func (s *Service) effectiveStatus(item *kilnv1.Item) kilnv1.Status {
	switch k := item.GetKind().(type) {
	case *kilnv1.Item_Task:
		return k.Task.GetStatus()
	case *kilnv1.Item_Feature:
		var tasks []kilnv1.Status
		for _, it := range s.items {
			if t := it.GetTask(); t != nil && t.GetFeatureId() == item.GetId() {
				tasks = append(tasks, t.GetStatus())
			}
		}
		return core.DeriveFeatureStatus(k.Feature, tasks)
	default:
		return kilnv1.Status_STATUS_UNSPECIFIED
	}
}

// statusOf returns the effective status of the item with id, for
// core.IsBlocked.
func (s *Service) statusOf(id string) (kilnv1.Status, bool) {
	item, ok := s.items[id]
	if !ok {
		return kilnv1.Status_STATUS_UNSPECIFIED, false
	}
	return s.effectiveStatus(item), true
}

// isBlocked reports whether any dependency still blocks item. A task is also
// blocked while its feature is: it waits for its feature to start. Projects
// are never blocked.
func (s *Service) isBlocked(item *kilnv1.Item) bool {
	switch k := item.GetKind().(type) {
	case *kilnv1.Item_Feature:
		return core.IsBlocked(k.Feature.GetDependsOn(), s.statusOf)
	case *kilnv1.Item_Task:
		if core.IsBlocked(k.Task.GetDependsOn(), s.statusOf) {
			return true
		}
		f, ok := s.items[k.Task.GetFeatureId()]
		return ok && f.GetFeature() != nil && s.isBlocked(f)
	default:
		return false
	}
}

// sortedFeatures returns every feature by rank, ties broken by ID.
func (s *Service) sortedFeatures() []*kilnv1.Item {
	var fs []*kilnv1.Item
	for _, it := range s.items {
		if it.GetFeature() != nil {
			fs = append(fs, it)
		}
	}
	slices.SortFunc(fs, func(a, b *kilnv1.Item) int {
		return cmp.Or(cmp.Compare(a.GetFeature().GetRank(), b.GetFeature().GetRank()), cmp.Compare(a.GetId(), b.GetId()))
	})
	return fs
}

// sortedTasks returns the tasks of the feature featureID by rank, ties broken
// by ID.
func (s *Service) sortedTasks(featureID string) []*kilnv1.Item {
	var ts []*kilnv1.Item
	for _, it := range s.items {
		if it.GetTask() != nil && it.GetTask().GetFeatureId() == featureID {
			ts = append(ts, it)
		}
	}
	slices.SortFunc(ts, func(a, b *kilnv1.Item) int {
		return cmp.Or(cmp.Compare(a.GetTask().GetRank(), b.GetTask().GetRank()), cmp.Compare(a.GetId(), b.GetId()))
	})
	return ts
}
