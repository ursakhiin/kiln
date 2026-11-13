package service

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
	"github.com/ursakhiin/kiln/internal/views"
)

// GetTaskContext returns the markdown context of a task and its revision.
func (s *Service) GetTaskContext(ctx context.Context, req *kilnv1.GetTaskContextRequest) (*kilnv1.GetTaskContextResponse, error) {
	_, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.lookupTask(req.GetId())
	if err != nil {
		return nil, err
	}
	_, claimed := s.claims[task.GetId()]
	resumed := task.GetTask().GetStatus() == kilnv1.Status_STATUS_IN_PROGRESS && !claimed
	return &kilnv1.GetTaskContextResponse{
		Context:  s.taskContext(task, resumed),
		Revision: task.GetRevision(),
	}, nil
}

// lookupTask returns the cached task with id; any other kind of item is
// INVALID_ARGUMENT.
func (s *Service) lookupTask(id string) (*kilnv1.Item, error) {
	item, err := s.lookup(id)
	if err != nil {
		return nil, err
	}
	if item.GetTask() == nil {
		return nil, fmt.Errorf("item %s is a %s, not a task: %w", id, kindOf(item), core.ErrInvalidArgument)
	}
	return item, nil
}

// taskContext renders the markdown context of task. resumed marks a task
// another agent started and abandoned.
func (s *Service) taskContext(task *kilnv1.Item, resumed bool) string {
	c := views.TaskContext{
		Task:         s.info(task),
		Dependencies: s.resolve(task.GetTask().GetDependsOn()),
		Resumed:      resumed,
	}
	featureID := task.GetTask().GetFeatureId()
	if f := s.items[featureID]; f.GetFeature() != nil {
		c.Feature = s.info(f)
		c.FeatureDependencies = s.resolve(f.GetFeature().GetDependsOn())
		if p := s.items[f.GetFeature().GetProjectId()]; p.GetProject() != nil {
			c.Project = proto.Clone(p).(*kilnv1.Item)
		}
	}
	for _, t := range s.sortedTasks(featureID) {
		c.Siblings = append(c.Siblings, s.info(t))
	}
	return views.RenderTaskContext(c)
}

// resolve looks up each dependency for the task view.
func (s *Service) resolve(deps []*kilnv1.Dependency) []views.Dependency {
	out := make([]views.Dependency, 0, len(deps))
	for _, d := range deps {
		vd := views.Dependency{ID: d.GetItemId(), BlockUntil: d.GetBlockUntil()}
		if it, ok := s.items[d.GetItemId()]; ok {
			vd.Item = s.info(it)
		}
		out = append(out, vd)
	}
	return out
}
