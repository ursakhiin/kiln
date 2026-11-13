package service

import (
	"context"
	"fmt"
	"slices"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
)

// NextTask claims the best task for the calling agent and returns it with
// its context, or an empty response if there is none.
func (s *Service) NextTask(ctx context.Context, req *kilnv1.NextTaskRequest) (*kilnv1.NextTaskResponse, error) {
	caller, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := requireAgent(caller); err != nil {
		return nil, err
	}
	if p := req.GetProjectId(); p != "" && !core.ValidID(p) {
		return nil, fmt.Errorf("invalid project_id %q: %w", p, core.ErrInvalidArgument)
	}
	task := s.nextCandidate(caller.AgentID, req.GetProjectId())
	if task == nil {
		return &kilnv1.NextTaskResponse{}, nil
	}
	info, text, err := s.claimLocked(caller, task)
	if err != nil {
		return nil, err
	}
	return &kilnv1.NextTaskResponse{Task: info, Context: text}, nil
}

// ClaimTask claims a specific task for the calling agent.
func (s *Service) ClaimTask(ctx context.Context, req *kilnv1.ClaimTaskRequest) (*kilnv1.ClaimTaskResponse, error) {
	caller, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := requireAgent(caller); err != nil {
		return nil, err
	}
	task, err := s.lookupTask(req.GetId())
	if err != nil {
		return nil, err
	}
	if s.holds(caller, task.GetId()) {
		// begin already renewed the claim.
		return &kilnv1.ClaimTaskResponse{Task: s.info(task), Context: s.taskContext(task, false)}, nil
	}
	if c, ok := s.claims[task.GetId()]; ok {
		return nil, fmt.Errorf("task %s is claimed by agent %s: %w", task.GetId(), c.agentID, core.ErrFailedPrecondition)
	}
	if err := s.checkClaimable(task); err != nil {
		return nil, err
	}
	info, text, err := s.claimLocked(caller, task)
	if err != nil {
		return nil, err
	}
	return &kilnv1.ClaimTaskResponse{Task: info, Context: text}, nil
}

// ReleaseTask ends a claim. The task returns to READY if it is IN_PROGRESS.
// Only the claim holder or the user may release it.
func (s *Service) ReleaseTask(ctx context.Context, req *kilnv1.ReleaseTaskRequest) (*kilnv1.ReleaseTaskResponse, error) {
	caller, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.lookupTask(req.GetId())
	if err != nil {
		return nil, err
	}
	c, ok := s.claims[task.GetId()]
	if !ok {
		return nil, fmt.Errorf("task %s is not claimed: %w", task.GetId(), core.ErrFailedPrecondition)
	}
	if caller.Actor != kilnv1.ActorType_ACTOR_TYPE_USER && !s.holds(caller, task.GetId()) {
		return nil, fmt.Errorf("task %s is claimed by agent %s: %w", task.GetId(), c.agentID, core.ErrFailedPrecondition)
	}
	if task.GetTask().GetStatus() == kilnv1.Status_STATUS_IN_PROGRESS {
		if err := s.setStatusLocked(caller, task, kilnv1.Status_STATUS_READY); err != nil {
			return nil, err
		}
	}
	s.endClaim(task.GetId())
	return &kilnv1.ReleaseTaskResponse{Task: s.info(s.items[task.GetId()])}, nil
}

// requireAgent fails with FAILED_PRECONDITION unless the caller is an agent.
func requireAgent(c Caller) error {
	if c.Actor != kilnv1.ActorType_ACTOR_TYPE_AGENT {
		return fmt.Errorf("only agents claim tasks: %w", core.ErrFailedPrecondition)
	}
	return nil
}

