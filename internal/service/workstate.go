package service

import (
	"context"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/core"
)

// workStateFields are the WorkState fields an update mask may name. Underdraw
// sets update_time itself.
var workStateFields = []protoreflect.Name{
	"branch", "worktree_path", "base_commit", "head_commit", "pull_request_url",
	"session", "progress", "files", "credential_refs", "extra",
}

// workStatePrefix prefixes a WorkState field to make an item field path.
const workStatePrefix = "task.work_state."

// pendingWorkState is the work state of a task changed since its last
// history entry: who changed it (the agent that changed it last, or the user
// if no agent did), and the changed item field paths (e.g.
// "task.work_state.branch").
type pendingWorkState struct {
	actor   underdrawv1.ActorType
	agentID string
	fields  map[string]bool
}

// UpdateWorkState records an agent's work state on a task. It rewrites the
// task file without changing its revision or update_time and adds no history
// entry: the changed fields are recorded in the task's next history entry,
// or in their own entry when the claim ends.
func (s *Service) UpdateWorkState(ctx context.Context, req *underdrawv1.UpdateWorkStateRequest) (*underdrawv1.UpdateWorkStateResponse, error) {
	caller, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	task, err := s.lookupTask(req.GetId())
	if err != nil {
		return nil, err
	}
	if caller.Actor != underdrawv1.ActorType_ACTOR_TYPE_USER && !s.holds(caller, task.GetId()) {
		return nil, fmt.Errorf("you don't hold task %s; claim the task with claim_task: %w",
			task.GetId(), core.ErrFailedPrecondition)
	}
	names, err := workStateMask(req.GetUpdateMask().GetPaths())
	if err != nil {
		return nil, err
	}

	ws := &underdrawv1.WorkState{}
	if cur := task.GetTask().GetWorkState(); cur != nil {
		ws = proto.Clone(cur).(*underdrawv1.WorkState)
	}
	src := proto.Clone(req.GetWorkState()).(*underdrawv1.WorkState)
	if src == nil {
		src = &underdrawv1.WorkState{}
	}
	dst, from := ws.ProtoReflect(), src.ProtoReflect()
	fds := dst.Descriptor().Fields()
	for _, name := range names {
		fd := fds.ByName(name)
		if from.Has(fd) {
			dst.Set(fd, from.Get(fd))
		} else {
			dst.Clear(fd)
		}
	}
	if caller.Actor == underdrawv1.ActorType_ACTOR_TYPE_AGENT {
		if ws.Session == nil {
			ws.Session = &underdrawv1.AgentSession{}
		}
		ws.Session.Host, ws.Session.HostVersion = caller.Host, caller.HostVersion
	}
	ws.UpdateTime = timestamppb.New(s.now())
	if err := core.CheckWorkState(ws); err != nil {
		return nil, err
	}

	updated := proto.Clone(task).(*underdrawv1.Item)
	updated.GetTask().WorkState = ws
	if err := s.store.PutItem(updated); err != nil {
		return nil, err
	}
	s.items[updated.GetId()] = updated

	p := s.pending[updated.GetId()]
	if p == nil {
		p = &pendingWorkState{actor: caller.Actor, fields: map[string]bool{}}
		s.pending[updated.GetId()] = p
	}
	if caller.Actor == underdrawv1.ActorType_ACTOR_TYPE_AGENT {
		p.actor, p.agentID = caller.Actor, caller.AgentID
	}
	for _, name := range names {
		p.fields[workStatePrefix+string(name)] = true
	}
	if !proto.Equal(ws.GetSession(), task.GetTask().GetWorkState().GetSession()) {
		p.fields[workStatePrefix+"session"] = true
	}
	return &underdrawv1.UpdateWorkStateResponse{Task: s.info(updated)}, nil
}

// workStateMask checks an update mask's paths, which name top-level
// WorkState fields. An empty mask names all of them.
func workStateMask(paths []string) ([]protoreflect.Name, error) {
	if len(paths) == 0 {
		return workStateFields, nil
	}
	names := make([]protoreflect.Name, 0, len(paths))
	for _, p := range paths {
		name := protoreflect.Name(p)
		if !slices.Contains(workStateFields, name) {
			return nil, fmt.Errorf("unknown work_state field %q in update_mask: %w", p, core.ErrInvalidArgument)
		}
		names = append(names, name)
	}
	return names, nil
}

