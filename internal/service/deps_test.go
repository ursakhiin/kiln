package service_test

import (
	"testing"

	"connectrpc.com/connect"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
)

const (
	untilUnspecified = kilnv1.BlockUntil_BLOCK_UNTIL_UNSPECIFIED
	untilInReview    = kilnv1.BlockUntil_BLOCK_UNTIL_IN_REVIEW
)

func addDep(item int, base int64, on int, until kilnv1.BlockUntil) *kilnv1.AddDependencyRequest {
	return &kilnv1.AddDependencyRequest{ItemId: id(item), BaseRevision: base, Dependency: dep(on, until)}
}

// seedTasks seeds project 1, feature 2 and tasks 3 and 4 in feature 2.
func seedTasks() []*kilnv1.Item {
	return []*kilnv1.Item{
		proj(1), feat(2, 1, "a"),
		task(3, 2, "a", kilnv1.Status_STATUS_READY),
		task(4, 2, "b", kilnv1.Status_STATUS_READY),
	}
}

func TestAddDependency(t *testing.T) {
	svc, _, repo := newService(t, seedTasks()...)
	resp, err := svc.AddDependency(user, addDep(4, 1, 3, untilUnspecified))
	if err != nil {
		t.Fatal(err)
	}
	got := resp.GetItem()
	deps := got.GetItem().GetTask().GetDependsOn()
	if len(deps) != 1 || deps[0].GetItemId() != id(3) || deps[0].GetBlockUntil() != kilnv1.BlockUntil_BLOCK_UNTIL_DONE {
		t.Fatalf("depends_on = %v", deps)
	}
	if !got.GetBlocked() || got.GetItem().GetRevision() != 2 {
		t.Fatalf("blocked=%v revision=%d", got.GetBlocked(), got.GetItem().GetRevision())
	}
	h := history(t, repo, id(4))
	last := h[len(h)-1].GetChange()
	if last.GetType() != kilnv1.ChangeType_CHANGE_TYPE_DEPENDENCY_ADDED || len(last.GetChangedFields()) != 1 || last.GetChangedFields()[0] != "task.depends_on" {
		t.Fatalf("history = %v", last)
	}
}

func TestAddDependencyFeatureFields(t *testing.T) {
	svc, _, repo := newService(t, proj(1), feat(2, 1, "a"), feat(3, 1, "b"))
	if _, err := svc.AddDependency(user, addDep(3, 1, 2, untilUnspecified)); err != nil {
		t.Fatal(err)
	}
	h := history(t, repo, id(3))
	if f := h[len(h)-1].GetChange().GetChangedFields(); len(f) != 1 || f[0] != "feature.depends_on" {
		t.Fatalf("fields = %v", f)
	}
}

func TestAddDependencyRules(t *testing.T) {
	svc, _, _ := newService(t, seedTasks()...)
	if _, err := svc.AddDependency(user, addDep(3, 1, 4, untilUnspecified)); err != nil {
		t.Fatal(err)
	}
	_, err := svc.AddDependency(user, addDep(4, 1, 3, untilUnspecified))
	wantCode(t, err, connect.CodeFailedPrecondition) // cycle

	_, err = svc.AddDependency(user, addDep(4, 1, 1, untilUnspecified))
	wantCode(t, err, connect.CodeInvalidArgument) // project blocker

	_, err = svc.AddDependency(user, addDep(1, 1, 3, untilUnspecified))
	wantCode(t, err, connect.CodeInvalidArgument) // project blocked item

	_, err = svc.AddDependency(user, addDep(3, 2, 4, untilUnspecified))
	wantCode(t, err, connect.CodeFailedPrecondition) // duplicate

	_, err = svc.AddDependency(user, addDep(4, 1, 99, untilUnspecified))
	wantCode(t, err, connect.CodeNotFound) // unknown blocker

	_, err = svc.AddDependency(user, addDep(99, 1, 3, untilUnspecified))
	wantCode(t, err, connect.CodeNotFound) // unknown item

	_, err = svc.AddDependency(user, addDep(3, 1, 2, untilUnspecified))
	wantCode(t, err, connect.CodeAborted) // stale base
}

func TestBlockUntilInReview(t *testing.T) {
	svc, _, _ := newService(t, seedTasks()...)
	resp, err := svc.AddDependency(user, addDep(4, 1, 3, untilInReview))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetItem().GetBlocked() {
		t.Fatal("T4 should be blocked")
	}
	if _, err := svc.SetStatus(user, &kilnv1.SetStatusRequest{Id: id(3), BaseRevision: 1, Status: kilnv1.Status_STATUS_IN_REVIEW}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetItem(user, &kilnv1.GetItemRequest{Id: id(4)})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetItem().GetBlocked() {
		t.Fatal("T4 should be unblocked")
	}
}

func TestRemoveDependency(t *testing.T) {
	svc, _, repo := newService(t, seedTasks()...)
	if _, err := svc.AddDependency(user, addDep(4, 1, 3, untilUnspecified)); err != nil {
		t.Fatal(err)
	}
	req := &kilnv1.RemoveDependencyRequest{ItemId: id(4), BaseRevision: 1, DependsOnId: id(3)}
	_, err := svc.RemoveDependency(user, req)
	wantCode(t, err, connect.CodeAborted)

	req.BaseRevision = 2
	resp, err := svc.RemoveDependency(user, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetItem().GetBlocked() || resp.GetItem().GetItem().GetRevision() != 3 {
		t.Fatalf("blocked=%v revision=%d", resp.GetItem().GetBlocked(), resp.GetItem().GetItem().GetRevision())
	}
	h := history(t, repo, id(4))
	last := h[len(h)-1].GetChange()
	if last.GetType() != kilnv1.ChangeType_CHANGE_TYPE_DEPENDENCY_REMOVED || last.GetChangedFields()[0] != "task.depends_on" {
		t.Fatalf("history = %v", last)
	}

	req.BaseRevision = 3
	_, err = svc.RemoveDependency(user, req)
	wantCode(t, err, connect.CodeNotFound)
}
