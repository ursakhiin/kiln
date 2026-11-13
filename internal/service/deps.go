package service

import (
	"context"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
)

// AddDependency makes a feature or task depend on another feature or task.
func (s *Service) AddDependency(ctx context.Context, req *kilnv1.AddDependencyRequest) (*kilnv1.AddDependencyResponse, error) {
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
	if slices.ContainsFunc(dependenciesOf(item), func(d *kilnv1.Dependency) bool { return d.GetItemId() == blocker.GetId() }) {
		return nil, fmt.Errorf("item %s already depends on %s; remove it first: %w",
			item.GetId(), blocker.GetId(), core.ErrFailedPrecondition)
	}
	if core.WouldCreateCycle(item.GetId(), blocker.GetId(), graph{s: s}) {
		return nil, fmt.Errorf("item %s depending on %s would create a dependency cycle: %w",
			item.GetId(), blocker.GetId(), core.ErrFailedPrecondition)
	}

	d := proto.Clone(req.GetDependency()).(*kilnv1.Dependency)
	switch d.GetBlockUntil() {
	case kilnv1.BlockUntil_BLOCK_UNTIL_UNSPECIFIED:
		d.BlockUntil = kilnv1.BlockUntil_BLOCK_UNTIL_DONE
	case kilnv1.BlockUntil_BLOCK_UNTIL_DONE, kilnv1.BlockUntil_BLOCK_UNTIL_IN_REVIEW:
	default:
		return nil, fmt.Errorf("unknown block_until value %d: %w", d.GetBlockUntil(), core.ErrInvalidArgument)
	}
	updated := proto.Clone(item).(*kilnv1.Item)
	switch k := updated.GetKind().(type) {
	case *kilnv1.Item_Feature:
		k.Feature.DependsOn = append(k.Feature.DependsOn, d)
	case *kilnv1.Item_Task:
		k.Task.DependsOn = append(k.Task.DependsOn, d)
	}
	if err := s.commit(caller, updated, kilnv1.ChangeType_CHANGE_TYPE_DEPENDENCY_ADDED, []string{field}); err != nil {
		return nil, err
	}
	return &kilnv1.AddDependencyResponse{Item: s.info(updated)}, nil
}

// RemoveDependency removes one of an item's dependencies.
func (s *Service) RemoveDependency(ctx context.Context, req *kilnv1.RemoveDependencyRequest) (*kilnv1.RemoveDependencyResponse, error) {
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
	matches := func(d *kilnv1.Dependency) bool { return d.GetItemId() == req.GetDependsOnId() }
	if !slices.ContainsFunc(dependenciesOf(item), matches) {
		return nil, fmt.Errorf("item %s does not depend on %s: %w", item.GetId(), req.GetDependsOnId(), core.ErrNotFound)
	}

	updated := proto.Clone(item).(*kilnv1.Item)
	switch k := updated.GetKind().(type) {
	case *kilnv1.Item_Feature:
		k.Feature.DependsOn = slices.DeleteFunc(k.Feature.DependsOn, matches)
	case *kilnv1.Item_Task:
		k.Task.DependsOn = slices.DeleteFunc(k.Task.DependsOn, matches)
	}
	if err := s.commit(caller, updated, kilnv1.ChangeType_CHANGE_TYPE_DEPENDENCY_REMOVED, []string{field}); err != nil {
		return nil, err
	}
	return &kilnv1.RemoveDependencyResponse{Item: s.info(updated)}, nil
}

// dependsOnField returns the field path of a feature's or task's
// dependencies, or INVALID_ARGUMENT for any other kind of item.
func dependsOnField(item *kilnv1.Item) (string, error) {
	switch item.GetKind().(type) {
	case *kilnv1.Item_Feature:
		return "feature.depends_on", nil
	case *kilnv1.Item_Task:
		return "task.depends_on", nil
	}
	return "", fmt.Errorf("item %s is a %s; only features and tasks have dependencies: %w",
		item.GetId(), kindOf(item), core.ErrInvalidArgument)
}
