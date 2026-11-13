package core_test

import (
	"errors"
	"testing"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/core"
)

const (
	user     = underdrawv1.ActorType_ACTOR_TYPE_USER
	agent    = underdrawv1.ActorType_ACTOR_TYPE_AGENT
	external = underdrawv1.ActorType_ACTOR_TYPE_EXTERNAL

	UNSPECIFIED = underdrawv1.Status_STATUS_UNSPECIFIED
	DRAFT       = underdrawv1.Status_STATUS_DRAFT
	READY       = underdrawv1.Status_STATUS_READY
	IN_PROGRESS = underdrawv1.Status_STATUS_IN_PROGRESS
	IN_REVIEW   = underdrawv1.Status_STATUS_IN_REVIEW
	DONE        = underdrawv1.Status_STATUS_DONE
)

func task(status underdrawv1.Status) *underdrawv1.Item {
	return &underdrawv1.Item{Kind: &underdrawv1.Item_Task{Task: &underdrawv1.Task{Status: status}}}
}

func feature() *underdrawv1.Item {
	return &underdrawv1.Item{Kind: &underdrawv1.Item_Feature{Feature: &underdrawv1.Feature{}}}
}

func project() *underdrawv1.Item {
	return &underdrawv1.Item{Kind: &underdrawv1.Item_Project{Project: &underdrawv1.Project{}}}
}

func TestCheckStatusChange(t *testing.T) {
	for i, c := range []struct {
		actor underdrawv1.ActorType
		item  *underdrawv1.Item
		to    underdrawv1.Status
		want  error
	}{
		{user, task(READY), DONE, nil},
		{user, task(DONE), READY, nil},
		{agent, task(READY), IN_PROGRESS, nil},
		{agent, task(IN_PROGRESS), IN_REVIEW, nil},
		{agent, task(IN_REVIEW), DONE, core.ErrPermissionDenied},
		{agent, task(DONE), IN_PROGRESS, core.ErrPermissionDenied},
		{agent, task(READY), DRAFT, core.ErrPermissionDenied},
		{user, task(READY), UNSPECIFIED, core.ErrInvalidArgument},
		{user, feature(), DONE, nil},
		{user, feature(), UNSPECIFIED, nil},
		{agent, feature(), IN_REVIEW, core.ErrPermissionDenied},
		{user, project(), READY, core.ErrFailedPrecondition},
		{external, task(READY), IN_PROGRESS, core.ErrPermissionDenied},
		{agent, task(DRAFT), READY, core.ErrPermissionDenied},
		{agent, task(DRAFT), IN_PROGRESS, core.ErrPermissionDenied},
		{user, task(DRAFT), READY, nil},
	} {
		err := core.CheckStatusChange(c.actor, c.item, c.to)
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("row %d (%v, %v): got %v, want %v", i, c.actor, c.to, err, c.want)
		}
	}
}

func TestDeriveFeatureStatus(t *testing.T) {
	for i, c := range []struct {
		f     *underdrawv1.Feature
		tasks []underdrawv1.Status
		want  underdrawv1.Status
	}{
		{&underdrawv1.Feature{StatusOverride: DONE}, []underdrawv1.Status{READY}, DONE},
		{&underdrawv1.Feature{Draft: true}, []underdrawv1.Status{IN_PROGRESS}, DRAFT},
		{&underdrawv1.Feature{}, nil, READY},
		{&underdrawv1.Feature{}, []underdrawv1.Status{READY, DRAFT}, READY},
		{&underdrawv1.Feature{}, []underdrawv1.Status{READY, IN_PROGRESS}, IN_PROGRESS},
		{&underdrawv1.Feature{}, []underdrawv1.Status{READY, DONE}, IN_PROGRESS},
		{&underdrawv1.Feature{}, []underdrawv1.Status{IN_REVIEW, DONE}, IN_REVIEW},
		{&underdrawv1.Feature{}, []underdrawv1.Status{DONE, DONE}, DONE},
	} {
		if got := core.DeriveFeatureStatus(c.f, c.tasks); got != c.want {
			t.Errorf("row %d: got %v, want %v", i, got, c.want)
		}
	}
}

func TestIsBlocked(t *testing.T) {
	statuses := map[string]underdrawv1.Status{"A": IN_REVIEW, "B": DONE}
	statusOf := func(id string) (underdrawv1.Status, bool) {
		s, ok := statuses[id]
		return s, ok
	}
	dep := func(id string, until underdrawv1.BlockUntil) *underdrawv1.Dependency {
		return &underdrawv1.Dependency{ItemId: id, BlockUntil: until}
	}
	for i, c := range []struct {
		deps []*underdrawv1.Dependency
		want bool
	}{
		{nil, false},
		{[]*underdrawv1.Dependency{dep("A", underdrawv1.BlockUntil_BLOCK_UNTIL_IN_REVIEW)}, false},
		{[]*underdrawv1.Dependency{dep("A", underdrawv1.BlockUntil_BLOCK_UNTIL_DONE)}, true},
		{[]*underdrawv1.Dependency{dep("A", underdrawv1.BlockUntil_BLOCK_UNTIL_UNSPECIFIED)}, true},
		{[]*underdrawv1.Dependency{dep("B", underdrawv1.BlockUntil_BLOCK_UNTIL_DONE)}, false},
		{[]*underdrawv1.Dependency{dep("MISSING", underdrawv1.BlockUntil_BLOCK_UNTIL_IN_REVIEW)}, true},
	} {
		if got := core.IsBlocked(c.deps, statusOf); got != c.want {
			t.Errorf("row %d: got %v, want %v", i, got, c.want)
		}
	}
}
