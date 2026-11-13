package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/ursakhiin/kiln/gen/go/kiln/v1/kilnv1mcp"
)

func TestGeneratedTools(t *testing.T) {
	var names []string
	for _, tool := range kilnv1mcp.KilnServiceTools {
		names = append(names, tool.Name)
		var s map[string]any
		if err := json.Unmarshal(tool.InputSchema, &s); err != nil || s["type"] != "object" {
			t.Errorf("%s: bad schema %s (%v)", tool.Name, tool.InputSchema, err)
		}
	}
	want := []string{"create_item", "get_item", "list_items", "update_item", "add_dependency",
		"remove_dependency", "set_status", "next_task", "claim_task", "update_work_state",
		"release_task", "get_task_context", "get_item_changes", "list_history", "add_decision_note"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v", names)
	}
	next := kilnv1mcp.KilnServiceTools[7]
	if !strings.HasPrefix(next.Description, "Claim the next task to work on") {
		t.Errorf("next_task description = %q", next.Description)
	}
	if !strings.Contains(string(kilnv1mcp.KilnServiceTools[3].InputSchema), "feature.draft") {
		t.Error("update_item schema lacks the update_mask field comment")
	}
}
