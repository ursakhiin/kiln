package service_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/service"
	"github.com/ursakhiin/kiln/internal/store"
)

const t1 = 3 // the task in oneTask

// updateWS records ws on task n for ctx, with the given mask paths.
func updateWS(svc *service.Service, ctx context.Context, n int, ws *kilnv1.WorkState, paths ...string) (*kilnv1.UpdateWorkStateResponse, error) {
	req := &kilnv1.UpdateWorkStateRequest{Id: id(n), WorkState: ws}
	if len(paths) > 0 {
		req.UpdateMask = mask(paths...)
	}
	return svc.UpdateWorkState(ctx, req)
}

// recordBranch has ctx record branch kiln/t1 and some progress on task n.
func recordBranch(t *testing.T, svc *service.Service, ctx context.Context, n int) {
	t.Helper()
	_, err := updateWS(svc, ctx, n, &kilnv1.WorkState{
		Branch:   "kiln/t1",
		Progress: "half done",
		Session:  &kilnv1.AgentSession{Host: "evil", HostVersion: "6.6.6"},
	}, "branch", "progress", "session")
	if err != nil {
		t.Fatal(err)
	}
}

// taskHistory reads item n's history straight from the repo, oldest first.
func taskHistory(t *testing.T, repo string, n int) []*kilnv1.HistoryEntry {
	t.Helper()
	es := history(t, repo, id(n))
	slices.SortFunc(es, func(a, b *kilnv1.HistoryEntry) int { return strings.Compare(a.GetId(), b.GetId()) })
	return es
}

// workStateEntries returns item n's WORK_STATE_UPDATED entries.
func workStateEntries(t *testing.T, repo string, n int) []*kilnv1.HistoryEntry {
	t.Helper()
	var out []*kilnv1.HistoryEntry
	for _, e := range taskHistory(t, repo, n) {
		if e.GetChange().GetType() == kilnv1.ChangeType_CHANGE_TYPE_WORK_STATE_UPDATED {
			out = append(out, e)
		}
	}
	return out
}

