package core_test

import (
	"errors"
	"testing"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
)

func newFeature(f *kilnv1.Feature) *kilnv1.Item {
	return &kilnv1.Item{Title: "Offline sync", Kind: &kilnv1.Item_Feature{Feature: f}}
}

func newTask(tk *kilnv1.Task) *kilnv1.Item {
	return &kilnv1.Item{Title: "Sync engine", Kind: &kilnv1.Item_Task{Task: tk}}
}

func TestNormalizeNewItem(t *testing.T) {
	agentFeature := newFeature(&kilnv1.Feature{ProjectId: "P"})
	if err := core.NormalizeNewItem(agent, agentFeature); err != nil || !agentFeature.GetFeature().GetDraft() {
		t.Errorf("agent feature: err=%v draft=%v, want nil, true", err, agentFeature.GetFeature().GetDraft())
	}
	userFeature := newFeature(&kilnv1.Feature{ProjectId: "P"})
	if err := core.NormalizeNewItem(user, userFeature); err != nil || userFeature.GetFeature().GetDraft() {
		t.Errorf("user feature: err=%v draft=%v, want nil, false", err, userFeature.GetFeature().GetDraft())
	}
	newT := newTask(&kilnv1.Task{FeatureId: "F"})
	if err := core.NormalizeNewItem(agent, newT); err != nil || newT.GetTask().GetStatus() != READY {
		t.Errorf("task: err=%v status=%v, want nil, READY", err, newT.GetTask().GetStatus())
	}

	withID := newTask(&kilnv1.Task{FeatureId: "F"})
	withID.Id = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	blankTitle := newTask(&kilnv1.Task{FeatureId: "F"})
	blankTitle.Title = "  "
	for name, item := range map[string]*kilnv1.Item{
		"id set":                  withID,
		"blank title":             blankTitle,
		"no kind":                 {Title: "x"},
		"feature without project": newFeature(&kilnv1.Feature{}),
		"task in progress":        newTask(&kilnv1.Task{FeatureId: "F", Status: IN_PROGRESS}),
		"task with depends_on": newTask(&kilnv1.Task{FeatureId: "F",
			DependsOn: []*kilnv1.Dependency{{ItemId: "X"}}}),
	} {
		if err := core.NormalizeNewItem(user, item); !errors.Is(err, core.ErrInvalidArgument) {
			t.Errorf("%s: got %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestCheckUpdate(t *testing.T) {
	draftFeature := newFeature(&kilnv1.Feature{ProjectId: "P", Draft: true})
	readyFeature := newFeature(&kilnv1.Feature{ProjectId: "P"})
	tk := newTask(&kilnv1.Task{FeatureId: "F"})
	renamed := newTask(&kilnv1.Task{FeatureId: "F"})
	renamed.Title = "Sync engine v2"
	untitled := newTask(&kilnv1.Task{FeatureId: "F"})
	untitled.Title = ""

	for name, c := range map[string]struct {
		actor            kilnv1.ActorType
		current, updated *kilnv1.Item
		paths            []string
		want             error
	}{
		"rename":                   {agent, tk, renamed, []string{"title"}, nil},
		"status via update":        {user, tk, tk, []string{"task.status"}, core.ErrInvalidArgument},
		"feature path on task":     {user, tk, tk, []string{"feature.draft"}, core.ErrInvalidArgument},
		"no paths":                 {user, tk, renamed, []string{}, core.ErrInvalidArgument},
		"empty title":              {user, tk, untitled, []string{"title"}, core.ErrInvalidArgument},
		"agent promotes draft":     {agent, draftFeature, readyFeature, []string{"feature.draft"}, core.ErrPermissionDenied},
		"user promotes draft":      {user, draftFeature, readyFeature, []string{"feature.draft"}, nil},
		"agent demotes to a draft": {agent, readyFeature, draftFeature, []string{"feature.draft"}, nil},
	} {
		err := core.CheckUpdate(c.actor, c.current, c.updated, c.paths)
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
}
