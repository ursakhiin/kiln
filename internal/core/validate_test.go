package core_test

import (
	"errors"
	"testing"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/core"
)

func newFeature(f *underdrawv1.Feature) *underdrawv1.Item {
	return &underdrawv1.Item{Title: "Offline sync", Kind: &underdrawv1.Item_Feature{Feature: f}}
}

func newTask(tk *underdrawv1.Task) *underdrawv1.Item {
	return &underdrawv1.Item{Title: "Sync engine", Kind: &underdrawv1.Item_Task{Task: tk}}
}

func TestNormalizeNewItem(t *testing.T) {
	agentFeature := newFeature(&underdrawv1.Feature{ProjectId: "P"})
	if err := core.NormalizeNewItem(agent, agentFeature); err != nil || !agentFeature.GetFeature().GetDraft() {
		t.Errorf("agent feature: err=%v draft=%v, want nil, true", err, agentFeature.GetFeature().GetDraft())
	}
	userFeature := newFeature(&underdrawv1.Feature{ProjectId: "P"})
	if err := core.NormalizeNewItem(user, userFeature); err != nil || userFeature.GetFeature().GetDraft() {
		t.Errorf("user feature: err=%v draft=%v, want nil, false", err, userFeature.GetFeature().GetDraft())
	}
	newT := newTask(&underdrawv1.Task{FeatureId: "F"})
	if err := core.NormalizeNewItem(agent, newT); err != nil || newT.GetTask().GetStatus() != READY {
		t.Errorf("task: err=%v status=%v, want nil, READY", err, newT.GetTask().GetStatus())
	}

	withID := newTask(&underdrawv1.Task{FeatureId: "F"})
	withID.Id = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	blankTitle := newTask(&underdrawv1.Task{FeatureId: "F"})
	blankTitle.Title = "  "
	for name, item := range map[string]*underdrawv1.Item{
		"id set":                  withID,
		"blank title":             blankTitle,
		"no kind":                 {Title: "x"},
		"feature without project": newFeature(&underdrawv1.Feature{}),
		"task in progress":        newTask(&underdrawv1.Task{FeatureId: "F", Status: IN_PROGRESS}),
		"task with depends_on": newTask(&underdrawv1.Task{FeatureId: "F",
			DependsOn: []*underdrawv1.Dependency{{ItemId: "X"}}}),
	} {
		if err := core.NormalizeNewItem(user, item); !errors.Is(err, core.ErrInvalidArgument) {
			t.Errorf("%s: got %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestCheckUpdate(t *testing.T) {
	draftFeature := newFeature(&underdrawv1.Feature{ProjectId: "P", Draft: true})
	readyFeature := newFeature(&underdrawv1.Feature{ProjectId: "P"})
	tk := newTask(&underdrawv1.Task{FeatureId: "F"})
	renamed := newTask(&underdrawv1.Task{FeatureId: "F"})
	renamed.Title = "Sync engine v2"
	untitled := newTask(&underdrawv1.Task{FeatureId: "F"})
	untitled.Title = ""

	for name, c := range map[string]struct {
		actor            underdrawv1.ActorType
		current, updated *underdrawv1.Item
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
		"empty project_id":         {user, readyFeature, newFeature(&underdrawv1.Feature{}), []string{"feature.project_id"}, core.ErrInvalidArgument},
		"empty feature_id":         {user, tk, newTask(&underdrawv1.Task{}), []string{"task.feature_id"}, core.ErrInvalidArgument},
		"agent demotes to a draft": {agent, readyFeature, draftFeature, []string{"feature.draft"}, nil},
	} {
		err := core.CheckUpdate(c.actor, c.current, c.updated, c.paths)
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
}
