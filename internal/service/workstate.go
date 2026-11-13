package service

import (
	"context"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
)

// workStateFields are the WorkState fields an update mask may name. Kiln
// sets update_time itself.
var workStateFields = []protoreflect.Name{
	"branch", "worktree_path", "base_commit", "head_commit", "pull_request_url",
	"session", "progress", "files", "credential_refs", "extra",
}

// workStatePrefix prefixes a WorkState field to make an item field path.
const workStatePrefix = "task.work_state."

// pendingWorkState is the work state of a task changed since its last
// history entry: the agent that changed it last, and the changed item field
// paths (e.g. "task.work_state.branch").
type pendingWorkState struct {
	agentID string
	fields  map[string]bool
}

// UpdateWorkState records an agent's work state on a task. It rewrites the
// task file without changing its revision or update_time and adds no history
// entry: the changed fields are recorded in the task's next history entry,
// or in their own entry when the claim ends.
func (s *Service) UpdateWorkState(ctx context.Context, req *kilnv1.UpdateWorkStateRequest) (*kilnv1.UpdateWorkStateResponse, error) {
	caller, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	task, err := s.lookupTask(req.GetId())
	if err != nil {
		return nil, err
	}
	if caller.Actor != kilnv1.ActorType_ACTOR_TYPE_USER && !s.holds(caller, task.GetId()) {
		return nil, fmt.Errorf("you don't hold task %s; claim the task with claim_task: %w",
			task.GetId(), core.ErrFailedPrecondition)
	}
	names, err := workStateMask(req.GetUpdateMask().GetPaths())
	if err != nil {
		return nil, err
	}

	ws := &kilnv1.WorkState{}
	if cur := task.GetTask().GetWorkState(); cur != nil {
		ws = proto.Clone(cur).(*kilnv1.WorkState)
	}
	src := proto.Clone(req.GetWorkState()).(*kilnv1.WorkState)
	if src == nil {
		src = &kilnv1.WorkState{}
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
	if caller.Actor == kilnv1.ActorType_ACTOR_TYPE_AGENT {
		if ws.Session == nil {
			ws.Session = &kilnv1.AgentSession{}
		}
		ws.Session.Host, ws.Session.HostVersion = caller.Host, caller.HostVersion
	}
	ws.UpdateTime = timestamppb.New(s.now())
	if err := core.CheckWorkState(ws); err != nil {
		return nil, err
	}

	updated := proto.Clone(task).(*kilnv1.Item)
	updated.GetTask().WorkState = ws
	if err := s.store.PutItem(updated); err != nil {
		return nil, err
	}
	s.items[updated.GetId()] = updated

	p := s.pending[updated.GetId()]
	if p == nil {
		p = &pendingWorkState{fields: map[string]bool{}}
		s.pending[updated.GetId()] = p
	}
	p.agentID = caller.AgentID
	for _, name := range names {
		p.fields[workStatePrefix+string(name)] = true
	}
	return &kilnv1.UpdateWorkStateResponse{Task: s.info(updated)}, nil
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
// pending work state, if any, attributed to the agent that changed it.
func (s *Service) recordPendingWorkState(taskID string) {
	p, task := s.pending[taskID], s.items[taskID]
	if p == nil || task == nil {
		return
	}
	s.appendWorkStateEntry(task, p.agentID, s.takePending(taskID))
}

// appendWorkStateEntry appends a WORK_STATE_UPDATED entry for task with the
// given changed fields, attributed to agentID and the work state's session.
func (s *Service) appendWorkStateEntry(task *kilnv1.Item, agentID string, fields []string) {
	ws := task.GetTask().GetWorkState()
	actor := &kilnv1.Actor{Type: kilnv1.ActorType_ACTOR_TYPE_AGENT, AgentId: agentID}
	if ws.GetSession() != nil {
		actor.Session = proto.Clone(ws.GetSession()).(*kilnv1.AgentSession)
	}
	s.appendHistory(actor, task.GetId(), timestamppb.New(s.now()), &kilnv1.ChangeEvent{
		Type:          kilnv1.ChangeType_CHANGE_TYPE_WORK_STATE_UPDATED,
		Revision:      task.GetRevision(),
		ChangedFields: fields,
		Snapshot:      proto.Clone(task).(*kilnv1.Item),
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
		var recorded *kilnv1.WorkState
		for _, e := range slices.Backward(entries) {
			if ch := e.GetChange(); ch != nil {
				recorded = ch.GetSnapshot().GetTask().GetWorkState()
				break
			}
		}
		if recorded == nil {
			recorded = &kilnv1.WorkState{}
		}
		if proto.Equal(ws, recorded) {
			continue
		}
		s.appendWorkStateEntry(item, "", workStateDiff(recorded, ws))
	}
}

// workStateDiff returns the item field paths of the mask-settable WorkState
// fields that differ between a and b, sorted.
func workStateDiff(a, b *kilnv1.WorkState) []string {
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