// restart opens a new service over repo, as after a daemon restart.
func restart(t *testing.T, repo string, clk *clock) *service.Service {
	t.Helper()
	st, err := store.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	svc, _, err := service.New(st, service.Options{Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestUpdateWorkState(t *testing.T) {
	svc, clk, repo := newService(t, oneTask()...)
	claim(t, svc, agent("a1"), t1)
	before := len(taskHistory(t, repo, t1))
	recordBranch(t, svc, agent("a1"), t1)

	info := getInfo(t, svc, t1)
	if info.GetItem().GetRevision() != 2 {
		t.Fatalf("revision = %d, want 2", info.GetItem().GetRevision())
	}
	if got := len(taskHistory(t, repo, t1)); got != before {
		t.Fatalf("history has %d entries, want %d", got, before)
	}
	saved, err := openStore(t, repo).GetItem(id(t1))
	if err != nil {
		t.Fatal(err)
	}
	ws := saved.GetTask().GetWorkState()
	if ws.GetBranch() != "kiln/t1" || saved.GetRevision() != 2 {
		t.Fatalf("saved = %v", saved)
	}
	if ws.GetSession().GetHost() != "test-host" || ws.GetSession().GetHostVersion() != "1.0" {
		t.Fatalf("session = %v", ws.GetSession())
	}
	if !ws.GetUpdateTime().AsTime().Equal(clk.Now()) {
		t.Fatalf("work state update_time = %v", ws.GetUpdateTime().AsTime())
	}
	if !saved.GetUpdateTime().AsTime().Equal(info.GetItem().GetUpdateTime().AsTime()) {
		t.Fatal("item update_time changed")
	}

	_, err = updateWS(svc, agent("a1"), t1, &kilnv1.WorkState{}, "nope")
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = updateWS(svc, agent("a1"), 2, &kilnv1.WorkState{})
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = updateWS(svc, agent("a1"), 99, &kilnv1.WorkState{})
	wantCode(t, err, connect.CodeNotFound)
}

func TestUpdateWorkStateMask(t *testing.T) {
	svc, _, _ := newService(t, oneTask()...)
	claim(t, svc, agent("a1"), t1)
	recordBranch(t, svc, agent("a1"), t1)
	resp, err := updateWS(svc, agent("a1"), t1, &kilnv1.WorkState{Progress: "done"}, "progress")
	if err != nil {
		t.Fatal(err)
	}
	ws := resp.GetTask().GetItem().GetTask().GetWorkState()
	if ws.GetBranch() != "kiln/t1" || ws.GetProgress() != "done" {
		t.Fatalf("work state = %v", ws)
	}

	// An empty mask replaces every field.
	resp, err = updateWS(svc, agent("a1"), t1, &kilnv1.WorkState{Progress: "all"})
	if err != nil {
		t.Fatal(err)
	}
	ws = resp.GetTask().GetItem().GetTask().GetWorkState()
	if ws.GetBranch() != "" || ws.GetProgress() != "all" || ws.GetSession().GetHost() != "test-host" {
		t.Fatalf("work state = %v", ws)
	}
}

func TestUpdateWorkStatePermission(t *testing.T) {
	svc, _, _ := newService(t, oneTask()...)
	claim(t, svc, agent("a1"), t1)
	_, err := updateWS(svc, agent("a2"), t1, &kilnv1.WorkState{Branch: "x"}, "branch")
	wantCode(t, err, connect.CodeFailedPrecondition)
	resp, err := updateWS(svc, user, t1, &kilnv1.WorkState{Branch: "x"}, "branch")
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.GetTask().GetItem().GetTask().GetWorkState().GetBranch(); got != "x" {
		t.Fatalf("branch = %q", got)
	}
}

func TestUpdateWorkStateRejectsSecret(t *testing.T) {
	svc, _, _ := newService(t, oneTask()...)
	claim(t, svc, agent("a1"), t1)
	_, err := updateWS(svc, agent("a1"), t1,
		&kilnv1.WorkState{Progress: "token ghp_" + strings.Repeat("a", 36)}, "progress")
	wantCode(t, err, connect.CodeInvalidArgument)
	if strings.Contains(err.Error(), "ghp_") {
		t.Fatalf("error leaks the secret: %v", err)
	}
	if ws := getInfo(t, svc, t1).GetItem().GetTask().GetWorkState(); ws != nil {
		t.Fatalf("work state saved: %v", ws)
	}
}

func TestWorkStateRecordedInNextEntry(t *testing.T) {
	svc, _, repo := newService(t, oneTask()...)
	rev := claim(t, svc, agent("a1"), t1).GetTask().GetItem().GetRevision()
	_, err := updateWS(svc, agent("a1"), t1, &kilnv1.WorkState{Branch: "kiln/t1"}, "branch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetStatus(agent("a1"), setStatus(t1, rev, inReview)); err != nil {
		t.Fatal(err)
	}
	es := taskHistory(t, repo, t1)
	last := es[len(es)-1].GetChange()
	if want := []string{"task.status", "task.work_state.branch"}; !slices.Equal(last.GetChangedFields(), want) {
		t.Fatalf("changed_fields = %v, want %v", last.GetChangedFields(), want)
	}
	if got := last.GetSnapshot().GetTask().GetWorkState().GetBranch(); got != "kiln/t1" {
		t.Fatalf("snapshot branch = %q", got)
	}
	if n := len(workStateEntries(t, repo, t1)); n != 0 {
		t.Fatalf("%d WORK_STATE_UPDATED entries, want 0", n)
	}
}

func TestWorkStateRecordedOnRelease(t *testing.T) {
	svc, _, repo := newService(t, oneTask()...)
	claim(t, svc, agent("a1"), t1)
	recordBranch(t, svc, agent("a1"), t1)
	if _, err := svc.ReleaseTask(agent("a1"), &kilnv1.ReleaseTaskRequest{Id: id(t1)}); err != nil {
		t.Fatal(err)
	}
	es := taskHistory(t, repo, t1)
	last := es[len(es)-1].GetChange()
	if last.GetType() != kilnv1.ChangeType_CHANGE_TYPE_STATUS_CHANGED ||
		!slices.Contains(last.GetChangedFields(), "task.work_state.branch") {
		t.Fatalf("release entry = %v", last)
	}
	if n := len(workStateEntries(t, repo, t1)); n != 0 {
		t.Fatalf("%d WORK_STATE_UPDATED entries, want 0", n)
	}

}

func TestWorkStateRecordedOnExpiry(t *testing.T) {
	svc, clk, repo := newService(t, oneTask()...)
	claim(t, svc, agent("a1"), t1)
	recordBranch(t, svc, agent("a1"), t1)
	clk.Advance(31 * time.Minute)
	if _, err := svc.GetItem(user, &kilnv1.GetItemRequest{Id: id(1)}); err != nil {
		t.Fatal(err)
	}
	es := workStateEntries(t, repo, t1)
	if len(es) != 1 || es[0].GetActor().GetAgentId() != "a1" ||
		es[0].GetActor().GetType() != kilnv1.ActorType_ACTOR_TYPE_AGENT {
		t.Fatalf("WORK_STATE_UPDATED entries = %v", es)
	}
	ch := es[0].GetChange()
	if want := []string{"task.work_state.branch", "task.work_state.progress", "task.work_state.session"}; !slices.Equal(ch.GetChangedFields(), want) {
		t.Fatalf("changed_fields = %v, want %v", ch.GetChangedFields(), want)
	}
	if ch.GetRevision() != 2 || ch.GetSnapshot().GetTask().GetWorkState().GetBranch() != "kiln/t1" {
		t.Fatalf("change = %v", ch)
	}
	if es[0].GetActor().GetSession().GetHost() != "test-host" {
		t.Fatalf("actor = %v", es[0].GetActor())
	}
}

func TestRestartRecordsPendingOnce(t *testing.T) {
	svc, clk, repo := newService(t, oneTask()...)
	claim(t, svc, agent("a1"), t1)
	recordBranch(t, svc, agent("a1"), t1)

	// The first service stops without its claim ending.
	restart(t, repo, clk)
	es := workStateEntries(t, repo, t1)
	if len(es) != 1 {
		t.Fatalf("%d WORK_STATE_UPDATED entries, want 1", len(es))
	}
	if !slices.Contains(es[0].GetChange().GetChangedFields(), "task.work_state.branch") {
		t.Fatalf("changed_fields = %v", es[0].GetChange().GetChangedFields())
	}
	if a := es[0].GetActor(); a.GetType() != kilnv1.ActorType_ACTOR_TYPE_AGENT || a.GetAgentId() != "" ||
		a.GetSession().GetHost() != "test-host" {
		t.Fatalf("actor = %v", a)
	}
	restart(t, repo, clk)
	if n := len(workStateEntries(t, repo, t1)); n != 1 {
		t.Fatalf("%d WORK_STATE_UPDATED entries after second restart, want 1", n)
	}
}

func TestResumedContextHasWorkState(t *testing.T) {
	svc, clk, _ := newService(t, oneTask()...)
	claim(t, svc, agent("a1"), t1)
	recordBranch(t, svc, agent("a1"), t1)
	clk.Advance(31 * time.Minute)
	resp := next(t, svc, agent("a2"))
	if !strings.Contains(resp.GetContext(), "- Branch: `kiln/t1`") {
		t.Fatalf("context has no branch:\n%s", resp.GetContext())
	}
}
