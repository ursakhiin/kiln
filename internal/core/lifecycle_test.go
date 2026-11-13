package core_test

import (
	"errors"
	"testing"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
)

const (
	user     = kilnv1.ActorType_ACTOR_TYPE_USER
	agent    = kilnv1.ActorType_ACTOR_TYPE_AGENT
	external = kilnv1.ActorType_ACTOR_TYPE_EXTERNAL

	UNSPECIFIED = kilnv1.Status_STATUS_UNSPECIFIED
	DRAFT       = kilnv1.Status_STATUS_DRAFT
	READY       = kilnv1.Status_STATUS_READY
	IN_PROGRESS = kilnv1.Status_STATUS_IN_PROGRESS
	IN_REVIEW   = kilnv1.Status_STATUS_IN_REVIEW
	DONE        = kilnv1.Status_STATUS_DONE
)

func task(status kilnv1.Status) *kilnv1.Item {
	return &kilnv1.Item{Kind: &kilnv1.Item_Task{Task: &kilnv1.Task{Status: status}}}
}

func feature() *kilnv1.Item {
	return &kilnv1.Item{Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{}}}
}

func project() *kilnv1.Item {
	return &kilnv1.Item{Kind: &kilnv1.Item_Project{Project: &kilnv1.Project{}}}
}

func TestCheckStatusChange(t *testing.T) {
	for i, c := range []struct {
		actor kilnv1.ActorType
		item  *kilnv1.Item
		to    kilnv1.Status
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
	} {
		err := core.CheckStatusChange(c.actor, c.item, c.to)
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("row %d (%v, %v): got %v, want %v", i, c.actor, c.to, err, c.want)
		}
	}
}

func TestDeriveFeatureStatus(t *testing.T) {
	for i, c := range []struct {
		f     *kilnv1.Feature
		tasks []kilnv1.Status
		want  kilnv1.Status
	}{
		{&kilnv1.Feature{StatusOverride: DONE}, []kilnv1.Status{READY}, DONE},
		{&kilnv1.Feature{Draft: true}, []kilnv1.Status{IN_PROGRESS}, DRAFT},
		{&kilnv1.Feature{}, nil, READY},
		{&kilnv1.Feature{}, []kilnv1.Status{READY, DRAFT}, READY},
		{&kilnv1.Feature{}, []kilnv1.Status{READY, IN_PROGRESS}, IN_PROGRESS},
		{&kilnv1.Feature{}, []kilnv1.Status{READY, DONE}, IN_PROGRESS},
		{&kilnv1.Feature{}, []kilnv1.Status{IN_REVIEW, DONE}, IN_REVIEW},
		{&kilnv1.Feature{}, []kilnv1.Status{DONE, DONE}, DONE},
	} {
		if got := core.DeriveFeatureStatus(c.f, c.tasks); got != c.want {
			t.Errorf("row %d: got %v, want %v", i, got, c.want)
		}
	}
}

func TestIsBlocked(t *testing.T) {
	statuses := map[string]kilnv1.Status{"A": IN_REVIEW, "B": DONE}
	statusOf := func(id string) (kilnv1.Status, bool) {
		s, ok := statuses[id]
		return s, ok
	}
	dep := func(id string, until kilnv1.BlockUntil) *kilnv1.Dependency {
		return &kilnv1.Dependency{ItemId: id, BlockUntil: until}
	}
	for i, c := range []struct {
		deps []*kilnv1.Dependency
		want bool
	}{
		{nil, false},
		{[]*kilnv1.Dependency{dep("A", kilnv1.BlockUntil_BLOCK_UNTIL_IN_REVIEW)}, false},
		{[]*kilnv1.Dependency{dep("A", kilnv1.BlockUntil_BLOCK_UNTIL_DONE)}, true},
		{[]*kilnv1.Dependency{dep("A", kilnv1.BlockUntil_BLOCK_UNTIL_UNSPECIFIED)}, true},
		{[]*kilnv1.Dependency{dep("B", kilnv1.BlockUntil_BLOCK_UNTIL_DONE)}, false},
		{[]*kilnv1.Dependency{dep("MISSING", kilnv1.BlockUntil_BLOCK_UNTIL_IN_REVIEW)}, true},
	} {
		if got := core.IsBlocked(c.deps, statusOf); got != c.want {
			t.Errorf("row %d: got %v, want %v", i, got, c.want)
		}
	}
}
