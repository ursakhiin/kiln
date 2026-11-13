package core_test

import (
	"testing"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
)

func TestWouldCreateCycle(t *testing.T) {
	// F1 is a feature; T1 is a task in F1; T2 depends on T1; F2 depends on T2.
	items := map[string]*kilnv1.Item{
		"F1": {Id: "F1", Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{}}},
		"T1": {Id: "T1", Kind: &kilnv1.Item_Task{Task: &kilnv1.Task{FeatureId: "F1"}}},
		"T2": {Id: "T2", Kind: &kilnv1.Item_Task{Task: &kilnv1.Task{FeatureId: "F1",
			DependsOn: []*kilnv1.Dependency{{ItemId: "T1"}}}}},
		"F2": {Id: "F2", Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{
			DependsOn: []*kilnv1.Dependency{{ItemId: "T2"}}}}},
		// F3 holds T3; F4 depends on F3.
		"F3": {Id: "F3", Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{}}},
		"T3": {Id: "T3", Kind: &kilnv1.Item_Task{Task: &kilnv1.Task{FeatureId: "F3"}}},
		"F4": {Id: "F4", Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{
			DependsOn: []*kilnv1.Dependency{{ItemId: "F3"}}}}},
	}
	g := mapGraph(items)
	for _, c := range []struct {
		from, to string
		want     bool
	}{
		{"T1", "T1", true},  // self
		{"T1", "T2", true},  // direct back-edge
		{"F1", "T1", true},  // feature depends on its own task (via parent edge)
		{"T1", "F2", true},  // transitive: F2 → T2 → T1
		{"F2", "T1", false}, // already implied, no cycle
		{"T1", "MISSING", false},
		{"T1", "F1", true},  // task depends on its own feature: F1 can't finish without T1
		{"T2", "F2", true},  // T2 → F2 → T2 (F2 already depends on T2)
		{"T3", "F4", true},  // T3 → F4 → F3, and F3 can't finish without T3
		{"T3", "T1", false}, // ordinary cross-feature task dependency
		{"F4", "F1", false}, // ordinary feature dependency
	} {
		if got := core.WouldCreateCycle(c.from, c.to, g); got != c.want {
			t.Errorf("WouldCreateCycle(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

// A cycle that already exists (e.g. from a hand edit) must not make the
// search loop forever.
func TestWouldCreateCycleTerminatesOnExistingCycle(t *testing.T) {
	dependsOn := func(id string) *kilnv1.Item {
		return &kilnv1.Item{Id: id, Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{
			DependsOn: []*kilnv1.Dependency{{ItemId: map[string]string{"A": "B", "B": "A"}[id]}}}}}
	}
	g := mapGraph{"A": dependsOn("A"), "B": dependsOn("B")}
	if core.WouldCreateCycle("C", "A", g) {
		t.Error("C → A reported as a cycle")
	}
}

func TestHasCycle(t *testing.T) {
	// F1 has T1; F2 has no tasks and depends on T1.
	g := mapGraph{
		"F1": {Id: "F1", Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{}}},
		"T1": {Id: "T1", Kind: &kilnv1.Item_Task{Task: &kilnv1.Task{FeatureId: "F1"}}},
		"F2": {Id: "F2", Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{
			DependsOn: []*kilnv1.Dependency{{ItemId: "T1"}}}}},
	}
	if core.HasCycle([]string{"F2"}, g) {
		t.Error("HasCycle reported a cycle in an acyclic graph")
	}
	// Moving T1 into F2 makes F2 wait on T1, which waits on F2.
	g["T1"].GetTask().FeatureId = "F2"
	if !core.HasCycle([]string{"F2", "T1"}, g) {
		t.Error("HasCycle missed the cycle after reparenting T1")
	}
}

// mapGraph is a core.Graph over an in-memory set of items.
type mapGraph map[string]*kilnv1.Item

func (g mapGraph) Item(id string) *kilnv1.Item { return g[id] }

func (g mapGraph) Tasks(featureID string) []string {
	var ids []string
	for id, it := range g {
		if it.GetTask() != nil && it.GetTask().GetFeatureId() == featureID {
			ids = append(ids, id)
		}
	}
	return ids
}