// checkClaimable returns why task can't be claimed, apart from an existing
// claim: its feature is missing or a draft, it is blocked, or its status is
// neither READY nor IN_PROGRESS. An IN_PROGRESS task is abandoned work, so
// its blocking isn't checked.
func (s *Service) checkClaimable(task *kilnv1.Item) error {
	id := task.GetId()
	f := s.items[task.GetTask().GetFeatureId()]
	switch {
	case f.GetFeature() == nil:
		return fmt.Errorf("task %s has no feature: %w", id, core.ErrFailedPrecondition)
	case f.GetFeature().GetDraft():
		return fmt.Errorf("task %s is in draft feature %s: %w", id, f.GetId(), core.ErrFailedPrecondition)
	}
	switch st := task.GetTask().GetStatus(); st {
	case kilnv1.Status_STATUS_IN_PROGRESS:
		return nil
	case kilnv1.Status_STATUS_READY:
		if s.isBlocked(task) {
			return fmt.Errorf("task %s is blocked: %w", id, core.ErrFailedPrecondition)
		}
		return nil
	default:
		return fmt.Errorf("task %s has status %v; only ready or abandoned in-progress tasks can be claimed: %w",
			id, st, core.ErrFailedPrecondition)
	}
}

// nextCandidate returns the task NextTask gives agentID, or nil. Among
// unclaimed, claimable tasks (in project projectID, if set) it prefers:
//  1. abandoned IN_PROGRESS tasks,
//  2. ready tasks in the agent's affinity feature,
//  3. ready tasks in features where no other agent holds a claim,
//  4. any other ready task,
//
// each group in feature rank and then task rank order.
func (s *Service) nextCandidate(agentID, projectID string) *kilnv1.Item {
	othersWorking := map[string]bool{} // feature IDs
	for taskID, c := range s.claims {
		if c.agentID != agentID {
			othersWorking[s.items[taskID].GetTask().GetFeatureId()] = true
		}
	}
	var abandoned, affine, free, rest []*kilnv1.Item
	for _, f := range s.sortedFeatures() {
		if projectID != "" && f.GetFeature().GetProjectId() != projectID {
			continue
		}
		for _, t := range s.sortedTasks(f.GetId()) {
			if _, claimed := s.claims[t.GetId()]; claimed || s.checkClaimable(t) != nil {
				continue
			}
			switch {
			case t.GetTask().GetStatus() == kilnv1.Status_STATUS_IN_PROGRESS:
				abandoned = append(abandoned, t)
			case f.GetId() == s.affinity[agentID]:
				affine = append(affine, t)
			case !othersWorking[f.GetId()]:
				free = append(free, t)
			default:
				rest = append(rest, t)
			}
		}
	}
	if all := slices.Concat(abandoned, affine, free, rest); len(all) > 0 {
		return all[0]
	}
	return nil
}

// claimLocked gives the calling agent the claim on task, which must be
// unclaimed and claimable, and returns the task's info and context. A READY
// task moves to IN_PROGRESS; an abandoned IN_PROGRESS task keeps its
// revision and its context says it was resumed.
func (s *Service) claimLocked(caller Caller, task *kilnv1.Item) (*kilnv1.ItemInfo, string, error) {
	id, featureID := task.GetId(), task.GetTask().GetFeatureId()
	resumed := task.GetTask().GetStatus() == kilnv1.Status_STATUS_IN_PROGRESS
	prevAffinity, hadAffinity := s.affinity[caller.AgentID]
	s.startClaim(id, featureID, caller.AgentID)
	if !resumed {
		// setStatusLocked requires the claim to set IN_PROGRESS, so it is
		// taken first.
		if err := s.setStatusLocked(caller, task, kilnv1.Status_STATUS_IN_PROGRESS); err != nil {
			// The claim never took effect, so it is undone rather than ended.
			delete(s.claims, id)
			if hadAffinity {
				s.affinity[caller.AgentID] = prevAffinity
			} else {
				delete(s.affinity, caller.AgentID)
			}
			return nil, "", err
		}
	}
	current := s.items[id]
	return s.info(current), s.taskContext(current, resumed), nil
}
