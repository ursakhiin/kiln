package service

import (
	"context"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/core"
)

// AddDependency makes a feature or task depend on another feature or task.
func (s *Service) AddDependency(ctx context.Context, req *underdrawv1.AddDependencyRequest) (*underdrawv1.AddDependencyResponse, error) {
	caller, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	item, err := s.lookup(req.GetItemId())
	if err != nil {
		return nil, err
	}
	if err := s.checkBase(item, req.GetBaseRevision()); err != nil {
		return nil, err
	}
	field, err := dependsOnField(item)
	if err != nil {
		return nil, err
	}
	blocker, err := s.lookup(req.GetDependency().GetItemId())
	if err != nil {
		return nil, err
	}
	if _, err := dependsOnField(blocker); err != nil {
		return nil, err
	}
	if slices.ContainsFunc(dependenciesOf(item), func(d *underdrawv1.Dependency) bool { return d.GetItemId() == blocker.GetId() }) {
		return nil, fmt.Errorf("item %s already depends on %s; remove it first: %w",
			item.GetId(), blocker.GetId(), core.ErrFailedPrecondition)
	}
	if core.WouldCreateCycle(item.GetId(), blocker.GetId(), graph{s: s}) {
		return nil, fmt.Errorf("item %s depending on %s would create a dependency cycle: %w",
			item.GetId(), blocker.GetId(), core.ErrFailedPrecondition)
	}

	d := proto.Clone(req.GetDependency()).(*underdrawv1.Dependency)
	switch d.GetBlockUntil() {
	case underdrawv1.BlockUntil_BLOCK_UNTIL_UNSPECIFIED:
		d.BlockUntil = underdrawv1.BlockUntil_BLOCK_UNTIL_DONE
	case underdrawv1.BlockUntil_BLOCK_UNTIL_DONE, underdrawv1.BlockUntil_BLOCK_UNTIL_IN_REVIEW:
	default:
		return nil, fmt.Errorf("unknown block_until value %d: %w", d.GetBlockUntil(), core.ErrInvalidArgument)
	}
	updated := proto.Clone(item).(*underdrawv1.Item)
	switch k := updated.GetKind().(type) {
	case *underdrawv1.Item_Feature:
		k.Feature.DependsOn = append(k.Feature.DependsOn, d)
	case *underdrawv1.Item_Task:
		k.Task.DependsOn = append(k.Task.DependsOn, d)
	}
	if err := s.commit(caller, updated, underdrawv1.ChangeType_CHANGE_TYPE_DEPENDENCY_ADDED, []string{field}); err != nil {
		return nil, err
	}
	return &underdrawv1.AddDependencyResponse{Item: s.info(updated)}, nil
}

// RemoveDependency removes one of an item's dependencies.
func (s *Service) RemoveDependency(ctx context.Context, req *underdrawv1.RemoveDependencyRequest) (*underdrawv1.RemoveDependencyResponse, error) {
	caller, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	item, err := s.lookup(req.GetItemId())
	if err != nil {
		return nil, err
	}
	if err := s.checkBase(item, req.GetBaseRevision()); err != nil {
		return nil, err
	}
	field, err := dependsOnField(item)
	if err != nil {
		return nil, err
	}
	matches := func(d *underdrawv1.Dependency) bool { return d.GetItemId() == req.GetDependsOnId() }
	if !slices.ContainsFunc(dependenciesOf(item), matches) {
		return nil, fmt.Errorf("item %s does not depend on %s: %w", item.GetId(), req.GetDependsOnId(), core.ErrNotFound)
	}

	updated := proto.Clone(item).(*underdrawv1.Item)
	switch k := updated.GetKind().(type) {
	case *underdrawv1.Item_Feature:
		k.Feature.DependsOn = slices.DeleteFunc(k.Feature.DependsOn, matches)
	case *underdrawv1.Item_Task:
		k.Task.DependsOn = slices.DeleteFunc(k.Task.DependsOn, matches)
	}
	if err := s.commit(caller, updated, underdrawv1.ChangeType_CHANGE_TYPE_DEPENDENCY_REMOVED, []string{field}); err != nil {
		return nil, err
	}
	return &underdrawv1.RemoveDependencyResponse{Item: s.info(updated)}, nil
}

// dependsOnField returns the field path of a feature's or task's
// dependencies, or INVALID_ARGUMENT for any other kind of item.
func dependsOnField(item *underdrawv1.Item) (string, error) {
	switch item.GetKind().(type) {
	case *underdrawv1.Item_Feature:
		return "feature.depends_on", nil
	case *underdrawv1.Item_Task:
		return "task.depends_on", nil
	}
	return "", fmt.Errorf("item %s is a %s; only features and tasks have dependencies: %w",
		item.GetId(), kindOf(item), core.ErrInvalidArgument)
}
