package core

import kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"

// Edges returns the IDs an item waits on: its dependencies, plus its parent
// feature for a task, because a task is blocked whenever its feature is.
func Edges(item *kilnv1.Item) []string {
	var deps []*kilnv1.Dependency
	var ids []string
	switch k := item.GetKind().(type) {
	case *kilnv1.Item_Feature:
		deps = k.Feature.GetDependsOn()
	case *kilnv1.Item_Task:
		deps = k.Task.GetDependsOn()
		if f := k.Task.GetFeatureId(); f != "" {
			ids = append(ids, f)
		}
	}
	for _, d := range deps {
		ids = append(ids, d.GetItemId())
	}
	return ids
}

// WouldCreateCycle reports whether making itemID depend on dependsOnID would
// create a cycle: itemID is dependsOnID, or itemID is reachable from
// dependsOnID by following edges.
func WouldCreateCycle(itemID, dependsOnID string, edges func(id string) []string) bool {
	visited := map[string]bool{}
	stack := []string{dependsOnID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == itemID {
			return true
		}
		if visited[id] {
			continue
		}
		visited[id] = true
		stack = append(stack, edges(id)...)
	}
	return false
}
