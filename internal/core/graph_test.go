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
	}
	edges := func(id string) []string {
		if it, ok := items[id]; ok {
			return core.Edges(it)
		}
		return nil
	}
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
	} {
		if got := core.WouldCreateCycle(c.from, c.to, edges); got != c.want {
			t.Errorf("WouldCreateCycle(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

// A cycle that already exists (e.g. from a hand edit) must not make the
// search loop forever.
func TestWouldCreateCycleTerminatesOnExistingCycle(t *testing.T) {
	edges := func(id string) []string {
		return map[string][]string{"A": {"B"}, "B": {"A"}}[id]
	}
	if core.WouldCreateCycle("C", "A", edges) {
		t.Error("C → A reported as a cycle")
	}
}
