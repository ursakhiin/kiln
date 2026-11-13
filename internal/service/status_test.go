package service_test

import (
	"testing"

	"connectrpc.com/connect"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
)

func setStatus(n int, base int64, st underdrawv1.Status) *underdrawv1.SetStatusRequest {
	return &underdrawv1.SetStatusRequest{Id: id(n), BaseRevision: base, Status: st}
}

func TestSetStatusPermissions(t *testing.T) {
	svc, _, repo := newService(t, seedTasks()...)
	_, err := svc.SetStatus(agent("a"), setStatus(3, 1, underdrawv1.Status_STATUS_DONE))
	wantCode(t, err, connect.CodePermissionDenied)

	resp, err := svc.SetStatus(user, setStatus(3, 1, underdrawv1.Status_STATUS_DONE))
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetItem().GetItem().GetRevision() != 2 || resp.GetItem().GetStatus() != underdrawv1.Status_STATUS_DONE {
		t.Fatalf("got %v", resp.GetItem())
	}
	h := history(t, repo, id(3))
	last := h[len(h)-1].GetChange()
	if last.GetType() != underdrawv1.ChangeType_CHANGE_TYPE_STATUS_CHANGED || len(last.GetChangedFields()) != 1 || last.GetChangedFields()[0] != "task.status" {
		t.Fatalf("history = %v", last)
	}

	_, err = svc.SetStatus(agent("a"), setStatus(2, 1, underdrawv1.Status_STATUS_IN_REVIEW))
	wantCode(t, err, connect.CodePermissionDenied)

	resp, err = svc.SetStatus(user, setStatus(2, 1, underdrawv1.Status_STATUS_IN_REVIEW))
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetItem().GetItem().GetFeature().GetStatusOverride() != underdrawv1.Status_STATUS_IN_REVIEW ||
		resp.GetItem().GetStatus() != underdrawv1.Status_STATUS_IN_REVIEW {
		t.Fatalf("got %v", resp.GetItem())
	}
	h = history(t, repo, id(2))
	if f := h[len(h)-1].GetChange().GetChangedFields(); len(f) != 1 || f[0] != "feature.status_override" {
		t.Fatalf("fields = %v", f)
	}

	resp, err = svc.SetStatus(user, setStatus(2, 2, underdrawv1.Status_STATUS_UNSPECIFIED))
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetItem().GetItem().GetFeature().GetStatusOverride() != underdrawv1.Status_STATUS_UNSPECIFIED ||
		resp.GetItem().GetStatus() == underdrawv1.Status_STATUS_IN_REVIEW {
		t.Fatalf("override not cleared: %v", resp.GetItem())
	}
}

func TestSetStatusStaleBase(t *testing.T) {
	svc, _, _ := newService(t, seedTasks()...)
	_, err := svc.SetStatus(user, setStatus(3, 5, underdrawv1.Status_STATUS_DONE))
	wantCode(t, err, connect.CodeAborted)
}

func TestSetStatusNoop(t *testing.T) {
	svc, _, repo := newService(t, seedTasks()...)
	resp, err := svc.SetStatus(user, setStatus(3, 1, underdrawv1.Status_STATUS_READY))
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetItem().GetItem().GetRevision() != 1 {
		t.Fatalf("revision = %d", resp.GetItem().GetItem().GetRevision())
	}
	if h := history(t, repo, id(3)); len(h) != 0 {
		t.Fatalf("history = %v", h)
	}
	// A feature with no override also treats UNSPECIFIED as a no-op.
	resp, err = svc.SetStatus(user, setStatus(2, 1, underdrawv1.Status_STATUS_UNSPECIFIED))
	if err != nil || resp.GetItem().GetItem().GetRevision() != 1 {
		t.Fatalf("feature noop: %v %v", err, resp)
	}
}