// takePending returns taskID's pending work-state field paths, sorted, and
// clears them.
func (s *Service) takePending(taskID string) []string {
	p := s.pending[taskID]
	if p == nil {
		return nil
	}
	delete(s.pending, taskID)
	fields := make([]string, 0, len(p.fields))
	for f := range p.fields {
		fields = append(fields, f)
	}
	slices.Sort(fields)
	return fields
}

// recordPendingWorkState appends a WORK_STATE_UPDATED entry for taskID's
// pending work state, if any, attributed to whoever changed it.
func (s *Service) recordPendingWorkState(taskID string) {
	p, task := s.pending[taskID], s.items[taskID]
	if p == nil || task == nil {
		return
	}
	actor := &underdrawv1.Actor{Type: underdrawv1.ActorType_ACTOR_TYPE_USER}
	if p.actor == underdrawv1.ActorType_ACTOR_TYPE_AGENT {
		actor = agentWorkStateActor(task, p.agentID)
	}
	s.appendWorkStateEntry(task, actor, s.takePending(taskID))
}

// agentWorkStateActor returns the agent agentID as the actor of a
// WORK_STATE_UPDATED entry for task, with the work state's session.
func agentWorkStateActor(task *underdrawv1.Item, agentID string) *underdrawv1.Actor {
	actor := &underdrawv1.Actor{Type: underdrawv1.ActorType_ACTOR_TYPE_AGENT, AgentId: agentID}
	if sess := task.GetTask().GetWorkState().GetSession(); sess != nil {
		actor.Session = proto.Clone(sess).(*underdrawv1.AgentSession)
	}
	return actor
}

// appendWorkStateEntry appends a WORK_STATE_UPDATED entry for task with the
// given changed fields.
func (s *Service) appendWorkStateEntry(task *underdrawv1.Item, actor *underdrawv1.Actor, fields []string) {
	s.appendHistory(actor, task.GetId(), timestamppb.New(s.now()), &underdrawv1.ChangeEvent{
		Type:          underdrawv1.ChangeType_CHANGE_TYPE_WORK_STATE_UPDATED,
		Revision:      task.GetRevision(),
		ChangedFields: fields,
		Snapshot:      proto.Clone(task).(*underdrawv1.Item),
	})
}

// reconcileWorkState records work state that a previous daemon saved but
// didn't record in history before it stopped: for each task whose work state
// differs from the snapshot in its latest ChangeEvent, it appends a
// WORK_STATE_UPDATED entry listing the differing fields. A task without a
// ChangeEvent is compared with an empty work state.
func (s *Service) reconcileWorkState() {
	for _, item := range s.items {
		ws := item.GetTask().GetWorkState()
		if ws == nil {
			continue
		}
		entries, err := s.itemHistory(item.GetId())
		if err != nil {
			s.log.Error("can't check work state against history", "item", item.GetId(), "err", err)
			continue
		}
		var recorded *underdrawv1.WorkState
		for _, e := range slices.Backward(entries) {
			if ch := e.GetChange(); ch != nil {
				recorded = ch.GetSnapshot().GetTask().GetWorkState()
				break
			}
		}
		if recorded == nil {
			recorded = &underdrawv1.WorkState{}
		}
		// update_time alone isn't a change worth recording.
		fields := workStateDiff(recorded, ws)
		if len(fields) == 0 {
			continue
		}
		s.appendWorkStateEntry(item, agentWorkStateActor(item, ""), fields)
	}
}

// workStateDiff returns the item field paths of the mask-settable WorkState
// fields that differ between a and b, sorted.
func workStateDiff(a, b *underdrawv1.WorkState) []string {
	ra, rb := a.ProtoReflect(), b.ProtoReflect()
	fds := ra.Descriptor().Fields()
	var fields []string
	for _, name := range workStateFields {
		fd := fds.ByName(name)
		if ra.Has(fd) != rb.Has(fd) || (ra.Has(fd) && !ra.Get(fd).Equal(rb.Get(fd))) {
			fields = append(fields, workStatePrefix+string(name))
		}
	}
	slices.Sort(fields)
	return fields
}
