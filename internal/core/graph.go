package core

import underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"

// Graph gives cycle detection read access to items. The service implements it
// over the index; core stays free of I/O.
type Graph interface {
	// Item returns the item with this ID, or nil if there is none.
	Item(id string) *underdrawv1.Item
	// Tasks returns the IDs of a feature's tasks.
	Tasks(featureID string) []string
}

// node is one point in an item's life: when it can start, or when it is
// finished. One node per item can't express both "a task waits for its
// feature to be unblocked" and "a feature isn't finished until its tasks
// are" without every task looking like a cycle with its parent.
type node struct {
	id   string
	done bool
}

// waitsOn returns the nodes n can't be reached before:
//   - start(X) waits on done(D) for each dependency D of X
//   - start(task) waits on start(its feature)
//   - done(X) waits on start(X)
//   - done(feature) waits on done(each of its tasks)
func waitsOn(n node, g Graph) []node {
	item := g.Item(n.id)
	if item == nil {
		return nil
	}
	var out []node
	if n.done {
		out = append(out, node{n.id, false})
		if item.GetFeature() != nil {
			for _, t := range g.Tasks(n.id) {
				out = append(out, node{t, true})
			}
		}
		return out
	}
	var deps []*underdrawv1.Dependency
	switch k := item.GetKind().(type) {
	case *underdrawv1.Item_Feature:
		deps = k.Feature.GetDependsOn()
	case *underdrawv1.Item_Task:
		deps = k.Task.GetDependsOn()
		if f := k.Task.GetFeatureId(); f != "" {
			out = append(out, node{f, false})
		}
	}
	for _, d := range deps {
		out = append(out, node{d.GetItemId(), true})
	}
	return out
}

// WouldCreateCycle reports whether making itemID depend on dependsOnID would
// create a deadlock. The new dependency makes start(itemID) wait on
// done(dependsOnID), so it deadlocks if done(dependsOnID) already waits on
// start(itemID), directly or through other items, their features or their
// tasks.
func WouldCreateCycle(itemID, dependsOnID string, g Graph) bool {
	if itemID == dependsOnID {
		return true
	}
	target := node{itemID, false}
	visited := map[node]bool{}
	stack := []node{{dependsOnID, true}}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == target {
			return true
		}
		if visited[n] {
			continue
		}
		visited[n] = true
		stack = append(stack, waitsOn(n, g)...)
	}
	return false
}

// HasCycle reports whether any dependency held by one of the items in ids
// lies on a cycle. Every cycle passes through a dependency edge, because
// parent edges alone are acyclic, so checking each dependency is enough.
// Call it with every item a change could have affected, e.g. after a task
// moves to another feature.
func HasCycle(ids []string, g Graph) bool {
	for _, id := range ids {
		var deps []*underdrawv1.Dependency
		item := g.Item(id)
		switch k := item.GetKind().(type) {
		case *underdrawv1.Item_Feature:
			deps = k.Feature.GetDependsOn()
		case *underdrawv1.Item_Task:
			deps = k.Task.GetDependsOn()
		}
		for _, d := range deps {
			if WouldCreateCycle(id, d.GetItemId(), g) {
				return true
			}
		}
	}
	return false
}
