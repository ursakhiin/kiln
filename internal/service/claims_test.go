package service_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/service"
)

const (
	ready      = underdrawv1.Status_STATUS_READY
	inProgress = underdrawv1.Status_STATUS_IN_PROGRESS
	inReview   = underdrawv1.Status_STATUS_IN_REVIEW
)

// next calls NextTask for ctx and fails the test on error.
func next(t *testing.T, svc *service.Service, ctx context.Context) *underdrawv1.NextTaskResponse {
	t.Helper()
	resp, err := svc.NextTask(ctx, &underdrawv1.NextTaskRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// nextID returns the ID of the task NextTask gives ctx, or "" for none.
func nextID(t *testing.T, svc *service.Service, ctx context.Context) string {
	t.Helper()
	return next(t, svc, ctx).GetTask().GetItem().GetId()
}

func claim(t *testing.T, svc *service.Service, ctx context.Context, n int) *underdrawv1.ClaimTaskResponse {
	t.Helper()
	resp, err := svc.ClaimTask(ctx, &underdrawv1.ClaimTaskRequest{Id: id(n)})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// oneTask is project 1, feature 2 and READY task 3.
func oneTask() []*underdrawv1.Item {
	return []*underdrawv1.Item{proj(1), feat(2, 1, "a"), task(3, 2, "a", ready)}
}

func TestNextTaskSpreadsAgents(t *testing.T) {
	const fA, fB, a1, a2, b1 = 2, 3, 4, 5, 6
	svc, _, _ := newService(t,
		proj(1), feat(fA, 1, "a"), feat(fB, 1, "b"),
		task(a1, fA, "a", ready), task(a2, fA, "b", ready), task(b1, fB, "a", ready),
	)
	for _, step := range []struct {
		agent string
		want  string
	}{
		{"a1", id(a1)},
		{"a2", id(b1)}, // A has a1's claim
		{"a3", id(a2)},
		{"a4", ""},
	} {
		if got := nextID(t, svc, agent(step.agent)); got != step.want {
			t.Fatalf("%s got %q, want %q", step.agent, got, step.want)
		}
	}
	info := getInfo(t, svc, a1)
	if info.GetStatus() != inProgress || info.GetItem().GetRevision() != 2 || info.GetClaim().GetAgentId() != "a1" {
		t.Fatalf("A1 = %v", info)
	}
}

func TestNextTaskAffinity(t *testing.T) {
	const fA, fB, a1, b1, b2 = 2, 3, 4, 5, 6
	svc, _, _ := newService(t,
		proj(1), feat(fA, 1, "a"), feat(fB, 1, "b"),
		task(a1, fA, "a", ready), task(b1, fB, "a", ready), task(b2, fB, "b", ready),
	)
	resp := claim(t, svc, agent("a1"), b1)
	if _, err := svc.SetStatus(agent("a1"), setStatus(b1, resp.GetTask().GetItem().GetRevision(), inReview)); err != nil {
		t.Fatal(err)
	}
	if got := nextID(t, svc, agent("a1")); got != id(b2) {
		t.Fatalf("got %q, want B2 %q", got, id(b2))
	}
}

func TestNextTaskSkips(t *testing.T) {
	const (
		fOK, fDraft, fBlocked, fOther                              = 2, 3, 4, 5
		tDraftFeature, tBlocked, tBlockedFeature, tClaimed, tDraft = 10, 11, 12, 13, 14
		tOtherProject                                              = 15
	)
	draftFeature := feat(fDraft, 1, "b")
	draftFeature.GetFeature().Draft = true
	svc, _, _ := newService(t,
		proj(1), proj(6),
		feat(fOK, 1, "a"), draftFeature,
		feat(fBlocked, 1, "c", dep(tDraft, underdrawv1.BlockUntil_BLOCK_UNTIL_DONE)),
		feat(fOther, 6, "d"),
		task(tDraftFeature, fDraft, "a", ready),
		task(tBlocked, fOK, "a", ready, dep(tDraft, underdrawv1.BlockUntil_BLOCK_UNTIL_DONE)),
		task(tBlockedFeature, fBlocked, "a", ready),
		task(tClaimed, fOK, "b", ready),
		task(tDraft, fOK, "c", underdrawv1.Status_STATUS_DRAFT),
		task(tOtherProject, fOther, "a", ready),
	)
	claim(t, svc, agent("a2"), tClaimed)

	resp, err := svc.NextTask(agent("a1"), &underdrawv1.NextTaskRequest{ProjectId: id(1)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetTask() != nil || resp.GetContext() != "" {
		t.Fatalf("got %v", resp)
	}
	// Without the project filter, the other project's task is offered.
	if got := nextID(t, svc, agent("a1")); got != id(tOtherProject) {
		t.Fatalf("got %q, want %q", got, id(tOtherProject))
	}

	_, err = svc.NextTask(agent("a1"), &underdrawv1.NextTaskRequest{ProjectId: "nope"})
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestClaimExpiresAndResumes(t *testing.T) {
	svc, clk, _ := newService(t, oneTask()...)
	first := next(t, svc, agent("a1"))
	if first.GetTask().GetItem().GetId() != id(3) {
		t.Fatalf("a1 got %v", first.GetTask())
	}
	if strings.Contains(first.GetContext(), "**Resumed task:**") {
		t.Fatal("fresh task marked resumed")
	}
	clk.Advance(31 * time.Minute)

	resp := next(t, svc, agent("a2"))
	got := resp.GetTask()
	if got.GetItem().GetId() != id(3) || got.GetItem().GetRevision() != first.GetTask().GetItem().GetRevision() {
		t.Fatalf("a2 got %v", got)
	}
	if got.GetClaim().GetAgentId() != "a2" || got.GetStatus() != inProgress {
		t.Fatalf("claim = %v status = %v", got.GetClaim(), got.GetStatus())
	}
	if !got.GetClaim().GetExpireTime().AsTime().Equal(clk.Now().Add(service.DefaultClaimTTL)) {
		t.Fatalf("expire = %v", got.GetClaim().GetExpireTime().AsTime())
	}
	if !strings.Contains(resp.GetContext(), "**Resumed task:**") {
		t.Fatalf("context not resumed:\n%s", resp.GetContext())
	}
}

func TestClaimRenews(t *testing.T) {
	svc, clk, _ := newService(t, oneTask()...)
	if got := nextID(t, svc, agent("a1")); got != id(3) {
		t.Fatalf("a1 got %q", got)
	}
	clk.Advance(20 * time.Minute)
	if _, err := svc.GetItem(agent("a1"), &underdrawv1.GetItemRequest{Id: id(1)}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(20 * time.Minute)
	if got := nextID(t, svc, agent("a2")); got != "" {
		t.Fatalf("a2 got %q, want none", got)
	}
	if c := getInfo(t, svc, 3).GetClaim(); c.GetAgentId() != "a1" {
		t.Fatalf("claim = %v", c)
	}
}

func TestClaimTask(t *testing.T) {
	const blocked = 4
	svc, _, _ := newService(t, append(oneTask(),
		task(blocked, 2, "b", ready, dep(3, underdrawv1.BlockUntil_BLOCK_UNTIL_DONE)))...)
	resp := claim(t, svc, agent("a1"), 3)
	if resp.GetTask().GetStatus() != inProgress || !strings.HasPrefix(resp.GetContext(), "# Task: ") {
		t.Fatalf("got %v", resp)
	}

	_, err := svc.ClaimTask(agent("a2"), &underdrawv1.ClaimTaskRequest{Id: id(3)})
	wantCode(t, err, connect.CodeFailedPrecondition)

	_, err = svc.ClaimTask(agent("a2"), &underdrawv1.ClaimTaskRequest{Id: id(blocked)})
	wantCode(t, err, connect.CodeFailedPrecondition)
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("err = %v", err)
	}

	_, err = svc.NextTask(user, &underdrawv1.NextTaskRequest{})
	wantCode(t, err, connect.CodeFailedPrecondition)
	_, err = svc.ClaimTask(user, &underdrawv1.ClaimTaskRequest{Id: id(3)})
	wantCode(t, err, connect.CodeFailedPrecondition)

	_, err = svc.ClaimTask(agent("a1"), &underdrawv1.ClaimTaskRequest{Id: id(2)})
	wantCode(t, err, connect.CodeInvalidArgument)

	again := claim(t, svc, agent("a1"), 3)
	if again.GetTask().GetItem().GetRevision() != resp.GetTask().GetItem().GetRevision() ||
		again.GetTask().GetClaim().GetAgentId() != "a1" || again.GetContext() == "" {
		t.Fatalf("re-claim got %v", again)
	}
}

func TestClaimTaskDraftFeature(t *testing.T) {
	draft := feat(2, 1, "a")
	draft.GetFeature().Draft = true
	svc, _, _ := newService(t, proj(1), draft, task(3, 2, "a", ready))
	_, err := svc.ClaimTask(agent("a1"), &underdrawv1.ClaimTaskRequest{Id: id(3)})
	wantCode(t, err, connect.CodeFailedPrecondition)
	if !strings.Contains(err.Error(), "draft") {
		t.Fatalf("err = %v", err)
	}
}

func TestReleaseTask(t *testing.T) {
	svc, _, _ := newService(t, oneTask()...)
	claim(t, svc, agent("a1"), 3)
	resp, err := svc.ReleaseTask(agent("a1"), &underdrawv1.ReleaseTaskRequest{Id: id(3)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetTask().GetStatus() != ready || resp.GetTask().GetClaim() != nil {
		t.Fatalf("got %v", resp.GetTask())
	}
	_, err = svc.ReleaseTask(agent("a1"), &underdrawv1.ReleaseTaskRequest{Id: id(3)})
	wantCode(t, err, connect.CodeFailedPrecondition)

	claim(t, svc, agent("a1"), 3)
	_, err = svc.ReleaseTask(agent("a2"), &underdrawv1.ReleaseTaskRequest{Id: id(3)})
	wantCode(t, err, connect.CodeFailedPrecondition)

	resp, err = svc.ReleaseTask(user, &underdrawv1.ReleaseTaskRequest{Id: id(3)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetTask().GetStatus() != ready || resp.GetTask().GetClaim() != nil {
		t.Fatalf("got %v", resp.GetTask())
	}
}

func TestSetStatusClaimRules(t *testing.T) {
	svc, _, _ := newService(t, append(oneTask(), task(4, 2, "b", ready))...)
	rev := claim(t, svc, agent("a1"), 3).GetTask().GetItem().GetRevision()

	_, err := svc.SetStatus(agent("a2"), setStatus(3, rev, inReview))
	wantCode(t, err, connect.CodeFailedPrecondition)

	_, err = svc.SetStatus(agent("a2"), setStatus(4, 1, inProgress))
	wantCode(t, err, connect.CodeFailedPrecondition)
	if !strings.Contains(err.Error(), "claim the task with claim_task") {
		t.Fatalf("err = %v", err)
	}

	resp, err := svc.SetStatus(agent("a1"), setStatus(3, rev, inReview))
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetItem().GetClaim() != nil {
		t.Fatalf("claim = %v", resp.GetItem().GetClaim())
	}
}

func TestDeleteClaimedTask(t *testing.T) {
	svc, _, _ := newService(t, oneTask()...)
	rev := claim(t, svc, agent("a1"), 3).GetTask().GetItem().GetRevision()
	_, err := svc.DeleteItem(user, &underdrawv1.DeleteItemRequest{Id: id(3), BaseRevision: rev})
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func TestConcurrentNextTask(t *testing.T) {
	svc, _, _ := newService(t, oneTask()...)
	const n = 10
	got := make([]*underdrawv1.NextTaskResponse, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			got[i], errs[i] = svc.NextTask(agent(string(rune('a'+i))), &underdrawv1.NextTaskRequest{})
		})
	}
	wg.Wait()
	claimed := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if got[i].GetTask() != nil {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("%d agents got a task, want 1", claimed)
	}
}

func TestGetTaskContext(t *testing.T) {
	svc, _, _ := newService(t, oneTask()...)
	claim(t, svc, agent("a1"), 3)
	resp, err := svc.GetTaskContext(user, &underdrawv1.GetTaskContextRequest{Id: id(3)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.GetContext(), "# Task: ") || resp.GetRevision() != 2 {
		t.Fatalf("revision = %d, context:\n%s", resp.GetRevision(), resp.GetContext())
	}
	if !strings.Contains(resp.GetContext(), "Claimed by: agent `a1`") {
		t.Fatalf("context has no claim:\n%s", resp.GetContext())
	}
	if strings.Contains(resp.GetContext(), "**Resumed task:**") {
		t.Fatal("claimed task marked resumed")
	}

	_, err = svc.GetTaskContext(user, &underdrawv1.GetTaskContextRequest{Id: id(2)})
	wantCode(t, err, connect.CodeInvalidArgument)
}
